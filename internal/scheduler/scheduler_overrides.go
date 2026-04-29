package scheduler

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// applyAccountOverrides drops recs that the per-account override marks
// disabled, or whose engine / region / resource_type is excluded by the
// resolved (global ⊕ override) ServiceConfig.
//
// Architecturally this is the read-time half of issue #196 (Option B from
// issue #111). PR #193 already wired the override into the purchase forms;
// this helper applies the remaining override fields (enabled, include /
// exclude lists) to the recs list returned by Scheduler.ListRecommendations.
//
// Why post-DB rather than pushing into ListStoredRecommendations' WHERE clause:
//   - The override table is sparse (most accounts have no row).
//   - Filtering by include/exclude TEXT[] arrays per-(account, provider, service)
//     would force a correlated subquery per row — messier than the in-Go pass.
//   - The existing applySuppressions helper sets the precedent of post-DB
//     filtering inside ListRecommendations.
//
// Errors from the resolver are non-fatal: we log and pass the un-filtered
// list through, matching applySuppressions. Over-show is the safer default
// (an unconfigured-looking dashboard is recoverable; a blanked one is not).
func (s *Scheduler) applyAccountOverrides(ctx context.Context, recs []config.RecommendationRecord) ([]config.RecommendationRecord, error) {
	if len(recs) == 0 {
		return recs, nil
	}
	resolved, err := config.ResolveAccountConfigsForRecs(ctx, s.config, recs)
	if err != nil {
		return recs, fmt.Errorf("resolve account configs: %w", err)
	}
	if len(resolved) == 0 {
		return recs, nil
	}
	return filterRecsByResolvedConfigs(recs, resolved), nil
}

// filterRecsByResolvedConfigs drops recs whose resolved per-account
// ServiceConfig says enabled=false, or whose engine / region / resource_type
// is rejected by the resolved include/exclude lists.
//
// Recs without a CloudAccountID (e.g. AWS ambient-credentials path) and
// recs whose triple has no resolved entry (no global ServiceConfig row, or
// the resolver caller skipped them) pass through unfiltered — there is no
// per-account policy to enforce on them.
func filterRecsByResolvedConfigs(
	recs []config.RecommendationRecord,
	resolved map[string]*config.ServiceConfig,
) []config.RecommendationRecord {
	out := recs[:0]
	for i := range recs {
		rec := recs[i]
		if rec.CloudAccountID == nil {
			out = append(out, rec)
			continue
		}
		cfg := resolved[config.AccountConfigKey(*rec.CloudAccountID, rec.Provider, rec.Service)]
		if cfg == nil {
			out = append(out, rec)
			continue
		}
		if !cfg.Enabled {
			continue
		}
		if !engineMatches(rec.Engine, cfg) {
			continue
		}
		if !inListRule(rec.Region, cfg.IncludeRegions, cfg.ExcludeRegions) {
			continue
		}
		if !inListRule(rec.ResourceType, cfg.IncludeTypes, cfg.ExcludeTypes) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// engineMatches applies the engine include/exclude rule with a "lax for
// engine-less services" carve-out.
//
// Some recommendation services (Savings Plans, Compute) carry no engine
// field. Strictly applying an IncludeEngines list would silently drop every
// such rec the moment a user added an engine include for a different service
// on the same (account, provider). The carve-out: when rec.Engine == "" we
// skip the engine check entirely and let the region / type filters decide.
func engineMatches(engine string, cfg *config.ServiceConfig) bool {
	if engine == "" {
		return true
	}
	return inListRule(engine, cfg.IncludeEngines, cfg.ExcludeEngines)
}

// inListRule returns true when value is allowed by an include/exclude pair:
//   - empty include list = allow anything not on the exclude list
//   - non-empty include list = value must be on it AND not on exclude
//
// Order matters: exclude wins over include when the same value appears on both.
func inListRule(value string, include, exclude []string) bool {
	for _, e := range exclude {
		if e == value {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, i := range include {
		if i == value {
			return true
		}
	}
	return false
}
