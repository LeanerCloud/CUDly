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
	// MonthlyCost is the effective per-instance per-month price (amortised
	// upfront + recurring charges, AWS-canonical 730 hours per month).
	// Used by the local passesDollarUnitsCheck pre-filter that gates
	// cross-family alternatives. Zero when the caller didn't compute it
	// (e.g. older callers that only need primary-target analysis); the
	// pre-filter treats zero as "skip the check" so existing behaviour
	// is preserved.
	MonthlyCost float64
	// CurrencyCode is the ISO-4217 currency the prices are denominated
	// in (typically "USD"). Used by the cross-family check to refuse
	// comparisons across currencies. Empty matches the same "skip"
	// semantics as MonthlyCost == 0.
	CurrencyCode string
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
// PurchaseRecLookup (what the recommendations-store closure returns) and
// as the element type of ReshapeRecommendation.AlternativeTargets (what
// reshape emits to the HTTP handler). Kept in pkg/exchange so
// pkg/exchange stays AWS-free and providers/aws/services/ec2 imports
// the type rather than defining its own.
type OfferingOption struct {
	InstanceType         string  `json:"instance_type"`
	OfferingID           string  `json:"offering_id"`
	EffectiveMonthlyCost float64 `json:"effective_monthly_cost"`
	// NormalizationFactor is the AWS normalization factor for the
	// offering's instance size — needed by the local
	// passesDollarUnitsCheck pre-filter that gates which cross-family
	// alternatives surface in the UI. Zero on missing means the
	// alternative cannot be safely compared and the pre-filter
	// excludes it.
	NormalizationFactor float64 `json:"normalization_factor,omitempty"`
	// CurrencyCode is the ISO-4217 currency the EffectiveMonthlyCost is
	// denominated in (typically "USD"). The pre-filter requires source
	// and target currencies to match; empty on either side falls back to
	// "skip the currency guard" so today's USD-only fixtures stay green.
	CurrencyCode string `json:"currency_code,omitempty"`
}

// PurchaseRecLookup is the signature of the closure that resolves a
// region + currency to the cached AWS Cost Explorer purchase
// recommendations available there. Used by AnalyzeReshapingWithRecs to
// generate cross-family alternatives without per-recommendation AWS API
// calls — the recommendations table is already populated by the
// scheduler tick. Implementations read from store.ListStoredRecommendations
// and map each RecommendationRecord to an OfferingOption (effective
// monthly cost amortised from the upfront and monthly fields).
//
// region is the AWS region the reshape page is viewing; currencyCode is
// the source RIs' currency, propagated onto each returned
// OfferingOption.CurrencyCode so the dollar-units pre-filter can refuse
// cross-currency comparisons cleanly.
type PurchaseRecLookup func(ctx context.Context, region, currencyCode string) ([]OfferingOption, error)

// ReshapeRecommendation describes a suggested exchange for an underutilized RI.
//
// AlternativeTargets lists cross-family options enriched with real
// offering IDs and monthly cost from cached AWS Cost Explorer purchase
// recommendations (see AnalyzeReshapingWithRecs). This is advisory data
// for the UI to surface alongside the primary target; the auto-exchange
// pipeline still acts on TargetInstanceType only so existing automated
// behaviour is unchanged. Empty when the base AnalyzeReshaping is used
// directly (auto.go) or when no cached recommendations exist for the
// region.
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

