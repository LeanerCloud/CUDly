package main

import (
	"log"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/recfilter"
)

// filtersFromConfig maps the CLI Config's dimension-filter and min-pool-size
// fields onto a recfilter.Filters value. Account filtering stays cmd-only
// (see shouldIncludeAccount) so it is not part of recfilter.Filters.
func filtersFromConfig(cfg *Config) recfilter.Filters {
	return recfilter.Filters{
		IncludeRegions:       cfg.IncludeRegions,
		ExcludeRegions:       cfg.ExcludeRegions,
		IncludeInstanceTypes: cfg.IncludeInstanceTypes,
		ExcludeInstanceTypes: cfg.ExcludeInstanceTypes,
		IncludeEngines:       cfg.IncludeEngines,
		ExcludeEngines:       cfg.ExcludeEngines,
		MinPoolSize:          cfg.MinPoolSize,
	}
}

// applyFilters applies region, instance type, engine, and engine version filters to recommendations.
// currentRegion is the region being processed in the current loop iteration; if non-empty, only
// recommendations for that region are included.
// drops accumulates per-reason drop counts for the end-of-run summary; pass nil to skip tracking.
func applyFilters(recs []common.Recommendation, cfg *Config, instanceVersions map[string][]InstanceEngineVersion, versionInfo map[string]MajorEngineVersionInfo, currentRegion string, drops *common.DropSummary) []common.Recommendation {
	survivors := filtersFromConfig(cfg).ApplyMinPoolSize(recs, log.Printf, drops)

	var filtered []common.Recommendation
	for i := range survivors {
		adjusted, include, dropReason := processRecommendation(&survivors[i], cfg, instanceVersions, versionInfo, currentRegion)
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
func processRecommendation(rec *common.Recommendation, cfg *Config, instanceVersions map[string][]InstanceEngineVersion, versionInfo map[string]MajorEngineVersionInfo, currentRegion string) (result common.Recommendation, include bool, dropReason string) {
	// Filter to only recommendations for the current region being processed.
	// This prevents duplicating recommendations across all regions.
	// Skip for Savings Plans (account-level, not regional). No drop reason:
	// same rec will be returned by its own region's pass.
	if currentRegion != "" && rec.Region != currentRegion && !common.IsSavingsPlan(rec.Service) {
		return *rec, false, ""
	}

	if !passesDimensionFilters(rec, cfg) {
		// Dimension mismatches (region/account/engine/instance-type) are expected
		// operator-scoping choices, not drops worth surfacing in the summary.
		// --min-pool-size drops are counted separately in applyFilters before
		// this function runs, so no drop reason is surfaced here.
		return *rec, false, ""
	}

	// Apply engine version filters - adjust instance count by subtracting extended support versions.
	if !cfg.IncludeExtendedSupport {
		adjusted := adjustRecommendationForExcludedVersions(*rec, instanceVersions, versionInfo)
		// Skip if all instances were excluded (count reduced to 0).
		if adjusted.Count <= 0 {
			return adjusted, false, common.DropExtendedSupport
		}
		return adjusted, true, ""
	}

	return *rec, true, ""
}

// passesDimensionFilters runs the stateless include/exclude checks on
// region, instance type, engine, and account. Returns false on
// the first failing filter. Split out of processRecommendation to keep
// each function's cyclomatic complexity under the gocyclo limit; the
// dimension filters here are pure functions of rec + cfg with no side
// effects. Pool-size filtering is handled with logging in applyFilters.
func passesDimensionFilters(rec *common.Recommendation, cfg *Config) bool {
	return filtersFromConfig(cfg).PassesDimensions(rec) && shouldIncludeAccount(rec.AccountName, cfg)
}

// shouldIncludePoolSize checks if a recommendation's pool size meets cfg.MinPoolSize.
// Thin wrapper over recfilter.Filters.IncludesPoolSize; kept so cmd's existing call sites
// and tests are unchanged.
func shouldIncludePoolSize(rec *common.Recommendation, cfg *Config) bool {
	return filtersFromConfig(cfg).IncludesPoolSize(rec)
}

// shouldIncludeRegion checks if a region should be included based on filters.
// Thin wrapper over recfilter.Filters.IncludesRegion; kept so cmd's existing call sites
// and tests are unchanged.
func shouldIncludeRegion(region string, cfg *Config) bool {
	return filtersFromConfig(cfg).IncludesRegion(region)
}

// shouldIncludeInstanceType checks if an instance type should be included based on filters.
// Thin wrapper over recfilter.Filters.IncludesInstanceType; kept so cmd's existing call sites
// and tests are unchanged.
func shouldIncludeInstanceType(instanceType string, cfg *Config) bool {
	return filtersFromConfig(cfg).IncludesInstanceType(instanceType)
}

// shouldIncludeEngine checks if a recommendation should be included based on engine filters.
// Thin wrapper over recfilter.Filters.IncludesEngine; kept so cmd's existing call sites
// and tests are unchanged.
func shouldIncludeEngine(rec *common.Recommendation, cfg *Config) bool {
	return filtersFromConfig(cfg).IncludesEngine(rec)
}

// shouldIncludeAccount checks if an account should be included based on filters.
func shouldIncludeAccount(accountName string, cfg *Config) bool {
	// If account name is empty and there are filters, skip it (unless include list is empty).
	if accountName == "" {
		return len(cfg.IncludeAccounts) == 0 && len(cfg.ExcludeAccounts) == 0
	}

	accountLower := strings.ToLower(accountName)

	// Check include list.
	if !checkIncludeList(accountLower, cfg.IncludeAccounts) {
		return false
	}

	// Check exclude list.
	if checkExcludeList(accountLower, cfg.ExcludeAccounts) {
		return false
	}

	return true
}

// checkIncludeList checks if an account matches the include filters.
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

// checkExcludeList checks if an account matches any exclude filters.
func checkExcludeList(accountLower string, excludeAccounts []string) bool {
	for _, filter := range excludeAccounts {
		if accountMatchesFilter(accountLower, filter) {
			return true
		}
	}
	return false
}

// accountMatchesFilter checks if an account matches a filter pattern (exact or substring match).
func accountMatchesFilter(accountLower, filter string) bool {
	filterLower := strings.ToLower(filter)
	return filterLower == accountLower || strings.Contains(accountLower, filterLower)
}
