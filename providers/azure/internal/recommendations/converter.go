// Package recommendations owns the shared extraction from Azure's consumption
// Reservation Recommendations API response into the fields every service
// converter (compute, database, cache, cosmosdb) needs to populate on
// common.Recommendation.
//
// The API returns one of two response shapes — Azure picks based on the
// subscription's billing account type and signals it via the top-level
// `Kind` field. The SDK models both as concrete types under the same
// `ReservationRecommendationClassification` interface:
//
//   - `"legacy"` → *LegacyReservationRecommendation (Enterprise Agreement
//     subscriptions and older MCA subscription-scope billing).
//   - `"modern"` → *ModernReservationRecommendation (newer Microsoft
//     Customer Agreement billing accounts, 2019+ rollouts).
//
// Real deployments get whichever shape their billing account emits; the
// client does not choose. Handling only Legacy would leave MCA customers
// with zero recommendations — so Extract type-switches between the two
// and normalises both into a single `*ExtractedFields`. Fields that look
// the same on the surface (`*float64` on Legacy, `*Amount` wrapping
// currency on Modern) are normalised here so the per-service converters
// never see the difference.
package recommendations

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// ExtractedFields holds the per-rec data common to all four Azure
// reservation recommendation services, normalised across both Legacy
// and Modern API response shapes.
type ExtractedFields struct {
	Region           string
	ResourceType     string
	Count            int
	OnDemandCost     float64
	CommitmentCost   float64
	EstimatedSavings float64
	Term             string
	// Scope is populated from the API response ("Shared" or a subscription ID)
	// but is not yet threaded into the purchase body. All service clients
	// currently hardcode "appliedScopeType": "Shared", which is correct because
	// the recommendation filter in recommendations.go already asserts
	// "properties/scope eq 'Shared'" — only Shared-scope recommendations are
	// ever requested. This field is retained for future wiring if single-
	// subscription scoped recommendations are ever requested. See finding M1/M2
	// in docs/code-review/09-provider-azure.md.
	Scope string
	// RecurringMonthlyCost is the covered/effective recurring cost for this
	// commitment, i.e. what the customer pays WITH the reservation in place.
	// The frontend renders this column as the "covered" spend; leaving it 0
	// makes the GUI fall back to displaying OnDemandCost (the spend WITHOUT
	// any reservation), which is the opposite of the intended figure.
	//
	// The value is sourced from TotalCostWithReservedInstances (preferred);
	// if that field is absent it is reconstructed as OnDemandCost - NetSavings
	// (covered = on-demand minus net savings). Like OnDemandCost and
	// EstimatedSavings, the figure is over Azure's lookback period and is
	// treated downstream as a monthly run-rate.
	//
	// nil means the provider returned neither a total-with-RI nor an
	// (on-demand, net-savings) pair to reconstruct it from. nil renders as
	// "—" (data not available); it is NEVER set to a fabricated 0 (which
	// would falsely claim "free recurring charge").
	RecurringMonthlyCost *float64
}

// float64Ptr returns a pointer to the given float64 value. Used to
// distinguish "explicitly zero" from "not provided" (nil) on pointer fields.
func float64Ptr(v float64) *float64 {
	return &v
}

// deriveCoveredMonthlyCost computes the covered/effective recurring cost
// (what the customer pays WITH the reservation) used to populate
// ExtractedFields.RecurringMonthlyCost. Inputs are the already-normalised
// pointers from either response shape:
//
//   - totalWithRI: TotalCostWithReservedInstances (preferred, authoritative).
//   - onDemand / netSavings: used to reconstruct covered = on-demand - net
//     savings only when totalWithRI is absent.
//
// Returns nil (NOT 0) when neither source is available, and logs a warning
// so the gap surfaces instead of silently shipping a fabricated figure. The
// returned value is over Azure's lookback period and treated as a monthly
// run-rate downstream, consistent with OnDemandCost/EstimatedSavings.
func deriveCoveredMonthlyCost(resourceType string, totalWithRI, onDemand, netSavings *float64) *float64 {
	if totalWithRI != nil {
		return float64Ptr(*totalWithRI)
	}
	if onDemand != nil && netSavings != nil {
		return float64Ptr(*onDemand - *netSavings)
	}
	logging.Warnf(
		"azure recommendations: covered monthly cost unavailable for %q "+
			"(no TotalCostWithReservedInstances and no on-demand/net-savings pair); "+
			"leaving RecurringMonthlyCost nil",
		resourceType,
	)
	return nil
}

