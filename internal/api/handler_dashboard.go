// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

func (h *Handler) getDashboardSummary(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (*DashboardSummaryResponse, error) {
	// Dashboard aggregates recommendations + purchase history. Gate on
	// view:recommendations — the closest existing resource. No dedicated
	// "dashboard" resource in the permission model.
	session, err := h.requirePermission(ctx, req, "view", "recommendations")
	if err != nil {
		return nil, err
	}

	accountUUIDs, accountExternalIDsByProvider, err := h.resolveDashboardAccountScope(ctx, params)
	if err != nil {
		return nil, err
	}

	// Issue #956 (CR): when the session is account-restricted and no explicit
	// account filter was supplied, scope the commitment metrics to the session's
	// allowed_accounts instead of falling through to all-accounts history. The
	// recommendations half is already gated by filterDashboardRecommendations;
	// without this the commitment KPIs (ActiveCommitments / CommittedMonthly /
	// CurrentCoverage / YTDSavings) would leak other accounts' data to a scoped
	// user. Unrestricted / admin sessions resolve to an empty scope and keep the
	// all-accounts behaviour.
	if len(accountUUIDs) == 0 && len(accountExternalIDsByProvider) == 0 {
		accountUUIDs, accountExternalIDsByProvider, err = h.resolveAllowedAccountScope(ctx, session)
		if err != nil {
			return nil, err
		}
	}

	recommendations, err := h.scheduler.ListRecommendations(ctx, config.RecommendationFilter{
		Provider: params["provider"],
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get recommendations: %w", err)
	}

	recommendations, err = h.filterDashboardRecommendations(ctx, session, recommendations)
	if err != nil {
		return nil, err
	}

	// Issue #196 — cap the headline "potential savings" by the per-account
	// coverage target. Without this scaling the dashboard always reports
	// the un-overridden total, which is misleading once a per-account
	// override has narrowed the user's intended commitment.
	//
	// The resolver is best-effort: a lookup error logs and falls through
	// to the un-scaled total, mirroring the over-show-vs-under-show
	// trade-off baked into Scheduler.applySuppressions / applyAccountOverrides.
	coverageByKey := h.resolveCoverageByAccountKey(ctx, recommendations)

	totalSavings, byService := summarizeRecommendationsWithCoverage(recommendations, coverageByKey)
	targetCoverage := h.resolveTargetCoverage(ctx)
	activeCommitments, committedMonthly, ytdSavings, currentSavingsByService := h.calculateCommitmentMetrics(ctx, accountUUIDs, accountExternalIDsByProvider)

	// Populate CurrentSavings on each per-service bucket so the Home page
	// chart can render the green "Current Savings" bars with real data.
	// Before this fix, CurrentSavings was always zero because the aggregation
	// in summarizeRecommendationsWithCoverage only filled PotentialSavings.
	for svc, monthlySavings := range currentSavingsByService {
		entry := byService[svc]
		entry.CurrentSavings = monthlySavings
		byService[svc] = entry
	}

	return &DashboardSummaryResponse{
		PotentialMonthlySavings: totalSavings,
		TotalRecommendations:    len(recommendations),
		ActiveCommitments:       activeCommitments,
		CommittedMonthly:        committedMonthly,
		CurrentCoverage:         h.calculateCurrentCoverage(totalSavings, committedMonthly),
		TargetCoverage:          targetCoverage,
		YTDSavings:              ytdSavings,
		ByService:               byService,
	}, nil
}

// resolveDashboardAccountScope parses the account filter params and resolves
// them to the dual-column purchase-history filter inputs: the cloud_accounts
// UUIDs and their cloud-provider external account numbers.
// Both are needed because purchase_history rows carry either identifier
// independently and the top-bar chip emits the UUID, so matching only
// cloud_account_id dropped every NULL-cloud_account_id row (issue #701/#498).
//
//   - account_ids (plural, UUIDs): resolved to UUID + external ids via
//     resolveAccountFilterIDs. Takes precedence over the legacy singular param.
//   - account_id (singular legacy): a top-bar chip UUID for current callers or a
//     raw external number for pre-UUID callers; resolved via
//     resolveSingleAccountFilterIDs.
//
// Both return values are nil when neither param is supplied, so the caller
// fetches across all accounts (or, for a restricted session, scopes to the
// session's allowed_accounts — see resolveAllowedAccountScope).
func (h *Handler) resolveDashboardAccountScope(ctx context.Context, params map[string]string) (uuids []string, externalIDsByProvider map[string][]string, err error) {
	parsedUUIDs, parseErr := parseAccountIDs(params["account_ids"])
	if parseErr != nil {
		return nil, nil, NewClientError(400, parseErr.Error())
	}

	if len(parsedUUIDs) > 0 {
		// UUID-based multi-account filter — takes precedence over legacy param.
		uuids, externalIDsByProvider = h.resolveAccountFilterIDs(ctx, parsedUUIDs)
		return uuids, externalIDsByProvider, nil
	}

	// No plural UUID filter: fall back to the legacy singular param.
	uuids, externalIDsByProvider = h.resolveSingleAccountFilterIDs(ctx, params["account_id"])
	return uuids, externalIDsByProvider, nil
}

// resolveAllowedAccountScope resolves a restricted session's allowed_accounts
// into the dual-column purchase-history filter inputs so dashboard commitment
// metrics never include accounts the session can't access (issue #956). It lists
// the cloud accounts, keeps those the session matches via auth.MatchesAccount,
// and resolves their UUIDs through resolveAccountFilterIDs (same code path as an
// explicit filter).
//
// Returns (nil, nil, nil) for unrestricted / admin sessions so the caller keeps
// the all-accounts behaviour. A restricted session that matches no account
// resolves to a non-nil-but-empty UUID set (a sentinel that selects no rows),
// so a scoped user with zero accessible accounts sees zeroed KPIs rather than
// everyone's data.
func (h *Handler) resolveAllowedAccountScope(ctx context.Context, session *Session) (uuids []string, externalIDsByProvider map[string][]string, err error) {
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return nil, nil, nil
	}
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list cloud accounts: %w", err)
	}
	// Non-nil empty slice: a sentinel meaning "scoped to zero accounts" so the
	// dual-column predicate matches no rows (never falls back to all-accounts).
	allowedUUIDs := []string{}
	for _, a := range accounts {
		if auth.MatchesAccount(allowed, a.ID, a.Name) {
			allowedUUIDs = append(allowedUUIDs, a.ID)
		}
	}
	uuids, externalIDsByProvider = h.resolveAccountFilterIDs(ctx, allowedUUIDs)
	return uuids, externalIDsByProvider, nil
}

