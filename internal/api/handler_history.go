// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

// History handlers
func (h *Handler) getHistory(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	// Purchase history can leak across accounts — gate on view:purchases AND
	// filter the returned records by the session's allowed_accounts list.
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	if err := logMultiAccountFilter(params); err != nil {
		return nil, err
	}

	purchases, err := h.fetchPurchaseHistory(ctx, params)
	if err != nil {
		return nil, err
	}

	purchases, err = h.filterPurchaseHistoryByAllowedAccounts(ctx, session, purchases)
	if err != nil {
		return nil, err
	}

	return HistoryResponse{
		Summary:   summarizePurchaseHistory(purchases),
		Purchases: purchases,
	}, nil
}

// logMultiAccountFilter validates and logs the multi-account filter param.
// GetPurchaseHistory/GetAllPurchaseHistory do not yet support multi-account
// filtering at the store layer; the param is accepted for observability.
func logMultiAccountFilter(params map[string]string) error {
	accountIDs, err := parseAccountIDs(params["account_ids"])
	if err != nil {
		return NewClientError(400, err.Error())
	}
	if len(accountIDs) > 0 {
		logging.Infof("history: account_ids filter received (%d accounts); per-account filtering not yet implemented", len(accountIDs))
	}
	return nil
}

// fetchPurchaseHistory reads purchases from the store, honouring the legacy
// singular account_id query param and the limit cap.
func (h *Handler) fetchPurchaseHistory(ctx context.Context, params map[string]string) ([]config.PurchaseHistoryRecord, error) {
	limit := config.DefaultListLimit
	if s := params["limit"]; s != "" {
		fmt.Sscanf(s, "%d", &limit)
	}
	if limit > config.MaxListLimit {
		limit = config.MaxListLimit
	}

	if accountID := params["account_id"]; accountID != "" {
		return h.config.GetPurchaseHistory(ctx, accountID, limit)
	}
	return h.config.GetAllPurchaseHistory(ctx, limit)
}

// filterPurchaseHistoryByAllowedAccounts drops records whose AccountID/Name
// is outside the session's allowed_accounts. Admin/unrestricted sessions pass
// through unchanged.
func (h *Handler) filterPurchaseHistoryByAllowedAccounts(ctx context.Context, session *Session, purchases []config.PurchaseHistoryRecord) ([]config.PurchaseHistoryRecord, error) {
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return purchases, nil
	}
	nameByID := h.resolveAccountNamesByID(ctx)
	filtered := purchases[:0]
	for _, p := range purchases {
		if auth.MatchesAccount(allowed, p.AccountID, nameByID[p.AccountID]) {
			filtered = append(filtered, p)
		}
	}
	return filtered, nil
}

func summarizePurchaseHistory(purchases []config.PurchaseHistoryRecord) HistorySummary {
	summary := HistorySummary{TotalPurchases: len(purchases)}
	for _, p := range purchases {
		summary.TotalUpfront += p.UpfrontCost
		summary.TotalMonthlySavings += p.EstimatedSavings
	}
	summary.TotalAnnualSavings = summary.TotalMonthlySavings * 12
	return summary
}
