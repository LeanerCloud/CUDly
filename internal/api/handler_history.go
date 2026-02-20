// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// History handlers
func (h *Handler) getHistory(ctx context.Context, params map[string]string) (any, error) {
	accountID := params["account_id"]
	limitStr := params["limit"]

	limit := config.DefaultListLimit
	if limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}

	var purchases []config.PurchaseHistoryRecord
	var err error

	if accountID != "" {
		purchases, err = h.config.GetPurchaseHistory(ctx, accountID, limit)
	} else {
		purchases, err = h.config.GetAllPurchaseHistory(ctx, limit)
	}

	if err != nil {
		return nil, err
	}

	// Calculate summary
	summary := HistorySummary{
		TotalPurchases: len(purchases),
	}
	for _, p := range purchases {
		summary.TotalUpfront += p.UpfrontCost
		summary.TotalMonthlySavings += p.EstimatedSavings
	}
	summary.TotalAnnualSavings = summary.TotalMonthlySavings * 12

	return HistoryResponse{
		Summary:   summary,
		Purchases: purchases,
	}, nil
}
