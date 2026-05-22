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
	Scope            string
	// RecurringMonthlyCost is the monthly recurring charge for this
	// commitment. Azure Reservation recommendations are all all-upfront
	// (a single payment, no recurring monthly charge), so this is always
	// a pointer to 0.0 — meaning "no recurring charge" rather than
	// "data not available" (which would be nil).
	RecurringMonthlyCost *float64
}

// float64Ptr returns a pointer to the given float64 value. Used to
// distinguish "explicitly zero" from "not provided" (nil) on pointer fields.
func float64Ptr(v float64) *float64 {
	return &v
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

	out := &ExtractedFields{
		Region:       strDeref(rec.Location),
		ResourceType: resolveLegacyResourceType(props),
		Term:         normaliseTerm(props.Term),
		Scope:        strDeref(props.Scope),
	}

	if props.RecommendedQuantity != nil {
		out.Count = int(*props.RecommendedQuantity)
	}
	if props.CostWithNoReservedInstances != nil {
		out.OnDemandCost = *props.CostWithNoReservedInstances
	}
	if props.TotalCostWithReservedInstances != nil {
		out.CommitmentCost = *props.TotalCostWithReservedInstances
	}
	if props.NetSavings != nil {
		// See package godoc — pass-through; existing downstream consumers
		// treat EstimatedSavings as lookback-period ≈ monthly.
		out.EstimatedSavings = *props.NetSavings
	}
	// Azure Reservation recommendations are always all-upfront (single payment,
	// no monthly recurring charge). Set to 0 (not nil) so the frontend renders
	// "$0" rather than "—" (which would imply "data not available").
	out.RecurringMonthlyCost = float64Ptr(0)
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

	out := &ExtractedFields{
		Region:       region,
		ResourceType: resolveModernResourceType(props),
		Term:         normaliseTerm(props.Term),
		Scope:        strDeref(props.Scope),
	}

	if props.RecommendedQuantity != nil {
		out.Count = int(*props.RecommendedQuantity)
	}
	out.OnDemandCost = amountValue(props.CostWithNoReservedInstances)
	out.CommitmentCost = amountValue(props.TotalCostWithReservedInstances)
	out.EstimatedSavings = amountValue(props.NetSavings)
	// Azure Reservation recommendations are always all-upfront (single payment,
	// no monthly recurring charge). Set to 0 (not nil) so the frontend renders
	// "$0" rather than "—" (which would imply "data not available").
	out.RecurringMonthlyCost = float64Ptr(0)

	return out
}

// resolveLegacyResourceType follows the Legacy field ladder:
// NormalizedSize → SKUProperties[Name==SKUName|skuName].Value → first
// non-empty SKUProperty.Value.
func resolveLegacyResourceType(props *armconsumption.LegacyReservationRecommendationProperties) string {
	if s := strDeref(props.NormalizedSize); s != "" {
		return s
	}
	return resourceTypeFromSKUProperties(props.SKUProperties)
}

// resolveModernResourceType follows the Modern field ladder. Modern adds
// a top-level SKUName pointer (the cleanest source), so the preference is
// SKUName → NormalizedSize → SKUProperties fallback. The SKUProperties
// fallback matches Legacy's contract so a switch between billing-account
// types doesn't change ResourceType semantics.
func resolveModernResourceType(props *armconsumption.ModernReservationRecommendationProperties) string {
	if s := strDeref(props.SKUName); s != "" {
		return s
	}
	if s := strDeref(props.NormalizedSize); s != "" {
		return s
	}
	return resourceTypeFromSKUProperties(props.SKUProperties)
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
	if totalOnDemand != 0 {
		savingsPct = (totalOnDemand - totalReservation) / totalOnDemand * 100
	}
	savings := totalOnDemand - totalReservation

	months := termToMonths(base.Term)
	recurringMonthly := totalReservation / float64(months)

	allUpfront := base
	allUpfront.PaymentOption = "upfront"
	allUpfront.EstimatedSavings = savings
	allUpfront.SavingsPercentage = savingsPct
	allUpfront.RecurringMonthlyCost = float64Ptr(0)

	noUpfront := base
	noUpfront.PaymentOption = "monthly"
	noUpfront.EstimatedSavings = savings
	noUpfront.SavingsPercentage = savingsPct
	noUpfront.RecurringMonthlyCost = float64Ptr(recurringMonthly)

	return []common.Recommendation{allUpfront, noUpfront}
}
