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
	// TermSeconds is the RI commitment term in seconds (31_536_000 for
	// 1y, 94_608_000 for 3y) — the same unit the AWS SDK uses on the
	// ReservedInstance.Duration field and the unit ec2.ConvertibleRI.Duration
	// already carries. Used by the cross-family alternatives pass to
	// reject term-mismatched offerings (e.g. surfacing a 3y RI as an
	// alternative to a 1y commitment is wrong because the customer's
	// existing exchange anchor is the 1y term). Zero means "unknown" —
	// the term filter is skipped for that rec to preserve today's
	// behaviour for older callers that don't populate it.
	TermSeconds int64
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
	// TermSeconds is the offering's commitment term in seconds
	// (31_536_000 for 1y, 94_608_000 for 3y) — same unit as
	// RIInfo.TermSeconds and the AWS SDK's ReservedInstance.Duration.
	// AnalyzeReshapingWithRecs rejects term-mismatched alternatives
	// before building AlternativeTargets so a 3y RI never surfaces as
	// an alternative to a 1y source commitment (and vice versa). Zero
	// on either side falls back to "skip the term guard" for callers
	// or fixtures that don't populate it.
	TermSeconds int64 `json:"term_seconds,omitempty"`
	// SavingsAbs is the absolute monthly savings (in CurrencyCode) that
	// AWS Cost Explorer reported for this recommendation. Used by
	// compositeScore to estimate savings % and derive a confidence
	// signal. Zero means "not supplied" — the score component is omitted
	// rather than coerced to 0 (which would falsely rank this offering
	// last on the savings axis).
	SavingsAbs *float64 `json:"savings_abs,omitempty"`
	// RecommendationCount is the number of instances AWS Cost Explorer
	// included in the recommendation. Used together with SavingsAbs to
	// derive a confidence signal (large fleet + large savings = high
	// confidence). Zero means "not supplied"; the confidence signal is
	// skipped rather than treated as a 0-instance fleet.
	RecommendationCount int `json:"recommendation_count,omitempty"`
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

// Composite score weights. Higher score = better alternative. The price term
// is dominant; the remaining bonuses/penalties nudge the ranking but cannot
// flip alternatives whose effective cost differs by more than a few percent.
//
//   - scoreWeightCost:        the cost term contributes the largest share
//   - scoreWeightFamilyGen:   same-generation-jump bonus (m5->m6i beats m5->r5)
//   - scoreWeightArch:        cross-architecture penalty (Intel->ARM requires AMI validation)
//   - scoreWeightConfidence:  high/medium/low CE confidence bonus
//   - scoreWeightNFProximity: normalized-capacity proximity bonus (1.0 = exact match)
const (
	scoreWeightCost        = 0.60
	scoreWeightFamilyGen   = 0.15
	scoreWeightArch        = 0.10
	scoreWeightConfidence  = 0.10
	scoreWeightNFProximity = 0.05
)

// compositeScore returns a score in the range [0, 1] for an offering
// relative to the source RI. Higher is better. The five components are:
//
//  1. Cost (dominant, 60%): inverted effective monthly cost relative to the
//     source RI so that cheaper offerings score higher. When source pricing
//     is absent the component is replaced with a neutral 0.5 mid-point rather
//     than 0 (which would unfairly penalise offerings for a data gap in src).
//
//  2. Family-generation fit (15%): a same-prefix generation-jump (m5->m6i)
//     scores full marks; a cross-prefix alternative (m5->r5) scores zero.
//
//  3. Architecture match (10%): same architecture (both x86 or both ARM)
//     avoids an AMI-compatibility concern; a cross-arch alternative pays a
//     penalty. When the architecture is ambiguous (unknown family suffix)
//     no penalty is applied.
//
//  4. CE confidence (10%): derived from SavingsAbs + RecommendationCount
//     using the same heuristic as confidenceBucketFor in the API layer.
//     High->1.0, medium->0.5, low->0.0. Zero-value signals are treated
//     as absent (neutral 0.5) rather than "low" to avoid penalising
//     offerings that predate the field.
//
//  5. NF proximity (5%): how close the alternative's normalization capacity
//     is to the source RI's. score = 1 - min(|ratio-1|, 1). Exact match
//     (ratio = 1) scores 1.0; double or half capacity scores 0.0.
//     Zero/absent NF is treated as neutral 0.5.
func compositeScore(off OfferingOption, src RIInfo) float64 {
	return scoreWeightCost*costComponent(off, src) +
		scoreWeightFamilyGen*familyGenComponent(off, src) +
		scoreWeightArch*archComponent(off, src) +
		scoreWeightConfidence*confidenceComponent(off) +
		scoreWeightNFProximity*nfProximityComponent(off, src)
}

