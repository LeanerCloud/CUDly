// Package scorer filters and ranks commitment purchase recommendations.
// It is a pure function package and must not import pkg/config.
package scorer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// Config controls which recommendations are allowed through the scorer.
// Zero values mean "no filter" for numeric thresholds; empty slice means "all services".
type Config struct {
	MinSavingsPct      float64  // Minimum savings percentage. 0 = no filter.
	MaxBreakEvenMonths int      // Maximum break-even months. 0 = no filter.
	MinCount           int      // Minimum count per recommendation. 0 = no filter.
	EnabledServices    []string // Empty = all services. E.g. ["ec2", "rds"].
}

// FilteredRecommendation holds a recommendation that did not pass a filter, with the reason.
type FilteredRecommendation struct {
	Recommendation common.Recommendation
	FilterReason   string
}

// ScoredResult holds recommendations that passed the scorer (Passed) and those that did not (Filtered).
type ScoredResult struct {
	Passed   []common.Recommendation
	Filtered []FilteredRecommendation
}

// Score applies cfg filters to recs and returns passed and filtered recommendations.
// Passed recommendations are sorted by SavingsPercentage (desc), then EstimatedSavings (desc),
// then a stable tie-breaker of Service+Region+ResourceType (asc) for deterministic output.
func Score(recs []common.Recommendation, cfg Config) ScoredResult {
	result := ScoredResult{
		Passed:   make([]common.Recommendation, 0, len(recs)),
		Filtered: make([]FilteredRecommendation, 0),
	}

	enabledSet := buildServiceSet(cfg.EnabledServices)

	for _, rec := range recs {
		if reason := filterReason(rec, cfg, enabledSet); reason != "" {
			result.Filtered = append(result.Filtered, FilteredRecommendation{
				Recommendation: rec,
				FilterReason:   reason,
			})
		} else {
			result.Passed = append(result.Passed, rec)
		}
	}

	sort.Slice(result.Passed, func(i, j int) bool {
		a, b := result.Passed[i], result.Passed[j]
		if a.SavingsPercentage != b.SavingsPercentage {
			return a.SavingsPercentage > b.SavingsPercentage
		}
		if a.EstimatedSavings != b.EstimatedSavings {
			return a.EstimatedSavings > b.EstimatedSavings
		}
		keyA := string(a.Service) + "|" + a.Region + "|" + a.ResourceType
		keyB := string(b.Service) + "|" + b.Region + "|" + b.ResourceType
		return keyA < keyB
	})

	return result
}

// filterReason returns a non-empty string describing why rec was filtered, or "" if it passes.
func filterReason(rec common.Recommendation, cfg Config, enabledServices map[string]struct{}) string {
	if len(enabledServices) > 0 {
		if _, ok := enabledServices[strings.ToLower(string(rec.Service))]; !ok {
			return fmt.Sprintf("service %q not in enabled list", rec.Service)
		}
	}

	if cfg.MinSavingsPct > 0 && rec.SavingsPercentage < cfg.MinSavingsPct {
		return fmt.Sprintf("savings %.1f%% below minimum %.1f%%", rec.SavingsPercentage, cfg.MinSavingsPct)
	}

	if cfg.MaxBreakEvenMonths > 0 && rec.BreakEvenMonths > float64(cfg.MaxBreakEvenMonths) {
		return fmt.Sprintf("break-even %.1f months exceeds maximum %d months", rec.BreakEvenMonths, cfg.MaxBreakEvenMonths)
	}

	if cfg.MinCount > 0 && rec.Count < cfg.MinCount {
		return fmt.Sprintf("count %d below minimum %d", rec.Count, cfg.MinCount)
	}

	return ""
}

// buildServiceSet converts a slice of service names to a lowercase set for O(1) lookup.
func buildServiceSet(services []string) map[string]struct{} {
	if len(services) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(services))
	for _, s := range services {
		set[strings.ToLower(s)] = struct{}{}
	}
	return set
}
