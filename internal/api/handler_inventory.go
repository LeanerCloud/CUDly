// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"sort"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
)

// knownProviders is the ordered list of providers we always include in
// the coverage response so the frontend can rely on a stable ordering.
// Providers with no usage data get an empty Services slice and a nil
// OverallCoveragePct — the frontend renders "No usage detected" for them.
var knownProviders = []string{"aws", "azure", "gcp"}

// listActiveCommitments handles GET /api/inventory/commitments.
//
// Returns one row per *active* (non-expired) PurchaseHistoryRecord, with
// the account name joined in for display and expiry computed via the
// shared commitmentExpiry helper. Rows are sorted by EndDate ascending
// so the most-actionable (soonest-expiring) commitments float to the
// top — matches the dashboard's "what should I renew next?" framing
// without forcing the UI to re-sort client-side.
//
// Auth: `view:purchases`. Purchase history is the same source we already
// gate behind that permission for the /api/history page, so reusing the
// resource keeps the role matrix consistent. The session's
// allowed_accounts list is then applied via
// filterPurchaseHistoryByAllowedAccounts so a restricted-access user
// sees only the commitments belonging to accounts they're entitled to.
func (h *Handler) listActiveCommitments(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	purchases, err := h.fetchCommitmentRecords(ctx, params)
	if err != nil {
		return nil, err
	}

	purchases, err = h.filterPurchaseHistoryByAllowedAccounts(ctx, session, purchases)
	if err != nil {
		return nil, err
	}

	nameByID := h.resolveAccountNamesByID(ctx)
	now := time.Now()

	commitments := make([]InventoryCommitment, 0, len(purchases))
	for _, p := range purchases {
		if !isActiveCommitment(p, now) {
			continue
		}
		commitments = append(commitments, buildInventoryCommitment(p, nameByID[p.AccountID]))
	}

	// Soonest-expiring first. The dashboard framing is "what do I need to
	// renew next" — surfacing the imminent end_date on top means the
	// frontend doesn't need to re-sort and the user's eye lands on the
	// most-actionable rows immediately.
	sort.SliceStable(commitments, func(i, j int) bool {
		return commitments[i].EndDate.Before(commitments[j].EndDate)
	})

	return InventoryCommitmentsResponse{Commitments: commitments}, nil
}

