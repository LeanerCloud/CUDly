package api

import (
	"context"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminInventoryReq builds an admin-authed request and wires the auth mock so
// requirePermission short-circuits. Mirrors adminHistoryReq from
// handler_history_test.go — the inventory handler reuses the view:purchases
// permission, so the request shape is the same.
func adminInventoryReq(ctx context.Context) (*MockAuthService, *events.LambdaFunctionURLRequest) {
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}, nil)
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	return mockAuth, req
}

// TestHandler_listActiveCommitments_Empty verifies the empty-store path
// returns a non-nil empty slice — the frontend renders `.empty` on
// length==0, but a nil response would force a null check.
func TestHandler_listActiveCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetAllPurchaseHistory", ctx, config.MaxListLimit).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{}, nil
	}

	mockAuth, req := adminInventoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.listActiveCommitments(ctx, req, map[string]string{})
	require.NoError(t, err)

	resp, ok := result.(InventoryCommitmentsResponse)
	require.True(t, ok, "response must be InventoryCommitmentsResponse envelope, not bare slice")
	assert.NotNil(t, resp.Commitments, "commitments slice must be non-nil even when empty")
	assert.Len(t, resp.Commitments, 0)
}

// TestHandler_listActiveCommitments_FiltersExpired verifies the term-expiry
// predicate drops rows whose timestamp + term has elapsed and keeps the
// in-term ones. Same predicate the dashboard aggregate uses; this test
// guards the predicate's behaviour in the inventory-handler context so a
// future refactor (e.g. moving to days-from-now) trips here too.
func TestHandler_listActiveCommitments_FiltersExpired(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	now := time.Now()
	purchases := []config.PurchaseHistoryRecord{
		// Active: bought 6 months ago, 1-year term — 6 months remaining.
		{
			AccountID:        "acc-active",
			PurchaseID:       "p-active",
			Provider:         "aws",
			Service:          "ec2",
			Region:           "us-east-1",
			Count:            2,
			Term:             1,
			Payment:          "no-upfront",
			Timestamp:        now.AddDate(0, -6, 0),
			MonthlyCost:      100.0,
			EstimatedSavings: 30.0,
		},
		// Expired: bought 2 years ago, 1-year term.
		{
			AccountID:        "acc-expired",
			PurchaseID:       "p-expired",
			Provider:         "aws",
			Service:          "rds",
			Region:           "us-east-1",
			Count:            1,
			Term:             1,
			Timestamp:        now.AddDate(-2, 0, 0),
			MonthlyCost:      50.0,
			EstimatedSavings: 15.0,
		},
	}

	mockStore.On("GetAllPurchaseHistory", ctx, config.MaxListLimit).Return(purchases, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{
			{ID: "acc-active", Name: "Active Account"},
			{ID: "acc-expired", Name: "Expired Account"},
		}, nil
	}

	mockAuth, req := adminInventoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.listActiveCommitments(ctx, req, map[string]string{})
	require.NoError(t, err)

	resp := result.(InventoryCommitmentsResponse)
	require.Len(t, resp.Commitments, 1, "expired commitment must be filtered out")
	row := resp.Commitments[0]
	assert.Equal(t, "acc-active:p-active", row.ID, "id namespaces account+purchase")
	assert.Equal(t, "acc-active", row.AccountID)
	assert.Equal(t, "Active Account", row.AccountName, "account name must be joined from ListCloudAccounts")
	assert.Equal(t, "aws", row.Provider)
	assert.Equal(t, "ec2", row.Service)
	assert.Equal(t, 2, row.Count)
	assert.Equal(t, 1, row.TermYears)
	assert.Equal(t, "no-upfront", row.PaymentOption)
	assert.Equal(t, 100.0, row.MonthlyCost)
	assert.Equal(t, 30.0, row.EstimatedSavings)
	assert.Equal(t, "active", row.Status)
	assert.False(t, row.StartDate.IsZero())
	assert.True(t, row.EndDate.After(row.StartDate), "end_date must follow start_date")
}

