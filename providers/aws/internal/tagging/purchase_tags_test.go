package tagging

import (
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
)

func TestPurchasePairs_BaseFields(t *testing.T) {
	rec := common.Recommendation{ResourceType: "db.m5.large", Region: "eu-west-1"}
	pairs := PurchasePairs(rec, "Reserved Instance Purchase", "ResourceType", "")

	want := map[string]string{
		"Purpose":      "Reserved Instance Purchase",
		"ResourceType": "db.m5.large",
		"Region":       "eu-west-1",
		"PurchaseDate": time.Now().Format("2006-01-02"),
		"Tool":         "CUDly",
	}
	got := make(map[string]string, len(pairs))
	for _, p := range pairs {
		got[p.Key] = p.Value
	}
	assert.Equal(t, want, got)
	assert.Len(t, pairs, 5, "empty source must NOT add a purchase-automation pair")
}

func TestPurchasePairs_AppendsPurchaseAutomationWhenSourceSet(t *testing.T) {
	rec := common.Recommendation{ResourceType: "cache.t3.medium", Region: "us-east-1"}
	pairs := PurchasePairs(rec, "Reserved Cache Node Purchase", "NodeType", common.PurchaseSourceWeb)

	var found bool
	for _, p := range pairs {
		if p.Key == common.PurchaseTagKey {
			assert.Equal(t, common.PurchaseSourceWeb, p.Value)
			found = true
		}
	}
	assert.True(t, found, "expected purchase-automation pair when source is set")
	// Also confirm the custom resource-key wins over the default "ResourceType".
	assert.Contains(t, pairsMap(pairs), "NodeType")
	assert.NotContains(t, pairsMap(pairs), "ResourceType")
}

func TestPurchasePairs_HonoursPerServiceKeyName(t *testing.T) {
	// Each call must use the key name the caller supplied — no hardcoding.
	rec := common.Recommendation{ResourceType: "r6gd.xlarge"}
	rds := PurchasePairs(rec, "p", "ResourceType", "")
	mdb := PurchasePairs(rec, "p", "NodeType", "")
	assert.Contains(t, pairsMap(rds), "ResourceType")
	assert.Contains(t, pairsMap(mdb), "NodeType")
}

// pairsMap is a test helper that flattens []Pair to a map for easier assertions.
func pairsMap(pairs []Pair) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		m[p.Key] = p.Value
	}
	return m
}