// fetchCommitmentRecords reads purchase history from the store, honouring
// optional `account_id` and `provider` query params the same way
// fetchPurchaseHistory does for /api/history. Limit defaults to
// MaxListLimit — commitments are a strict subset of purchase history (we
// drop expired rows before returning) so a high cap is appropriate; an
// over-truncation here would silently hide rows the user is entitled to.
//
// `provider` filtering is applied in-memory after the store read so that
// the existing single-account and all-accounts store paths remain
// unchanged; the record set is small enough that a post-read filter has
// negligible cost.
func (h *Handler) fetchCommitmentRecords(ctx context.Context, params map[string]string) ([]config.PurchaseHistoryRecord, error) {
	var rows []config.PurchaseHistoryRecord
	var err error
	if accountID := params["account_id"]; accountID != "" {
		rows, err = h.config.GetPurchaseHistory(ctx, accountID, config.MaxListLimit)
	} else {
		rows, err = h.config.GetAllPurchaseHistory(ctx, config.MaxListLimit)
	}
	if err != nil {
		return nil, err
	}

	// Apply provider filter in-memory. An absent or empty param means
	// "all providers". Case-sensitive match — providers are always
	// lowercase in the store (aws, azure, gcp).
	if provider := params["provider"]; provider != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.Provider == provider {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	return rows, nil
}

// buildInventoryCommitment maps a PurchaseHistoryRecord to the
// response-layer InventoryCommitment. The ID is namespaced by account so
// the JSON payload is globally unique without a DB schema change —
// purchase_id alone is only unique within an account.
func buildInventoryCommitment(p config.PurchaseHistoryRecord, accountName string) InventoryCommitment {
	return InventoryCommitment{
		ID:               p.AccountID + ":" + p.PurchaseID,
		Provider:         p.Provider,
		AccountID:        p.AccountID,
		AccountName:      accountName,
		Service:          p.Service,
		ResourceType:     p.ResourceType,
		Region:           p.Region,
		Count:            p.Count,
		TermYears:        p.Term,
		PaymentOption:    p.Payment,
		StartDate:        p.Timestamp,
		EndDate:          commitmentExpiry(p),
		UpfrontCost:      p.UpfrontCost,
		MonthlyCost:      p.MonthlyCost,
		EstimatedSavings: p.EstimatedSavings,
		Status:           "active",
	}
}

// getCoverageBreakdown handles GET /api/inventory/coverage.
//
// Returns per-provider, per-service coverage breakdowns computed from
// two data sources already available in the system:
//   - Active commitments (purchase history): their MonthlyCost is the
//     "covered" portion of monthly spend.
//   - Recommendations (scheduler): their Savings represent the remaining
//     on-demand gap that could still be committed.
//
// Coverage% per service = covered / (covered + on_demand).
// A provider with no data in either source gets Services=nil and
// OverallCoveragePct=nil — the frontend renders "No usage detected".
//
// Auth: `view:purchases`. Same gate as /api/inventory/commitments;
// both pull from purchase history so the role is consistent.
func (h *Handler) getCoverageBreakdown(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	// --- covered: active commitments ----------------------------------------
	purchases, err := h.fetchCommitmentRecords(ctx, params)
	if err != nil {
		return nil, err
	}
	purchases, err = h.filterPurchaseHistoryByAllowedAccounts(ctx, session, purchases)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	// coveredByKey accumulates MonthlyCost by "provider:service".
	coveredByKey := make(map[string]float64)
	for _, p := range purchases {
		if !isActiveCommitment(p, now) {
			continue
		}
		coveredByKey[p.Provider+":"+p.Service] += p.MonthlyCost
	}

	// --- on-demand gap: recommendations -------------------------------------
	// Recommendations represent uncommitted demand that could be purchased.
	// Their Savings field is the monthly on-demand cost of the uncovered gap.
	recs, err := h.scheduler.ListRecommendations(ctx, config.RecommendationFilter{})
	if err != nil {
		// Non-fatal: recommendations are best-effort for coverage display.
		// An empty rec list is treated as "no uncovered gap" — coverage
		// will show covered-only totals rather than failing the whole page.
		recs = nil
	}
	// Apply the same allowed-accounts scope as the commitments query above so
	// restricted users cannot infer data from accounts they are not entitled to.
	if recs != nil {
		recs, err = h.filterRecommendationsByAllowedAccounts(ctx, session, recs)
		if err != nil {
			return nil, err
		}
	}
	onDemandByKey := aggregateOnDemandByKey(recs, params["provider"])

	return buildCoverageBreakdown(coveredByKey, onDemandByKey), nil
}

// aggregateOnDemandByKey builds the "provider:service" → monthly-savings map
// from a recommendation slice, applying an optional provider filter.
// Pulled out of getCoverageBreakdown to keep that function under the
// cyclomatic limit.
func aggregateOnDemandByKey(recs []config.RecommendationRecord, providerFilter string) map[string]float64 {
	out := make(map[string]float64)
	for _, rec := range recs {
		if providerFilter != "" && rec.Provider != providerFilter {
			continue
		}
		out[rec.Provider+":"+rec.Service] += rec.Savings
	}
	return out
}

// buildCoverageBreakdown constructs the CoverageBreakdownResponse from
// the two pre-aggregated maps. Extracted so it can be unit-tested without
// requiring a full Handler.
//
// coveredByKey and onDemandByKey are both keyed by "provider:service".
// A provider that appears in neither map gets Services=nil and
// OverallCoveragePct=nil (no usage detected).
func buildCoverageBreakdown(
	coveredByKey map[string]float64,
	onDemandByKey map[string]float64,
) CoverageBreakdownResponse {
	// Collect all (provider, service) keys appearing in either map.
	type providerService struct{ provider, service string }
	keySet := make(map[providerService]struct{})
	for k := range coveredByKey {
		provider, service := splitProviderService(k)
		keySet[providerService{provider, service}] = struct{}{}
	}
	for k := range onDemandByKey {
		provider, service := splitProviderService(k)
		keySet[providerService{provider, service}] = struct{}{}
	}

	// Group by provider, then build per-service rows.
	providerSvcMap := make(map[string][]CoverageServiceRow)
	for ps := range keySet {
		key := ps.provider + ":" + ps.service
		covered := coveredByKey[key]
		onDemand := onDemandByKey[key]
		row := CoverageServiceRow{
			Service:         ps.service,
			CoveredMonthly:  covered,
			OnDemandMonthly: onDemand,
			CoveragePct:     coveragePct(covered, onDemand),
		}
		providerSvcMap[ps.provider] = append(providerSvcMap[ps.provider], row)
	}

	// Sort service rows within each provider for stable output.
	for p := range providerSvcMap {
		rows := providerSvcMap[p]
		sort.SliceStable(rows, func(i, j int) bool {
			return rows[i].Service < rows[j].Service
		})
		providerSvcMap[p] = rows
	}

	// Emit one section per known provider in canonical order; providers
	// with no data get nil Services and nil OverallCoveragePct so the
	// frontend can show "No usage detected" rather than an empty table.
	sections := make([]ProviderCoverageSection, 0, len(knownProviders))
	for _, p := range knownProviders {
		rows := providerSvcMap[p]
		if len(rows) == 0 {
			sections = append(sections, ProviderCoverageSection{
				Provider:           p,
				Services:           nil,
				OverallCoveragePct: nil,
			})
			continue
		}

		var totalCovered, totalOnDemand float64
		for _, r := range rows {
			totalCovered += r.CoveredMonthly
			totalOnDemand += r.OnDemandMonthly
		}
		sections = append(sections, ProviderCoverageSection{
			Provider:           p,
			Services:           rows,
			OverallCoveragePct: coveragePct(totalCovered, totalOnDemand),
		})
	}

	return CoverageBreakdownResponse{Providers: sections}
}

// coveragePct returns covered / (covered + onDemand) * 100 as a *float64.
// Returns nil when both inputs are zero (no usage signal) to preserve the
// "absent" semantic per feedback_nullable_not_zero — callers should render
// nil as "N/A", not "0%".
func coveragePct(covered, onDemand float64) *float64 {
	total := covered + onDemand
	if total == 0 {
		return nil
	}
	pct := covered / total * 100
	return &pct
}

// splitProviderService splits a "provider:service" key back into its two
// parts. The separator is always the first colon so service names
// containing colons (unlikely but possible) are preserved.
func splitProviderService(key string) (provider, service string) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