// filterDashboardRecommendations applies the session's allowed_accounts filter
// to the recommendations list before aggregation so scoped users don't see
// cross-account totals. Admin/unrestricted sessions pass through unchanged.
func (h *Handler) filterDashboardRecommendations(ctx context.Context, session *Session, recs []config.RecommendationRecord) ([]config.RecommendationRecord, error) {
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return recs, nil
	}

	nameByID := h.resolveAccountNamesByID(ctx)
	filtered := recs[:0]
	for _, rec := range recs {
		if rec.CloudAccountID == nil {
			continue
		}
		id := *rec.CloudAccountID
		if auth.MatchesAccount(allowed, id, nameByID[id]) {
			filtered = append(filtered, rec)
		}
	}
	return filtered, nil
}

// summarizeRecommendationsWithCoverage is the dashboard reducer that
// respects per-account, per-service coverage overrides. Each rec's
// contribution is scaled by min(coverage, 100) / 100 looked up in
// coverageByKey via config.AccountConfigKey(account, provider, service).
// Pass coverageByKey=nil to disable scaling (every rec counts fully).
//
// Variants of the same physical-resource cell are deduped to a single
// representative before summing (see bestVariantPerCell) so the per-(term,
// payment) fan-out does not over-report savings; details in the function body.
//
// Recs without a CloudAccountID and recs whose triple has no entry in the
// map all count at full weight — this matches the pre-#196 behaviour for
// un-configured accounts. Zero-coverage configs are excluded from the map
// by resolveCoverageByAccountKey (issue #201) so they also fall through to
// full weight rather than silently zeroing the headline.
//
// Coverage > 100 is capped at 100 so a misconfigured override cannot
// inflate the headline "potential savings" beyond the raw rec total.
//
// CONTRACT: every rec's Savings field represents 100%-coverage potential
// savings. All three upstream APIs satisfy this contract:
//   - AWS Cost Explorer GetReservationPurchaseRecommendation returns a
//     recommended quantity sized for ~100% coverage of historical demand
//     (cmd/helpers.go ApplyCoverage comment, issue #215 audit).
//   - Azure Consumption Reservation Recommendations (NetSavings) returns
//     the savings from purchasing the full recommended quantity.
//   - GCP Recommender PrimaryImpact.CostProjection returns the projected
//     savings from the recommended committed-use discount.
//
// The read-side scaling here (by the operator-configured coverage %) is
// therefore correct: it projects "how much would I save if I only bought
// RIs to cover X% of my instances" against the 100%-coverage baseline
// that every provider gives us. Verified by TestSummarizeRecommendationsWithCoverage_100PctContract.
func summarizeRecommendationsWithCoverage(
	recs []config.RecommendationRecord,
	coverageByKey map[string]float64,
) (float64, map[string]ServiceSavings) {
	// Dedupe to one representative variant per physical-resource cell BEFORE
	// summing. After PR #195's per-(term, payment) fan-out, a single physical
	// resource produces up to 6 rec rows (2 terms x 3 payments). Those rows are
	// mutually-exclusive ALTERNATIVES, not additive: the operator can only buy
	// one commitment for that resource. Summing every variant inflated both the
	// headline total and each by_service[svc].PotentialSavings by ~6x. The
	// headline KPI already avoids this on the frontend by grouping per cell
	// (frontend/src/recommendations.ts cellKey / groupRecsByCell, and the
	// dashboard.ts:139-142 comment about "~6x inflation of summing every
	// variant"); this reducer is the backend equivalent.
	representatives := bestVariantPerCell(recs, coverageByKey)

	var total float64
	byService := make(map[string]ServiceSavings)
	for _, rep := range representatives {
		scaled := rep.scaled
		total += scaled
		svc := byService[rep.rec.Service]
		svc.PotentialSavings += scaled
		// CurrentSavings is the committed/realized monthly savings for the
		// service: the full 100%-coverage potential (rec.Savings) projected
		// down to the operator-configured coverage %. scaledSavings already
		// computes exactly that (rec.Savings * min(coverage,100)/100), so we
		// reuse it rather than re-deriving the coverage lookup. When no
		// coverage override exists the rec falls through to full savings, so
		// CurrentSavings == PotentialSavings (nothing committed-away yet); a
		// configured coverage < 100 pulls CurrentSavings below the potential.
		// Issue #908: this field was previously never set, so the Home
		// chart's current-savings underlay always rendered as $0.
		svc.CurrentSavings += scaled
		byService[rep.rec.Service] = svc
	}
	return total, byService
}