// Extract reads the Azure reservation recommendation payload into
// *ExtractedFields, normalising the Legacy/Modern shape difference.
// Returns nil if the input is:
//
//   - nil,
//   - neither `*LegacyReservationRecommendation` nor `*ModernReservationRecommendation`
//     (defensively handles future SDK additions — a new Kind would surface
//     as a Warnf log and be filtered out rather than break the pipeline),
//   - missing Properties.
//
// Callers gate on the return and build their service-specific
// *common.Recommendation around the returned fields.
func Extract(rec armconsumption.ReservationRecommendationClassification) *ExtractedFields {
	if rec == nil {
		return nil
	}
	switch v := rec.(type) {
	case *armconsumption.LegacyReservationRecommendation:
		return extractLegacy(v)
	case *armconsumption.ModernReservationRecommendation:
		return extractModern(v)
	default:
		logging.Warnf("azure recommendations: unsupported concrete type %T — dropping rec", rec)
		return nil
	}
}

// extractLegacy handles EA (and older MCA) subscription recommendations.
// Location lives on the envelope; cost fields are bare *float64.
func extractLegacy(rec *armconsumption.LegacyReservationRecommendation) *ExtractedFields {
	if rec == nil || rec.Properties == nil {
		return nil
	}
	props := rec.Properties.GetLegacyReservationRecommendationProperties()
	if props == nil {
		return nil
	}

	resourceType, quantity := resolveLegacySKUAndQuantity(props)
	out := &ExtractedFields{
		Region:       strDeref(rec.Location),
		ResourceType: resourceType,
		Term:         normaliseTerm(props.Term),
		Scope:        strDeref(props.Scope),
	}

	if quantity != nil {
		out.Count = int(*quantity)
	}
	if props.CostWithNoReservedInstances != nil {
		out.OnDemandCost = *props.CostWithNoReservedInstances
	}
	if props.TotalCostWithReservedInstances != nil {
		out.CommitmentCost = *props.TotalCostWithReservedInstances
	}
	if props.NetSavings != nil {
		// NetSavings is the savings from buying the full recommended quantity.
		// Azure Advisor sizes recommendations for 100% coverage of the
		// subscription's historical demand; downstream consumers treat this as
		// the lookback-period monthly baseline. This is the 100%-coverage
		// contract the dashboard scaler in summarizeRecommendationsWithCoverage
		// depends on (issue #215 audit).
		out.EstimatedSavings = *props.NetSavings
	}
	// Covered/effective recurring cost = what the customer pays WITH the
	// reservation. Prefer the provider-reported total-with-RI; fall back to
	// on-demand minus net savings; nil (never 0) when neither is available.
	out.RecurringMonthlyCost = deriveCoveredMonthlyCost(
		out.ResourceType,
		props.TotalCostWithReservedInstances,
		props.CostWithNoReservedInstances,
		props.NetSavings,
	)
	return out
}

// extractModern handles MCA billing-account recommendations. Location is
// on the envelope (preferred) with a fallback to the inner Properties
// copy. Cost fields are `*Amount{Currency, Value}` — we unwrap .Value to
// a bare float; currency is discarded (downstream consumers assume a
// single-currency view per subscription, same as Legacy).
func extractModern(rec *armconsumption.ModernReservationRecommendation) *ExtractedFields {
	if rec == nil || rec.Properties == nil {
		return nil
	}
	props := rec.Properties

	region := strDeref(rec.Location)
	if region == "" {
		region = strDeref(props.Location)
	}

	resourceType, quantity := resolveModernSKUAndQuantity(props)
	out := &ExtractedFields{
		Region:       region,
		ResourceType: resourceType,
		Term:         normaliseTerm(props.Term),
		Scope:        strDeref(props.Scope),
	}

	if quantity != nil {
		out.Count = int(*quantity)
	}
	out.OnDemandCost = amountValue(props.CostWithNoReservedInstances)
	out.CommitmentCost = amountValue(props.TotalCostWithReservedInstances)
	out.EstimatedSavings = amountValue(props.NetSavings)
	// Covered/effective recurring cost = what the customer pays WITH the
	// reservation. Prefer the provider-reported total-with-RI; fall back to
	// on-demand minus net savings; nil (never 0) when neither is available.
	// amountValuePtr preserves the "field absent" signal (nil) so the
	// fallback/nil logic matches the Legacy path's *float64 inputs.
	out.RecurringMonthlyCost = deriveCoveredMonthlyCost(
		out.ResourceType,
		amountValuePtr(props.TotalCostWithReservedInstances),
		amountValuePtr(props.CostWithNoReservedInstances),
		amountValuePtr(props.NetSavings),
	)

	return out
}

