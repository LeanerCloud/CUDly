// Package recommendations owns the shared extraction from Azure's consumption
// Reservation Recommendations API response into the fields every service
// converter (compute, database, cache, cosmosdb) needs to populate on
// common.Recommendation.
//
// The Azure SDK hands each per-service converter an
// `armconsumption.ReservationRecommendationClassification`. The real data
// lives three indirections down (type-assert to *LegacyReservationRecommendation,
// unwrap Properties via GetLegacyReservationRecommendationProperties(), then
// read the struct fields). All four services go through the same ladder, so
// doing it here once prevents drift.
package recommendations

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"

	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// ExtractedFields holds the per-rec data common to all four Azure
// reservation recommendation services.
//
// Source-of-truth mapping:
//
//   - Region comes from the OUTER *LegacyReservationRecommendation.Location.
//   - Every other field comes from the INNER LegacyReservationRecommendationProperties
//     (reached via Properties.GetLegacyReservationRecommendationProperties(),
//     which the SDK provides for all three concrete property types: the
//     base LegacyReservationRecommendationProperties and the Shared/Single
//     scope variants).
type ExtractedFields struct {
	Region           string
	ResourceType     string
	Count            int
	OnDemandCost     float64
	CommitmentCost   float64
	EstimatedSavings float64
	Term             string
	Scope            string
}

// Extract reads the Azure Legacy reservation recommendation shape into
// *ExtractedFields. Returns nil if the input is nil, not a
// *LegacyReservationRecommendation, or has nil Properties — callers gate
//
//	if f := Extract(rec); f != nil { ... }
//
// and build their service-specific *common.Recommendation around the
// returned fields. Returning nil (rather than (struct, bool)) keeps the
// per-service wrappers to a single early return.
func Extract(rec armconsumption.ReservationRecommendationClassification) *ExtractedFields {
	if rec == nil {
		return nil
	}
	legacy, ok := rec.(*armconsumption.LegacyReservationRecommendation)
	if !ok || legacy == nil || legacy.Properties == nil {
		return nil
	}
	props := legacy.Properties.GetLegacyReservationRecommendationProperties()
	if props == nil {
		return nil
	}

	out := &ExtractedFields{
		Region:       strDeref(legacy.Location),
		ResourceType: resolveResourceType(props),
		Term:         normaliseTerm(props.Term),
		Scope:        strDeref(props.Scope),
	}

	if props.RecommendedQuantity != nil {
		// Go truncates float→int toward zero; for the non-negative quantities
		// Azure returns this matches the desired round-down.
		out.Count = int(*props.RecommendedQuantity)
	}
	if props.CostWithNoReservedInstances != nil {
		out.OnDemandCost = *props.CostWithNoReservedInstances
	}
	if props.TotalCostWithReservedInstances != nil {
		out.CommitmentCost = *props.TotalCostWithReservedInstances
	}
	if props.NetSavings != nil {
		// NetSavings is reported over the LookBackPeriod (the API's usage
		// window). Existing downstream consumers treat EstimatedSavings as
		// monthly, and Azure's reservation recommendation API returns
		// lookback-period savings that are typically equivalent to the
		// monthly cost delta for the recommended SKU × quantity. Pass
		// through verbatim; if a future audit shows this needs /12 (as the
		// Advisor path does — see commit 2c0bb9102 for the Advisor
		// annualSavingsAmount/12 conversion), revisit this line.
		out.EstimatedSavings = *props.NetSavings
	}

	return out
}

// resolveResourceType returns the SKU identifier for the recommendation,
// preferring the explicit NormalizedSize string when set, falling back to
// the first SKUProperty named "SKUName" (Azure's convention for the
// resource SKU in the key/value SKUProperties list), and finally to the
// first non-empty value in the list.
func resolveResourceType(props *armconsumption.LegacyReservationRecommendationProperties) string {
	if s := strDeref(props.NormalizedSize); s != "" {
		return s
	}
	for _, sku := range props.SKUProperties {
		if sku == nil {
			continue
		}
		if name := strDeref(sku.Name); name == "SKUName" || name == "skuName" {
			if v := strDeref(sku.Value); v != "" {
				return v
			}
		}
	}
	for _, sku := range props.SKUProperties {
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
// to the codebase's "1yr" / "3yr" convention. A nil or empty term defaults
// to "1yr" to match the previous stub's invariant (downstream code like
// the purchase flow assumes a non-empty term). Unknown values are passed
// through verbatim and logged so a future SDK enum addition surfaces in
// logs without silently breaking the pipeline.
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

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
