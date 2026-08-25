// Package recfilter holds the stateless dimension filters (region, instance
// type, engine) and the minimum-pool-size stage shared between the cmd CLI
// and the MCP server.
package recfilter

import (
	"fmt"
	"slices"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// Logf is an injected logging sink. Package recfilter never logs through a
// package-level logger: cmd's AppLogger writes to stdout, which the MCP
// server owns as its protocol transport. A nil Logf disables logging.
type Logf func(format string, args ...any)

// printf calls l with format/args, or no-ops if l is nil.
func (l Logf) printf(format string, args ...any) {
	if l == nil {
		return
	}
	l(format, args...)
}

// Filters holds the stateless include/exclude dimension filters plus the
// minimum-pool-size threshold. Zero value = allow everything.
type Filters struct {
	IncludeRegions       []string
	ExcludeRegions       []string
	IncludeInstanceTypes []string
	ExcludeInstanceTypes []string
	IncludeEngines       []string
	ExcludeEngines       []string
	MinPoolSize          float64
}

// IncludesRegion checks if a region should be included based on filters.
func (f Filters) IncludesRegion(region string) bool {
	// If include list is specified, region must be in it.
	if len(f.IncludeRegions) > 0 && !slices.Contains(f.IncludeRegions, region) {
		return false
	}

	// If exclude list is specified, region must not be in it.
	if slices.Contains(f.ExcludeRegions, region) {
		return false
	}

	return true
}

// IncludesInstanceType checks if an instance type should be included based on filters.
func (f Filters) IncludesInstanceType(instanceType string) bool {
	// If include list is specified, instance type must be in it.
	if len(f.IncludeInstanceTypes) > 0 && !slices.Contains(f.IncludeInstanceTypes, instanceType) {
		return false
	}

	// If exclude list is specified, instance type must not be in it.
	if slices.Contains(f.ExcludeInstanceTypes, instanceType) {
		return false
	}

	return true
}

// IncludesEngine checks if a recommendation should be included based on engine filters.
func (f Filters) IncludesEngine(rec *common.Recommendation) bool {
	// Extract engine from recommendation.
	engine := common.EngineFromDetails(rec.Details)
	if engine == "" {
		// If no engine info, include by default unless there's an include list.
		return len(f.IncludeEngines) == 0
	}

	// Normalize engine name to lowercase for comparison.
	engine = strings.ToLower(engine)

	// If include list is specified, engine must be in it.
	if len(f.IncludeEngines) > 0 {
		found := false
		for _, e := range f.IncludeEngines {
			if strings.EqualFold(e, engine) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// If exclude list is specified, engine must not be in it.
	if len(f.ExcludeEngines) > 0 {
		for _, e := range f.ExcludeEngines {
			if strings.EqualFold(e, engine) {
				return false
			}
		}
	}

	return true
}

// IncludesPoolSize filters out RI recommendations for pools whose
// AverageInstancesUsedPerHour is below f.MinPoolSize. The purpose is to
// drop tiny pools where integer-arithmetic sizing forces 100% coverage
// regardless of --target-coverage (e.g. avg=1 with target=80% -> floor(0.8)=0
// drops, ceil(0.8)=1 over-covers). Setting --min-pool-size=2 keeps pools
// where target can be meaningfully approximated.
//
// Pass-through cases: filter disabled (MinPoolSize<=0), or rec has no
// per-hour signal (avg<=0 -- SPs and recs CE didn't return usage for).
// Those pools aren't sized via the per-hour formula so the filter doesn't
// apply to them.
func (f Filters) IncludesPoolSize(rec *common.Recommendation) bool {
	if f.MinPoolSize <= 0 {
		return true
	}
	if rec.AverageInstancesUsedPerHour <= 0 {
		return true
	}
	return rec.AverageInstancesUsedPerHour >= f.MinPoolSize
}

// ApplyMinPoolSize drops recommendations whose AverageInstancesUsedPerHour
// is below f.MinPoolSize, logging a per-recommendation line via logf (no-op
// if nil) and recording each drop in drops under common.DropMinPoolSize.
// Returns recs unchanged, with no logging or allocation, when the filter is
// disabled (f.MinPoolSize <= 0).
func (f Filters) ApplyMinPoolSize(recs []common.Recommendation, logf Logf, drops *common.DropSummary) []common.Recommendation {
	if f.MinPoolSize <= 0 {
		return recs
	}

	var kept []common.Recommendation
	var poolDropCount int
	var poolDropInstances float64

	for i := range recs {
		if f.IncludesPoolSize(&recs[i]) {
			kept = append(kept, recs[i])
			continue
		}
		poolDropInstances += recs[i].AverageInstancesUsedPerHour
		label := fmt.Sprintf("%s/%s/%s", recs[i].Service, recs[i].Region, recs[i].ResourceType)
		logf.printf("INFO: --min-pool-size=%.1f dropped %s (avg=%.2f < threshold)", f.MinPoolSize, label, recs[i].AverageInstancesUsedPerHour)
		poolDropCount++
		drops.Add(common.DropMinPoolSize, 1)
	}

	if poolDropCount > 0 {
		logf.printf("INFO: --min-pool-size dropped %d recommendation(s) (%.2f avg instances/hr total)", poolDropCount, poolDropInstances)
	}

	return kept
}
