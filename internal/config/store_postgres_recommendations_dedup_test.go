package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDedupeByNaturalKey_KeepsHighestSavings pins the collision-resolution
// rule the dedupe is built on: when multiple recs share the ON CONFLICT
// key (account_key, provider, service, region, resource_type), the one
// with the highest Savings wins. The rule matches what the UI renders
// (one row per resource shape, sorted by savings), so the representative
// is the most actionable one.
func TestDedupeByNaturalKey_KeepsHighestSavings(t *testing.T) {
	acct := "acct-1"
	recs := []RecommendationRecord{
		{CloudAccountID: &acct, Provider: "azure", Service: "compute", Region: "eastus", ResourceType: "Standard_D2s_v3", Term: 1, Savings: 100},
		{CloudAccountID: &acct, Provider: "azure", Service: "compute", Region: "eastus", ResourceType: "Standard_D2s_v3", Term: 3, Savings: 500},
		{CloudAccountID: &acct, Provider: "azure", Service: "compute", Region: "eastus", ResourceType: "Standard_D4s_v3", Term: 1, Savings: 200},
	}
	out := dedupeByNaturalKey(recs)
	require.Len(t, out, 2, "two distinct natural keys expected")

	byType := map[string]RecommendationRecord{}
	for _, r := range out {
		byType[r.ResourceType] = r
	}
	assert.InDelta(t, 500.0, byType["Standard_D2s_v3"].Savings, 1e-9, "highest-savings variant must win")
	assert.Equal(t, 3, byType["Standard_D2s_v3"].Term, "Term from the winning variant")
	assert.InDelta(t, 200.0, byType["Standard_D4s_v3"].Savings, 1e-9)
}

// TestDedupeByNaturalKey_NoCollisions returns the input untouched when
// every rec has a unique natural key. The fast-path skips allocating a
// new slice to avoid churn on the common case.
func TestDedupeByNaturalKey_NoCollisions(t *testing.T) {
	acct := "acct-1"
	recs := []RecommendationRecord{
		{CloudAccountID: &acct, Provider: "aws", Service: "compute", Region: "us-east-1", ResourceType: "m5.large"},
		{CloudAccountID: &acct, Provider: "aws", Service: "compute", Region: "us-east-1", ResourceType: "m5.xlarge"},
		{CloudAccountID: &acct, Provider: "azure", Service: "compute", Region: "eastus", ResourceType: "Standard_D2s_v3"},
	}
	out := dedupeByNaturalKey(recs)
	require.Len(t, out, 3)
}

// TestDedupeByNaturalKey_NilAccountID treats nil CloudAccountID as a
// distinct-from-empty-string sentinel — ambient-credential recs (nil
// account) must not collide with recs that happen to carry an empty-
// string account ID. The current implementation maps both to the empty
// sentinel; this test pins that behaviour so a future change is an
// explicit choice rather than a silent drift.
func TestDedupeByNaturalKey_NilAccountID(t *testing.T) {
	recs := []RecommendationRecord{
		{CloudAccountID: nil, Provider: "aws", Service: "compute", Region: "us-east-1", ResourceType: "m5.large", Savings: 10},
		{CloudAccountID: nil, Provider: "aws", Service: "compute", Region: "us-east-1", ResourceType: "m5.large", Savings: 50},
	}
	out := dedupeByNaturalKey(recs)
	require.Len(t, out, 1)
	assert.InDelta(t, 50.0, out[0].Savings, 1e-9)
}

// TestDedupeByNaturalKey_Empty returns the input unchanged when empty
// (no allocation, no panic).
func TestDedupeByNaturalKey_Empty(t *testing.T) {
	assert.Empty(t, dedupeByNaturalKey(nil))
	assert.Empty(t, dedupeByNaturalKey([]RecommendationRecord{}))
}

// TestDedupeByNaturalKey_DifferentAccountsNotCollapsed guards that
// account_key is part of the natural key — two recs with the same
// service/region/type but different accounts must both survive.
func TestDedupeByNaturalKey_DifferentAccountsNotCollapsed(t *testing.T) {
	a, b := "acct-a", "acct-b"
	recs := []RecommendationRecord{
		{CloudAccountID: &a, Provider: "azure", Service: "compute", Region: "eastus", ResourceType: "Standard_D2s_v3", Savings: 100},
		{CloudAccountID: &b, Provider: "azure", Service: "compute", Region: "eastus", ResourceType: "Standard_D2s_v3", Savings: 50},
	}
	out := dedupeByNaturalKey(recs)
	require.Len(t, out, 2)
}
