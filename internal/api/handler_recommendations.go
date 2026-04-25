// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
)

// Recommendations handlers
func (h *Handler) getRecommendations(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (*RecommendationsResponse, error) {
	// Require view:recommendations permission
	session, err := h.requirePermission(ctx, req, "view", "recommendations")
	if err != nil {
		return nil, err
	}

	// Validate input parameters to prevent injection attacks
	if err := validateProvider(params["provider"]); err != nil {
		return nil, err
	}
	if err := validateServiceName(params["service"]); err != nil {
		return nil, err
	}
	if err := validateRegion(params["region"]); err != nil {
		return nil, err
	}

	accountIDs, err := parseAccountIDs(params["account_ids"])
	if err != nil {
		return nil, NewClientError(400, err.Error())
	}

	// Translate query string → DB-level filter. ListRecommendations
	// pushes these into the WHERE clause so the cache does the pruning.
	filter := config.RecommendationFilter{
		Provider:   params["provider"],
		Service:    params["service"],
		Region:     params["region"],
		AccountIDs: accountIDs,
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
	for _, a := range accounts {
		nameByID[a.ID] = a.Name
	}

	filtered := recs[:0]
	for _, rec := range recs {
		if rec.CloudAccountID == nil {
			continue
		}
		id := *rec.CloudAccountID
		if auth.MatchesAccount(allowedAccounts, id, nameByID[id]) {
			filtered = append(filtered, rec)
		}
	}
	return filtered, nil
}

// buildRecommendationsResponse calculates summary statistics and collects
// unique regions from a set of recommendations.
func buildRecommendationsResponse(recommendations []config.RecommendationRecord) *RecommendationsResponse {
	regionSet := make(map[string]struct{})
	var totalSavings, totalUpfront float64
	for _, rec := range recommendations {
		totalSavings += rec.Savings
		totalUpfront += rec.UpfrontCost
		if rec.Region != "" {
			regionSet[rec.Region] = struct{}{}
		}
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
// pipeline does not yet persist time-series utilisation per
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

	// The recommendation cache doesn't expose a get-by-id, so we look it
	// up from the unfiltered list. The list is already cached in
	// Postgres (see store_postgres_recommendations.go) so this is a
	// single round-trip; the in-memory linear scan is bounded by the
	// catalogue size which is small (low thousands at the high end).
	recs, err := h.scheduler.ListRecommendations(ctx, config.RecommendationFilter{})
	if err != nil {
		return nil, fmt.Errorf("failed to list recommendations: %w", err)
	}

	// Filter by allowed accounts FIRST, then look up by id within the
	// filtered set. Doing it the other way around would leak existence
	// of recommendations in accounts the caller can't see (a 404 vs
	// 403 timing/wording diff would let an attacker probe the
	// recommendation namespace across accounts).
	visible, err := h.filterRecommendationsByAllowedAccounts(ctx, session, recs)
	if err != nil {
		return nil, err
	}

	for i := range visible {
		if visible[i].ID == id {
			return h.buildRecommendationDetail(ctx, &visible[i]), nil
		}
	}
	return nil, errNotFound
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
// client-side in frontend/src/recommendations.ts. Centralising it on
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
