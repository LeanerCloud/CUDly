package recommendations

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/mocks"
)

func TestExtract_NilInput(t *testing.T) {
	assert.Nil(t, Extract(nil))
}

// wrongTypeRec implements ReservationRecommendationClassification without
// being a *LegacyReservationRecommendation, so Extract must refuse it.
type wrongTypeRec struct{}

func (w *wrongTypeRec) GetReservationRecommendation() *armconsumption.ReservationRecommendation {
	return nil
}

func TestExtract_WrongConcreteType(t *testing.T) {
	assert.Nil(t, Extract(&wrongTypeRec{}))
}

func TestExtract_NilProperties(t *testing.T) {
	rec := mocks.BuildLegacyReservationRecommendation(mocks.WithNilProperties())
	assert.Nil(t, Extract(rec))
}

func TestExtract_AllFieldsSet(t *testing.T) {
	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("westus2"),
		mocks.WithScope("Shared"),
		mocks.WithTerm("P3Y"),
		mocks.WithQuantity(3),
		mocks.WithNormalizedSize("Standard_D2s_v3"),
		mocks.WithCosts(100, 70, 30),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	assert.Equal(t, "westus2", f.Region)
	assert.Equal(t, "Shared", f.Scope)
	assert.Equal(t, "3yr", f.Term)
	assert.Equal(t, 3, f.Count)
	assert.Equal(t, "Standard_D2s_v3", f.ResourceType)
	assert.InDelta(t, 100.0, f.OnDemandCost, 1e-9)
	assert.InDelta(t, 70.0, f.CommitmentCost, 1e-9)
	assert.InDelta(t, 30.0, f.EstimatedSavings, 1e-9)
}

func TestExtract_TermNormalisation(t *testing.T) {
	cases := []struct {
		name     string
		in       string // empty → passes nil to the fixture
		expected string
		// Passing "ZZZ" logs a warning; we just assert the output.
	}{
		{"P1Y", "P1Y", "1yr"},
		{"P3Y", "P3Y", "3yr"},
		{"Empty defaults to 1yr", "", "1yr"},
		{"Unknown is passed through verbatim", "ZZZ", "ZZZ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := mocks.BuildLegacyReservationRecommendation(mocks.WithTerm(tc.in))
			f := Extract(rec)
			require.NotNil(t, f)
			assert.Equal(t, tc.expected, f.Term)
		})
	}
}

func TestExtract_QuantityRoundsDown(t *testing.T) {
	cases := []struct {
		in       float64
		expected int
	}{
		{0, 0},
		{0.5, 0},
		{1, 1},
		{2.7, 2}, // truncation toward zero
		{5.999, 5},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			rec := mocks.BuildLegacyReservationRecommendation(mocks.WithQuantity(tc.in))
			f := Extract(rec)
			require.NotNil(t, f)
			assert.Equal(t, tc.expected, f.Count)
		})
	}
}

func TestExtract_ResourceTypePrefersNormalizedSize(t *testing.T) {
	// When both NormalizedSize and SKUProperties are populated, NormalizedSize wins.
	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithNormalizedSize("Standard_D2s_v3"),
		mocks.WithSKU("SHOULD_NOT_WIN"),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	assert.Equal(t, "Standard_D2s_v3", f.ResourceType)
}