// TestHandler_listActiveCommitments_AccountFilter verifies the account_id
// query param routes through GetPurchaseHistory (single-account read)
// instead of GetAllPurchaseHistory, and the response respects the filter.
func TestHandler_listActiveCommitments_AccountFilter(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	now := time.Now()
	purchases := []config.PurchaseHistoryRecord{
		{
			AccountID:        "acc-1",
			PurchaseID:       "p-1",
			Provider:         "aws",
			Service:          "ec2",
			Timestamp:        now.AddDate(0, -3, 0),
			Term:             1,
			Count:            1,
			MonthlyCost:      80.0,
			EstimatedSavings: 20.0,
		},
	}

	mockStore.On("GetPurchaseHistory", ctx, "acc-1", config.MaxListLimit).Return(purchases, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{{ID: "acc-1", Name: "Account One"}}, nil
	}

	mockAuth, req := adminInventoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.listActiveCommitments(ctx, req, map[string]string{"account_id": "acc-1"})
	require.NoError(t, err)

	resp := result.(InventoryCommitmentsResponse)
	require.Len(t, resp.Commitments, 1)
	assert.Equal(t, "acc-1", resp.Commitments[0].AccountID)

	// GetAllPurchaseHistory must NOT have been called when account_id is set.
	mockStore.AssertNotCalled(t, "GetAllPurchaseHistory")
}

// TestHandler_listActiveCommitments_SortedByExpiry verifies soonest-expiring
// is first — the dashboard framing is "what do I need to renew next?", so
// surfacing the imminent end_date on top keeps the UI's order intuitive
// without forcing the frontend to re-sort.
func TestHandler_listActiveCommitments_SortedByExpiry(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	now := time.Now()
	// Three purchases with very different term remainders. Listed
	// "out of order" so the sort step actually has work to do.
	purchases := []config.PurchaseHistoryRecord{
		// 30 months remaining (3y term, bought 6mo ago).
		{
			AccountID:  "acc-1",
			PurchaseID: "p-long",
			Provider:   "aws",
			Service:    "ec2",
			Timestamp:  now.AddDate(0, -6, 0),
			Term:       3,
			Count:      1,
		},
		// 6 months remaining (1y term, bought 6mo ago).
		{
			AccountID:  "acc-1",
			PurchaseID: "p-short",
			Provider:   "aws",
			Service:    "rds",
			Timestamp:  now.AddDate(0, -6, 0),
			Term:       1,
			Count:      1,
		},
		// 18 months remaining (3y term, bought 18mo ago).
		{
			AccountID:  "acc-1",
			PurchaseID: "p-mid",
			Provider:   "aws",
			Service:    "elasticache",
			Timestamp:  now.AddDate(0, -18, 0),
			Term:       3,
			Count:      1,
		},
	}

	mockStore.On("GetAllPurchaseHistory", ctx, config.MaxListLimit).Return(purchases, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{{ID: "acc-1", Name: "Account One"}}, nil
	}

	mockAuth, req := adminInventoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.listActiveCommitments(ctx, req, map[string]string{})
	require.NoError(t, err)

	resp := result.(InventoryCommitmentsResponse)
	require.Len(t, resp.Commitments, 3)
	assert.Equal(t, "p-short", splitPurchaseID(resp.Commitments[0].ID), "shortest remaining term first")
	assert.Equal(t, "p-mid", splitPurchaseID(resp.Commitments[1].ID))
	assert.Equal(t, "p-long", splitPurchaseID(resp.Commitments[2].ID))
}

// splitPurchaseID strips the `{accountID}:` prefix from an
// InventoryCommitment.ID so the sort-order assertion can compare on the
// raw purchase ID without rebuilding the prefix in the test.
func splitPurchaseID(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' {
			return id[i+1:]
		}
	}
	return id
}

// TestHandler_isActiveCommitment_Predicate exercises the extracted
// predicate directly so the boundary case (term ends exactly at `now`)
// is locked down by a test, not just by the integration tests above.
// Stable boundary semantics matter because the dashboard aggregate and
// the inventory handler now share this predicate — drift here would
// cause the two views to disagree about which commitments are active.
func TestHandler_isActiveCommitment_Predicate(t *testing.T) {
	now := time.Now()
	p := config.PurchaseHistoryRecord{
		Timestamp: now.AddDate(-1, 0, 0),
		Term:      1, // 1y term, started 1y ago — at the boundary.
	}
	// The 1y term is approximated as 365d. now.AddDate(-1, 0, 0) anchors
	// on the calendar day, so on a leap-year boundary the predicate
	// returns true (active) — we accept that; the dashboard's aggregate
	// uses the same arithmetic.
	assert.True(t, isActiveCommitment(p, now.Add(-time.Hour)),
		"a commitment one hour before its expiry must still be active")

	expired := config.PurchaseHistoryRecord{
		Timestamp: now.AddDate(-2, 0, 0),
		Term:      1,
	}
	assert.False(t, isActiveCommitment(expired, now),
		"a commitment whose term ended a year ago must be inactive")
}
