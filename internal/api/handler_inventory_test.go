package api

import (
	"context"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	}, nil)
	mockAuth.grantAdmin()
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
			MonthlyCost:      float64Ptr(100.0),
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
			MonthlyCost:      float64Ptr(50.0),
			EstimatedSavings: 15.0,
		},
	}

	mockStore.On("GetAllPurchaseHistory", ctx, config.MaxListLimit).Return(purchases, nil)
	// Use realistic fixtures: CloudAccount.ID is a UUID; CloudAccount.ExternalID is
	// the provider external ID (e.g. AWS account number) that matches
	// PurchaseHistoryRecord.AccountID. The name lookup in resolveAccountNamesByID
	// must key by ExternalID, not UUID, to find the name — this is the regression
	// guard for issue #952.
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{
			{ID: "11111111-0000-0000-0000-000000000001", ExternalID: "acc-active", Name: "Active Account"},
			{ID: "11111111-0000-0000-0000-000000000002", ExternalID: "acc-expired", Name: "Expired Account"},
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
	assert.Equal(t, "Active Account", row.AccountName, "account name must be resolved via ExternalID (issue #952)")
	assert.Equal(t, "aws", row.Provider)
	assert.Equal(t, "ec2", row.Service)
	assert.Equal(t, 2, row.Count)
	assert.Equal(t, 1, row.TermYears)
	assert.Equal(t, "no-upfront", row.PaymentOption)
	require.NotNil(t, row.MonthlyCost)
	assert.Equal(t, 100.0, *row.MonthlyCost)
	assert.Equal(t, 30.0, row.EstimatedSavings)
	assert.Equal(t, "active", row.Status)
	assert.False(t, row.StartDate.IsZero())
	assert.True(t, row.EndDate.After(row.StartDate), "end_date must follow start_date")
}

// TestHandler_listActiveCommitments_AccountFilter verifies the account_id
// query param routes through the dual-column GetPurchaseHistoryFiltered
// instead of GetAllPurchaseHistory, and the response respects the filter.
// "acc-1" is a known cloud_accounts UUID (no external id in this fixture), so
// it is matched on cloud_account_id only (issue #701/#498/#866).
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
			MonthlyCost:      float64Ptr(80.0),
			EstimatedSavings: 20.0,
		},
	}

	mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{AccountIDs: []string{"acc-1"}, Limit: config.MaxListLimit}).Return(purchases, nil)
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

	// GetAllPurchaseHistory and the legacy single-column GetPurchaseHistory must
	// NOT have been called when account_id is set.
	mockStore.AssertNotCalled(t, "GetAllPurchaseHistory")
	mockStore.AssertNotCalled(t, "GetPurchaseHistory")
}

