package exchange

import (
	"fmt"
	"math"
	"strings"
)

// RIInfo describes a Reserved Instance for reshape analysis.
// Callers should pre-filter to convertible RIs only.
type RIInfo struct {
	ID                  string
	InstanceType        string
	InstanceCount       int32
	OfferingClass       string  // must be "convertible" — standard RIs cannot be exchanged
	NormalizationFactor float64 // AWS normalization factor for the instance size
}

// UtilizationInfo provides utilization data for a specific RI.
type UtilizationInfo struct {
	RIID               string
	UtilizationPercent float64 // 0.0–100.0
}

// ReshapeRecommendation describes a suggested exchange for an underutilized RI.
type ReshapeRecommendation struct {
	SourceRIID          string  `json:"source_ri_id"`
	SourceInstanceType  string  `json:"source_instance_type"`
	SourceCount         int32   `json:"source_count"`
	TargetInstanceType  string  `json:"target_instance_type"`
	TargetCount         int32   `json:"target_count"`
	UtilizationPercent  float64 `json:"utilization_percent"`
	NormalizedUsed      float64 `json:"normalized_used"`
	NormalizedPurchased float64 `json:"normalized_purchased"`
	Reason              string  `json:"reason"`
}

// normalizationFactors maps EC2 instance sizes to their AWS normalization factors.
// See: https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ri-modification-instancemove.html
var normalizationFactors = map[string]float64{
	"nano":     0.25,
	"micro":    0.5,
	"small":    1,
	"medium":   2,
	"large":    4,
	"xlarge":   8,
	"2xlarge":  16,
	"3xlarge":  24,
	"4xlarge":  32,
	"6xlarge":  48,
	"8xlarge":  64,
	"9xlarge":  72,
	"10xlarge": 80,
	"12xlarge": 96,
	"16xlarge": 128,
	"18xlarge": 144,
	"24xlarge": 192,
	"metal":    192,
}

// sizeOrder lists instance sizes from smallest to largest for iteration.
var sizeOrder = []string{
	"nano", "micro", "small", "medium", "large", "xlarge",
	"2xlarge", "3xlarge", "4xlarge", "6xlarge", "8xlarge",
	"9xlarge", "10xlarge", "12xlarge", "16xlarge", "18xlarge",
	"24xlarge", "metal",
}

// parseInstanceType splits "m5.xlarge" into ("m5", "xlarge").
// Returns empty strings if the format is unrecognized.
func parseInstanceType(instanceType string) (family, size string) {
	parts := strings.SplitN(instanceType, ".", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// NormalizationFactorForSize returns the normalization factor for a given instance size.
// Returns 0 if the size is unknown.
func NormalizationFactorForSize(size string) float64 {
	return normalizationFactors[size]
}

// AnalyzeReshaping identifies underutilized convertible RIs and suggests optimal
// exchange targets using AWS normalization factors.
//
// threshold is a percentage (0–100) below which an RI is considered underutilized.
// For example, threshold=95 means RIs with <95% utilization get recommendations.
func AnalyzeReshaping(ris []RIInfo, utilization []UtilizationInfo, threshold float64) []ReshapeRecommendation {
	// Build utilization lookup
	utilMap := make(map[string]float64, len(utilization))
	for _, u := range utilization {
		utilMap[u.RIID] = u.UtilizationPercent
	}

	var recommendations []ReshapeRecommendation

	for _, ri := range ris {
		// Safety: only convertible RIs can be exchanged
		if !strings.EqualFold(ri.OfferingClass, "convertible") {
			continue
		}

		// Must have utilization data
		util, ok := utilMap[ri.ID]
		if !ok {
			continue
		}

		// Well-utilized RIs don't need reshaping
		if util >= threshold {
			continue
		}

		family, size := parseInstanceType(ri.InstanceType)
		if family == "" || size == "" {
			continue
		}

		normFactor := ri.NormalizationFactor
		if normFactor == 0 {
			normFactor = normalizationFactors[size]
		}
		if normFactor == 0 {
			continue
		}

		normalizedPurchased := normFactor * float64(ri.InstanceCount)
		normalizedUsed := normalizedPurchased * (util / 100.0)

		// Find the best-fit instance size in the same family
		targetSize, targetCount := findBestFit(normalizedUsed)
		if targetSize == "" || targetCount == 0 {
			continue
		}

		targetInstanceType := family + "." + targetSize

		// Skip if no actual change
		if targetInstanceType == ri.InstanceType && targetCount == ri.InstanceCount {
			continue
		}

		recommendations = append(recommendations, ReshapeRecommendation{
			SourceRIID:          ri.ID,
			SourceInstanceType:  ri.InstanceType,
			SourceCount:         ri.InstanceCount,
			TargetInstanceType:  targetInstanceType,
			TargetCount:         targetCount,
			UtilizationPercent:  util,
			NormalizedUsed:      normalizedUsed,
			NormalizedPurchased: normalizedPurchased,
			Reason: fmt.Sprintf(
				"RI at %.0f%% utilization (%.1f/%.1f normalized units). Suggest exchanging %dx %s for %dx %s.",
				util, normalizedUsed, normalizedPurchased,
				ri.InstanceCount, ri.InstanceType,
				targetCount, targetInstanceType,
			),
		})
	}

	return recommendations
}

// findBestFit finds the instance size and count that best fits normalizedUsed units.
// Strategy: find the largest single-instance size that doesn't exceed normalizedUsed,
// then round up to that size. This gives a practical 1-instance recommendation.
// If no size is small enough for a single instance, use the smallest size with min count.
func findBestFit(normalizedUsed float64) (size string, count int32) {
	if normalizedUsed <= 0 {
		return "", 0
	}

	// Find the largest size where normFactor <= normalizedUsed (fits in 1 instance)
	bestIdx := -1
	for i, s := range sizeOrder {
		nf := normalizationFactors[s]
		if nf > 0 && nf <= normalizedUsed {
			bestIdx = i
		}
	}

	if bestIdx >= 0 {
		s := sizeOrder[bestIdx]
		nf := normalizationFactors[s]
		needed := int32(math.Ceil(normalizedUsed / nf))
		return s, needed
	}

	// normalizedUsed is smaller than the smallest size — use the smallest
	s := sizeOrder[0]
	return s, 1
}
