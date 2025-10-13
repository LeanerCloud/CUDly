package common

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DuplicateChecker checks for duplicate RI purchases
type DuplicateChecker struct {
	LookbackHours int // How many hours to look back for recent purchases
}

// NewDuplicateChecker creates a new duplicate checker with default 24-hour lookback
func NewDuplicateChecker() *DuplicateChecker {
	return &DuplicateChecker{
		LookbackHours: 24,
	}
}

// AdjustRecommendationsForExistingRIs adjusts recommendations based on existing RIs
func (dc *DuplicateChecker) AdjustRecommendationsForExistingRIs(ctx context.Context, recommendations []Recommendation, purchaseClient PurchaseClient) ([]Recommendation, error) {
	// Get existing RIs
	existingRIs, err := purchaseClient.GetExistingReservedInstances(ctx)
	if err != nil {
		// Log error but don't fail - we'll proceed without duplicate checking
		fmt.Printf("Warning: Could not check for existing RIs: %v\n", err)
		return recommendations, nil
	}

	// Filter to recent purchases only
	cutoffTime := time.Now().Add(-time.Duration(dc.LookbackHours) * time.Hour)
	recentRIs := dc.filterRecentRIs(existingRIs, cutoffTime)


	// Adjust recommendations
	adjusted := make([]Recommendation, 0, len(recommendations))
	for _, rec := range recommendations {
		adjustedRec := dc.adjustRecommendation(rec, recentRIs)
		if adjustedRec.Count > 0 {
			adjusted = append(adjusted, adjustedRec)
		}
	}

	if len(recentRIs) > 0 {
		fmt.Printf("Found %d recent RIs purchased in the last %d hours\n", len(recentRIs), dc.LookbackHours)
		fmt.Printf("Adjusted recommendations from %d to %d to avoid duplicates\n", len(recommendations), len(adjusted))
	}

	return adjusted, nil
}

// filterRecentRIs filters RIs to only those purchased recently
func (dc *DuplicateChecker) filterRecentRIs(existingRIs []ExistingRI, cutoffTime time.Time) []ExistingRI {
	var recent []ExistingRI
	for _, ri := range existingRIs {
		// Only include active or payment-pending RIs purchased after cutoff
		if (ri.State == "active" || ri.State == "payment-pending") && ri.StartTime.After(cutoffTime) {
			recent = append(recent, ri)
		}
	}
	return recent
}

// adjustRecommendation adjusts a single recommendation based on existing RIs
func (dc *DuplicateChecker) adjustRecommendation(rec Recommendation, existingRIs []ExistingRI) Recommendation {
	// Count matching existing RIs
	existingCount := int32(0)
	for _, ri := range existingRIs {
		if dc.isMatchingRI(rec, ri) {
			existingCount += ri.Count
		}
	}

	// Adjust the recommendation count
	if existingCount > 0 {
		originalCount := rec.Count
		rec.Count = rec.Count - existingCount
		if rec.Count < 0 {
			rec.Count = 0
		}

		engine := dc.getEngineFromRecommendation(rec)
		fmt.Printf("Adjusting %s %s %s: %d recommended - %d existing = %d to purchase\n",
			rec.GetServiceName(), engine, rec.InstanceType, originalCount, existingCount, rec.Count)
	}

	return rec
}

// isMatchingRI checks if an existing RI matches a recommendation
func (dc *DuplicateChecker) isMatchingRI(rec Recommendation, ri ExistingRI) bool {
	recEngine := dc.getEngineFromRecommendation(rec)

	// Match on instance type
	if !strings.EqualFold(rec.InstanceType, ri.InstanceType) {
		return false
	}

	// Match on region
	if !strings.EqualFold(rec.Region, ri.Region) {
		return false
	}

	// Match on engine (for services that have engines)
	if recEngine != "" && ri.Engine != "" {
		// Normalize engine names for comparison (handle "Aurora MySQL" vs "aurora-mysql")
		normalizedRecEngine := normalizeEngineName(recEngine)
		normalizedRiEngine := normalizeEngineName(ri.Engine)
		if !strings.EqualFold(normalizedRecEngine, normalizedRiEngine) {
			return false
		}
	}

	// Match on payment option (normalize format differences)
	recPayment := normalizePaymentOption(rec.PaymentOption)
	riPayment := normalizePaymentOption(ri.PaymentOption)
	if !strings.EqualFold(recPayment, riPayment) {
		return false
	}

	// Match on term
	if rec.Term != ri.Term {
		return false
	}

	return true
}

// normalizePaymentOption normalizes payment option strings for comparison
// Handles variations like "no-upfront" vs "No Upfront" vs "NoUpfront"
func normalizePaymentOption(payment string) string {
	// Remove spaces and hyphens, convert to lowercase
	normalized := strings.ToLower(payment)
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized
}

// normalizeEngineName normalizes engine names for comparison
// Handles variations like "Aurora MySQL" vs "aurora-mysql"
func normalizeEngineName(engine string) string {
	// Remove spaces and hyphens, convert to lowercase
	normalized := strings.ToLower(engine)
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized
}

// getEngineFromRecommendation extracts engine from recommendation details
func (dc *DuplicateChecker) getEngineFromRecommendation(rec Recommendation) string {
	switch details := rec.ServiceDetails.(type) {
	case *RDSDetails:
		return details.Engine
	case *ElastiCacheDetails:
		return details.Engine
	case *MemoryDBDetails:
		return "memorydb" // MemoryDB doesn't have multiple engines
	default:
		return ""
	}
}