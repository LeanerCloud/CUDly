package exchange

import (
	"context"
	"fmt"
	"math"
	"sort"
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

// OfferingOption is a single Convertible RI offering exposed to the
// reshape layer: the AWS offering ID, the instance type it provisions,
// and the effective monthly cost (amortised fixed price + recurring
// hourly charges × 730 hours). Used both as the return shape of
// OfferingLookup (what an AWS-provider closure returns) and as the
// element type of ReshapeRecommendation.AlternativeTargets (what
// reshape emits to the HTTP handler). Kept in pkg/exchange so
// pkg/exchange stays AWS-free and providers/aws/services/ec2 imports
// the type rather than defining its own.
type OfferingOption struct {
	InstanceType         string  `json:"instance_type"`
	OfferingID           string  `json:"offering_id"`
	EffectiveMonthlyCost float64 `json:"effective_monthly_cost"`
}

// OfferingLookup is the signature of the closure that resolves
// candidate instance types into concrete offerings with pricing. Used
// by AnalyzeReshapingWithOfferings. Implementations batch the request
// into a single DescribeReservedInstancesOfferings call per peer-family
// group so the N instance-types → N API calls fan-out is avoided.
type OfferingLookup func(ctx context.Context, instanceTypes []string) ([]OfferingOption, error)

// ReshapeRecommendation describes a suggested exchange for an underutilized RI.
//
// AlternativeTargets lists cross-family options within the same
// use-case group (general-purpose / compute / memory / burstable) at
// the same target size, enriched with real offering IDs and monthly
// cost when a pricing lookup is available. This is advisory data for
// the UI to surface alongside the primary target; the auto-exchange
// pipeline still acts on TargetInstanceType only so existing automated
// behaviour is unchanged. Emitted when the primary target's family
// belongs to a known peer group; empty otherwise.
type ReshapeRecommendation struct {
	SourceRIID          string           `json:"source_ri_id"`
	SourceInstanceType  string           `json:"source_instance_type"`
	SourceCount         int32            `json:"source_count"`
	TargetInstanceType  string           `json:"target_instance_type"`
	TargetCount         int32            `json:"target_count"`
	AlternativeTargets  []OfferingOption `json:"alternative_targets,omitempty"`
	UtilizationPercent  float64          `json:"utilization_percent"`
	NormalizedUsed      float64          `json:"normalized_used"`
	NormalizedPurchased float64          `json:"normalized_purchased"`
	Reason              string           `json:"reason"`
}

// peerFamilyGroups maps each family in the allowlist to the full set
// of peer families within its use-case group. AWS Convertible RIs can
// cross families when the target's $-value units (from
// DescribeReservedInstancesOfferings) match — and the most common
// viable cross-family moves are between same-generation same-use-case
// siblings (e.g. m5 ↔ m6i, c5 ↔ c7g). Suggesting these to the user
// broadens their options without pushing them into shapes that will
// fail the AWS exchange-units check at quote time.
//
// Scope discipline: only the mainstream general/compute/memory
// families and the burstable t-family are listed. Specialty (p*/g*/
// x*/hpc*) and legacy-generation (m4/c4/r3) families are deliberately
// omitted — those exchanges have awkward $-units and frequently fail
// at AWS level, so suggesting them would hurt user trust more than it
// helps. Tracked as a follow-up in known-issues.md.
var peerFamilyGroups = map[string][]string{
	// general-purpose
	"m5":  {"m5", "m6i", "m7g"},
	"m6i": {"m5", "m6i", "m7g"},
	"m7g": {"m5", "m6i", "m7g"},
	// compute-optimised
	"c5":  {"c5", "c6i", "c7g"},
	"c6i": {"c5", "c6i", "c7g"},
	"c7g": {"c5", "c6i", "c7g"},
	// memory-optimised
	"r5":  {"r5", "r6i", "r7g"},
	"r6i": {"r5", "r6i", "r7g"},
	"r7g": {"r5", "r6i", "r7g"},
	// burstable (maps to itself — generation variants are
	// typically distinct enough that AWS won't let you exchange
	// across them; listed to keep the helper returning a sensible
	// result rather than nil for t-family RIs).
	"t3":  {"t3", "t3a", "t4g"},
	"t3a": {"t3", "t3a", "t4g"},
	"t4g": {"t3", "t3a", "t4g"},
}

// candidateFamilies returns the peer families (including sourceFamily
// itself) within the same use-case group, or nil when the family is
// not in the allowlist. Callers surface the returned families to users
// as cross-family alternatives.
func candidateFamilies(sourceFamily string) []string {
	return peerFamilyGroups[strings.ToLower(sourceFamily)]
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
	utilMap := make(map[string]float64, len(utilization))
	for _, u := range utilization {
		utilMap[u.RIID] = u.UtilizationPercent
	}

	var recommendations []ReshapeRecommendation
	for _, ri := range ris {
		if rec := analyzeRI(ri, utilMap, threshold); rec != nil {
			recommendations = append(recommendations, *rec)
		}
	}
	return recommendations
}

// resolveNormFactor returns the normalization factor for the RI, falling back
// to the standard table value for the instance size. Returns 0 if unknown.
func resolveNormFactor(ri RIInfo, size string) float64 {
	if ri.NormalizationFactor != 0 {
		return ri.NormalizationFactor
	}
	return normalizationFactors[size]
}

// analyzeRI evaluates a single RI and returns a reshape recommendation if it is
// underutilized and convertible, or nil if no action is needed.
func analyzeRI(ri RIInfo, utilMap map[string]float64, threshold float64) *ReshapeRecommendation {
	if !strings.EqualFold(ri.OfferingClass, "convertible") {
		return nil
	}

	util, ok := utilMap[ri.ID]
	if !ok || util >= threshold {
		return nil
	}

	family, size := parseInstanceType(ri.InstanceType)
	if family == "" {
		return nil
	}

	normFactor := resolveNormFactor(ri, size)
	if normFactor == 0 {
		return nil
	}

	normalizedPurchased := normFactor * float64(ri.InstanceCount)
	normalizedUsed := normalizedPurchased * (util / 100.0)

	if normalizedUsed <= 0 {
		return &ReshapeRecommendation{
			SourceRIID:          ri.ID,
			SourceInstanceType:  ri.InstanceType,
			SourceCount:         ri.InstanceCount,
			TargetInstanceType:  "",
			TargetCount:         0,
			UtilizationPercent:  util,
			NormalizedUsed:      0,
			NormalizedPurchased: normalizedPurchased,
			Reason: fmt.Sprintf(
				"RI is completely unused (%.0f%% utilization, %.1f normalized units wasted). Consider letting it expire or exchanging for needed capacity.",
				util, normalizedPurchased,
			),
		}
	}

	targetSize, targetCount := findBestFit(normalizedUsed)
	if targetSize == "" {
		return nil
	}

	targetInstanceType := family + "." + targetSize
	if targetInstanceType == ri.InstanceType && targetCount == ri.InstanceCount {
		return nil
	}

	return &ReshapeRecommendation{
		SourceRIID:          ri.ID,
		SourceInstanceType:  ri.InstanceType,
		SourceCount:         ri.InstanceCount,
		TargetInstanceType:  targetInstanceType,
		TargetCount:         targetCount,
		AlternativeTargets:  alternativesForTarget(family, targetSize),
		UtilizationPercent:  util,
		NormalizedUsed:      normalizedUsed,
		NormalizedPurchased: normalizedPurchased,
		Reason: fmt.Sprintf(
			"RI at %.0f%% utilization (%.1f/%.1f normalized units). Suggest exchanging %dx %s for %dx %s.",
			util, normalizedUsed, normalizedPurchased,
			ri.InstanceCount, ri.InstanceType,
			targetCount, targetInstanceType,
		),
	}
}

// alternativesForTarget returns cross-family equivalents at the same
// target size for the families in sourceFamily's peer group. The
// source family itself is excluded (that's the primary
// TargetInstanceType already). Returns nil when sourceFamily isn't in
// the allowlist or has no peers.
//
// Pricing fields (OfferingID, EffectiveMonthlyCost) are left empty
// here. AnalyzeReshapingWithOfferings backfills them via an injected
// lookup; callers without a lookup (auto.go, no-pricing tests) see
// name-only entries.
func alternativesForTarget(sourceFamily, targetSize string) []OfferingOption {
	peers := candidateFamilies(sourceFamily)
	if len(peers) <= 1 {
		return nil
	}
	out := make([]OfferingOption, 0, len(peers)-1)
	for _, p := range peers {
		if strings.EqualFold(p, sourceFamily) {
			continue
		}
		out = append(out, OfferingOption{InstanceType: p + "." + targetSize})
	}
	return out
}

// AnalyzeReshapingWithOfferings is AnalyzeReshaping + offering
// enrichment: it calls the base analyzer, batches the distinct target
// instance types (primary + alternatives) into ONE lookup call, and
// fills each rec's AlternativeTargets with real OfferingID +
// EffectiveMonthlyCost.
//
// Missing-offering behaviour: if the lookup returns no entry for a
// candidate instance type, that alternative is silently dropped from
// the rec's slice — the rec still ships with its primary target and
// the alternatives that DID resolve. If the lookup itself errors, the
// base recs are returned with empty AlternativeTargets across the
// board; the dashboard's primary reshape suggestions stay intact.
//
// auto.go keeps calling the existing AnalyzeReshaping (no pricing
// needed); only the HTTP handlers that surface recommendations to
// users call the enriched version.
func AnalyzeReshapingWithOfferings(
	ctx context.Context,
	ris []RIInfo,
	utilization []UtilizationInfo,
	threshold float64,
	lookup OfferingLookup,
) []ReshapeRecommendation {
	recs := AnalyzeReshaping(ris, utilization, threshold)
	if lookup == nil || len(recs) == 0 {
		return recs
	}

	types := distinctCandidateTypes(recs)
	if len(types) == 0 {
		return recs
	}

	offerings, err := lookup(ctx, types)
	if err != nil {
		// Fall back to name-only alternatives — losing pricing is
		// strictly less bad than losing the whole reshape page.
		return recs
	}

	fillAlternativesFromOfferings(recs, offerings)
	return recs
}

// distinctCandidateTypes collects de-duplicated instance types from all
// recs' primary + alternative targets, sorted for deterministic
// lookups in tests.
func distinctCandidateTypes(recs []ReshapeRecommendation) []string {
	want := make(map[string]struct{})
	for _, r := range recs {
		if r.TargetInstanceType != "" {
			want[r.TargetInstanceType] = struct{}{}
		}
		for _, alt := range r.AlternativeTargets {
			if alt.InstanceType != "" {
				want[alt.InstanceType] = struct{}{}
			}
		}
	}
	types := make([]string, 0, len(want))
	for t := range want {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// fillAlternativesFromOfferings replaces each rec's AlternativeTargets
// with the matching OfferingOption from the lookup result. Missing
// instance types are silently dropped (per the doc on
// AnalyzeReshapingWithOfferings). The output is sorted ascending by
// EffectiveMonthlyCost so the UI shows cheapest alternatives first —
// this matches user intent (the primary advisory signal of this list
// is "is there a cheaper option than the primary target?") even though
// it differs from the peer-family allowlist order that the base
// AnalyzeReshaping emits.
func fillAlternativesFromOfferings(recs []ReshapeRecommendation, offerings []OfferingOption) {
	offByType := make(map[string]OfferingOption, len(offerings))
	for _, o := range offerings {
		if _, exists := offByType[o.InstanceType]; !exists {
			offByType[o.InstanceType] = o
		}
	}
	for i := range recs {
		filled := make([]OfferingOption, 0, len(recs[i].AlternativeTargets))
		for _, alt := range recs[i].AlternativeTargets {
			if found, ok := offByType[alt.InstanceType]; ok {
				filled = append(filled, found)
			}
		}
		sort.Slice(filled, func(a, b int) bool {
			return filled[a].EffectiveMonthlyCost < filled[b].EffectiveMonthlyCost
		})
		recs[i].AlternativeTargets = filled
	}
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
