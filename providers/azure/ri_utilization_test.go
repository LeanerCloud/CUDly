package azure

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/mocks"
)

// mockReservationsSummariesAPI implements reservationsSummariesAPI for testing.
// It captures the resourceScope, grain, and date arguments passed to newListPager
// so tests can assert they were formed correctly.
type mockReservationsSummariesAPI struct {
	pager         reservationsSummariesPager // accepts any pager implementation
	capturedScope string
	capturedGrain armconsumption.Datagrain
	capturedStart string
	capturedEnd   string
}

func (m *mockReservationsSummariesAPI) newListPager(resourceScope string, grain armconsumption.Datagrain, startDate, endDate string) reservationsSummariesPager {
	m.capturedScope = resourceScope
	m.capturedGrain = grain
	m.capturedStart = startDate
	m.capturedEnd = endDate
	return m.pager
}

// helpers (local to this test file — recommendations_test.go declares strPtr,
// so we use distinct names here to avoid a redeclaration compile error)
func riStrPtr(s string) *string       { return &s }
func riFloat64Ptr(f float64) *float64 { return &f }

func TestGetRIUtilization_HappyPath(t *testing.T) {
	skuA := "Standard_D2s_v3"
	skuB := "Standard_E4s_v3"
	idA := "reservation-aaa"
	idB := "reservation-bbb"

	summaries := []*armconsumption.ReservationSummary{
		{
			Properties: &armconsumption.ReservationSummaryProperties{
				ReservationID: riStrPtr(idA),
				SKUName:       riStrPtr(skuA),
				ReservedHours: riFloat64Ptr(720.0),
				UsedHours:     riFloat64Ptr(648.0), // 90%
			},
		},
		{
			Properties: &armconsumption.ReservationSummaryProperties{
				ReservationID: riStrPtr(idB),
				SKUName:       riStrPtr(skuB),
				ReservedHours: riFloat64Ptr(744.0),
				UsedHours:     riFloat64Ptr(0.0), // 0% (idle reservation)
			},
		},
	}

	mockPager := &mocks.MockReservationsSummariesPager{
		Results: summaries,
		HasMore: true,
	}

	api := &mockReservationsSummariesAPI{pager: mockPager}
	adapter := &RecommendationsClientAdapter{subscriptionID: "sub-123"}

	result, err := adapter.getRIUtilizationViaAPI(context.Background(), api, "2026-04-14", "2026-05-14")
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Build a lookup map for order-independent assertion.
	byID := make(map[string]common.RIUtilization, len(result))
	for _, u := range result {
		byID[u.ReservedInstanceID] = u
	}

	a := byID[idA]
	assert.Equal(t, idA, a.ReservedInstanceID)
	assert.Equal(t, skuA, a.SKUName)
	assert.InDelta(t, 90.0, a.UtilizationPercent, 0.01)
	assert.Equal(t, 720.0, a.PurchasedHours)
	assert.Equal(t, 648.0, a.TotalActualHours)
	assert.Equal(t, 72.0, a.UnusedHours)

	b := byID[idB]
	assert.Equal(t, idB, b.ReservedInstanceID)
	assert.Equal(t, skuB, b.SKUName)
	assert.Equal(t, 0.0, b.UtilizationPercent)
	assert.Equal(t, 744.0, b.UnusedHours) // all hours unused: UsedHours=0, ReservedHours=744

	// Verify the subscription scope was formatted correctly (leading slash required
	// by Azure's Consumption API resourceScope parameter).
	assert.Equal(t, "/subscriptions/sub-123", api.capturedScope)
	assert.Equal(t, armconsumption.DatagrainMonthlyGrain, api.capturedGrain)
}

func TestGetRIUtilization_MultiPageAggregation(t *testing.T) {
	// Two monthly summary rows for the same reservation: the function must
	// sum PurchasedHours and UsedHours across pages/rows.
	id := "reservation-multi"
	summaryPage1 := []*armconsumption.ReservationSummary{
		{
			Properties: &armconsumption.ReservationSummaryProperties{
				ReservationID: riStrPtr(id),
				SKUName:       riStrPtr("Standard_D4s_v3"),
				ReservedHours: riFloat64Ptr(744.0), // March
				UsedHours:     riFloat64Ptr(600.0),
			},
		},
	}
	summaryPage2 := []*armconsumption.ReservationSummary{
		{
			Properties: &armconsumption.ReservationSummaryProperties{
				ReservationID: riStrPtr(id),
				// SKUName deliberately absent in second row to test first-wins.
				ReservedHours: riFloat64Ptr(720.0), // April
				UsedHours:     riFloat64Ptr(700.0),
			},
		},
	}

	// Simulate two-page response with a multi-call pager.
	pager := &twoPagePager{
		page0: summaryPage1,
		page1: summaryPage2,
	}
	api := &mockReservationsSummariesAPI{pager: pager}
	adapter := &RecommendationsClientAdapter{subscriptionID: "sub-456"}

	result, err := adapter.getRIUtilizationViaAPI(context.Background(), api, "2026-03-01", "2026-05-01")
	require.NoError(t, err)
	require.Len(t, result, 1)

	u := result[0]
	assert.Equal(t, id, u.ReservedInstanceID)
	assert.Equal(t, "Standard_D4s_v3", u.SKUName, "SKUName from first non-empty row should win")
	assert.Equal(t, 744.0+720.0, u.PurchasedHours)
	assert.Equal(t, 600.0+700.0, u.TotalActualHours)
	assert.InDelta(t, (1300.0/1464.0)*100, u.UtilizationPercent, 0.01)
	assert.Equal(t, 1464.0-1300.0, u.UnusedHours)
}

