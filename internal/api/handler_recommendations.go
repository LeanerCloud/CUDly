// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-lambda-go/events"
)

// parseRecommendationFilter validates all query parameters and builds the DB
// filter. It centralizes the parameter-validation logic so getRecommendations
// stays below the cyclomatic-complexity threshold.
//
// account_ids filter semantics (issue #211):
//
//	account_ids param  | cloud_account_id row | Result
//	-------------------|----------------------|----------------------------
//	absent             | any (incl. NULL)     | included (no SQL filter)
//	non-empty          | NULL                 | excluded (legacy rows are
//	                   |                      |   not "in any account")
//	non-empty          | matches one of IDs   | included
//	non-empty          | doesn't match        | excluded
//	non-empty, contains| matches (account is  | excluded — enforced by
//	  a disabled ID    |   disabled)          |   filterRecommendationsByAllowedAccounts
//	                   |                      |   below, NOT by the SQL filter
//
// The SQL contract lives in config.buildRecommendationFilter
// (store_postgres_recommendations.go). The disabled-account case is enforced by
// filterRecommendationsByAllowedAccounts after the DB read.
//
// min_savings_usd: absolute dollar floor on monthly savings.
// min_savings_pct: effective savings percentage floor (0-100 scale).
// Both are optional; absent or "0" means no floor. Fractions are rejected (a
// user typing "30%" expects a percentage, not $30.5).
func parseRecommendationFilter(params map[string]string) (config.RecommendationFilter, error) {
	// Validate input parameters to prevent injection attacks.
	if err := validateProvider(params["provider"]); err != nil {
		return config.RecommendationFilter{}, err
	}
	if err := validateServiceName(params["service"]); err != nil {
		return config.RecommendationFilter{}, err
	}
	if err := validateRegion(params["region"]); err != nil {
		return config.RecommendationFilter{}, err
	}

	// parseAccountIDs splits, trims, and UUID-validates the comma-separated
	// account_ids query parameter. Returns nil (no filter) when absent.
	// Returns 400 if any entry is not a valid UUID or the list exceeds
	// MaxAccountIDsPerRequest (200). See validation.go::parseAccountIDs.
	accountIDs, err := parseAccountIDs(params["account_ids"])
	if err != nil {
		return config.RecommendationFilter{}, NewClientError(400, err.Error())
	}

	minSavingsUSD, err := parseMinSavingsParam(params["min_savings_usd"], "min_savings_usd")
	if err != nil {
		return config.RecommendationFilter{}, err
	}
	minSavingsPct, err := parseMinSavingsParam(params["min_savings_pct"], "min_savings_pct")
	if err != nil {
		return config.RecommendationFilter{}, err
	}
	if minSavingsPct < 0 || minSavingsPct > 100 {
		return config.RecommendationFilter{}, NewClientError(400, "min_savings_pct must be between 0 and 100")
	}

	return config.RecommendationFilter{
		Provider:      params["provider"],
		Service:       params["service"],
		Region:        params["region"],
		AccountIDs:    accountIDs,
		MinSavingsUSD: minSavingsUSD,
		MinSavingsPct: minSavingsPct,
	}, nil
}

// Recommendations handlers.
func (h *Handler) getRecommendations(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (*RecommendationsResponse, error) {
	// Require view:recommendations permission
	session, err := h.requirePermission(ctx, req, "view", "recommendations")
	if err != nil {
		return nil, err
	}

	// Translate query string to DB-level filter. ListRecommendations pushes
	// these into the WHERE clause so the cache does the pruning.
	filter, err := parseRecommendationFilter(params)
	if err != nil {
		return nil, err
	}

	recommendations, err := h.scheduler.ListRecommendations(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to get recommendations: %w", err)
	}

	// Filter by allowed accounts if the user has restricted access
	recommendations, err = h.filterRecommendationsByAllowedAccounts(ctx, session, recommendations)
	if err != nil {
		return nil, err
	}

	return buildRecommendationsResponse(recommendations), nil
}

// filterRecommendationsByAllowedAccounts filters recommendations to only include
// those belonging to accounts the user is allowed to access. Returns the
// unmodified slice when the user has unrestricted access (empty allowed list).
func (h *Handler) filterRecommendationsByAllowedAccounts(ctx context.Context, session *Session, recs []config.RecommendationRecord) ([]config.RecommendationRecord, error) {
	allowedAccounts, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowedAccounts) {
		return recs, nil
	}

	// Resolve account names so the allowed list can match on either ID or
	// display name. Recommendations only carry CloudAccountID, so the name
	// lookup needs a one-time fetch of the account list.
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{})
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts for filter: %w", err)
	}
	nameByID := make(map[string]string, len(accounts))
	for i := range accounts {
		nameByID[accounts[i].ID] = accounts[i].Name
	}

	filtered := recs[:0]
	for i := range recs {
		if recs[i].CloudAccountID == nil {
			continue
		}
		id := *recs[i].CloudAccountID
		if auth.MatchesAccount(allowedAccounts, id, nameByID[id]) {
			filtered = append(filtered, recs[i])
		}
	}
	return filtered, nil
}

