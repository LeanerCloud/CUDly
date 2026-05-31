package recommendations

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// RIUtilization is an alias for common.RIUtilization so existing callers that
// import this package continue to compile without changes. The canonical
// definition lives in pkg/common so the Azure provider can return the same
// type without a cross-provider dependency.
type RIUtilization = common.RIUtilization

// riAccumulator aggregates utilization hours across multiple daily entries for one RI.
type riAccumulator struct {
	purchasedHours   float64
	totalActualHours float64
	unusedHours      float64
}

// GetRIUtilization fetches per-RI utilization from Cost Explorer for the last N days.
func (c *Client) GetRIUtilization(ctx context.Context, lookbackDays int) ([]RIUtilization, error) {
	if lookbackDays <= 0 {
		lookbackDays = 30
	}

	end := time.Now().UTC()
	start := end.AddDate(0, 0, -lookbackDays)

	input := &costexplorer.GetReservationUtilizationInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
		GroupBy: []types.GroupDefinition{
			{
				Type: types.GroupDefinitionTypeDimension,
				Key:  aws.String("SUBSCRIPTION_ID"),
			},
		},
	}

	agg := make(map[string]*riAccumulator)

	var nextPageToken *string
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("utilization: pagination cancelled: %w", err)
		}
		input.NextPageToken = nextPageToken

		result, err := c.fetchUtilizationPage(ctx, input)
		if err != nil {
			return nil, err
		}

		for _, group := range result.UtilizationsByTime {
			for _, detail := range group.Groups {
				aggregateUtilizationDetail(agg, detail)
			}
		}

		if result.NextPageToken == nil || *result.NextPageToken == "" {
			break
		}
		nextPageToken = result.NextPageToken
	}

	return buildUtilizations(agg), nil
}

func buildUtilizations(agg map[string]*riAccumulator) []RIUtilization {
	utilizations := make([]RIUtilization, 0, len(agg))
	for id, a := range agg {
		pct := 0.0
		if a.purchasedHours > 0 {
			pct = (a.totalActualHours / a.purchasedHours) * 100
		}
		utilizations = append(utilizations, RIUtilization{
			ReservedInstanceID: id,
			UtilizationPercent: pct,
			PurchasedHours:     a.purchasedHours,
			TotalActualHours:   a.totalActualHours,
			UnusedHours:        a.unusedHours,
		})
	}
	return utilizations
}

// fetchUtilizationPage calls the Cost Explorer API with rate-limit retry.
func (c *Client) fetchUtilizationPage(ctx context.Context, input *costexplorer.GetReservationUtilizationInput) (*costexplorer.GetReservationUtilizationOutput, error) {
	c.rateLimiter.Reset()
	for {
		if waitErr := c.rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}

		result, err := c.costExplorerClient.GetReservationUtilization(ctx, input)
		if !c.rateLimiter.ShouldRetry(err) {
			if err != nil {
				return nil, fmt.Errorf("failed to get reservation utilization: %w", err)
			}
			return result, nil
		}
	}
}

// aggregateUtilizationDetail accumulates hours from a single utilization detail
// into the per-RI aggregation map.
func aggregateUtilizationDetail(agg map[string]*riAccumulator, detail types.ReservationUtilizationGroup) {
	if detail.Key == nil || detail.Utilization == nil {
		return
	}

	id := aws.ToString(detail.Key)
	a, ok := agg[id]
	if !ok {
		a = &riAccumulator{}
		agg[id] = a
	}

	if detail.Utilization.PurchasedHours != nil {
		a.purchasedHours += parseFloat(aws.ToString(detail.Utilization.PurchasedHours))
	}
	if detail.Utilization.TotalActualHours != nil {
		a.totalActualHours += parseFloat(aws.ToString(detail.Utilization.TotalActualHours))
	}
	if detail.Utilization.UnusedHours != nil {
		a.unusedHours += parseFloat(aws.ToString(detail.Utilization.UnusedHours))
	}
}

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		log.Printf("warning: failed to parse float %q: %v", s, err)
		return 0
	}
	return f
}