// resolveLegacySKUAndQuantity returns the resource type together with the
// quantity that counts THAT resource type, so the two are always in the same
// units.
//
// Azure carries two quantities on a Legacy recommendation and they are not
// interchangeable: RecommendedQuantity counts the actual SKU
// (SKUProperties[SKUName]), while RecommendedQuantityNormalized counts
// NormalizedSize. Resolving the two independently let the SKU come from one
// and the count from the other, understating the purchase by the family's
// instance-size-flexibility ratio -- 4 x Standard_D8s_v3 normalized to
// 16 x Standard_D2s_v3 was bought as 4 x Standard_D2s_v3, a quarter of the
// recommended capacity, with the full recommendation's savings still shown
// against it (issue #1540). They are resolved together here so no branch can
// produce a mixed pair.
//
// The actual SKU is preferred, matching resolveModernSKUAndQuantity, so EA and
// MCA subscriptions with identical usage yield the same ResourceType. Its
// partner RecommendedQuantity is also the *float64 field, avoiding the
// *float32 rounding question on a purchase path.
//
// When only NormalizedSize is available the normalized quantity is its only
// valid partner. If that partner is absent the count is left unset rather than
// borrowed from RecommendedQuantity: an unpaired count is precisely the defect
// above, and a zero count is refused downstream by the `Count <= 0` guard every
// service applies before purchasing.
func resolveLegacySKUAndQuantity(props *armconsumption.LegacyReservationRecommendationProperties) (resourceType string, quantity *float64) {
	if s := resourceTypeFromSKUProperties(props.SKUProperties); s != "" {
		return s, props.RecommendedQuantity
	}
	if normalized := strDeref(props.NormalizedSize); normalized != "" {
		if props.RecommendedQuantityNormalized == nil {
			logging.Warnf(
				"azure recommendations: %q has a normalized size but no RecommendedQuantityNormalized "+
					"and no SKU properties to pair RecommendedQuantity with; leaving the count unset "+
					"rather than pairing mismatched units",
				normalized,
			)
			return normalized, nil
		}
		return normalized, float64Ptr(float64(*props.RecommendedQuantityNormalized))
	}
	// No resource type from either source: there is no normalized size for the
	// count to disagree with, so this degenerate payload keeps the behavior it
	// had before the pairing fix.
	return "", props.RecommendedQuantity
}

// resolveModernSKUAndQuantity is the Modern counterpart of
// resolveLegacySKUAndQuantity, pairing each rung of the ladder with the
// quantity expressed in that rung's units.
//
// The ladder is unchanged: Modern adds a top-level SKUName pointer (the
// cleanest source), so the preference stays SKUName → NormalizedSize →
// SKUProperties fallback, and the SKUProperties rung still matches Legacy's
// contract so a switch between billing-account types does not change
// ResourceType semantics.
//
// Both SKU rungs count the actual SKU and so pair with RecommendedQuantity;
// only the NormalizedSize rung is counted by RecommendedQuantityNormalized.
// Modern was not the reported instance of #1540 -- real MCA payloads carry
// SKUName and take the first rung -- but its NormalizedSize rung mixed the
// same two fields, so it is paired here rather than left as the one branch
// that can still emit mismatched units.
func resolveModernSKUAndQuantity(props *armconsumption.ModernReservationRecommendationProperties) (resourceType string, quantity *float64) {
	if s := strDeref(props.SKUName); s != "" {
		return s, props.RecommendedQuantity
	}
	if s := strDeref(props.NormalizedSize); s != "" {
		if props.RecommendedQuantityNormalized == nil {
			logging.Warnf(
				"azure recommendations: modern recommendation %q has a normalized size but no "+
					"RecommendedQuantityNormalized; leaving the count unset rather than pairing "+
					"mismatched units",
				s,
			)
			return s, nil
		}
		return s, float64Ptr(float64(*props.RecommendedQuantityNormalized))
	}
	return resourceTypeFromSKUProperties(props.SKUProperties), props.RecommendedQuantity
}

// resourceTypeFromSKUProperties scans a SKUProperties key/value list for
// an identifier. Preference: entry named "SKUName" (Azure's convention
// for the resource SKU) or "skuName" (seen on some responses), then the
// first non-empty value as a last resort.
func resourceTypeFromSKUProperties(skus []*armconsumption.SKUProperty) string {
	for _, sku := range skus {
		if sku == nil {
			continue
		}
		if name := strDeref(sku.Name); name == "SKUName" || name == "skuName" {
			if v := strDeref(sku.Value); v != "" {
				return v
			}
		}
	}
	for _, sku := range skus {
		if sku == nil {
			continue
		}
		if v := strDeref(sku.Value); v != "" {
			return v
		}
	}
	return ""
}