// buildRecommendationsResponse calculates summary statistics and collects
// unique regions from a set of recommendations.
func buildRecommendationsResponse(recommendations []config.RecommendationRecord) *RecommendationsResponse {
	regionSet := make(map[string]struct{})
	var totalSavings, totalUpfront float64
	for i := range recommendations {
		rec := &recommendations[i]
		totalSavings += rec.Savings
		totalUpfront += rec.UpfrontCost
		if rec.Region != "" {
			regionSet[rec.Region] = struct{}{}
		}
		// Surface the compute size (VCPU / MemoryGB) at the top level so
		// the frontend Capacity column can render it without parsing the
		// opaque Details blob. The canonical values live in the typed
		// ComputeDetails nested inside Details; decode them here (the api
		// layer may import pkg/common, config may not) and stamp the
		// pointers when this is a compute rec with a known size. Non-compute
		// recs, legacy rows with empty Details, and decode failures all
		// leave the pointers nil, so JSON omits them and the frontend
		// renders "—".
		stampComputeCapacity(rec)
	}

	regions := make([]string, 0, len(regionSet))
	for r := range regionSet {
		regions = append(regions, r)
	}

	var avgPayback float64
	if totalSavings > 0 {
		avgPayback = totalUpfront / totalSavings
	}

	return &RecommendationsResponse{
		Recommendations: recommendations,
		Summary: RecommendationsSummary{
			TotalCount:          len(recommendations),
			TotalMonthlySavings: totalSavings,
			TotalUpfrontCost:    totalUpfront,
			AvgPaybackMonths:    avgPayback,
		},
		Regions: regions,
	}
}

// stampComputeCapacity decodes a recommendation's opaque Details blob and,
// when it is a ComputeDetails carrying a known instance size (VCPU > 0 &&
// MemoryGB > 0), stamps the top-level VCPU / MemoryGB pointers the frontend
// Capacity column reads (#219). For non-compute services, legacy rows with an
// empty Details, unknown sizes (0), or a decode error it leaves the pointers
// nil so the JSON omits them and the column renders "—" rather than a
// misleading "0 vCPU / 0 GB".
//
// VCPU and MemoryGB must both be present for the pair to be emitted: the
// frontend's formatCapacity treats a missing half as "absent" and renders a
// dash, so emitting one without the other would be inconsistent.
func stampComputeCapacity(rec *config.RecommendationRecord) {
	details, err := common.DecodeServiceDetailsFor(rec.Service, rec.Details)
	if err != nil || details == nil {
		// Decode error or no typed Details for this service: leave the
		// capacity fields absent. A malformed Details blob must not break
		// the listing; the rest of the row is still valid.
		return
	}
	compute, ok := details.(*common.ComputeDetails)
	if !ok || compute == nil {
		return
	}
	if compute.VCPU <= 0 || compute.MemoryGB <= 0 {
		// Unknown size (converter didn't wire a catalog lookup): keep
		// both absent so the frontend renders "—".
		return
	}
	vcpu := compute.VCPU
	mem := compute.MemoryGB
	rec.VCPU = &vcpu
	rec.MemoryGB = &mem
}

// getRecommendationsFreshness returns the cache-freshness state (last
// successful collection timestamp + most recent collection error) so the
// frontend can render a "Data from <N> min ago" indicator and surface a
// warning banner when the last collect was partial or failed.
//
// Gated by view:recommendations permission inside the handler. The route
// itself inherits AuthAdmin (the router default) so the permission check
// is currently defense-in-depth, matching the pattern used by other
// scoped handlers in this package.
func (h *Handler) getRecommendationsFreshness(ctx context.Context, req *events.LambdaFunctionURLRequest) (*config.RecommendationsFreshness, error) {
	if _, err := h.requirePermission(ctx, req, "view", "recommendations"); err != nil {
		return nil, err
	}
	freshness, err := h.config.GetRecommendationsFreshness(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get recommendations freshness: %w", err)
	}
	return freshness, nil
}