func TestGetRIUtilization_EmptyPage(t *testing.T) {
	mockPager := &mocks.MockReservationsSummariesPager{
		Results: []*armconsumption.ReservationSummary{},
		HasMore: true,
	}

	api := &mockReservationsSummariesAPI{pager: mockPager}
	adapter := &RecommendationsClientAdapter{subscriptionID: "sub-empty"}

	result, err := adapter.getRIUtilizationViaAPI(context.Background(), api, "2026-04-14", "2026-05-14")
	require.NoError(t, err)
	assert.Nil(t, result, "empty summaries should return nil slice, not empty")
}

func TestGetRIUtilization_NilProperties(t *testing.T) {
	// Rows with nil Properties must be skipped without panicking.
	summaries := []*armconsumption.ReservationSummary{
		nil,
		{Properties: nil},
		{
			Properties: &armconsumption.ReservationSummaryProperties{
				ReservationID: riStrPtr("reservation-ok"),
				ReservedHours: riFloat64Ptr(720.0),
				UsedHours:     riFloat64Ptr(360.0),
			},
		},
	}

	mockPager := &mocks.MockReservationsSummariesPager{
		Results: summaries,
		HasMore: true,
	}

	api := &mockReservationsSummariesAPI{pager: mockPager}
	adapter := &RecommendationsClientAdapter{subscriptionID: "sub-niltest"}

	result, err := adapter.getRIUtilizationViaAPI(context.Background(), api, "2026-04-14", "2026-05-14")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "reservation-ok", result[0].ReservedInstanceID)
	assert.InDelta(t, 50.0, result[0].UtilizationPercent, 0.01)
}

func TestGetRIUtilization_NilReservationID(t *testing.T) {
	// Rows with nil or empty ReservationID must be silently skipped.
	summaries := []*armconsumption.ReservationSummary{
		{
			Properties: &armconsumption.ReservationSummaryProperties{
				ReservationID: nil,
				ReservedHours: riFloat64Ptr(720.0),
				UsedHours:     riFloat64Ptr(360.0),
			},
		},
		{
			Properties: &armconsumption.ReservationSummaryProperties{
				ReservationID: riStrPtr(""),
				ReservedHours: riFloat64Ptr(100.0),
				UsedHours:     riFloat64Ptr(50.0),
			},
		},
	}

	mockPager := &mocks.MockReservationsSummariesPager{
		Results: summaries,
		HasMore: true,
	}

	api := &mockReservationsSummariesAPI{pager: mockPager}
	adapter := &RecommendationsClientAdapter{subscriptionID: "sub-nilid"}

	result, err := adapter.getRIUtilizationViaAPI(context.Background(), api, "2026-04-14", "2026-05-14")
	require.NoError(t, err)
	assert.Nil(t, result, "rows with nil/empty ReservationID should be skipped")
}

func TestGetRIUtilization_ZeroPurchasedHours(t *testing.T) {
	// When PurchasedHours is 0 the utilization percent should be 0, not NaN/Inf.
	summaries := []*armconsumption.ReservationSummary{
		{
			Properties: &armconsumption.ReservationSummaryProperties{
				ReservationID: riStrPtr("reservation-zero"),
				ReservedHours: riFloat64Ptr(0.0),
				UsedHours:     riFloat64Ptr(0.0),
			},
		},
	}

	mockPager := &mocks.MockReservationsSummariesPager{
		Results: summaries,
		HasMore: true,
	}

	api := &mockReservationsSummariesAPI{pager: mockPager}
	adapter := &RecommendationsClientAdapter{subscriptionID: "sub-zero"}

	result, err := adapter.getRIUtilizationViaAPI(context.Background(), api, "2026-04-14", "2026-05-14")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, 0.0, result[0].UtilizationPercent)
	assert.Equal(t, 0.0, result[0].UnusedHours)
}

// twoPagePager is a test helper that serves two fixed pages and then stops.
type twoPagePager struct {
	page0 []*armconsumption.ReservationSummary
	page1 []*armconsumption.ReservationSummary
	call  int
}

func (p *twoPagePager) More() bool {
	return p.call < 2
}

func (p *twoPagePager) NextPage(ctx context.Context) (armconsumption.ReservationsSummariesClientListResponse, error) {
	var rows []*armconsumption.ReservationSummary
	if p.call == 0 {
		rows = p.page0
	} else {
		rows = p.page1
	}
	p.call++
	return armconsumption.ReservationsSummariesClientListResponse{
		ReservationSummariesListResult: armconsumption.ReservationSummariesListResult{
			Value: rows,
		},
	}, nil
}
