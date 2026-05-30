package main

import (
	"slices"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// applyFilters applies region, instance type, engine, and engine version filters to recommendations
// currentRegion is the region being processed in the current loop iteration - if non-empty, only recommendations for that region are included
func applyFilters(recs []common.Recommendation, cfg Config, instanceVersions map[string][]InstanceEngineVersion, versionInfo map[string]MajorEngineVersionInfo, currentRegion string, drops *common.DropSummary) []common.Recommendation {
	var filtered []common.Recommendation

	for _, rec := range recs {
		adjusted, include, dropReason := processRecommendation(rec, cfg, instanceVersions, versionInfo, currentRegion)
		if include {
			filtered = append(filtered, adjusted)
		} else if dropReason != "" {
			drops.Add(dropReason, 1)
		}
	}

	return filtered
}

// processRecommendation applies all filters to a recommendation and returns
// (adjusted, include, dropReason). dropReason is non-empty only when
// include is false and the drop is worth surfacing in the end-of-run
// summary (dimension-filter mismatches such as region/account/engine are
// expected exclusions and are not counted). The flat boolean-filter checks
// are delegated to passesDimensionFilters to keep this function under
// gocyclo's complexity threshold.
func processRecommendation(rec common.Recommendation, cfg Config, instanceVersions map[string][]InstanceEngineVersion, versionInfo map[string]MajorEngineVersionInfo, currentRegion string) (common.Recommendation, bool, string) {
	// Filter to only recommendations for the current region being processed.
	// This prevents duplicating recommendations across all regions.
	// Skip for Savings Plans (account-level, not regional). No drop reason:
	// same rec will be returned by its own region's pass.
	if currentRegion != "" && rec.Region != currentRegion && !common.IsSavingsPlan(rec.Service) {
		return rec, false, ""
	}

	if !passesDimensionFilters(rec, cfg) {
		// Dimension mismatches (region/account/engine/instance-type) are expected
		// operator-scoping choices, not drops worth surfacing in the summary.
		// Only --min-pool-size is a sizing heuristic that operators may need to
		// investigate (hence reported separately in passesDimensionFiltersWithReason).
		_, reason := passesDimensionFiltersWithReason(rec, cfg)
		return rec, false, reason
	}

	// Apply engine version filters - adjust instance count by subtracting extended support versions
	if !cfg.IncludeExtendedSupport {
		rec = adjustRecommendationForExcludedVersions(rec, instanceVersions, versionInfo)
		// Skip if all instances were excluded (count reduced to 0)
		if rec.Count <= 0 {
			return rec, false, common.DropExtendedSupport
		}
	}

	return rec, true, ""
}

// passesDimensionFilters runs the stateless include/exclude checks on
// region, instance type, engine, account, and pool size. Returns false on
// the first failing filter. Split out of processRecommendation to keep
// each function's cyclomatic complexity under the gocyclo limit; the
// dimension filters here are pure functions of rec + cfg with no side
// effects.
func passesDimensionFilters(rec common.Recommendation, cfg Config) bool {
	ok, _ := passesDimensionFiltersWithReason(rec, cfg)
	return ok
}

// passesDimensionFiltersWithReason is the reporting variant of
// passesDimensionFilters. It returns (false, dropReason) when the rec is
// excluded, where dropReason is non-empty only for drops that operators
// should see in the end-of-run drop summary (currently only
// --min-pool-size). Region, account, engine, and instance-type mismatches
// are expected operator-scoping choices and return an empty reason.
func passesDimensionFiltersWithReason(rec common.Recommendation, cfg Config) (bool, string) {
	if !shouldIncludeRegion(rec.Region, cfg) {
		return false, ""
	}
	if !shouldIncludeInstanceType(rec.ResourceType, cfg) {
		return false, ""
	}
	if !shouldIncludeEngine(rec, cfg) {
		return false, ""
	}
	if !shouldIncludeAccount(rec.AccountName, cfg) {
		return false, ""
	}
	if !shouldIncludePoolSize(rec, cfg) {
		return false, common.DropMinPoolSize
	}
	return true, ""
}

// shouldIncludePoolSize filters out RI recommendations for pools whose
// AverageInstancesUsedPerHour is below cfg.MinPoolSize. The purpose is to
// drop tiny pools where integer-arithmetic sizing forces 100% coverage
// regardless of --target-coverage (e.g. avg=1 with target=80% → floor(0.8)=0
// drops, ceil(0.8)=1 over-covers). Setting --min-pool-size=2 keeps pools
// where target can be meaningfully approximated.
//
// Pass-through cases: filter disabled (MinPoolSize<=0), or rec has no
// per-hour signal (avg<=0 — SPs and recs CE didn't return usage for).
// Those pools aren't sized via the per-hour formula so the filter doesn't
// apply to them.
func shouldIncludePoolSize(rec common.Recommendation, cfg Config) bool {
	if cfg.MinPoolSize <= 0 {
		return true
	}
	if rec.AverageInstancesUsedPerHour <= 0 {
		return true
	}
	return rec.AverageInstancesUsedPerHour >= cfg.MinPoolSize
}

// shouldIncludeRegion checks if a region should be included based on filters
func shouldIncludeRegion(region string, cfg Config) bool {
	// If include list is specified, region must be in it
	if len(cfg.IncludeRegions) > 0 && !slices.Contains(cfg.IncludeRegions, region) {
		return false
	}

	// If exclude list is specified, region must not be in it
	if slices.Contains(cfg.ExcludeRegions, region) {
		return false
	}

	return true
}

// shouldIncludeInstanceType checks if an instance type should be included based on filters
func shouldIncludeInstanceType(instanceType string, cfg Config) bool {
	// If include list is specified, instance type must be in it
	if len(cfg.IncludeInstanceTypes) > 0 && !slices.Contains(cfg.IncludeInstanceTypes, instanceType) {
		return false
	}

	// If exclude list is specified, instance type must not be in it
	if slices.Contains(cfg.ExcludeInstanceTypes, instanceType) {
		return false
	}

	return true
}

// shouldIncludeEngine checks if a recommendation should be included based on engine filters
func shouldIncludeEngine(rec common.Recommendation, cfg Config) bool {
	// Extract engine from recommendation
	engine := getEngineFromRecommendation(rec)
	if engine == "" {
		// If no engine info, include by default unless there's an include list
		return len(cfg.IncludeEngines) == 0
	}

	// Normalize engine name to lowercase for comparison
	engine = strings.ToLower(engine)

	// If include list is specified, engine must be in it
	if len(cfg.IncludeEngines) > 0 {
		found := false
		for _, e := range cfg.IncludeEngines {
			if strings.ToLower(e) == engine {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// If exclude list is specified, engine must not be in it
	if len(cfg.ExcludeEngines) > 0 {
		for _, e := range cfg.ExcludeEngines {
			if strings.ToLower(e) == engine {
				return false
			}
		}
	}

	return true
}

// shouldIncludeAccount checks if an account should be included based on filters
func shouldIncludeAccount(accountName string, cfg Config) bool {
	// If account name is empty and there are filters, skip it (unless include list is empty)
	if accountName == "" {
		return len(cfg.IncludeAccounts) == 0 && len(cfg.ExcludeAccounts) == 0
	}

	accountLower := strings.ToLower(accountName)

	// Check include list
	if !checkIncludeList(accountLower, cfg.IncludeAccounts) {
		return false
	}

	// Check exclude list
	if checkExcludeList(accountLower, cfg.ExcludeAccounts) {
		return false
	}

	return true
}

// checkIncludeList checks if an account matches the include filters
func checkIncludeList(accountLower string, includeAccounts []string) bool {
	if len(includeAccounts) == 0 {
		return true
	}

	for _, filter := range includeAccounts {
		if accountMatchesFilter(accountLower, filter) {
			return true
		}
	}

	return false
}

// checkExcludeList checks if an account matches any exclude filters
func checkExcludeList(accountLower string, excludeAccounts []string) bool {
	for _, filter := range excludeAccounts {
		if accountMatchesFilter(accountLower, filter) {
			return true
		}
	}
	return false
}

// accountMatchesFilter checks if an account matches a filter pattern (exact or substring match)
func accountMatchesFilter(accountLower, filter string) bool {
	filterLower := strings.ToLower(filter)
	return filterLower == accountLower || strings.Contains(accountLower, filterLower)
}