// TestHandler_listActiveCommitments_ProviderFilter verifies the provider
// query param filters results in-memory, returning only commitments whose
// Provider field matches the requested value (issue #866).
func TestHandler_listActiveCommitments_ProviderFilter(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	now := time.Now()
	purchases := []config.PurchaseHistoryRecord{
		{
			AccountID:   "acc-1",
			PurchaseID:  "p-aws",
			Provider:    "aws",
			Service:     "ec2",
			Timestamp:   now.AddDate(0, -3, 0),
			Term:        1,
			Count:       1,
			MonthlyCost: float64Ptr(80.0),
		},
		{
			AccountID:   "acc-1",
			PurchaseID:  "p-azure",
			Provider:    "azure",
			Service:     "compute",
			Timestamp:   now.AddDate(0, -3, 0),
			Term:        1,
			Count:       2,
			MonthlyCost: float64Ptr(120.0),
		},
	}

	mockStore.On("GetAllPurchaseHistory", ctx, config.MaxListLimit).Return(purchases, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{{ID: "acc-1", Name: "Account One"}}, nil
	}

	mockAuth, req := adminInventoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.listActiveCommitments(ctx, req, map[string]string{"provider": "aws"})
	require.NoError(t, err)

	resp := result.(InventoryCommitmentsResponse)
	require.Len(t, resp.Commitments, 1, "only the aws commitment should pass the provider filter")
	assert.Equal(t, "aws", resp.Commitments[0].Provider)
	assert.Equal(t, "p-aws", splitPurchaseID(resp.Commitments[0].ID))
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

// ──────────────────────────────────────────────
// buildCoverageBreakdown unit tests (issue #754)
// ──────────────────────────────────────────────

// TestBuildCoverageBreakdown_SingleProvider verifies that covered/on-demand
// sums are correctly attributed per service and the overall coverage% is
// computed across all services in the provider.
func TestBuildCoverageBreakdown_SingleProvider(t *testing.T) {
	covered := map[string]float64{
		"aws:ec2": 200.0,
		"aws:rds": 100.0,
	}
	onDemand := map[string]float64{
		"aws:ec2": 300.0, // ec2 coverage = 200/(200+300) = 40%
		// rds has no on-demand gap, so rds coverage = 100%
	}

	resp := buildCoverageBreakdown(covered, onDemand)

	require.Len(t, resp.Providers, 3, "always 3 known providers")
	aws := resp.Providers[0]
	assert.Equal(t, "aws", aws.Provider)
	require.NotNil(t, aws.Services, "AWS has usage so Services must be non-nil")
	require.Len(t, aws.Services, 2)

	// Services are sorted alphabetically; ec2 < rds.
	ec2 := aws.Services[0]
	assert.Equal(t, "ec2", ec2.Service)
	assert.Equal(t, 200.0, ec2.CoveredMonthly)
	assert.Equal(t, 300.0, ec2.OnDemandMonthly)
	require.NotNil(t, ec2.CoveragePct)
	assert.InDelta(t, 40.0, *ec2.CoveragePct, 0.001, "ec2 coverage = 200/500 * 100")

	rds := aws.Services[1]
	assert.Equal(t, "rds", rds.Service)
	assert.Equal(t, 100.0, rds.CoveredMonthly)
	assert.Equal(t, 0.0, rds.OnDemandMonthly)
	require.NotNil(t, rds.CoveragePct)
	assert.InDelta(t, 100.0, *rds.CoveragePct, 0.001, "rds coverage = 100/100 * 100")

	// Overall: (200+100) / (200+100+300+0) * 100 = 300/600 = 50%
	require.NotNil(t, aws.OverallCoveragePct)
	assert.InDelta(t, 50.0, *aws.OverallCoveragePct, 0.001, "AWS overall coverage = 300/600 * 100")
}

// TestBuildCoverageBreakdown_EmptyProvider verifies that a provider with no
// data in either map gets Services=nil and OverallCoveragePct=nil — not
// a zero — per feedback_nullable_not_zero.
func TestBuildCoverageBreakdown_EmptyProvider(t *testing.T) {
	covered := map[string]float64{"aws:ec2": 100.0}
	onDemand := map[string]float64{"aws:ec2": 100.0}

	resp := buildCoverageBreakdown(covered, onDemand)

	require.Len(t, resp.Providers, 3)
	for _, p := range resp.Providers {
		if p.Provider == "aws" {
			continue
		}
		assert.Nil(t, p.Services, "provider %s has no data, Services must be nil", p.Provider)
		assert.Nil(t, p.OverallCoveragePct, "provider %s has no data, OverallCoveragePct must be nil", p.Provider)
	}
}

// TestBuildCoverageBreakdown_ZeroBothSides verifies that a service with
// both covered=0 and on_demand=0 produces a nil CoveragePct, not 0.
func TestBuildCoverageBreakdown_ZeroBothSides(t *testing.T) {
	assert.Nil(t, coveragePct(0, 0), "no usage: coverage% must be nil, not 0")
}

// TestBuildCoverageBreakdown_OnlyOnDemand verifies that a provider with
// recommendations but no commitments shows 0% coverage (not nil).
func TestBuildCoverageBreakdown_OnlyOnDemand(t *testing.T) {
	covered := map[string]float64{}
	onDemand := map[string]float64{"azure:compute": 500.0}

	resp := buildCoverageBreakdown(covered, onDemand)

	var azure *ProviderCoverageSection
	for i := range resp.Providers {
		if resp.Providers[i].Provider == "azure" {
			azure = &resp.Providers[i]
			break
		}
	}
	require.NotNil(t, azure)
	require.NotNil(t, azure.Services)
	require.Len(t, azure.Services, 1)
	require.NotNil(t, azure.Services[0].CoveragePct)
	assert.InDelta(t, 0.0, *azure.Services[0].CoveragePct, 0.001, "0 covered / 500 on-demand = 0%")
}

// TestHandler_getCoverageBreakdown_Integration exercises the full handler
// path including auth and purchase-history filtering.
func TestHandler_getCoverageBreakdown_Integration(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockScheduler := new(MockScheduler)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockScheduler.AssertExpectations(t)
	})

	now := time.Now()
	purchases := []config.PurchaseHistoryRecord{
		{
			AccountID:   "acc-1",
			PurchaseID:  "p-1",
			Provider:    "aws",
			Service:     "ec2",
			Timestamp:   now.AddDate(-1, 0, 1), // active: 1y term started ~1y ago
			Term:        1,
			MonthlyCost: float64Ptr(150.0),
		},
	}
	recs := []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Savings: 350.0},
	}

	mockStore.On("GetAllPurchaseHistory", ctx, config.MaxListLimit).Return(purchases, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{}, nil
	}
	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(recs, nil)

	mockAuth, req := adminInventoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore, scheduler: mockScheduler}

	result, err := handler.getCoverageBreakdown(ctx, req, map[string]string{})
	require.NoError(t, err)

	resp, ok := result.(CoverageBreakdownResponse)
	require.True(t, ok)
	require.Len(t, resp.Providers, 3)

	aws := resp.Providers[0]
	assert.Equal(t, "aws", aws.Provider)
	require.NotNil(t, aws.OverallCoveragePct)
	// coverage = 150 / (150+350) * 100 = 30%
	assert.InDelta(t, 30.0, *aws.OverallCoveragePct, 0.001)
}

