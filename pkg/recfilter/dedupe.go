package recfilter

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// DefaultDuplicateCheckLookbackHours is the default lookback period for checking recent purchases.
const DefaultDuplicateCheckLookbackHours = 24

// DuplicateChecker checks for existing commitments to avoid duplicates.
type DuplicateChecker struct {
	LookbackHours int // How many hours to look back for recent purchases

	// Logf receives the per-commitment decision trail. Nil is silent.
	// recfilter never logs through a package-level logger: cmd's AppLogger
	// writes to stdout, which the MCP server owns as its protocol transport.
	Logf Logf
}

// NewDuplicateChecker creates a new duplicate checker. Pass 0 to use the default lookback period.
func NewDuplicateChecker(hours int) *DuplicateChecker {
	if hours <= 0 {
		hours = DefaultDuplicateCheckLookbackHours
	}
	return &DuplicateChecker{
		LookbackHours: hours,
	}
}

// AdjustRecommendationsForExisting adjusts recommendations based on existing commitments
// This checks for recently purchased RIs (within LookbackHours) to avoid duplicate purchases.
// Note: This is designed to prevent re-purchasing something you just bought, not to prevent
// purchasing RIs in other accounts that happen to have the same characteristics.
func (d *DuplicateChecker) AdjustRecommendationsForExisting(ctx context.Context, recs []common.Recommendation, client provider.ServiceClient) (passed, filtered []common.Recommendation, err error) {
	existing, err := client.GetExistingCommitments(ctx)
	if err != nil {
		return recs, nil, err
	}

	d.Logf.printf("    [DuplicateChecker] Found %d total existing commitments", len(existing))

	recentExisting := d.filterRecentCommitments(existing)
	d.Logf.printf("    [DuplicateChecker] Found %d recent commitments (purchased in last %d hours)", len(recentExisting), d.LookbackHours)

	if len(recentExisting) == 0 {
		return recs, nil, nil
	}

	existingMap := buildExistingCommitmentsMap(recentExisting, d.Logf)
	d.Logf.printf("    [DuplicateChecker] Existing map has %d unique keys", len(existingMap))

	passed, filtered = adjustRecommendationsAgainstExisting(recs, existingMap, d.Logf)

	if len(filtered) > 0 {
		d.Logf.printf("    [DuplicateChecker] Result: %d recommendations kept out of %d (avoided %d duplicates)",
			len(passed), len(recs), len(filtered))
	}
	return passed, filtered, nil
}

// filterRecentCommitments filters commitments to only recent purchases within the lookback window.
func (d *DuplicateChecker) filterRecentCommitments(existing []common.Commitment) []common.Commitment {
	cutoffTime := time.Now().Add(-time.Duration(d.LookbackHours) * time.Hour)
	recentExisting := make([]common.Commitment, 0)

	for _rvc := range existing {
		c := existing[_rvc]
		if isRecentActiveCommitment(c, cutoffTime) {
			recentExisting = append(recentExisting, c)
		}
	}

	return recentExisting
}

// isRecentActiveCommitment checks if a commitment is active and purchased after the cutoff time.
func isRecentActiveCommitment(c common.Commitment, cutoffTime time.Time) bool {
	return (c.State == "active" || c.State == "payment-pending") && c.StartDate.After(cutoffTime)
}

// buildExistingCommitmentsMap builds a map of commitments by resource type, region, and engine.
func buildExistingCommitmentsMap(commitments []common.Commitment, logf Logf) map[string]int {
	existingMap := make(map[string]int)

	for _rvc := range commitments {
		c := commitments[_rvc]
		normalizedEngine := common.NormalizeEngineName(c.Engine)
		key := fmt.Sprintf("%s|%s|%s", c.ResourceType, c.Region, normalizedEngine)
		existingMap[key] += c.Count
		logf.printf("    [DuplicateChecker] Recent RI: key=%s count=%d startDate=%s (raw engine=%s)",
			key, c.Count, c.StartDate.Format("2006-01-02 15:04:05"), c.Engine)
	}

	return existingMap
}

// adjustRecommendationsAgainstExisting adjusts recommendations based on existing commitments.
// Returns (passed, filtered) where filtered contains recs whose count was reduced to zero.
func adjustRecommendationsAgainstExisting(recs []common.Recommendation, existingMap map[string]int, logf Logf) (passed, filtered []common.Recommendation) {
	passed = make([]common.Recommendation, 0, len(recs))
	filtered = make([]common.Recommendation, 0)

	for _rvc := range recs {
		rec := recs[_rvc]
		adjusted := adjustSingleRecommendation(rec, existingMap, logf)
		if adjusted.Count > 0 {
			passed = append(passed, adjusted)
		} else {
			filtered = append(filtered, rec)
		}
	}

	return passed, filtered
}

// adjustSingleRecommendation adjusts a single recommendation based on existing commitments.
func adjustSingleRecommendation(rec common.Recommendation, existingMap map[string]int, logf Logf) common.Recommendation {
	engine := common.EngineFromDetails(rec.Details)
	key := fmt.Sprintf("%s|%s|%s", rec.ResourceType, rec.Region, engine)
	existingCount := existingMap[key]

	if existingCount >= rec.Count {
		// All of this recommendation is covered by recent RIs.
		// Return a zero-value Recommendation (Count=0) as a sentinel; the caller
		// (adjustRecommendationsAgainstExisting) filters out recommendations with Count <= 0.
		logf.printf("    [DuplicateChecker] SKIP %s: recent %d >= recommended %d", key, existingCount, rec.Count)
		existingMap[key] -= rec.Count
		return common.Recommendation{Count: 0}
	}

	// Partial or no coverage by recent RIs
	adjusted := rec
	if existingCount > 0 {
		adjusted.Count = rec.Count - existingCount
		existingMap[key] = 0
		logf.printf("    [DuplicateChecker] PARTIAL %s: adjusted count from %d to %d", key, rec.Count, adjusted.Count)
	}

	return adjusted
}

// AdjustRecommendationsForExistingRIs is an alias for AdjustRecommendationsForExisting.
func (d *DuplicateChecker) AdjustRecommendationsForExistingRIs(ctx context.Context, recs []common.Recommendation, client provider.ServiceClient) (passed, filtered []common.Recommendation, err error) {
	return d.AdjustRecommendationsForExisting(ctx, recs, client)
}
