package memorydb

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/memorydb"
)

// MemoryDBAPI defines the interface for MemoryDB operations we use
type MemoryDBAPI interface {
	PurchaseReservedNodesOffering(ctx context.Context, params *memorydb.PurchaseReservedNodesOfferingInput, optFns ...func(*memorydb.Options)) (*memorydb.PurchaseReservedNodesOfferingOutput, error)
	DescribeReservedNodesOfferings(ctx context.Context, params *memorydb.DescribeReservedNodesOfferingsInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOfferingsOutput, error)
}