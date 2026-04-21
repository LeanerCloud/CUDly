package recommendations

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
