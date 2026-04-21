package mocks

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
)

// LegacyOpt configures a LegacyReservationRecommendation fixture built by
// BuildLegacyReservationRecommendation. Use the With* helpers in this file
// rather than constructing the SDK types by hand — the nested pointer shape
// makes ad-hoc literals verbose and error-prone.
type LegacyOpt func(*armconsumption.LegacyReservationRecommendation, *armconsumption.LegacyReservationRecommendationProperties)

// BuildLegacyReservationRecommendation returns a fully-typed SDK
// *LegacyReservationRecommendation (implements
// ReservationRecommendationClassification) with sensible defaults. Apply
// LegacyOpt functions to override specific fields.
//
// Defaults match a plausible VM reservation recommendation: location
// "eastus", scope "Shared", term "P1Y", quantity 1, zero costs/savings,
// empty SKU properties. Tests that assert populated fields must override
// via With* helpers.
func BuildLegacyReservationRecommendation(opts ...LegacyOpt) *armconsumption.LegacyReservationRecommendation {
	location := "eastus"
	scope := "Shared"
	term := "P1Y"
	qty := float64(1)

	props := &armconsumption.LegacyReservationRecommendationProperties{
		Scope:               &scope,
		Term:                &term,
		RecommendedQuantity: &qty,
	}
	rec := &armconsumption.LegacyReservationRecommendation{
		Location:   &location,
		Properties: props,
	}

	for _, opt := range opts {
		opt(rec, props)
	}
	return rec
}

// WithRegion sets the outer Location field used by the helper as "Region".
func WithRegion(region string) LegacyOpt {
	return func(rec *armconsumption.LegacyReservationRecommendation, _ *armconsumption.LegacyReservationRecommendationProperties) {
		rec.Location = &region
	}
}

// WithScope overrides the default "Shared" scope ("Shared" or "Single").
func WithScope(scope string) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		props.Scope = &scope
	}
}

// WithTerm overrides the Azure term (e.g. "P1Y", "P3Y"). Pass an empty
// string to exercise the "missing term defaults to 1yr" path.
func WithTerm(term string) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		if term == "" {
			props.Term = nil
			return
		}
		props.Term = &term
	}
}

// WithQuantity overrides RecommendedQuantity. Use float values (e.g. 0.5,
// 2.7) to cover the float→int truncation contract.
func WithQuantity(qty float64) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		props.RecommendedQuantity = &qty
	}
}

// WithNormalizedSize populates the preferred ResourceType source.
func WithNormalizedSize(size string) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		props.NormalizedSize = &size
	}
}

// WithSKU is a convenience that seeds SKUProperties with a single
// `{Name: "SKUName", Value: sku}` entry — the fallback path the converter
// uses when NormalizedSize is unset.
func WithSKU(sku string) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		name := "SKUName"
		value := sku
		props.SKUProperties = append(props.SKUProperties, &armconsumption.SKUProperty{
			Name:  &name,
			Value: &value,
		})
	}
}

// WithSKUProperty adds a key/value pair to SKUProperties. Use this when a
// test needs to cover specific property keys beyond the SKUName shortcut.
func WithSKUProperty(key, value string) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		k := key
		v := value
		props.SKUProperties = append(props.SKUProperties, &armconsumption.SKUProperty{
			Name:  &k,
			Value: &v,
		})
	}
}

// WithCosts sets the three cost fields in one call. Zero values are
// written as explicit pointers so the converter sees "non-nil but zero"
// (distinct from "field absent").
func WithCosts(onDemand, commitment, savings float64) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		props.CostWithNoReservedInstances = &onDemand
		props.TotalCostWithReservedInstances = &commitment
		props.NetSavings = &savings
	}
}

// WithNilProperties zeroes the Properties field, exercising the
// converter's nil-guard.
func WithNilProperties() LegacyOpt {
	return func(rec *armconsumption.LegacyReservationRecommendation, _ *armconsumption.LegacyReservationRecommendationProperties) {
		rec.Properties = nil
	}
}