// getRecommendationDetail returns the per-id drill-down payload backing
// the Recommendations row-click drawer. Surfaces the data that's already
// computed server-side (provider/service for provenance, savings + count
// for the confidence bucket) on a per-id GET so the listing payload
// stays compact for the common case where the drawer never opens.
//
// Contract documented in issue #44 + RecommendationDetailResponse in
// types.go. Gated by view:recommendations permission. Returns errNotFound
// on unknown id (404) and on ids that exist but belong to an account the
// caller is not allowed to see (matches the existence-disclosure pattern
// used by handler_accounts.go's account lookup).
//
// usage_history is intentionally empty in this first pass: the collector
// pipeline does not yet persist time-series utilization per
// recommendation. Surfacing the missing field as an empty slice (rather
// than a 501) keeps the drawer functional today and means the day the
// collector starts populating it, the frontend automatically picks it
// up. The empty-slice case is documented in known_issues/28.
func (h *Handler) getRecommendationDetail(ctx context.Context, req *events.LambdaFunctionURLRequest, id string) (*RecommendationDetailResponse, error) {
	// Authn/permission gate runs first so an unauthenticated caller
	// can't probe id-shape validation to learn anything about the
	// endpoint's existence.
	session, err := h.requirePermission(ctx, req, "view", "recommendations")
	if err != nil {
		return nil, err
	}

	if id == "" {
		return nil, NewClientError(400, "recommendation id is required")
	}

	// GetRecommendationByID bypasses the account-override filter so that a
	// deep-linked URL to a rec that has been hidden by an account override
	// still resolves. Suppressions are still applied: a fully-suppressed rec
	// (actively dismissed) returns nil and we 404 as before. See issue #214.
	rec, hiddenBy, err := h.scheduler.GetRecommendationByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch recommendation: %w", err)
	}
	if rec == nil {
		return nil, errNotFound
	}

	// Tenant-scoping gate: an authenticated user must not be able to read recs
	// from accounts outside their allowed set, even via the detail endpoint.
	// The existence-disclosure-safe check (match before reveal) is preserved:
	// we checked GetRecommendationByID first and only reach here on a hit, so
	// the caller still cannot distinguish "doesn't exist" from "wrong tenant"
	// via a timing or wording difference.
	visible, err := h.filterRecommendationsByAllowedAccounts(ctx, session, []config.RecommendationRecord{*rec})
	if err != nil {
		return nil, err
	}
	if len(visible) == 0 {
		return nil, errNotFound
	}

	resp := h.buildRecommendationDetail(ctx, &visible[0])
	if len(hiddenBy) > 0 {
		resp.HiddenBy = hiddenBy
	}
	return resp, nil
}

// buildRecommendationDetail assembles the drawer payload from a single
// recommendation record + the global freshness state. The freshness
// lookup is best-effort; on error the provenance note degrades to its
// no-window form so the drawer still renders.
func (h *Handler) buildRecommendationDetail(ctx context.Context, rec *config.RecommendationRecord) *RecommendationDetailResponse {
	return &RecommendationDetailResponse{
		ID:               rec.ID,
		UsageHistory:     []UsagePoint{},
		ConfidenceBucket: confidenceBucketFor(rec.Savings, rec.Count),
		ProvenanceNote:   h.provenanceNoteFor(ctx, rec),
	}
}

// confidenceBucketFor mirrors the heuristic that previously lived
// client-side in frontend/src/recommendations.ts. Centralizing it on
// the server lets future provider-specific tuning land in one place
// without a frontend deploy. Thresholds intentionally match the original
// shim 1:1 so the drawer label doesn't visibly shift on rollout.
//
//	high   = ≥ $200/mo savings AND ≥ 3-instance fleet (both signals)
//	medium = ≥ $50/mo savings (single signal)
//	low    = otherwise
func confidenceBucketFor(savings float64, count int) string {
	if count < 1 {
		count = 1
	}
	if savings >= 200 && count >= 3 {
		return "high"
	}
	if savings >= 50 {
		return "medium"
	}
	return "low"
}

// provenanceNoteFor returns the short "Derived from … last collected …"
// string the drawer renders verbatim. Format mirrors the wording the
// frontend previously assembled inline so the rollout doesn't visibly
// change the drawer copy. Timestamp is RFC3339 UTC; the frontend can
// re-format relative if it wants — keeping the wire format absolute
// avoids ambiguity around the user's locale.
func (h *Handler) provenanceNoteFor(ctx context.Context, rec *config.RecommendationRecord) string {
	provider := providerDisplayName(rec.Provider)
	base := fmt.Sprintf("%s %s recommendation APIs", provider, rec.Service)

	freshness, err := h.config.GetRecommendationsFreshness(ctx)
	if err != nil || freshness == nil || freshness.LastCollectedAt == nil {
		return base + "."
	}
	ts := freshness.LastCollectedAt.UTC().Format(time.RFC3339)
	return fmt.Sprintf("%s · last collected %s", base, ts)
}

// providerDisplayName converts the lowercase provider slug used in
// storage to the canonical display casing used in user-facing strings.
// Mirrors the equivalent helper in frontend/src/recommendations.ts.
func providerDisplayName(provider string) string {
	switch provider {
	case "aws":
		return "AWS"
	case "azure":
		return "Azure"
	case "gcp":
		return "GCP"
	default:
		return provider
	}
}
