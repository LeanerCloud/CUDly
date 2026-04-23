// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"sort"
	"time"

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

	// Pull non-completed executions so the user can see (and cancel) their
	// own in-flight approvals without waiting for the email, AND see the
	// ones that never made it out (failed) or that sat unapproved past the
	// 7-day cutoff (expired). Executions live in a separate table
	// (purchase_executions) from the completed purchase_history rows, so we
	// merge after the fact. A failure to list executions must not hide
	// completed history — log, skip, continue.
	extra := h.fetchExecutionsAsHistory(ctx)

	all := append(completed, extra...) //nolint:gocritic // intentional new slice

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

// historyExecutionStatuses enumerates the PurchaseExecution statuses the
// History view shows alongside completed purchases. "approved" and
// "completed" are excluded because an approved execution becomes a
// purchase_history row once the scheduler processes it, and completed
// executions live in purchase_history directly — listing them here would
// duplicate rows. Everything else (pending, notified, failed, expired,
// cancelled) is a terminal or in-flight state the user needs audit-trail
// visibility into: a cancelled purchase that simply vanishes is worse UX
// than a cancelled row with a clear badge.
var historyExecutionStatuses = []string{"pending", "notified", "failed", "expired", "cancelled"}

// approvalExpiryWindow is how long a pending approval stays actionable
// before the History view flips it to "expired". Aligns with the
// cleanup-executions TTL and the user expectation that a week-old
// approval link is stale.
const approvalExpiryWindow = 7 * 24 * time.Hour

// fetchExecutionsAsHistory returns non-completed PurchaseExecutions as
// synthetic PurchaseHistoryRecord entries so the /api/history response is a
// single flat list. Execution-level aggregates (TotalUpfrontCost /
// EstimatedSavings) become the row's cost fields; provider/service collapse
// to "multiple" because a single execution can span providers. The approver
// address is looked up from global config once and attached to pending/
// notified rows so the UI can tell the user exactly whose inbox holds the
// approval link. Pending executions older than approvalExpiryWindow are
// lazily transitioned to "expired" in the store so a stale approval link
// can't be clicked and the History badge reflects reality. A listing error
// is logged and skipped — completed history must still render.
func (h *Handler) fetchExecutionsAsHistory(ctx context.Context) []config.PurchaseHistoryRecord {
	executions, err := h.config.GetExecutionsByStatuses(ctx, historyExecutionStatuses, config.DefaultListLimit)
	if err != nil {
		logging.Warnf("history: failed to load non-completed executions: %v", err)
		return nil
	}
	if len(executions) == 0 {
		return nil
	}
	approver := h.resolvePendingApproverEmail(ctx)
	out := make([]config.PurchaseHistoryRecord, 0, len(executions))
	for _, exec := range executions {
		exec = h.expireIfStale(ctx, exec)
		out = append(out, executionToHistoryRow(exec, approver))
	}
	return out
}

// expireIfStale transitions a pending/notified execution to "expired" when
// its ScheduledDate is older than approvalExpiryWindow. Returns the possibly-
// updated execution. Transition failures are non-fatal — the row still
// renders, just with its original status.
func (h *Handler) expireIfStale(ctx context.Context, exec config.PurchaseExecution) config.PurchaseExecution {
	if exec.Status != "pending" && exec.Status != "notified" {
		return exec
	}
	if time.Since(exec.ScheduledDate) < approvalExpiryWindow {
		return exec
	}
	updated, err := h.config.TransitionExecutionStatus(ctx, exec.ExecutionID, []string{"pending", "notified"}, "expired")
	if err != nil {
		logging.Warnf("history: failed to expire execution %s: %v", exec.ExecutionID, err)
		return exec
	}
	return *updated
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

// executionToHistoryRow projects any non-completed PurchaseExecution into
// the flat history-row shape. Status maps 1:1 onto the history Status field.
// For pending/notified rows we attach the approver email so the UI can show
// "awaiting approval from X"; for failed rows we surface the stored Error
// message as the status description so the user sees WHY it failed (e.g.
// "send failed: Missing domain").
func executionToHistoryRow(exec config.PurchaseExecution, approver string) config.PurchaseHistoryRecord {
	var accountID string
	if exec.CloudAccountID != nil {
		accountID = *exec.CloudAccountID
	}
	row := config.PurchaseHistoryRecord{
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
		Status:           exec.Status,
	}
	switch exec.Status {
	case "pending", "notified":
		row.Approver = approver
	case "failed":
		row.StatusDescription = exec.Error
	case "expired":
		row.StatusDescription = "approval link expired (not approved within 7 days)"
	case "cancelled":
		// Cancelled via the token link in the approval email. We don't
		// currently track who clicked it (the approval token is a bearer
		// credential, not a per-user one), so the description is generic.
		// See known_issues/30_history_pending_cancel_ui.md for the
		// session-authed cancel path that would let us record the
		// cancelling user.
		row.StatusDescription = "cancelled via approval link"
	}
	return row
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
		// Non-completed rows count toward TotalPurchases and their specific
		// bucket (pending / failed / expired) but are excluded from the
		// dollar totals — the money hasn't been committed for any of those
		// states. "completed" and unset (legacy DB rows that pre-date the
		// status field) both count as completed.
		switch p.Status {
		case "pending", "notified":
			summary.TotalPending++
			continue
		case "failed":
			summary.TotalFailed++
			continue
		case "expired":
			summary.TotalExpired++
			continue
		}
		summary.TotalCompleted++
		summary.TotalUpfront += p.UpfrontCost
		summary.TotalMonthlySavings += p.EstimatedSavings
	}
	summary.TotalAnnualSavings = summary.TotalMonthlySavings * 12
	return summary
}
