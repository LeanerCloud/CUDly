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

	normQty := float32(qty)

	props := &armconsumption.LegacyReservationRecommendationProperties{
		Scope:                         &scope,
		Term:                          &term,
		RecommendedQuantity:           &qty,
		RecommendedQuantityNormalized: &normQty,
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

// WithQuantity overrides BOTH recommended quantities, modeling the common
// case where the recommended SKU is already the family's base size so the
// normalized and un-normalized counts agree. Use float values (e.g. 0.5, 2.7)
// to cover the float→int truncation contract.
//
// Setting both matters because the converter pairs each resource-type source
// with the quantity expressed in that source's units (issue #1540): a fixture
// that set only RecommendedQuantity while naming a NormalizedSize would
// describe a payload with no valid pairing. Use WithNormalizedQuantity to make
// the two differ deliberately.
func WithQuantity(qty float64) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		normQty := float32(qty)
		props.RecommendedQuantity = &qty
		props.RecommendedQuantityNormalized = &normQty
	}
}

// WithNormalizedQuantity overrides RecommendedQuantityNormalized alone, so a
// test can express the real shape the normalization ratio produces: e.g.
// 4 x Standard_D8s_v3 recommended, 16 x Standard_D2s_v3 normalized. Pass it
// after WithQuantity, which sets both.
func WithNormalizedQuantity(qty float32) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		props.RecommendedQuantityNormalized = &qty
	}
}

// WithoutNormalizedQuantity clears RecommendedQuantityNormalized, modeling a
// payload that names a NormalizedSize but gives no count in its units.
func WithoutNormalizedQuantity() LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		props.RecommendedQuantityNormalized = nil
	}
}

// WithNormalizedSize populates NormalizedSize, the ResourceType source used
// when no SKU property names the actual SKU. It is counted by
// RecommendedQuantityNormalized, not RecommendedQuantity (issue #1540).
func WithNormalizedSize(size string) LegacyOpt {
	return func(_ *armconsumption.LegacyReservationRecommendation, props *armconsumption.LegacyReservationRecommendationProperties) {
		props.NormalizedSize = &size
	}
}

// WithSKU is a convenience that seeds SKUProperties with a single
// `{Name: "SKUName", Value: sku}` entry — the actual SKU, which the converter
// prefers over NormalizedSize because RecommendedQuantity counts it.
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

// ModernOpt configures a *ModernReservationRecommendation fixture built
// by BuildModernReservationRecommendation. Mirror of LegacyOpt.
type ModernOpt func(*armconsumption.ModernReservationRecommendation, *armconsumption.ModernReservationRecommendationProperties)

// BuildModernReservationRecommendation returns an SDK-typed
// *ModernReservationRecommendation (MCA billing account shape). Defaults
// mirror the Legacy builder: "eastus" location, "Shared" scope, "P1Y"
// term, quantity 1, empty SKU. Cost fields use Azure's *Amount wrapper —
// pass WithModernCosts or leave them nil to exercise the amountValue
// nil-guard.
func BuildModernReservationRecommendation(opts ...ModernOpt) *armconsumption.ModernReservationRecommendation {
	location := "eastus"
	scope := "Shared"
	term := "P1Y"
	qty := float64(1)

	normQty := float32(qty)

	props := &armconsumption.ModernReservationRecommendationProperties{
		Scope:                         &scope,
		Term:                          &term,
		RecommendedQuantity:           &qty,
		RecommendedQuantityNormalized: &normQty,
	}
	rec := &armconsumption.ModernReservationRecommendation{
		Location:   &location,
		Properties: props,
	}
	for _, opt := range opts {
		opt(rec, props)
	}
	return rec
}

// WithModernRegion sets the outer envelope Location (the preferred source
// for Region extraction).
func WithModernRegion(region string) ModernOpt {
	return func(rec *armconsumption.ModernReservationRecommendation, _ *armconsumption.ModernReservationRecommendationProperties) {
		rec.Location = &region
	}
}