// cellRepresentative is the chosen variant for one physical-resource cell plus
// its already-computed coverage-scaled savings (cached so callers don't re-run
// scaledSavings).
type cellRepresentative struct {
	rec    config.RecommendationRecord
	scaled float64
}

// recCellKey composes the physical-resource cell key for a recommendation:
// (provider, cloud_account_id, service, region, resource_type, engine). Recs
// sharing this key are per-(term, payment) ALTERNATIVES for the same resource,
// not additive entries.
//
// Cross-language duplication note: this MUST stay aligned with the frontend
// cellKey in frontend/src/recommendations.ts, which joins the same six fields
// with "|" in this order: provider, cloud_account_id, service, region,
// resource_type, engine. The two implementations must bucket the exact same
// variants so the backend by_service rollup and the frontend per-cell grouping
// agree. A nil CloudAccountID maps to the empty segment, matching the
// frontend's nullish-coalescing of cloud_account_id to an empty string.
func recCellKey(rec config.RecommendationRecord) string {
	account := ""
	if rec.CloudAccountID != nil {
		account = *rec.CloudAccountID
	}
	return strings.Join([]string{
		rec.Provider,
		account,
		rec.Service,
		rec.Region,
		rec.ResourceType,
		rec.Engine,
	}, "|")
}

