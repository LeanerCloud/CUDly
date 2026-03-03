package recommendations

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

// RIUtilization holds utilization data for a single Reserved Instance.
type RIUtilization struct {
	ReservedInstanceID string  `json:"reserved_instance_id"`
	UtilizationPercent float64 `json:"utilization_percent"`
	PurchasedHours     float64 `json:"purchased_hours"`
	TotalActualHours   float64 `json:"total_actual_hours"`
	UnusedHours        float64 `json:"unused_hours"`
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

	var result *costexplorer.GetReservationUtilizationOutput
	var err error

	c.rateLimiter.Reset()
	for {
		if waitErr := c.rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}

		result, err = c.costExplorerClient.GetReservationUtilization(ctx, input)
		if !c.rateLimiter.ShouldRetry(err) {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get reservation utilization: %w", err)
	}

	var utilizations []RIUtilization
	for _, group := range result.UtilizationsByTime {
		for _, detail := range group.Groups {
			if detail.Key == nil || detail.Utilization == nil {
				continue
			}

			util := RIUtilization{
				ReservedInstanceID: aws.ToString(detail.Key),
			}

			if detail.Utilization.UtilizationPercentage != nil {
				util.UtilizationPercent = parseFloat(aws.ToString(detail.Utilization.UtilizationPercentage))
			}
			if detail.Utilization.PurchasedHours != nil {
				util.PurchasedHours = parseFloat(aws.ToString(detail.Utilization.PurchasedHours))
			}
			if detail.Utilization.TotalActualHours != nil {
				util.TotalActualHours = parseFloat(aws.ToString(detail.Utilization.TotalActualHours))
			}
			if detail.Utilization.UnusedHours != nil {
				util.UnusedHours = parseFloat(aws.ToString(detail.Utilization.UnusedHours))
			}

			utilizations = append(utilizations, util)
		}
	}

	return utilizations, nil
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
