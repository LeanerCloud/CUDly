// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"sort"

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

	completed, err := h.fetchPurchaseHistory(ctx, params)
	if err != nil {
		return nil, err
	}
	for i := range completed {
		completed[i].Status = "completed"
	}

	// Pull in-flight ad-hoc executions so the user can see (and cancel) their
	// own pending approvals without waiting for the email. Pending executions
	// live in a separate table (purchase_executions) from the completed
	// purchase_history rows, so we merge after the fact. A failure to list
	// pending executions must not hide completed history — log, skip, continue.
	pending := h.fetchPendingAsHistory(ctx)

	all := append(completed, pending...) //nolint:gocritic // intentional new slice

	all, err = h.filterPurchaseHistoryByAllowedAccounts(ctx, session, all)
	if err != nil {
		return nil, err
	}

	// Newest first across both sources so pending items don't get buried at
	// the bottom just because their synthetic records were appended last.
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Timestamp.After(all[j].Timestamp)
	})

	return HistoryResponse{
		Summary:   summarizePurchaseHistory(all),
		Purchases: all,
	}, nil
}

// fetchPendingAsHistory returns pending PurchaseExecutions as synthetic
// PurchaseHistoryRecord entries so the /api/history response is a single flat
// list. Execution-level aggregates (TotalUpfrontCost / EstimatedSavings)
// become the row's cost fields; provider/service collapse to "multiple"
// because a single execution can span providers. The approver address is
// looked up from global config once and attached to every pending row so the
// UI can tell the user exactly whose inbox holds the approval link. A
// listing error is logged and skipped — completed history must still render.
func (h *Handler) fetchPendingAsHistory(ctx context.Context) []config.PurchaseHistoryRecord {
	executions, err := h.config.GetPendingExecutions(ctx)
	if err != nil {
		logging.Warnf("history: failed to load pending executions: %v", err)
		return nil
	}
	if len(executions) == 0 {
		return nil
	}
	approver := h.resolvePendingApproverEmail(ctx)
	out := make([]config.PurchaseHistoryRecord, 0, len(executions))
	for _, exec := range executions {
		row, ok := pendingExecutionToHistoryRow(exec, approver)
		if !ok {
			continue
		}
		out = append(out, row)
	}
	return out
}

// resolvePendingApproverEmail returns the notification email the approval
// link was sent to (or would have been, if SES failed). Single-tenant
// deployments share one value across every pending row, so this is looked up
// once per /api/history call. A lookup error or an empty address returns a
// sentinel so the UI still renders the pending row — just without an
// addressee.
func (h *Handler) resolvePendingApproverEmail(ctx context.Context) string {
	globalCfg, cfgErr := h.config.GetGlobalConfig(ctx)
	if cfgErr != nil {
		logging.Warnf("history: failed to resolve approver email for pending rows: %v", cfgErr)
		return "(notification email not set)"
	}
	if globalCfg.NotificationEmail == nil || *globalCfg.NotificationEmail == "" {
		return "(notification email not set)"
	}
	return *globalCfg.NotificationEmail
}

// pendingExecutionToHistoryRow projects a PurchaseExecution into the
// flat history-row shape. Returns ok=false for terminal states so the
// belt-and-suspenders filter stays cheap at the call site (keeps the
// call-site loop linear, under the cyclo threshold).
func pendingExecutionToHistoryRow(exec config.PurchaseExecution, approver string) (config.PurchaseHistoryRecord, bool) {
	if exec.Status != "pending" && exec.Status != "notified" {
		return config.PurchaseHistoryRecord{}, false
	}
	var accountID string
	if exec.CloudAccountID != nil {
		accountID = *exec.CloudAccountID
	}
	return config.PurchaseHistoryRecord{
		AccountID:        accountID,
		PurchaseID:       exec.ExecutionID,
		Timestamp:        exec.ScheduledDate,
		Provider:         collapseRecommendationProvider(exec.Recommendations),
		Region:           "multiple",
		ResourceType:     fmt.Sprintf("%d commitment(s)", len(exec.Recommendations)),
		Count:            len(exec.Recommendations),
		UpfrontCost:      exec.TotalUpfrontCost,
		EstimatedSavings: exec.EstimatedSavings,
		PlanID:           exec.PlanID,
		Status:           "pending",
		Approver:         approver,
	}, true
}

// collapseRecommendationProvider returns the single provider shared by every
// recommendation in an execution, or "multiple" when the execution spans
// providers (e.g. a plan that mixes AWS EC2 and Azure VM reservations).
func collapseRecommendationProvider(recs []config.RecommendationRecord) string {
	if len(recs) == 0 {
		return "multiple"
	}
	p := recs[0].Provider
	for _, r := range recs[1:] {
		if r.Provider != p {
			return "multiple"
		}
	}
	return p
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
		// Pending rows count toward TotalPurchases / TotalPending but not
		// toward the dollar totals — the money hasn't been committed yet.
		// "completed" and unset (legacy DB rows that pre-date the status
		// field) both count as completed.
		if p.Status == "pending" || p.Status == "notified" {
			summary.TotalPending++
			continue
		}
		summary.TotalCompleted++
		summary.TotalUpfront += p.UpfrontCost
		summary.TotalMonthlySavings += p.EstimatedSavings
	}
	summary.TotalAnnualSavings = summary.TotalMonthlySavings * 12
	return summary
}