// bestVariantPerCell collapses recs to one representative per physical-resource
// cell, keeping the variant with the MAXIMUM coverage-scaled savings. Max (not
// sum) is correct because the variants are mutually-exclusive alternatives, and
// the best realistic single purchase is the per-cell potential a user expects.
// Order of first-seen cells is preserved for deterministic aggregation.
func bestVariantPerCell(
	recs []config.RecommendationRecord,
	coverageByKey map[string]float64,
) []cellRepresentative {
	indexByCell := make(map[string]int, len(recs))
	reps := make([]cellRepresentative, 0, len(recs))
	for _, rec := range recs {
		scaled := scaledSavings(rec, coverageByKey)
		key := recCellKey(rec)
		if idx, ok := indexByCell[key]; ok {
			if scaled > reps[idx].scaled {
				reps[idx] = cellRepresentative{rec: rec, scaled: scaled}
			}
			continue
		}
		indexByCell[key] = len(reps)
		reps = append(reps, cellRepresentative{rec: rec, scaled: scaled})
	}
	return reps
}

// scaledSavings returns rec.Savings * min(max(coverage, 0), 100) / 100 when
// a coverage entry exists for the rec's (account, provider, service) triple.
// Otherwise returns rec.Savings unchanged.
func scaledSavings(rec config.RecommendationRecord, coverageByKey map[string]float64) float64 {
	if rec.CloudAccountID == nil || coverageByKey == nil {
		return rec.Savings
	}
	coverage, ok := coverageByKey[config.AccountConfigKey(*rec.CloudAccountID, rec.Provider, rec.Service)]
	if !ok {
		return rec.Savings
	}
	if coverage <= 0 {
		return 0
	}
	if coverage >= 100 {
		return rec.Savings
	}
	return rec.Savings * coverage / 100
}