// WithModernInnerRegion clears the outer Location and sets the inner
// Properties.Location instead — exercises the fallback path.
func WithModernInnerRegion(region string) ModernOpt {
	return func(rec *armconsumption.ModernReservationRecommendation, props *armconsumption.ModernReservationRecommendationProperties) {
		rec.Location = nil
		props.Location = &region
	}
}

// WithModernScope overrides the default "Shared" scope.
func WithModernScope(scope string) ModernOpt {
	return func(_ *armconsumption.ModernReservationRecommendation, props *armconsumption.ModernReservationRecommendationProperties) {
		props.Scope = &scope
	}
}

// WithModernTerm overrides the Azure term. Empty string clears the field
// entirely to exercise the "missing defaults to 1yr" path.
func WithModernTerm(term string) ModernOpt {
	return func(_ *armconsumption.ModernReservationRecommendation, props *armconsumption.ModernReservationRecommendationProperties) {
		if term == "" {
			props.Term = nil
			return
		}
		props.Term = &term
	}
}

// WithModernQuantity overrides BOTH recommended quantities, for the same
// reason as the Legacy WithQuantity: the converter pairs each resource-type
// source with the quantity in that source's units (issue #1540).
func WithModernQuantity(qty float64) ModernOpt {
	return func(_ *armconsumption.ModernReservationRecommendation, props *armconsumption.ModernReservationRecommendationProperties) {
		normQty := float32(qty)
		props.RecommendedQuantity = &qty
		props.RecommendedQuantityNormalized = &normQty
	}
}

// WithModernNormalizedQuantity overrides RecommendedQuantityNormalized alone,
// so a test can make the normalized and un-normalized counts differ. Pass it
// after WithModernQuantity, which sets both.
func WithModernNormalizedQuantity(qty float32) ModernOpt {
	return func(_ *armconsumption.ModernReservationRecommendation, props *armconsumption.ModernReservationRecommendationProperties) {
		props.RecommendedQuantityNormalized = &qty
	}
}

// WithModernSKUName sets the top-level Modern SKUName — the preferred
// source for ResourceType on Modern recs.
func WithModernSKUName(sku string) ModernOpt {
	return func(_ *armconsumption.ModernReservationRecommendation, props *armconsumption.ModernReservationRecommendationProperties) {
		props.SKUName = &sku
	}
}

// WithModernNormalizedSize populates NormalizedSize (second-preference
// source for ResourceType on Modern). Counted by
// RecommendedQuantityNormalized, not RecommendedQuantity (issue #1540).
func WithModernNormalizedSize(size string) ModernOpt {
	return func(_ *armconsumption.ModernReservationRecommendation, props *armconsumption.ModernReservationRecommendationProperties) {
		props.NormalizedSize = &size
	}
}

// WithModernCosts sets the three *Amount cost fields. All three receive
// the same currency ("USD" default); pass an empty currency to cover the
// "currency not sent" edge case (the converter discards currency anyway).
func WithModernCosts(onDemand, commitment, savings float64) ModernOpt {
	return func(_ *armconsumption.ModernReservationRecommendation, props *armconsumption.ModernReservationRecommendationProperties) {
		currency := "USD"
		props.CostWithNoReservedInstances = &armconsumption.Amount{Currency: &currency, Value: &onDemand}
		props.TotalCostWithReservedInstances = &armconsumption.Amount{Currency: &currency, Value: &commitment}
		props.NetSavings = &armconsumption.Amount{Currency: &currency, Value: &savings}
	}
}

// WithModernNilProperties zeroes Properties, exercising the guard.
func WithModernNilProperties() ModernOpt {
	return func(rec *armconsumption.ModernReservationRecommendation, _ *armconsumption.ModernReservationRecommendationProperties) {
		rec.Properties = nil
	}
}