// passesDollarUnitsCheck approximates AWS's exchange-validity rule
// locally: a target offering passes if (target.NF × target.EMC) >=
// (srcNF × srcMonthlyCost). AWS's actual rule is two parallel
// ≥-checks (new upfront ≥ original AND new recurring ≥ original);
// the single product approximates both because EffectiveMonthlyCost
// folds upfront amortisation + recurring + usage into one number.
//
// Currency must match between source and target. Empty CurrencyCode
// on either side is treated as "skip the currency guard" so today's
// USD-only fixtures and call-sites that don't populate it stay
// green.
//
// The approximation can produce false positives — those are caught
// at exchange time by the existing auto.go skip path on
// IsValidExchange=false. The win: zero per-pair
// GetReservedInstancesExchangeQuote API calls during recommendation
// generation, which used to make cross-family alternatives
// prohibitively expensive to surface.
func passesDollarUnitsCheck(srcNF, srcMonthlyCost float64, srcCurrency string, target OfferingOption) bool {
	if srcCurrency != "" && target.CurrencyCode != "" && srcCurrency != target.CurrencyCode {
		return false
	}
	if srcNF <= 0 || target.EffectiveMonthlyCost <= 0 || target.NormalizationFactor <= 0 {
		return false
	}
	return target.NormalizationFactor*target.EffectiveMonthlyCost >= srcNF*srcMonthlyCost
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
//
// Recommendations are emitted with empty AlternativeTargets — alternatives
// are populated only by AnalyzeReshapingWithRecs, which pairs each rec
// against the cached AWS Cost Explorer recommendations.
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
		SourceRIID:         ri.ID,
		SourceInstanceType: ri.InstanceType,
		SourceCount:        ri.InstanceCount,
		TargetInstanceType: targetInstanceType,
		TargetCount:        targetCount,
		// AlternativeTargets stays nil here; AnalyzeReshapingWithRecs
		// fills it from the cached AWS Cost Explorer recommendations.
		AlternativeTargets:  nil,
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

// AnalyzeReshapingWithRecs is AnalyzeReshaping plus cross-family
// alternatives sourced from the cached AWS Cost Explorer purchase
// recommendations table. It calls the base analyzer, then makes a
// SINGLE region-scoped lookup and pairs each rec against the returned
// offerings — no per-recommendation AWS API call, no hand-curated
// peer-family allowlist.
//
// Filtering rules per rec:
//   - The source family (parsed from rec.SourceInstanceType) is excluded
//     so the alternatives slice carries only cross-family options.
//   - The primary target (rec.TargetInstanceType) is also excluded — it
//     is already surfaced as the primary suggestion.
//   - passesDollarUnitsCheck gates each surviving offering against the
//     source RI's NF / MonthlyCost / CurrencyCode so the UI doesn't
//     show options that would be rejected at AWS exchange time. When
//     the source RI lacks pricing (MonthlyCost == 0) the gate is
//     skipped for that rec to preserve today's behaviour for callers
//     that don't supply pricing.
//
// Missing cache, lookup error, or empty-region response: rec ships with
// empty AlternativeTargets — the dashboard's primary reshape suggestion
// stays intact and the UX matches "AWS hasn't recommended anything for
// this region yet". auto.go keeps calling AnalyzeReshaping (no
// alternatives needed); only the HTTP reshape handler calls this enriched
// version.
func AnalyzeReshapingWithRecs(
	ctx context.Context,
	ris []RIInfo,
	utilization []UtilizationInfo,
	threshold float64,
	region, currencyCode string,
	lookup PurchaseRecLookup,
) []ReshapeRecommendation {
	recs := AnalyzeReshaping(ris, utilization, threshold)
	if lookup == nil || len(recs) == 0 {
		return recs
	}

	offerings, err := lookup(ctx, region, currencyCode)
	if err != nil || len(offerings) == 0 {
		// Fall through to base recs — losing alternatives is strictly
		// less bad than losing the whole reshape page.
		return recs
	}

	// Build the RI-by-ID lookup so the per-rec dollar-units pre-filter
	// can resolve source NF / MonthlyCost / CurrencyCode without
	// extending ReshapeRecommendation with internal-only fields.
	risByID := make(map[string]RIInfo, len(ris))
	for _, r := range ris {
		risByID[r.ID] = r
	}
	fillAlternativesFromRecs(recs, offerings, risByID)
	return recs
}

// fillAlternativesFromRecs is the per-rec body of
// AnalyzeReshapingWithRecs: for each rec it filters the offerings list
// to cross-family options that pass the dollar-units gate, then sorts
// them ascending by EffectiveMonthlyCost so the cheapest lands first
// in the UI list. The source family and the primary target are excluded
// so the alternatives slice is meaningfully different from the primary
// suggestion. When source pricing is missing the gate is skipped for
// that rec (NF/MonthlyCost==0 case).
func fillAlternativesFromRecs(recs []ReshapeRecommendation, offerings []OfferingOption, risByID map[string]RIInfo) {
	for i := range recs {
		src, hasSrc := risByID[recs[i].SourceRIID]
		srcFamily, _ := parseInstanceType(recs[i].SourceInstanceType)
		filled := make([]OfferingOption, 0, len(offerings))
		for _, off := range offerings {
			// Skip same-family offerings — those collapse onto the
			// primary same-family TargetInstanceType already surfaced.
			offFamily, _ := parseInstanceType(off.InstanceType)
			if offFamily == "" || strings.EqualFold(offFamily, srcFamily) {
				continue
			}
			// Skip the primary target — it's not an "alternative" to
			// itself.
			if off.InstanceType == recs[i].TargetInstanceType {
				continue
			}
			// Apply the pre-filter only when source pricing info is
			// available; otherwise keep today's behaviour and pass the
			// alternative through untouched.
			if hasSrc && src.MonthlyCost > 0 {
				if !passesDollarUnitsCheck(src.NormalizationFactor, src.MonthlyCost, src.CurrencyCode, off) {
					continue
				}
			}
			filled = append(filled, off)
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
