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

	filters, err := parseHistoryFilters(params)
	if err != nil {
		return nil, err
	}

	completed, err := h.fetchPurchaseHistory(ctx, filters)
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
	// completed history — log, skip, continue. The same filter set is applied
	// here (in-memory against the synthesised row's recommendations and
	// scheduled_date) so the two halves of the merged response are
	// consistently scoped (issue #701).
	extra := h.fetchExecutionsAsHistory(ctx, filters)

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
// History view loads alongside completed purchases. Issue #372 assumed
// approval executes synchronously (approved -> completed/failed inside one
// HTTP request) and so excluded "approved"; issue #621 showed that
// assumption breaks under interruption: a Lambda timeout or a crash mid-
// execution leaves the row stuck in "approved"/"running"/"paused", in
// neither purchase_history nor this list, and it silently vanishes — the
// worst failure mode for a financial action (the user can't tell whether
// the purchase fired and may re-approve). So we now load those in-flight
// states too, rendered with a clear "in progress" badge rather than as a
// (misleading) completed row.
//
// "completed" is also loaded, but fetchExecutionsAsHistory synthesises a
// row for it ONLY when the execution carries a non-empty Error — the
// audit-gap case where the purchase succeeded but its purchase_history
// write failed (issue #621 secondary path). A normal completed execution
// has Error=="" and is skipped here so it surfaces exactly once via its
// purchase_history rows (no duplicate). The execution row's PurchaseID is
// the ExecutionID while a purchase_history row's is the CommitmentID, so
// the keys never collide even when both happen to render.
//
// "partially_completed" (issue #642) is loaded and ALWAYS synthesised: a
// partial run committed some recs to purchase_history (those render from the
// DB rows) and failed others. The synthesised execution row carries the
// partial-failure marker and is flagged IsAuditGap so its execution-level
// dollars are excluded from the dashboard totals — the committed dollars are
// already counted via the per-rec purchase_history rows that succeeded.
var historyExecutionStatuses = []string{"pending", "notified", "approved", "running", "paused", "completed", "partially_completed", "failed", "expired", "cancelled"}

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
//
// The filter set (issue #701) is applied in Go against the synthetic row:
// provider via the recs' collapsed provider, account via CloudAccountID,
// date via ScheduledDate. Filtering after expireIfStale so a row that
// transitioned to expired during this request still gets evaluated against
// the same predicates as a DB row.
func (h *Handler) fetchExecutionsAsHistory(ctx context.Context, filters historyFilters) []config.PurchaseHistoryRecord {
	executions, err := h.config.GetExecutionsByStatuses(ctx, historyExecutionStatuses, config.DefaultListLimit)
	if err != nil {
		logging.Warnf("history: failed to load non-completed executions: %v", err)
		return nil
	}
	if len(executions) == 0 {
		return nil
	}
	approver := h.resolvePendingApproverEmail(ctx)
	userEmailCache := h.resolveUserEmails(ctx, executions)
	out := make([]config.PurchaseHistoryRecord, 0, len(executions))
	for _, exec := range executions {
		// Dedup: a normal completed execution is already represented by its
		// purchase_history rows. Skip it here so it shows exactly once. Only
		// completed executions carrying an audit-gap Error (history write
		// failed after a successful purchase, issue #621) are synthesised —
		// those have no purchase_history row to collide with.
		if exec.Status == "completed" && exec.Error == "" {
			continue
		}
		exec = h.expireIfStale(ctx, exec)
		if !filters.matchesExecution(exec) {
			continue
		}
		var createdByEmail string
		if exec.CreatedByUserID != nil {
			createdByEmail = userEmailCache[*exec.CreatedByUserID]
		}
		out = append(out, executionToHistoryRow(exec, approver, createdByEmail))
	}
	return out
}

