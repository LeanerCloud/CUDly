package elasticache

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/elasticache"
)

// ElastiCacheClientInterface defines the interface for ElastiCache operations we use
type ElastiCacheClientInterface interface {
	DescribeReservedCacheNodesOfferings(ctx context.Context, params *elasticache.DescribeReservedCacheNodesOfferingsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error)
	PurchaseReservedCacheNodesOffering(ctx context.Context, params *elasticache.PurchaseReservedCacheNodesOfferingInput, optFns ...func(*elasticache.Options)) (*elasticache.PurchaseReservedCacheNodesOfferingOutput, error)
	DescribeReservedCacheNodes(ctx context.Context, params *elasticache.DescribeReservedCacheNodesInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOutput, error)
}