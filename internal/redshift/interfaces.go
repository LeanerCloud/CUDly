package redshift

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/redshift"
)

// RedshiftAPI defines the interface for Redshift operations we use
type RedshiftAPI interface {
	PurchaseReservedNodeOffering(ctx context.Context, params *redshift.PurchaseReservedNodeOfferingInput, optFns ...func(*redshift.Options)) (*redshift.PurchaseReservedNodeOfferingOutput, error)
	DescribeReservedNodeOfferings(ctx context.Context, params *redshift.DescribeReservedNodeOfferingsInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodeOfferingsOutput, error)
}