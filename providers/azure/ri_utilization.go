package azure

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// reservationsSummariesPager is the page-iterator interface returned by
// armconsumption.ReservationsSummariesClient.NewListPager. Extracted as an
// interface so tests can inject a stub without a real Azure connection.
type reservationsSummariesPager interface {
	More() bool
	NextPage(ctx context.Context) (armconsumption.ReservationsSummariesClientListResponse, error)
}

// reservationsSummariesAPI is the narrow slice of
// armconsumption.ReservationsSummariesClient that GetRIUtilization needs.
// It lets tests inject a fake client that returns a controlled pager.
type reservationsSummariesAPI interface {
	newListPager(resourceScope string, grain armconsumption.Datagrain, startDate, endDate string) reservationsSummariesPager
}

// realReservationsSummariesAPI wraps the concrete Azure SDK client and
// implements reservationsSummariesAPI using the real Consumption API.
type realReservationsSummariesAPI struct {
	client *armconsumption.ReservationsSummariesClient
}

func (r *realReservationsSummariesAPI) newListPager(resourceScope string, grain armconsumption.Datagrain, startDate, endDate string) reservationsSummariesPager {
	return r.client.NewListPager(resourceScope, grain, &armconsumption.ReservationsSummariesClientListOptions{
		StartDate: &startDate,
		EndDate:   &endDate,
	})
}

// riAccumulator collects hour totals across multiple monthly summary rows
// for the same reservation ID so a single RIUtilization can be returned
// per reservation regardless of how many calendar months fall in the window.
type riAccumulator struct {
	skuName        string
	purchasedHours float64
	usedHours      float64
}

// GetRIUtilization fetches per-reservation utilization data from the Azure
// Consumption Reservations Summaries API for the subscription and returns it
// in the same shape as the AWS recommendations.Client.GetRIUtilization so
// callers can treat both providers uniformly.
//
// lookbackDays controls the date window: [today - lookbackDays, today].
// Values <= 0 default to 30 days, matching the AWS implementation.
//
// The function uses monthly grain so the API returns one row per
// reservation per calendar month. Rows are accumulated by ReservationID
// across months and a single common.RIUtilization is returned per
// reservation with derived UtilizationPercent = (UsedHours / ReservedHours) * 100.
//
// An empty result (no reservations in the subscription, or the subscription
// has no active reservations in the date range) returns (nil, nil).
func (r *RecommendationsClientAdapter) GetRIUtilization(ctx context.Context, lookbackDays int) ([]common.RIUtilization, error) {
	if lookbackDays <= 0 {
		lookbackDays = 30
	}

	end := time.Now().UTC()
	start := end.AddDate(0, 0, -lookbackDays)
	startDate := start.Format("2006-01-02")
	endDate := end.Format("2006-01-02")

	// Build the real client and wrap it in the interface so tests can substitute.
	summariesClient, err := armconsumption.NewReservationsSummariesClient(r.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure ri utilization: failed to create summaries client: %w", err)
	}

	api := &realReservationsSummariesAPI{client: summariesClient}
	return r.getRIUtilizationViaAPI(ctx, api, startDate, endDate)
}

// getRIUtilizationViaAPI performs the actual pager loop. It is separate from
// GetRIUtilization so tests can inject a fake reservationsSummariesAPI
// without needing a real Azure credential.
func (r *RecommendationsClientAdapter) getRIUtilizationViaAPI(
	ctx context.Context,
	api reservationsSummariesAPI,
	startDate, endDate string,
) ([]common.RIUtilization, error) {
	// Subscription-scoped resource path: Azure's Consumption API accepts
	// "/subscriptions/{id}" as the resourceScope for the List endpoint.
	resourceScope := fmt.Sprintf("subscriptions/%s", r.subscriptionID)

	pager := api.newListPager(resourceScope, armconsumption.DatagrainMonthlyGrain, startDate, endDate)

	agg := make(map[string]*riAccumulator)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("azure ri utilization: failed to fetch summaries page: %w", err)
		}

		for _, summary := range page.Value {
			accumulateSummary(agg, summary)
		}
	}

	return buildUtilizations(agg), nil
}

// accumulateSummary adds one ReservationSummary row into the aggregation map.
// Rows with nil Properties or nil ReservationID are silently skipped.
func accumulateSummary(agg map[string]*riAccumulator, summary *armconsumption.ReservationSummary) {
	if summary == nil || summary.Properties == nil {
		return
	}
	props := summary.Properties
	if props.ReservationID == nil || *props.ReservationID == "" {
		return
	}

	id := *props.ReservationID
	a, ok := agg[id]
	if !ok {
		a = &riAccumulator{}
		agg[id] = a
	}

	// Capture SKU on first non-empty occurrence.
	if a.skuName == "" && props.SKUName != nil {
		a.skuName = *props.SKUName
	}

	if props.ReservedHours != nil {
		a.purchasedHours += *props.ReservedHours
	}
	if props.UsedHours != nil {
		a.usedHours += *props.UsedHours
	}
}

// buildUtilizations converts the per-reservation accumulator map into the
// common.RIUtilization slice. UtilizationPercent is derived as
// (usedHours / purchasedHours) * 100; it stays 0 when purchasedHours == 0
// (defensive, avoids division by zero for zero-hour reservations).
func buildUtilizations(agg map[string]*riAccumulator) []common.RIUtilization {
	if len(agg) == 0 {
		return nil
	}
	result := make([]common.RIUtilization, 0, len(agg))
	for id, a := range agg {
		pct := 0.0
		if a.purchasedHours > 0 {
			pct = (a.usedHours / a.purchasedHours) * 100
		}
		unusedHours := a.purchasedHours - a.usedHours
		if unusedHours < 0 {
			unusedHours = 0
		}
		result = append(result, common.RIUtilization{
			ReservedInstanceID: id,
			UtilizationPercent: pct,
			PurchasedHours:     a.purchasedHours,
			TotalActualHours:   a.usedHours,
			UnusedHours:        unusedHours,
			SKUName:            a.skuName,
		})
	}
	return result
}