// resolveUserEmails builds a map of user-ID to email by calling GetUser once
// per unique non-nil CreatedByUserID found in the execution list. Lookup
// failures are logged and skipped — a missing email degrades gracefully (the
// UI falls back to the raw UUID via created_by_user_id). Called once per
// /api/history request so the cost is proportional to the number of distinct
// creators, not the number of execution rows.
func (h *Handler) resolveUserEmails(ctx context.Context, executions []config.PurchaseExecution) map[string]string {
	seen := make(map[string]struct{})
	for _, exec := range executions {
		if exec.CreatedByUserID != nil && *exec.CreatedByUserID != "" {
			seen[*exec.CreatedByUserID] = struct{}{}
		}
	}
	out := make(map[string]string, len(seen))
	for uid := range seen {
		user, err := h.auth.GetUser(ctx, uid)
		if err != nil {
			logging.Warnf("history: failed to resolve email for user %s: %v", uid, err)
			continue
		}
		out[uid] = user.Email
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
// "send failed: Missing domain"). createdByEmail is the resolved email for
// the execution's creator (empty when not resolvable).
func executionToHistoryRow(exec config.PurchaseExecution, approver, createdByEmail string) config.PurchaseHistoryRecord {
	var accountID string
	if exec.CloudAccountID != nil {
		accountID = *exec.CloudAccountID
	} else {
		// Web-initiated bulk-purchase executions (handler_purchases.go's
		// buildPendingExecution) never populate exec.CloudAccountID — the
		// per-rec CloudAccountID is the canonical source. Fall back to that
		// so the Approval queue's Account cell shows the actual account ID
		// instead of "-". Returns "" when recs disagree (a basket that
		// genuinely spans accounts honestly renders as the dash fallback
		// rather than a misleading single account).
		accountID = collapseRecommendationAccount(exec.Recommendations)
	}
	var createdBy string
	if exec.CreatedByUserID != nil {
		createdBy = *exec.CreatedByUserID
	}
	var retryExecID string
	if exec.RetryExecutionID != nil {
		retryExecID = *exec.RetryExecutionID
	}
	row := config.PurchaseHistoryRecord{
		AccountID:          accountID,
		PurchaseID:         exec.ExecutionID,
		Timestamp:          exec.ScheduledDate,
		Provider:           collapseRecommendationProvider(exec.Recommendations),
		Count:              len(exec.Recommendations),
		PlanID:             exec.PlanID,
		Status:             exec.Status,
		CreatedByUserID:    createdBy,
		CreatedByUserEmail: createdByEmail,
		RetryExecutionID:   retryExecID,
		RetryAttemptN:      exec.RetryAttemptN,
	}
	projectRecommendationFields(&row, exec)
	// Compute ops_hint at read time (issue #47, Q3) so updates to the
	// persistent-failure map land instantly without a re-collect. Only
	// failed rows can carry a hint — the resolver returns "" for empty
	// inputs, so this is safe to call unconditionally.
	if exec.Status == "failed" {
		row.OpsHint = resolveOpsHint(exec.Error)
	}
	annotateHistoryRowByStatus(&row, exec, approver)
	return row
}

// annotateHistoryRowByStatus fills in Approver + StatusDescription on the
// row based on exec.Status. Split from executionToHistoryRow to keep the
// switch below under the gocyclo threshold.
func annotateHistoryRowByStatus(row *config.PurchaseHistoryRecord, exec config.PurchaseExecution, approver string) {
	switch exec.Status {
	case "pending", "notified":
		row.Approver = approver
	case "failed":
		row.StatusDescription = exec.Error
	case "expired":
		row.StatusDescription = "approval link expired (not approved within 7 days)"
	case "cancelled":
		annotateCancelled(row, exec, approver)
	case "approved", "running":
		// In-flight (issue #621): approved/running rows are NOT terminal —
		// the synchronous AWS purchase is mid-execution or got interrupted
		// (Lambda timeout / crash). Resolve who approved (so the user knows
		// who to ask) and overlay an "in progress" note so the UI never
		// renders this as a finished purchase.
		annotateApproved(row, exec, approver)
		row.StatusDescription = "approved — purchase in progress"
	case "paused":
		row.Approver = approver
		row.StatusDescription = "purchase paused — resume or cancel from the plan"
	case "partially_completed":
		// #642: some recs committed, some failed. The committed recs are
		// surfaced via their own purchase_history rows; this synthesised row
		// is the audit flag for the failures. Flag IsAuditGap so the dashboard
		// excludes its execution-level dollars (the committed dollars are
		// counted on the per-rec purchase_history rows, not here) — same
		// double-count guard as the audit-gap completed case below.
		row.IsAuditGap = true
		annotateApproved(row, exec, approver)
		row.StatusDescription = "partially completed — some commitments succeeded, others failed: " + exec.Error
	case "completed":
		// Only audit-gap completed executions reach here (fetchExecutionsAsHistory
		// skips clean completed rows). exec.Error carries why the history write
		// failed after a successful purchase — surface it so the purchase is
		// visible despite the missing purchase_history row (issue #621). Flag the
		// row so the dollar accounting excludes its execution-level total without
		// relying on StatusDescription being set (which annotateApproved may also
		// populate).
		row.IsAuditGap = true
		annotateApproved(row, exec, approver)
		if exec.Error != "" {
			row.StatusDescription = "purchase completed but its history record could not be saved: " + exec.Error
		}
	}
}

// annotateCancelled resolves who cancelled the execution:
//  1. exec.CancelledBy — populated by the session-authed deep-link flow;
//     exact session-authed click attribution.
//  2. approver — the notification inbox that received the cancel token;
//     authoritative accountable party but not necessarily the clicker.
//     Used on legacy token-only paths (async workers, old email clicks).
func annotateCancelled(row *config.PurchaseHistoryRecord, exec config.PurchaseExecution, approver string) {
	if exec.CancelledBy != nil && *exec.CancelledBy != "" {
		row.Approver = *exec.CancelledBy
		row.StatusDescription = "cancelled by " + *exec.CancelledBy
		return
	}
	if approver != "" {
		row.Approver = approver
		row.StatusDescription = "cancelled by " + approver + " (via approval link)"
		return
	}
	row.StatusDescription = "cancelled via approval link"
}

// annotateApproved resolves who approved the execution using the same
// two-tier lookup as annotateCancelled — session-authed click wins,
// notification inbox fills in otherwise.
func annotateApproved(row *config.PurchaseHistoryRecord, exec config.PurchaseExecution, approver string) {
	if exec.ApprovedBy != nil && *exec.ApprovedBy != "" {
		row.Approver = *exec.ApprovedBy
		row.StatusDescription = "approved by " + *exec.ApprovedBy
		return
	}
	if approver != "" {
		row.Approver = approver
	}
}

// projectRecommendationFields fills the row's resource/cost/term columns from
// the execution's recommendations so an in-progress (approved/running/paused)
// or audit-gap row renders with the SAME shape the completed purchase_history
// row would (issue #631). The completed row carries per-commitment service /
// resource_type / region / term / monthly_cost / upfront / savings; the
// synthetic execution row must too, or a single valid 1yr t4g.nano RI shows up
// as "0 Years / multiple / $0" because Term/MonthlyCost/ResourceType were never
// mapped.
//
//   - Single-rec execution (the common single-RI case in #631): project that
//     rec's fields verbatim — exactly what the completed row stores per
//     commitment. Costs come from the rec itself (UpfrontCost / MonthlyCost /
//     Savings), matching how purchase_history persists per-commitment dollars.
//   - Multi-rec execution: keep the aggregate display — "N commitment(s)" /
//     "multiple" region, term collapsed to the shared value (0 when recs
//     disagree), and execution-level cost totals. A single row cannot honestly
//     show one resource type or term for a heterogeneous basket.
func projectRecommendationFields(row *config.PurchaseHistoryRecord, exec config.PurchaseExecution) {
	recs := exec.Recommendations
	if len(recs) == 1 {
		r := recs[0]
		row.Service = r.Service
		row.ResourceType = r.ResourceType
		row.Region = r.Region
		row.Term = r.Term
		row.Payment = r.Payment
		row.UpfrontCost = r.UpfrontCost
		row.EstimatedSavings = r.Savings
		if r.MonthlyCost != nil {
			row.MonthlyCost = *r.MonthlyCost
		}
		return
	}
	row.Region = "multiple"
	row.ResourceType = fmt.Sprintf("%d commitment(s)", len(recs))
	row.Service = collapseRecommendationService(recs)
	row.Term = collapseRecommendationTerm(recs)
	row.Payment = collapseRecommendationPayment(recs)
	row.MonthlyCost = sumRecommendationMonthlyCost(recs)
	row.UpfrontCost = exec.TotalUpfrontCost
	row.EstimatedSavings = exec.EstimatedSavings
}

// collapseRecommendationService returns the single service shared by every
// recommendation in a multi-rec execution, or "multiple" when they differ (or
// the slice is empty).
func collapseRecommendationService(recs []config.RecommendationRecord) string {
	if len(recs) == 0 {
		return "multiple"
	}
	s := recs[0].Service
	for _, r := range recs[1:] {
		if r.Service != s {
			return "multiple"
		}
	}
	return s
}

// collapseRecommendationTerm returns the term shared by every recommendation in
// a multi-rec execution, or 0 when they differ (or the slice is empty). 0
// renders as "" in the UI rather than a misleading single term for a basket
// that spans 1yr and 3yr commitments.
func collapseRecommendationTerm(recs []config.RecommendationRecord) int {
	if len(recs) == 0 {
		return 0
	}
	t := recs[0].Term
	for _, r := range recs[1:] {
		if r.Term != t {
			return 0
		}
	}
	return t
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

// collapseRecommendationPayment returns the payment option shared by every
// recommendation in an execution, or "" when they disagree (or the slice is
// empty). Empty renders as the dash fallback in the Approval queue rather
// than a misleading single payment string for a basket that mixes options.
func collapseRecommendationPayment(recs []config.RecommendationRecord) string {
	if len(recs) == 0 {
		return ""
	}
	p := recs[0].Payment
	for _, r := range recs[1:] {
		if r.Payment != p {
			return ""
		}
	}
	return p
}

// collapseRecommendationAccount returns the cloud-account ID shared by every
// recommendation in an execution, or "" when they disagree (or none have one
// set). Used as the Account fallback when exec.CloudAccountID is nil —
// notably for web-initiated bulk purchases, which only populate the per-rec
// CloudAccountID and leave the execution-level field blank.
func collapseRecommendationAccount(recs []config.RecommendationRecord) string {
	if len(recs) == 0 {
		return ""
	}
	var first string
	if recs[0].CloudAccountID != nil {
		first = *recs[0].CloudAccountID
	}
	for _, r := range recs[1:] {
		var cur string
		if r.CloudAccountID != nil {
			cur = *r.CloudAccountID
		}
		if cur != first {
			return ""
		}
	}
	return first
}

// sumRecommendationMonthlyCost adds up the per-rec MonthlyCost values in a
// multi-rec execution so the Approval queue's Monthly Cost cell shows the
// committed recurring spend for the full basket. Nil per-rec entries
// contribute 0 (the provider API did not return a monthly breakdown for
// that rec) — the same treatment as the single-rec branch, which only
// copies MonthlyCost when non-nil and otherwise leaves the row's field at
// the zero value.
func sumRecommendationMonthlyCost(recs []config.RecommendationRecord) float64 {
	var total float64
	for _, r := range recs {
		if r.MonthlyCost != nil {
			total += *r.MonthlyCost
		}
	}
	return total
}

// MaxHistoryDateRangeDays caps the inclusive start/end window the History
// handler accepts on a single request. Mirrors the analytics cap (issue
// #414 / PR #529): an unbounded range turns the WHERE-on-timestamp into a
// full-table scan over purchase_history, which the dashboard frontend
// neither needs nor renders coherently past a year. 366 admits a full leap
// year and rejects anything larger with 400.
const MaxHistoryDateRangeDays = 366

// historyFilters carries the shared filter set used by both halves of the
// merged /api/history response: the SQL path (purchase_history rows in
// fetchPurchaseHistory) and the in-memory path (synthesised execution rows
// in fetchExecutionsAsHistory). Keeping them in one struct guarantees the
// two halves stay scoped consistently — the bug behind issue #701 was that
// the executions path ignored the filters the SQL path was supposed to apply.
//
// Two account inputs are accepted but they are NOT interchangeable:
//   - LegacyAccountID is the singular `account_id` query param. It is the
//     cloud-provider account number (VARCHAR(20) — e.g. "123456789012"),
//     matched against purchase_history.account_id by GetPurchaseHistory.
//     Only the no-other-filters fast path consumes it; the filtered SQL
//     path ignores it because cloud_account_id is a UUID column.
//   - AccountIDs is the plural `account_ids` query param, a list of UUIDs
//     matched against purchase_history.cloud_account_id (the
//     cloud_accounts FK). This is what the frontend sends.
//
// HasDate is true iff start OR end was supplied. When false the Start/End
// times are zero-valued and the SQL/in-memory date predicates are skipped
// entirely (so legacy clients that don't send dates keep working).
type historyFilters struct {
	Provider        string
	LegacyAccountID string
	AccountIDs      []string
	HasDate         bool
	Start           time.Time
	End             time.Time
	Limit           int
}

// parseHistoryFilters validates and normalises the /api/history query string.
// Returns a 400 ClientError for malformed provider, non-UUID account_ids, a
// start/end that doesn't parse as YYYY-MM-DD, an inverted range
// (start > end), or a range exceeding MaxHistoryDateRangeDays.
func parseHistoryFilters(params map[string]string) (historyFilters, error) {
	var f historyFilters

	provider := params["provider"]
	if provider == "all" {
		provider = "" // "all" is the explicit "no filter" sentinel
	}
	if err := validateProvider(provider); err != nil {
		return f, err
	}
	f.Provider = provider

	accountIDs, err := parseAccountIDs(params["account_ids"])
	if err != nil {
		return f, NewClientError(400, err.Error())
	}
	f.AccountIDs = accountIDs
	f.LegacyAccountID = params["account_id"]

	start, end, hasDate, err := parseHistoryDateRange(params["start"], params["end"])
	if err != nil {
		return f, err
	}
	f.HasDate = hasDate
	f.Start = start
	f.End = end

	limit := config.DefaultListLimit
	if s := params["limit"]; s != "" {
		fmt.Sscanf(s, "%d", &limit)
	}
	if limit <= 0 {
		limit = config.DefaultListLimit
	}
	if limit > config.MaxListLimit {
		limit = config.MaxListLimit
	}
	f.Limit = limit

	return f, nil
}

// parseHistoryDateRange parses optional YYYY-MM-DD start/end strings. Both
// empty -> no date filter (hasDate=false, returned times are zero). One or
// both populated -> hasDate=true; an unset bound defaults to span the
// maximum allowed window from the supplied side. The window is capped at
// MaxHistoryDateRangeDays to mirror PR #529 (issue #414) and prevent a full-
// table-scan DoS via start=1970-01-01&end=2100-12-31.
//
// YYYY-MM-DD only (per issue #701 spec): the frontend's <input type="date">
// fields emit exactly that format, so accepting RFC 3339 here would be a
// surface area not exercised in production and a divergence from the
// analytics handler's broader format set.
func parseHistoryDateRange(startStr, endStr string) (time.Time, time.Time, bool, error) {
	if startStr == "" && endStr == "" {
		return time.Time{}, time.Time{}, false, nil
	}
	start, end, err := parseHistoryDateBounds(startStr, endStr)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	const maxWindow = MaxHistoryDateRangeDays * 24 * time.Hour
	// Fill in the open side, if any, so the absent bound spans the maximum
	// allowed window from the supplied side. This keeps a "start only" or
	// "end only" call meaningful AND still bounded by the same DoS cap.
	switch {
	case startStr == "" && endStr != "":
		start = end.Add(-maxWindow)
	case startStr != "" && endStr == "":
		end = start.Add(maxWindow)
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, false, NewClientError(400,
			"start date must be before or equal to end date")
	}
	if end.Sub(start) > maxWindow {
		return time.Time{}, time.Time{}, false, NewClientError(400,
			fmt.Sprintf("date range too large: maximum allowed range is %d days", MaxHistoryDateRangeDays))
	}
	return start, end, true, nil
}

// parseHistoryDateBounds parses each YYYY-MM-DD side independently. An empty
// string returns the zero value for that side; the caller is responsible for
// substituting a sensible default. The end side is rolled forward to end-of-
// day so a date input is inclusive of the chosen day.
func parseHistoryDateBounds(startStr, endStr string) (time.Time, time.Time, error) {
	const layout = "2006-01-02"
	var start, end time.Time
	if startStr != "" {
		s, err := time.ParseInLocation(layout, startStr, time.UTC)
		if err != nil {
			return time.Time{}, time.Time{}, NewClientError(400,
				"invalid start date format: expected YYYY-MM-DD")
		}
		start = s
	}
	if endStr != "" {
		e, err := time.ParseInLocation(layout, endStr, time.UTC)
		if err != nil {
			return time.Time{}, time.Time{}, NewClientError(400,
				"invalid end date format: expected YYYY-MM-DD")
		}
		// Make end inclusive of the requested day (frontend semantics: a
		// user picking 2024-01-31 expects rows from that day to render).
		end = e.Add(24*time.Hour - time.Second)
	}
	return start, end, nil
}

// matchesExecution reports whether a PurchaseExecution should be retained
// after applying the request filters in Go (the SQL path is on
// purchase_history; this in-memory equivalent keeps the two halves of the
// merged response consistently scoped, issue #701).
//
//   - Provider: matches when ANY recommendation in the execution carries
//     the filter value. A single-rec execution collapses to one provider; a
//     multi-rec basket that spans providers (e.g. aws+azure) matches any of
//     them — dropping such an execution because its collapsed display label
//     is "multiple" would hide real activity the user owns.
//   - Account: matches when the execution's CloudAccountID is one of the
//     filtered IDs. Executions with a NULL CloudAccountID are excluded once
//     account_ids is non-empty, mirroring the SQL semantics on
//     purchase_history.cloud_account_id (issue #211).
//   - Date: matches when ScheduledDate is within [Start, End]. Inclusive
//     both sides; End is the end-of-day for YYYY-MM-DD inputs.
func (f historyFilters) matchesExecution(exec config.PurchaseExecution) bool {
	if f.Provider != "" {
		if !executionHasProvider(exec, f.Provider) {
			return false
		}
	}
	if len(f.AccountIDs) > 0 {
		// AccountIDs are UUIDs against cloud_account_id; legacy
		// LegacyAccountID is intentionally NOT folded here because it is a
		// different (VARCHAR(20)) cloud-provider account number that the
		// fast path applies on the SQL side.
		if exec.CloudAccountID == nil {
			return false
		}
		if !stringInSlice(*exec.CloudAccountID, f.AccountIDs) {
			return false
		}
	}
	if f.HasDate {
		if exec.ScheduledDate.Before(f.Start) || exec.ScheduledDate.After(f.End) {
			return false
		}
	}
	return true
}

// executionHasProvider reports whether any of the execution's
// recommendations carries the given provider value.
func executionHasProvider(exec config.PurchaseExecution, provider string) bool {
	for _, r := range exec.Recommendations {
		if r.Provider == provider {
			return true
		}
	}
	return false
}

// stringInSlice is a tiny linear-search helper for the per-execution
// account filter. The slice is bounded by MaxAccountIDsPerRequest (200) so
// a linear scan is comfortably cheaper than the map allocation a set would
// require, and the call is hot only for executions (DB rows go through SQL).
func stringInSlice(needle string, haystack []string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// fetchPurchaseHistory reads purchases from the store, applying the parsed
// filter set. The store-level filtered method (added with issue #701)
// pushes provider / cloud_account_id / timestamp range into SQL; the
// fast-path legacy methods (no-filter, single-account-only) are kept for
// the dashboard/inventory/analytics callers that don't speak the new shape.
//
// LegacyAccountID is honoured only on the fast path (it filters the
// VARCHAR(20) purchase_history.account_id column). When combined with any
// new filter it is ignored: the new filtered method's account predicate is
// on cloud_account_id (UUID), and silently coercing one column's identifier
// into the other would either match nothing or, worse, match the wrong
// rows. Callers using the new filter set should send `account_ids`
// (UUIDs); the legacy singular param is preserved for the dashboard's
// historical, single-cloud-account view.
func (h *Handler) fetchPurchaseHistory(ctx context.Context, filters historyFilters) ([]config.PurchaseHistoryRecord, error) {
	noNewFilters := !filters.HasDate && filters.Provider == "" && len(filters.AccountIDs) == 0
	if noNewFilters {
		if filters.LegacyAccountID != "" {
			return h.config.GetPurchaseHistory(ctx, filters.LegacyAccountID, filters.Limit)
		}
		return h.config.GetAllPurchaseHistory(ctx, filters.Limit)
	}

	var startPtr, endPtr *time.Time
	if filters.HasDate {
		s, e := filters.Start, filters.End
		startPtr = &s
		endPtr = &e
	}
	return h.config.GetPurchaseHistoryFiltered(
		ctx,
		filters.Provider,
		filters.AccountIDs,
		startPtr,
		endPtr,
		filters.Limit,
	)
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
		// bucket (pending / in-progress / failed / expired) but are excluded
		// from the dollar totals — the money hasn't been committed for any of
		// those states. "completed" and unset (legacy DB rows that pre-date
		// the status field) both count as completed.
		switch p.Status {
		case "pending", "notified":
			summary.TotalPending++
			continue
		case "approved", "running", "paused":
			// In-flight (issue #621). Not yet confirmed final — keep out of the
			// committed dollar totals so an interrupted approval can't inflate
			// reported spend/savings.
			summary.TotalInProgress++
			continue
		case "failed":
			summary.TotalFailed++
			continue
		case "expired":
			summary.TotalExpired++
			continue
		}
		summary.TotalCompleted++
		// Audit-gap completed rows (issue #621) are synthesised execution rows
		// whose purchase_history write failed. Count them as completed (the
		// money WAS committed and they must stay visible) but exclude their
		// execution-level dollars: a partially-saved multi-rec execution can
		// have BOTH some purchase_history rows AND this synthesised row, and
		// adding the full execution total here would double-count the recs that
		// did save. The dollars are surfaced via the individual purchase_history
		// rows that succeeded; the synthesised row is the audit flag, not a
		// money source. IsAuditGap is the explicit marker: real purchase_history
		// rows loaded from the DB always leave it false, so a future change that
		// annotates completed DB rows can't silently drop them from the totals.
		if p.IsAuditGap {
			continue
		}
		summary.TotalUpfront += p.UpfrontCost
		summary.TotalMonthlySavings += p.EstimatedSavings
	}
	summary.TotalAnnualSavings = summary.TotalMonthlySavings * 12
	return summary
}