// costComponent returns a [0,1] cost score for the offering.
// When src pricing is absent (MonthlyCost == 0) returns 0.5 (neutral) so
// that a missing field does not falsely penalise or reward the offering.
func costComponent(off OfferingOption, src RIInfo) float64 {
	if src.MonthlyCost <= 0 || off.EffectiveMonthlyCost <= 0 {
		return 0.5
	}
	// Cheaper than source scores close to 1.0; equal scores ~0.5;
	// more expensive trends toward 0.0. Clamp to [0,1].
	ratio := src.MonthlyCost / off.EffectiveMonthlyCost
	// ratio > 1 means offering is cheaper (good); ratio < 1 means more expensive.
	// Map via: score = ratio / (1 + ratio) which is monotone on (0,+inf),
	// passes through 0.5 at ratio=1, and is bounded in [0,1].
	return ratio / (1.0 + ratio)
}

// familyGenComponent returns 1.0 when the offering is a same-family-prefix
// generation jump (e.g. m5->m6i), 0.0 otherwise. The prefix is the leading
// letters of the family name before the generation digit.
func familyGenComponent(off OfferingOption, src RIInfo) float64 {
	srcFamily, _ := parseInstanceType(src.InstanceType)
	offFamily, _ := parseInstanceType(off.InstanceType)
	if srcFamily == "" || offFamily == "" {
		return 0.0
	}
	if familyPrefix(srcFamily) == familyPrefix(offFamily) {
		return 1.0
	}
	return 0.0
}

// familyPrefix returns the leading letter(s) from a family name, stripping
// the generation number and suffix (e.g. "m6i" -> "m", "r5" -> "r",
// "c6gn" -> "c"). Returns the whole string if no digit is found.
func familyPrefix(family string) string {
	for i, r := range family {
		if r >= '0' && r <= '9' {
			return family[:i]
		}
	}
	return family
}

// archComponent returns 1.0 when source and offering share the same
// processor architecture, 0.0 for a cross-arch pairing. When either
// family is ambiguous (unknown suffix) returns 0.5 (neutral, no penalty).
//
// Architecture is inferred from the family suffix:
//   - families ending in "g" or "gd" or "gn" use ARM (AWS Graviton)
//   - all other known suffixes (blank, "i", "n", "d", "a", "id", etc.) use x86
func archComponent(off OfferingOption, src RIInfo) float64 {
	srcFamily, _ := parseInstanceType(src.InstanceType)
	offFamily, _ := parseInstanceType(off.InstanceType)
	if srcFamily == "" || offFamily == "" {
		return 0.5
	}
	srcIsARM := isARMFamily(srcFamily)
	offIsARM := isARMFamily(offFamily)
	if srcIsARM == offIsARM {
		return 1.0
	}
	return 0.0
}

// isARMFamily returns true when the EC2 family name indicates a Graviton
// (ARM) processor. The Graviton suffix is always "g" or a "g"-prefixed
// qualifier ("gd", "gn", "g2"). Detection: strip the generation digit+
// cluster from the family and check whether the remainder ends in "g".
// Examples: "m6g"->ARM, "m6gd"->ARM, "m6gn"->ARM, "m7g"->ARM,
// "m6i"->x86, "c5n"->x86, "r6a"->x86 (AMD), "hpc7g"->ARM.
func isARMFamily(family string) bool {
	// Strip the leading prefix letters to get "generation+suffix".
	genSuffix := strings.TrimLeft(family, "abcdefghijklmnopqrstuvwxyz")
	// Strip leading digits (the generation number).
	suffix := strings.TrimLeft(genSuffix, "0123456789")
	// Graviton families have a suffix that starts with "g".
	return strings.HasPrefix(suffix, "g")
}

// confidenceComponent maps SavingsAbs + RecommendationCount to a [0,1]
// confidence score using the same thresholds as the API layer:
//
//	high   ($200+ savings, 3+ instances) -> 1.0
//	medium ($50+ savings)                -> 0.5
//	low    (otherwise)                   -> 0.0
//
// When SavingsAbs is nil (not supplied) the component returns 0.5 (neutral)
// so that an absent field doesn't tilt the ranking.
func confidenceComponent(off OfferingOption) float64 {
	if off.SavingsAbs == nil {
		return 0.5
	}
	savings := *off.SavingsAbs
	count := off.RecommendationCount
	if count < 1 {
		count = 1
	}
	switch {
	case savings >= 200 && count >= 3:
		return 1.0
	case savings >= 50:
		return 0.5
	default:
		return 0.0
	}
}