// TestHandler_getCoverageBreakdown_ProviderAndAccountChip locks down the CR
// follow-up to PR #881: when the Main Header chips select BOTH a provider and
// an account, getCoverageBreakdown must scope the on-demand (recommendations)
// side AND the covered (commitments) side to the same account. Before the
// fix the on-demand leg called ListRecommendations with a zero filter, so
// the covered side respected the account chip while the on-demand side
// bled in other accounts' gaps — producing misleading per-service coverage.
//
// The test seeds commitments + recs spanning two accounts and two providers,
// then asserts:
//   - GetPurchaseHistory is called with the selected account (single-account
//     read path), GetAllPurchaseHistory is NOT called.
//   - ListRecommendations is called with RecommendationFilter.AccountIDs set
//     to the selected account — the assertion the regression hinges on.
//   - The aws provider section reflects only the acc-1+aws rows on both
//     sides (covered=200, on-demand=300 → 40% coverage).
func TestHandler_getCoverageBreakdown_ProviderAndAccountChip(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockScheduler := new(MockScheduler)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockScheduler.AssertExpectations(t)
	})

	now := time.Now()
	// Only the acc-1 + aws commitment should reach the aws section.
	// GetPurchaseHistoryFiltered(AccountIDs:[acc-1]) is the per-account read
	// path; the account_id chip routes through it, so the store only returns
	// acc-1 rows here. The provider chip is applied in-memory by
	// fetchCommitmentRecords to drop the azure row.
	acc1Purchases := []config.PurchaseHistoryRecord{
		{
			AccountID:   "acc-1",
			PurchaseID:  "p-aws-acc1",
			Provider:    "aws",
			Service:     "ec2",
			Timestamp:   now.AddDate(0, -3, 0),
			Term:        1,
			MonthlyCost: float64Ptr(200.0),
		},
		{
			AccountID:   "acc-1",
			PurchaseID:  "p-azure-acc1",
			Provider:    "azure",
			Service:     "compute",
			Timestamp:   now.AddDate(0, -3, 0),
			Term:        1,
			MonthlyCost: float64Ptr(999.0), // must be dropped by the provider=aws chip
		},
	}

	// The mock scheduler simulates a real store: it only returns recs that
	// match the filter. The assertion that AccountIDs is plumbed lives on
	// the mock's expected-argument match — if the handler regresses to
	// passing RecommendationFilter{} the mock won't match and the test
	// will fail with an unexpected-call message naming the actual call.
	acc1Recs := []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Savings: 300.0, CloudAccountID: strPtr("acc-1")},
		// An azure rec for the same account exists in the real store but
		// would be filtered out by aggregateOnDemandByKey's provider chip.
		{Provider: "azure", Service: "compute", Savings: 999.0, CloudAccountID: strPtr("acc-1")},
	}

	mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{AccountIDs: []string{"acc-1"}, Limit: config.MaxListLimit}).Return(acc1Purchases, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{
			{ID: "acc-1", Name: "Account One"},
			{ID: "acc-2", Name: "Account Two"},
		}, nil
	}
	// The crux of the F1 fix: the on-demand fetch MUST scope by account_id.
	mockScheduler.On(
		"ListRecommendations",
		ctx,
		&config.RecommendationFilter{AccountIDs: []string{"acc-1"}},
	).Return(acc1Recs, nil)

	mockAuth, req := adminInventoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore, scheduler: mockScheduler}

	result, err := handler.getCoverageBreakdown(ctx, req, map[string]string{
		"provider":   "aws",
		"account_id": "acc-1",
	})
	require.NoError(t, err)

	resp, ok := result.(CoverageBreakdownResponse)
	require.True(t, ok)
	require.Len(t, resp.Providers, 3, "envelope always carries 3 known providers")

	// Locate the aws section by provider (knownProviders ordering is stable
	// but we look it up by name so a future reorder doesn't masquerade as a
	// real regression).
	var aws *ProviderCoverageSection
	for i := range resp.Providers {
		if resp.Providers[i].Provider == "aws" {
			aws = &resp.Providers[i]
			break
		}
	}
	require.NotNil(t, aws)
	require.NotNil(t, aws.Services, "aws section must have services from the acc-1 rows")
	require.Len(t, aws.Services, 1, "only ec2 contributes after provider=aws chip")

	ec2 := aws.Services[0]
	assert.Equal(t, "ec2", ec2.Service)
	assert.Equal(t, 200.0, ec2.CoveredMonthly, "covered comes from acc-1 purchase only")
	assert.Equal(t, 300.0, ec2.OnDemandMonthly, "on-demand comes from acc-1 rec only")
	require.NotNil(t, ec2.CoveragePct)
	// 200 / (200+300) * 100 = 40
	assert.InDelta(t, 40.0, *ec2.CoveragePct, 0.001)

	// GetAllPurchaseHistory must NOT be called when account_id is set —
	// belt-and-braces with the per-account read path on the covered side.
	mockStore.AssertNotCalled(t, "GetAllPurchaseHistory")
}