func TestExtract_ResourceTypeFallsBackToSKUName(t *testing.T) {
	rec := mocks.BuildLegacyReservationRecommendation(
		// No NormalizedSize → should fall back to SKUProperties entry with Name="SKUName".
		mocks.WithSKU("Standard_E4s_v5"),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	assert.Equal(t, "Standard_E4s_v5", f.ResourceType)
}

func TestExtract_ResourceTypeFallsBackToFirstPropertyValue(t *testing.T) {
	// No NormalizedSize, no SKUName-keyed property — fall back to the
	// first non-empty value in the list (last-ditch fallback).
	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithSKUProperty("Cores", "4"),
		mocks.WithSKUProperty("MemoryGB", "16"),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	assert.Equal(t, "4", f.ResourceType)
}

func TestExtract_MissingCostsReadAsZero(t *testing.T) {
	// Cost fields are *float64 in the SDK — nil must become 0.0, not panic.
	rec := mocks.BuildLegacyReservationRecommendation() // no WithCosts
	f := Extract(rec)
	require.NotNil(t, f)
	assert.InDelta(t, 0.0, f.OnDemandCost, 1e-9)
	assert.InDelta(t, 0.0, f.CommitmentCost, 1e-9)
	assert.InDelta(t, 0.0, f.EstimatedSavings, 1e-9)
}

func TestExtract_Legacy_RecurringMonthlyCostIsZeroPointer(t *testing.T) {
	// Azure reservations are always all-upfront (single payment, no monthly
	// recurring charge). RecurringMonthlyCost must be a non-nil pointer to 0
	// so the frontend renders "$0" instead of "—" (which would mean unknown).
	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithNormalizedSize("Standard_D2s_v3"),
		mocks.WithCosts(100, 70, 30),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	require.NotNil(t, f.RecurringMonthlyCost, "RecurringMonthlyCost must be non-nil for Azure reservations")
	assert.InDelta(t, 0.0, *f.RecurringMonthlyCost, 1e-9)
}

// --- Modern (MCA billing account) ----------------------------------------

func TestExtract_Modern_NilProperties(t *testing.T) {
	rec := mocks.BuildModernReservationRecommendation(mocks.WithModernNilProperties())
	assert.Nil(t, Extract(rec))
}

func TestExtract_Modern_AllFieldsSet(t *testing.T) {
	rec := mocks.BuildModernReservationRecommendation(
		mocks.WithModernRegion("westeurope"),
		mocks.WithModernScope("Shared"),
		mocks.WithModernTerm("P3Y"),
		mocks.WithModernQuantity(4),
		mocks.WithModernSKUName("Standard_D4s_v5"),
		mocks.WithModernCosts(400, 260, 140),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	assert.Equal(t, "westeurope", f.Region)
	assert.Equal(t, "Shared", f.Scope)
	assert.Equal(t, "3yr", f.Term)
	assert.Equal(t, 4, f.Count)
	assert.Equal(t, "Standard_D4s_v5", f.ResourceType)
	assert.InDelta(t, 400.0, f.OnDemandCost, 1e-9)
	assert.InDelta(t, 260.0, f.CommitmentCost, 1e-9)
	assert.InDelta(t, 140.0, f.EstimatedSavings, 1e-9)
}

func TestExtract_Modern_RegionFallsBackToInnerProperties(t *testing.T) {
	// Envelope Location is nil; Properties.Location supplies the fallback.
	rec := mocks.BuildModernReservationRecommendation(
		mocks.WithModernInnerRegion("northeurope"),
		mocks.WithModernSKUName("Standard_D2"),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	assert.Equal(t, "northeurope", f.Region)
}

func TestExtract_Modern_ResourceTypePrefersSKUNameOverNormalizedSize(t *testing.T) {
	// Both populated — Modern's top-level SKUName wins over NormalizedSize
	// (matches the Modern field-preference documented in resolveModernResourceType).
	rec := mocks.BuildModernReservationRecommendation(
		mocks.WithModernSKUName("Standard_E4s_v5"),
		mocks.WithModernNormalizedSize("Standard_D2"),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	assert.Equal(t, "Standard_E4s_v5", f.ResourceType)
}

func TestExtract_Modern_ResourceTypeFallsBackToNormalizedSize(t *testing.T) {
	// No SKUName — should fall back to NormalizedSize (second preference).
	rec := mocks.BuildModernReservationRecommendation(
		mocks.WithModernNormalizedSize("Standard_D2"),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	assert.Equal(t, "Standard_D2", f.ResourceType)
}

func TestExtract_Modern_MissingCostAmountsReadAsZero(t *testing.T) {
	// *Amount fields left nil by default — amountValue guards both
	// "nil *Amount" and "non-nil *Amount with nil Value".
	rec := mocks.BuildModernReservationRecommendation()
	f := Extract(rec)
	require.NotNil(t, f)
	assert.InDelta(t, 0.0, f.OnDemandCost, 1e-9)
	assert.InDelta(t, 0.0, f.CommitmentCost, 1e-9)
	assert.InDelta(t, 0.0, f.EstimatedSavings, 1e-9)
}

func TestExtract_Modern_RecurringMonthlyCostIsZeroPointer(t *testing.T) {
	// Azure Reservation recommendations are always all-upfront regardless
	// of billing account type. RecurringMonthlyCost must be a non-nil pointer
	// to 0 for both Legacy and Modern response shapes.
	rec := mocks.BuildModernReservationRecommendation(
		mocks.WithModernRegion("westeurope"),
		mocks.WithModernSKUName("Standard_D4s_v5"),
		mocks.WithModernCosts(400, 260, 140),
	)
	f := Extract(rec)
	require.NotNil(t, f)
	require.NotNil(t, f.RecurringMonthlyCost, "RecurringMonthlyCost must be non-nil for Azure reservations")
	assert.InDelta(t, 0.0, *f.RecurringMonthlyCost, 1e-9)
}

// --- ExpandPaymentVariants --------------------------------------------------

func baseRec(service common.ServiceType, term string, onDemand, commitment float64) common.Recommendation {
	return common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        service,
		Region:         "eastus",
		ResourceType:   "Standard_D2s_v3",
		CommitmentType: common.CommitmentReservedInstance,
		Term:           term,
		PaymentOption:  "upfront",
		OnDemandCost:   onDemand,
		CommitmentCost: commitment,
	}
}

func TestExpandPaymentVariants_ReturnsTwoVariants(t *testing.T) {
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "1yr", 100, 70))
	require.Len(t, variants, 2, "must return exactly two variants")
}

func TestExpandPaymentVariants_PaymentOptionValues(t *testing.T) {
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "1yr", 100, 70))
	assert.Equal(t, "upfront", variants[0].PaymentOption)
	assert.Equal(t, "monthly", variants[1].PaymentOption)
}

