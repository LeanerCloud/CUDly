// Package tagging builds the shared CUDly tag set applied to AWS Reserved
// Instance-style commitments at purchase time.
//
// It exists so RDS, ElastiCache, and MemoryDB (all three of which accept a
// Tags field on their respective PurchaseReserved*OfferingInput types) share
// ONE source of truth for what that tag set looks like, instead of the
// byte-for-byte identical `createPurchaseTags` method that used to live on
// each service client. Each AWS SDK exposes its own `types.Tag` struct
// (rds/types.Tag, elasticache/types.Tag, memorydb/types.Tag) — distinct Go
// types with the same underlying shape — so this package returns
// SDK-agnostic Pair values and each service converts with a 5-line adapter.
package tagging

import (
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// Pair is a single tag key/value independent of any AWS SDK type.
type Pair struct {
	Key   string
	Value string
}

// PurchasePairs returns the standard CUDly purchase-time tag set:
//
//	Purpose          = <caller-supplied purpose string>
//	<resourceKey>    = rec.ResourceType   // "ResourceType" for RDS,
//	                                      // "NodeType" for ElastiCache & MemoryDB
//	Region           = rec.Region
//	PurchaseDate     = <today, YYYY-MM-DD>
//	Tool             = CUDly
//	purchase-automation = source           // only when source != ""
//
// The purpose text and resource-type key name differ per service (AWS
// convention varies — RDS uses "ResourceType", ElastiCache and MemoryDB use
// "NodeType"; purpose strings describe the product being bought) — those two
// are the only things each service customizes.
//
// Empty source skips the purchase-automation pair rather than writing an
// empty value, matching the convention established when the tag was first
// rolled out (so callers that haven't set a Source don't poison cloud tag
// inventories).
func PurchasePairs(rec common.Recommendation, purpose, resourceKey, source string) []Pair {
	pairs := []Pair{
		{Key: "Purpose", Value: purpose},
		{Key: resourceKey, Value: rec.ResourceType},
		{Key: "Region", Value: rec.Region},
		{Key: "PurchaseDate", Value: time.Now().Format("2006-01-02")},
		{Key: "Tool", Value: "CUDly"},
	}
	if source != "" {
		pairs = append(pairs, Pair{Key: common.PurchaseTagKey, Value: source})
	}
	return pairs
}