// TestHandler_getCoverageBreakdown_AzureAllUpfrontConsistency is the
// regression guard for the live bug: the Home dashboard showed a non-zero
// "current / committed" figure for Azure (it counts active commitments via
// EstimatedSavings, which is always populated) while the Coverage tab showed
// $0 / "No usage detected" for the same Azure subscription.
//
// Root cause: an Azure all-upfront RI carries MonthlyCost == nil (no recurring
// charge — see config.PurchaseHistoryRecord.MonthlyCost), and the old Coverage
// path summed only non-nil MonthlyCost, silently dropping the row. The covered
// monthly of such a commitment is its amortised upfront (UpfrontCost / term
// months), so the two surfaces disagreed: the dashboard found the commitment,
// Coverage acted as if Azure had none.
//
// This test seeds exactly that shape — an active Azure compute commitment with
// MonthlyCost nil but a real UpfrontCost over a 1-year term — and asserts:
//   - the dashboard's active-commitment aggregation sees it (non-zero
//     EstimatedSavings for azure:compute), proving the commitment is "current";
//   - the Coverage tab now reports a non-zero covered monthly for Azure equal
//     to the amortised upfront, instead of nil / zero coverage.
//
// Pre-fix the Coverage assertion fails (azure section has nil Services and nil
// OverallCoveragePct). Post-fix both surfaces agree that Azure has an active,
// covered commitment.
func TestHandler_getCoverageBreakdown_AzureAllUpfrontConsistency(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockScheduler := new(MockScheduler)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockScheduler.AssertExpectations(t)
	})

	now := time.Now()
	// Azure all-upfront RI: no recurring monthly charge (MonthlyCost nil),
	// $1200 upfront over a 1-year term => $100/mo amortised covered spend.
	// EstimatedSavings is populated, which is what the dashboard's
	// "current / committed" figure renders.
	azureCommitment := config.PurchaseHistoryRecord{
		AccountID:        "acc-az",
		PurchaseID:       "p-azure-allupfront",
		Provider:         "azure",
		Service:          "compute",
		Timestamp:        now.AddDate(0, -2, 0), // active: 1y term started 2mo ago
		Term:             1,
		UpfrontCost:      1200.0,
		MonthlyCost:      nil, // all-upfront: no recurring charge at the commitment layer
		EstimatedSavings: 166.0,
	}
	purchases := []config.PurchaseHistoryRecord{azureCommitment}

	// --- Consistency leg 1: the dashboard counts this commitment as active ---
	// aggregateActiveCommitmentsPerService is the exact primitive the dashboard
	// "current / committed" figure is built from (handler_dashboard.go). It must
	// see the Azure commitment, otherwise there would be nothing to reconcile.
	dashByService := aggregateActiveCommitmentsPerService(purchases, now)
	require.Equal(t, 166.0, dashByService["compute"],
		"dashboard must count the active Azure commitment (EstimatedSavings)")

	// --- Consistency leg 2: the Coverage tab must reflect the same commitment ---
	mockStore.On("GetAllPurchaseHistory", ctx, config.MaxListLimit).Return(purchases, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{}, nil
	}
	// No Azure on-demand recommendations: the only signal for Azure is the
	// covered commitment. Pre-fix this yields nil/zero coverage; post-fix the
	// amortised upfront makes Azure 100% covered for compute.
	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{}, nil)

	mockAuth, req := adminInventoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore, scheduler: mockScheduler}

	result, err := handler.getCoverageBreakdown(ctx, req, map[string]string{})
	require.NoError(t, err)

	resp, ok := result.(CoverageBreakdownResponse)
	require.True(t, ok)

	var azure *ProviderCoverageSection
	for i := range resp.Providers {
		if resp.Providers[i].Provider == "azure" {
			azure = &resp.Providers[i]
			break
		}
	}
	require.NotNil(t, azure)
	// The crux: pre-fix this is nil (row dropped → "No usage detected"); the
	// commitment must surface as covered.
	require.NotNil(t, azure.Services,
		"Azure must show its all-upfront commitment as covered, not 'No usage detected'")
	require.Len(t, azure.Services, 1)

	compute := azure.Services[0]
	assert.Equal(t, "compute", compute.Service)
	// $1200 upfront / (1yr * 12mo) = $100/mo amortised covered spend.
	assert.InDelta(t, 100.0, compute.CoveredMonthly, 0.001,
		"covered monthly = amortised upfront for an all-upfront commitment")
	assert.Equal(t, 0.0, compute.OnDemandMonthly)
	require.NotNil(t, compute.CoveragePct)
	// 100 covered / (100 covered + 0 on-demand) = 100% — never nil/zero.
	assert.InDelta(t, 100.0, *compute.CoveragePct, 0.001)

	require.NotNil(t, azure.OverallCoveragePct)
	assert.InDelta(t, 100.0, *azure.OverallCoveragePct, 0.001)
}