func TestExpandPaymentVariants_AllUpfrontCashflow(t *testing.T) {
	// all-upfront: RecurringMonthlyCost must be a non-nil pointer to 0.
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "1yr", 100, 70))
	allUpfront := variants[0]
	require.NotNil(t, allUpfront.RecurringMonthlyCost)
	assert.InDelta(t, 0.0, *allUpfront.RecurringMonthlyCost, 1e-9)
}

func TestExpandPaymentVariants_NoUpfront1yrCashflow(t *testing.T) {
	// no-upfront 1yr: RecurringMonthlyCost = CommitmentCost / 12.
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "1yr", 100, 72))
	noUpfront := variants[1]
	require.NotNil(t, noUpfront.RecurringMonthlyCost)
	assert.InDelta(t, 72.0/12.0, *noUpfront.RecurringMonthlyCost, 1e-9)
}

func TestExpandPaymentVariants_NoUpfront3yrCashflow(t *testing.T) {
	// no-upfront 3yr: RecurringMonthlyCost = CommitmentCost / 36.
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "3yr", 200, 120))
	noUpfront := variants[1]
	require.NotNil(t, noUpfront.RecurringMonthlyCost)
	assert.InDelta(t, 120.0/36.0, *noUpfront.RecurringMonthlyCost, 1e-9)
}

func TestExpandPaymentVariants_SavingsIdenticalAcrossVariants(t *testing.T) {
	// EstimatedSavings and SavingsPercentage must be the same for both
	// variants — Azure's total reservation price is unchanged between billing
	// plans; only cashflow splits.
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "1yr", 100, 70))
	assert.InDelta(t, variants[0].EstimatedSavings, variants[1].EstimatedSavings, 1e-9)
	assert.InDelta(t, variants[0].SavingsPercentage, variants[1].SavingsPercentage, 1e-9)
}

func TestExpandPaymentVariants_SavingsValues(t *testing.T) {
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "1yr", 100, 70))
	assert.InDelta(t, 30.0, variants[0].EstimatedSavings, 1e-9)
	assert.InDelta(t, 30.0, variants[1].EstimatedSavings, 1e-9)
	assert.InDelta(t, 30.0, variants[0].SavingsPercentage, 1e-9)
	assert.InDelta(t, 30.0, variants[1].SavingsPercentage, 1e-9)
}

func TestExpandPaymentVariants_ZeroOnDemand_NoSavings(t *testing.T) {
	// Guard: avoid divide-by-zero when OnDemandCost is 0.
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "1yr", 0, 0))
	for _, v := range variants {
		assert.InDelta(t, 0.0, v.EstimatedSavings, 1e-9)
		assert.InDelta(t, 0.0, v.SavingsPercentage, 1e-9)
	}
}

func TestExpandPaymentVariants_ZeroCommitmentCost(t *testing.T) {
	// Zero reservation total: both variants still emitted; no-upfront monthly = 0.
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "1yr", 50, 0))
	require.Len(t, variants, 2)
	require.NotNil(t, variants[1].RecurringMonthlyCost)
	assert.InDelta(t, 0.0, *variants[1].RecurringMonthlyCost, 1e-9)
}

func TestExpandPaymentVariants_SharedFieldsCarriedThrough(t *testing.T) {
	// Service, Region, ResourceType, CommitmentType, Term, Account must be
	// identical between the two returned variants (only payment schedule changes).
	base := baseRec(common.ServiceRelationalDB, "3yr", 200, 140)
	base.Account = "sub-123"
	variants := ExpandPaymentVariants(base)
	for _, v := range variants {
		assert.Equal(t, common.ServiceRelationalDB, v.Service)
		assert.Equal(t, "eastus", v.Region)
		assert.Equal(t, "Standard_D2s_v3", v.ResourceType)
		assert.Equal(t, common.CommitmentReservedInstance, v.CommitmentType)
		assert.Equal(t, "3yr", v.Term)
		assert.Equal(t, "sub-123", v.Account)
	}
}

func TestExpandPaymentVariants_RecurringMonthlyCostPointersAreIndependent(t *testing.T) {
	// The two variants must hold independent pointer values — mutating one
	// must not affect the other.
	variants := ExpandPaymentVariants(baseRec(common.ServiceCompute, "1yr", 100, 60))
	require.NotNil(t, variants[0].RecurringMonthlyCost)
	require.NotNil(t, variants[1].RecurringMonthlyCost)
	assert.True(t, variants[0].RecurringMonthlyCost != variants[1].RecurringMonthlyCost,
		"each variant must own an independent pointer to its RecurringMonthlyCost")
}
