// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// History handlers
func (h *Handler) getHistory(ctx context.Context, params map[string]string) (any, error) {
	// Parse account_ids (comma-separated). GetPurchaseHistory/GetAllPurchaseHistory do not yet
	// support multi-account filtering; the parameter is accepted and logged for observability.
	// Per-account filtering will be wired to the store in a future step.
	if accountIDs := parseAccountIDs(params["account_ids"]); len(accountIDs) > 0 {
		logging.Infof("history: account_ids filter received (%d accounts); per-account filtering not yet implemented", len(accountIDs))
	}

	accountID := params["account_id"]
	limitStr := params["limit"]

	limit := config.DefaultListLimit
	if limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}
	if limit > config.MaxListLimit {
		limit = config.MaxListLimit
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
