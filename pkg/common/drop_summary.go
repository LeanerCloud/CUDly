package common

import (
	"fmt"
	"sort"
	"strings"
)

// Drop reason keys used by all filter and sizing stages. Using typed constants
// avoids typos at call sites and lets tests assert on the exact key.
const (
	DropMinPoolSize         = "--min-pool-size"
	DropExtendedSupport     = "--include-extended-support"
	DropTargetAlreadyMet    = "target-already-met"
	DropTargetSizedToZero   = "target-sized-to-zero"
	DropFamilyAlreadyAtTarget = "family-nu-already-at-target"
	DropFamilyNoNUSignal     = "family-nu-no-nu-signal"
	DropFamilySizedToZero    = "family-nu-sized-to-zero"
	DropDuplicateDedup      = "duplicate-dedup"
)

// DropSummary accumulates the count of recommendations dropped per reason
// across the full fetch-filter-size pipeline. It is not safe for concurrent
// use from multiple goroutines; the main pipeline is sequential per service
// and region so no synchronisation is needed.
type DropSummary struct {
	counts map[string]int
}

// NewDropSummary returns a zero-valued DropSummary ready to record drops.
func NewDropSummary() *DropSummary {
	return &DropSummary{counts: make(map[string]int)}
}

// Add increments the drop count for the given reason by n. Safe to call on a
// zero-value DropSummary (e.g. var d DropSummary); the underlying map is
// lazily initialized on first use.
func (d *DropSummary) Add(reason string, n int) {
	if d == nil || n == 0 {
		return
	}
	if d.counts == nil {
		d.counts = make(map[string]int)
	}
	d.counts[reason] += n
}

// Total returns the sum of all drops recorded.
func (d *DropSummary) Total() int {
	if d == nil {
		return 0
	}
	total := 0
	for _, n := range d.counts {
		total += n
	}
	return total
}

// IsEmpty reports whether no drops have been recorded.
func (d *DropSummary) IsEmpty() bool {
	return d == nil || len(d.counts) == 0
}

// FormatOneLine returns a compact single-line summary such as:
//
//	Dropped 14 recs: --min-pool-size=8, target-already-met=4, duplicate-dedup=2
//
// Returns an empty string when no drops have been recorded.
func (d *DropSummary) FormatOneLine() string {
	if d.IsEmpty() {
		return ""
	}

	// Sort reasons for deterministic output.
	reasons := make([]string, 0, len(d.counts))
	for r := range d.counts {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)

	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%s=%d", r, d.counts[r]))
	}

	return fmt.Sprintf("Dropped %d recs: %s", d.Total(), strings.Join(parts, ", "))
}