// normaliseTerm maps Azure's ISO-8601 duration term strings ("P1Y", "P3Y")
// to the codebase's "1yr" / "3yr" convention. A nil or empty term
// defaults to "1yr" (matches the previous stub's invariant — downstream
// code like the purchase flow assumes a non-empty term). Unknown values
// pass through verbatim and are logged so a future SDK enum addition
// surfaces rather than breaking the pipeline silently.
func normaliseTerm(term *string) string {
	if term == nil || *term == "" {
		return "1yr"
	}
	switch *term {
	case "P1Y":
		return "1yr"
	case "P3Y":
		return "3yr"
	default:
		logging.Warnf("azure recommendations: unrecognised Term value %q; passing through verbatim", *term)
		return *term
	}
}

// amountValue unwraps Modern's *Amount{Currency, Value} wrapper to a
// bare float. Returns 0 for nil or missing-Value payloads. Currency is
// discarded — downstream Recommendation consumers assume a single-
// currency view per subscription, same as the Legacy path.
func amountValue(a *armconsumption.Amount) float64 {
	if a == nil || a.Value == nil {
		return 0
	}
	return *a.Value
}

// amountValuePtr unwraps Modern's *Amount to a *float64, preserving the
// "field absent" signal: it returns nil (not a pointer to 0) when the
// *Amount or its Value is missing. Used where the caller must distinguish
// "value absent" from "value is zero" (e.g. deriveCoveredMonthlyCost).
func amountValuePtr(a *armconsumption.Amount) *float64 {
	if a == nil || a.Value == nil {
		return nil
	}
	return float64Ptr(*a.Value)
}

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// termToMonths maps a normalised term string ("1yr", "3yr") to the number of
// months it spans. Unknown terms default to 12 so the monthly-cost arithmetic
// stays valid rather than dividing by zero.
func termToMonths(term string) int {
	switch term {
	case "3yr":
		return 36
	default:
		return 12
	}
}

// ExpandPaymentVariants fans out a single Azure reservation recommendation
// into two variants that differ only in payment schedule:
//
//   - "upfront"  — the full reservation cost is paid today; no monthly
//     recurring charge (RecurringMonthlyCost = pointer to 0).
//   - "monthly"  — nothing is paid today; the same total reservation cost
//     is spread evenly across the term months (RecurringMonthlyCost =
//     CommitmentCost / termMonths).
//
// Azure charges the same total reservation price for both billing plans
// (unlike AWS, which prices partial-upfront separately), so EstimatedSavings
// and SavingsPercentage vs on-demand are identical between the two variants;
// only the cashflow split changes.
//
// The base recommendation must already have PaymentOption set to "upfront"
// and a valid CommitmentCost (total reservation price) and OnDemandCost (total
// on-demand cost over the same period). If OnDemandCost is zero the savings
// fields are forced to zero to avoid a divide-by-zero; if CommitmentCost is
// zero both variants are still emitted with zero costs (caller's responsibility
// to validate upstream).
func ExpandPaymentVariants(base common.Recommendation) []common.Recommendation {
	totalReservation := base.CommitmentCost
	totalOnDemand := base.OnDemandCost

	var savingsPct float64
	var savings float64
	if totalOnDemand != 0 {
		savings = totalOnDemand - totalReservation
		savingsPct = savings / totalOnDemand * 100
	}

	months := termToMonths(base.Term)

	// RecurringMonthlyCost semantics:
	//   - nil   : CommitmentCost was absent from the provider response; the
	//             frontend renders "-" (data not available) rather than "$0".
	//   - &0.0  : all-upfront variant with a known non-zero CommitmentCost; the
	//             full charge was already paid upfront, so the recurring charge
	//             is a known zero.
	//   - &N    : monthly variant; CommitmentCost spread evenly over term months.
	//
	// When CommitmentCost is 0 it means data was absent from the provider, NOT
	// that the reservation is free. Using float64Ptr(0) in that case fabricates
	// a non-nil &0.0 that the frontend renders as "$0" instead of "-", which is
	// incorrect. Guard: only set non-nil pointers when we have real cost data.
	var upfrontRecurring, monthlyRecurring *float64
	if totalReservation != 0 {
		upfrontRecurring = float64Ptr(0)
		monthly := totalReservation / float64(months)
		monthlyRecurring = float64Ptr(monthly)
	}

	allUpfront := base
	allUpfront.PaymentOption = "upfront"
	allUpfront.EstimatedSavings = savings
	allUpfront.SavingsPercentage = savingsPct
	allUpfront.RecurringMonthlyCost = upfrontRecurring

	noUpfront := base
	noUpfront.PaymentOption = "monthly"
	noUpfront.EstimatedSavings = savings
	noUpfront.SavingsPercentage = savingsPct
	noUpfront.RecurringMonthlyCost = monthlyRecurring

	return []common.Recommendation{allUpfront, noUpfront}
}