// nfProximityComponent returns a [0,1] score based on how close the
// offering's total normalized capacity is to the source RI. An exact match
// (ratio = 1.0) scores 1.0; a 2x or 0.5x difference scores 0.0. When
// either NF is zero/absent (src.NormalizationFactor == 0 or
// off.NormalizationFactor == 0) the component returns 0.5 (neutral).
func nfProximityComponent(off OfferingOption, src RIInfo) float64 {
	srcNF := src.NormalizationFactor
	if srcNF <= 0 {
		_, size := parseInstanceType(src.InstanceType)
		srcNF = normalizationFactors[size]
	}
	if srcNF <= 0 || off.NormalizationFactor <= 0 {
		return 0.5
	}
	ratio := off.NormalizationFactor / srcNF
	deviation := math.Abs(ratio - 1.0)
	if deviation >= 1.0 {
		return 0.0
	}
	return 1.0 - deviation
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
// to cross-family options that match the source RI's term and pass the
// dollar-units gate, then ranks them by compositeScore (descending) so
// the best overall alternative lands first. Tie-break: ascending
// EffectiveMonthlyCost. The source family and the primary target are
// excluded so the alternatives slice is meaningfully different from the
// primary suggestion. When source pricing or the source/offering term is
// missing the relevant gate is skipped for that rec (NF/MonthlyCost==0
// / TermSeconds==0 cases).
func fillAlternativesFromRecs(recs []ReshapeRecommendation, offerings []OfferingOption, risByID map[string]RIInfo) {
	for i := range recs {
		src, hasSrc := risByID[recs[i].SourceRIID]
		srcFamily, _ := parseInstanceType(recs[i].SourceInstanceType)
		filled := make([]OfferingOption, 0, len(offerings))
		for _, off := range offerings {
			if !alternativeIsEligible(recs[i], off, src, hasSrc, srcFamily) {
				continue
			}
			filled = append(filled, off)
		}
		sort.Slice(filled, func(a, b int) bool {
			// Higher composite score = better alternative. Tie-break by
			// ascending EffectiveMonthlyCost so cheaper wins on equal scores.
			sa := compositeScore(filled[a], src)
			sb := compositeScore(filled[b], src)
			if sa != sb {
				return sa > sb
			}
			return filled[a].EffectiveMonthlyCost < filled[b].EffectiveMonthlyCost
		})
		recs[i].AlternativeTargets = filled
	}
}

// alternativeIsEligible decides whether a single offering should appear
// in the AlternativeTargets slice for the given rec. Pulled out of
// fillAlternativesFromRecs so the per-offering guards stay readable
// (and below the 10-cyclomatic-complexity gate). Returns true only when
// every gate passes:
//
//   - Cross-family: the offering must be a different family from the
//     source RI; same-family offerings collapse onto the primary
//     TargetInstanceType already surfaced.
//   - Not the primary target: an "alternative to itself" is no
//     alternative.
//   - Term match: when both sides report TermSeconds, they must match
//     because AWS only allows exchanges within the same term. Either
//     side at zero falls back to "skip the gate" so legacy callers
//     stay green.
//   - $-units: when source pricing is available, the local
//     passesDollarUnitsCheck approximation must hold; otherwise the
//     gate is skipped.
func alternativeIsEligible(rec ReshapeRecommendation, off OfferingOption, src RIInfo, hasSrc bool, srcFamily string) bool {
	if !isCrossFamilyAlternative(off, srcFamily, rec.TargetInstanceType) {
		return false
	}
	if !termMatchesIfKnown(src, off, hasSrc) {
		return false
	}
	if !pricingGatePasses(src, off, hasSrc) {
		return false
	}
	return true
}

// isCrossFamilyAlternative returns true when the offering is a valid
// cross-family alternative slot — i.e. its family parses, differs from
// the source family, and the offering isn't the same as the primary
// target the rec already surfaces.
func isCrossFamilyAlternative(off OfferingOption, srcFamily, primaryTarget string) bool {
	offFamily, _ := parseInstanceType(off.InstanceType)
	if offFamily == "" || strings.EqualFold(offFamily, srcFamily) {
		return false
	}
	if off.InstanceType == primaryTarget {
		return false
	}
	return true
}

// termMatchesIfKnown enforces the term-match guard when both sides
// report TermSeconds. Either side at zero (or no source RI at all)
// returns true so legacy fixtures and older callers keep today's
// behaviour.
func termMatchesIfKnown(src RIInfo, off OfferingOption, hasSrc bool) bool {
	if !hasSrc || src.TermSeconds <= 0 || off.TermSeconds <= 0 {
		return true
	}
	return src.TermSeconds == off.TermSeconds
}

// pricingGatePasses runs the $-units pre-filter when source pricing
// is available. Without source pricing the gate is skipped to preserve
// backwards compatibility for callers that only need the primary-target
// analysis.
//
// Defensive NF fallback: callers occasionally populate MonthlyCost but
// leave NormalizationFactor at zero (e.g. tests, partial RIInfo
// constructions). A zero NF here would reject every alternative even
// when InstanceType is parseable. Derive NF from the instance size when
// it's missing; if the size doesn't match the AWS canonical table,
// NormalizationFactorForSize returns 1.0 — degrades to "no NF
// adjustment" rather than a hard reject.
func pricingGatePasses(src RIInfo, off OfferingOption, hasSrc bool) bool {
	if !hasSrc || src.MonthlyCost <= 0 {
		return true
	}
	srcNF := src.NormalizationFactor
	if srcNF <= 0 {
		_, size := parseInstanceType(src.InstanceType)
		srcNF = NormalizationFactorForSize(size)
	}
	return passesDollarUnitsCheck(srcNF, src.MonthlyCost, src.CurrencyCode, off)
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