// resolveCoverageByAccountKey returns a map of AccountConfigKey -> resolved
// coverage% for every (account, provider, service) triple represented in
// recs. Lookup errors degrade gracefully to a nil map (no scaling applied
// → un-overridden behaviour).
//
// Entries with a resolved coverage of zero are omitted from the map.
// ServiceConfig.Coverage is a float64 whose zero-value means "not configured",
// so including a zero entry would silently scale that account's savings to $0
// even though the operator never set an explicit coverage cap (issue #201).
// When an entry is absent, scaledSavings falls through to full savings,
// matching the pre-#196 behaviour for un-configured accounts.
func (h *Handler) resolveCoverageByAccountKey(ctx context.Context, recs []config.RecommendationRecord) map[string]float64 {
	if len(recs) == 0 {
		return nil
	}
	resolved, err := config.ResolveAccountConfigsForRecs(ctx, h.config, recs)
	if err != nil {
		logging.Errorf("dashboard: failed to resolve per-account configs for coverage cap; using un-scaled totals: %v", err)
		return nil
	}
	if len(resolved) == 0 {
		return nil
	}
	out := make(map[string]float64, len(resolved))
	for k, cfg := range resolved {
		if cfg.Coverage == 0 {
			// Zero is the float64 zero-value, meaning "not configured".
			// Omit the entry so scaledSavings returns full savings
			// rather than silently zeroing the dashboard headline.
			continue
		}
		out[k] = cfg.Coverage
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveTargetCoverage returns the configured default coverage or 80% when
// no global config is set or the configured value is zero.
func (h *Handler) resolveTargetCoverage(ctx context.Context) float64 {
	globalCfg, _ := h.config.GetGlobalConfig(ctx)
	if globalCfg != nil && globalCfg.DefaultCoverage > 0 {
		return globalCfg.DefaultCoverage
	}
	return 80.0
}

// getUpcomingPurchases enumerates pending purchase_executions joined to
// their parent PurchasePlan for display. Mirrors the data flow in
// getPlannedPurchases (handler_purchases.go) so the dashboard widget and
// the Plans page walk the same canonical "what's about to happen" set.
//
// The widget previously enumerated plans and synthesised one row per plan
// from plan.NextExecutionDate. That was wrong because action endpoints
// (DELETE /api/purchases/planned/{id}, pause, resume, run) all target
// purchase_executions.execution_id, not purchase_plans.id; the Cancel
// button could only fire correctly when the row in front of the operator
// IS a real execution. PR #207 worked around it by routing Cancel to
// api.deletePlan(planID), but that deleted the whole plan — too
// aggressive for "skip this scheduled run". This version emits actual
// execution rows so Cancel can target just the planned purchase via
// DELETE /api/purchases/planned/{id}.
func (h *Handler) getUpcomingPurchases(ctx context.Context, req *events.LambdaFunctionURLRequest) (*UpcomingPurchaseResponse, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	executions, err := h.config.GetPendingExecutions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending executions: %w", err)
	}

	plans, err := h.config.ListPurchasePlans(ctx, config.PurchasePlanFilter{})
	if err != nil {
		return nil, fmt.Errorf("failed to get purchase plans: %w", err)
	}
	planMap := make(map[string]*config.PurchasePlan, len(plans))
	for i := range plans {
		planMap[plans[i].ID] = &plans[i]
	}

	// Per-plan access cache mirrors getPlannedPurchases — all executions
	// for the same plan share the same account scope, so we resolve once
	// per plan.
	allowedPlan := make(map[string]bool)

	var upcoming []UpcomingPurchase
	for i := range executions {
		exec := executions[i]
		plan := planMap[exec.PlanID]
		if plan == nil {
			// Orphaned execution (plan was deleted but cleanup missed it).
			// Hide rather than crash; cleanup is a separate concern.
			continue
		}
		ok, err := h.isPlanAllowedCached(ctx, session, exec.PlanID, allowedPlan)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		upcoming = append(upcoming, upcomingFromExecution(plan, &exec))
	}

	return &UpcomingPurchaseResponse{Purchases: upcoming}, nil
}

// upcomingFromExecution projects a (plan, execution) pair onto the
// UpcomingPurchase response. Uses the first service entry from the plan as
// representative — the response shape doesn't support multi-service plans.
// Step number comes from the execution row directly (already stamped by the
// scheduler at instance-create time).
func upcomingFromExecution(plan *config.PurchasePlan, exec *config.PurchaseExecution) UpcomingPurchase {
	var provider, service string
	for _, svcCfg := range plan.Services {
		provider = svcCfg.Provider
		service = svcCfg.Service
		break
	}
	return UpcomingPurchase{
		ExecutionID:      exec.ExecutionID,
		PlanID:           plan.ID,
		PlanName:         plan.Name,
		ScheduledDate:    exec.ScheduledDate.Format("2006-01-02"),
		Provider:         provider,
		Service:          service,
		StepNumber:       exec.StepNumber,
		TotalSteps:       plan.RampSchedule.TotalSteps,
		EstimatedSavings: exec.EstimatedSavings,
		CreatedByUserID:  exec.CreatedByUserID,
	}
}

// planIntersectsAllowed returns true when any of the plan's associated cloud
// accounts is in the allowed list (matched by ID or display name). Returns
// false when the plan has no account rows — scoped users don't get to see
// unattributed plans.
func (h *Handler) planIntersectsAllowed(ctx context.Context, planID string, allowed []string) (bool, error) {
	accounts, err := h.config.GetPlanAccounts(ctx, planID)
	if err != nil {
		return false, fmt.Errorf("failed to get plan accounts: %w", err)
	}
	for _, acct := range accounts {
		if auth.MatchesAccount(allowed, acct.ID, acct.Name) {
			return true, nil
		}
	}
	return false, nil
}

// getPublicInfo returns public information about the CUDly instance (no auth required).
// No rate limiting — this is hit by Terraform deployment checks and the frontend on every page load.
// Sensitive identifiers (API key secret URL, deployment AWS account ID) are intentionally
// absent here; they live on the authenticated GET /api/info/deployment endpoint (#633).
func (h *Handler) getPublicInfo(ctx context.Context, req *events.LambdaFunctionURLRequest) (*PublicInfoResponse, error) {
	// Check if admin exists
	adminExists := false
	if h.auth != nil {
		exists, err := h.auth.CheckAdminExists(ctx)
		if err == nil {
			adminExists = exists
		}
	}

	return &PublicInfoResponse{
		Version:     "1.0.0",
		AdminExists: adminExists,
	}, nil
}

// getDeploymentInfo returns sensitive deployment identifiers for authenticated callers.
// Requires at least AuthUser (enforced by the router). The two fields it returns
// expose the AWS account ID and the Secrets Manager ARN path — neither should be
// reachable without a valid session (#633).
func (h *Handler) getDeploymentInfo(ctx context.Context, _ *events.LambdaFunctionURLRequest) (*DeploymentInfoResponse, error) {
	// Build the AWS Console deep-link to the Secrets Manager secret.
	var apiKeySecretURL string
	if h.secretsARN != "" {
		// Extract region from ARN: arn:aws:secretsmanager:region:account:secret:name
		parts := strings.Split(h.secretsARN, ":")
		if len(parts) >= 4 {
			region := parts[3]
			apiKeySecretURL = fmt.Sprintf("https://%s.console.aws.amazon.com/secretsmanager/secret?name=%s&region=%s",
				region, h.secretsARN, region)
		}
	}

	// Resolve the host AWS account ID so the frontend can distinguish a
	// legitimate ambient-credential execution (cloud_account_id IS NULL
	// because the rec targets the CUDly Lambda's own account) from an
	// orphan execution whose account was deleted (issue #608). The call
	// is best-effort: non-AWS deployments and STS transient failures
	// return "" and the frontend falls back to the "Account deleted"
	// warning label, which is safe.
	deploymentAWSAccountID, _ := h.resolveAWSAccountID(ctx)

	return &DeploymentInfoResponse{
		APIKeySecretURL:        apiKeySecretURL,
		DeploymentAWSAccountID: deploymentAWSAccountID,
	}, nil
}

// commitmentExpiry returns the moment a purchase's commitment term ends.
// Computed from Timestamp + Term*365d. Extracted from
// calculateCommitmentMetrics so the inventory handler can surface the
// same date in its per-row response without re-deriving the math (and
// so a future tweak to "what counts as expired" lands in exactly one
// place). One year is approximated as 365 days — matches the original
// dashboard arithmetic verbatim; leap-year precision isn't material for
// a multi-year RI/SP/CUD term.
func commitmentExpiry(p config.PurchaseHistoryRecord) time.Time {
	termDuration := time.Duration(p.Term) * 365 * 24 * time.Hour
	return p.Timestamp.Add(termDuration)
}

// isActiveCommitment reports whether the purchase is active: its term has not
// yet expired as of `now` AND its status is one of the successful terminal
// states ("" for DB-backed rows where the column is unpersisted, or
// "completed"). Rows synthesised from failed/cancelled/expired executions
// carry a non-empty status other than "completed" and are excluded so they
// do not inflate the committed_monthly KPI. The boundary is strict (After):
// a commitment is active right up to the instant its term ends.
//
// Same predicate shared by the dashboard aggregate and the per-commitment
// inventory endpoint. Status values: see PurchaseHistoryRecord.Status doc.
func isActiveCommitment(p config.PurchaseHistoryRecord, now time.Time) bool {
	// Status is unpersisted (dynamodbav:"-"); DB rows always read back as "".
	// Synthesised rows set it to "failed", "expired", "cancelled", "pending",
	// "notified", "approved", "running", or "paused". Only "" and "completed"
	// represent a commitment that is actually live on the provider.
	if p.Status != "" && p.Status != "completed" {
		return false
	}
	return !now.After(commitmentExpiry(p))
}

// aggregateActiveCommitmentsPerService sums EstimatedSavings of active
// purchase history rows, grouped by their Service field. It applies the
// shared isActiveCommitment gate (term not expired AND a successful status)
// so both the KPI total and the per-service chart breakdowns use exactly the
// same "active" definition.
func aggregateActiveCommitmentsPerService(purchases []config.PurchaseHistoryRecord, now time.Time) map[string]float64 {
	byService := make(map[string]float64)
	for _, p := range purchases {
		if !isActiveCommitment(p, now) {
			continue
		}
		byService[p.Service] += p.EstimatedSavings
	}
	return byService
}

// fetchCommitmentPurchases loads the purchase_history rows that calculateCommitmentMetrics
// aggregates, applying the pre-resolved account scope. Extracted to keep the
// parent under the cyclomatic limit (mirrors the appendAccountPredicate /
// accountMatchesFilters pattern). Returns ok=false on a store error or an
// explicit zero-account scope so the caller emits zeroed KPIs without querying.
//
//   - non-nil but empty accountUUIDs (no external groups): explicit "scoped to
//     zero accounts" sentinel (a restricted session whose allowed_accounts match
//     nothing). Returns (nil, false) WITHOUT querying so a scoped user never sees
//     all-accounts data (issue #956). A nil/empty filter sent to the store would
//     match every row (no WHERE clause), so this must short-circuit.
//   - either accountUUIDs or accountExternalIDsByProvider non-empty:
//     GetPurchaseHistoryFiltered with the dual-column predicate.
//   - both nil: GetAllPurchaseHistory (no account filter — unrestricted session).
func (h *Handler) fetchCommitmentPurchases(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string) ([]config.PurchaseHistoryRecord, bool) {
	const fetchLimit = 1000

	if accountUUIDs != nil && len(accountUUIDs) == 0 && len(accountExternalIDsByProvider) == 0 {
		return nil, false
	}

	var (
		purchases []config.PurchaseHistoryRecord
		err       error
	)
	if len(accountUUIDs) > 0 || len(accountExternalIDsByProvider) > 0 {
		// Account filter: dual-column match so NULL-cloud_account_id rows are
		// counted via their external id.
		purchases, err = h.config.GetPurchaseHistoryFiltered(ctx, config.PurchaseHistoryFilter{
			AccountIDs:            accountUUIDs,
			ExternalIDsByProvider: accountExternalIDsByProvider,
			Limit:                 fetchLimit,
		})
	} else {
		// No account filter: fetch across all accounts.
		purchases, err = h.config.GetAllPurchaseHistory(ctx, fetchLimit)
	}
	if err != nil {
		// Log error but don't fail the dashboard request.
		return nil, false
	}
	return purchases, true
}

// calculateCommitmentMetrics aggregates active-commitment counts and monthly
// savings from purchase history. The account scope arrives pre-resolved as the
// dual-column filter inputs (see resolveDashboardAccountScope); the
// scope-to-query mapping (including the zero-account short-circuit for issue
// #956) lives in fetchCommitmentPurchases.
//
// EstimatedSavings on purchase_history rows is always written in monthly units
// (populated from PurchaseExecution.EstimatedSavings which derives from
// recommendation monthly savings at purchase time, see SavePurchaseHistory
// and the purchase manager). No unit normalisation is needed.
//
// The fourth return value is the per-service breakdown of active
// EstimatedSavings, derived from aggregateActiveCommitmentsPerService so both
// this KPI path (committedMonthly) and the per-service chart use exactly the
// same gate.
func (h *Handler) calculateCommitmentMetrics(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string) (activeCommitments int, committedMonthly, ytdSavings float64, savingsByService map[string]float64) {
	purchases, ok := h.fetchCommitmentPurchases(ctx, accountUUIDs, accountExternalIDsByProvider)
	if !ok {
		return 0, 0, 0, nil
	}

	currentTime := time.Now()
	yearStart := time.Date(currentTime.Year(), 1, 1, 0, 0, 0, 0, time.UTC)

	// Derive the per-service breakdown from the shared primitive so the
	// active-commitment gate is applied consistently everywhere.
	savingsByService = aggregateActiveCommitmentsPerService(purchases, currentTime)
	for _, v := range savingsByService {
		committedMonthly += v
	}

	for _, p := range purchases {
		if !isActiveCommitment(p, currentTime) {
			continue
		}

		activeCommitments++

		// committedMonthly is derived from aggregateActiveCommitmentsPerService
		// above (same gate), so it is intentionally NOT summed here to avoid
		// double-counting. EstimatedSavings is in monthly units (see doc above).
		//
		// YTD savings: count from year start or from purchase date, whichever
		// is later, using a 30-day month approximation (same as original).
		if p.Timestamp.Before(yearStart) {
			monthsSinceYearStart := int(currentTime.Sub(yearStart).Hours() / (24 * 30))
			ytdSavings += p.EstimatedSavings * float64(monthsSinceYearStart)
		} else {
			monthsSincePurchase := int(currentTime.Sub(p.Timestamp).Hours() / (24 * 30))
			ytdSavings += p.EstimatedSavings * float64(monthsSincePurchase)
		}
	}

	return activeCommitments, committedMonthly, ytdSavings, savingsByService
}

// calculateCurrentCoverage calculates the current coverage percentage
func (h *Handler) calculateCurrentCoverage(potentialSavings, committedMonthly float64) float64 {
	if potentialSavings == 0 {
		return 100.0 // No recommendations means 100% coverage
	}

	totalPossible := potentialSavings + committedMonthly
	if totalPossible == 0 {
		return 0
	}

	return (committedMonthly / totalPossible) * 100
}
