package opensearch

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
)

// OpenSearchAPI defines the interface for OpenSearch operations we use
type OpenSearchAPI interface {
	PurchaseReservedInstanceOffering(ctx context.Context, params *opensearch.PurchaseReservedInstanceOfferingInput, optFns ...func(*opensearch.Options)) (*opensearch.PurchaseReservedInstanceOfferingOutput, error)
	DescribeReservedInstanceOfferings(ctx context.Context, params *opensearch.DescribeReservedInstanceOfferingsInput, optFns ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstanceOfferingsOutput, error)
}