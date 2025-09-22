package opensearch

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/opensearch/types"
)

// PurchaseClient wraps the AWS OpenSearch client for purchasing Reserved Instances
type PurchaseClient struct {
	client OpenSearchAPI
	common.BasePurchaseClient
}

// NewPurchaseClient creates a new OpenSearch purchase client
func NewPurchaseClient(cfg aws.Config) *PurchaseClient {
	return &PurchaseClient{
		client: opensearch.NewFromConfig(cfg),
		BasePurchaseClient: common.BasePurchaseClient{
			Region: cfg.Region,
		},
	}
}

// PurchaseRI attempts to purchase an OpenSearch Reserved Instance based on the recommendation
func (c *PurchaseClient) PurchaseRI(ctx context.Context, rec common.Recommendation) common.PurchaseResult {
	result := common.PurchaseResult{
		Config:    rec,
		Timestamp: time.Now(),
	}

	// Validate it's an OpenSearch recommendation
	if rec.Service != common.ServiceOpenSearch && rec.Service != common.ServiceElasticsearch {
		result.Success = false
		result.Message = "Invalid service type for OpenSearch purchase"
		return result
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to find offering: %v", err)
		return result
	}

	// Create a unique reservation ID for tracking
	reservationID := fmt.Sprintf("opensearch-ri-%s-%d", rec.Region, time.Now().Unix())

	// Create the purchase request
	input := &opensearch.PurchaseReservedInstanceOfferingInput{
		ReservedInstanceOfferingId: aws.String(offeringID),
		ReservationName:            aws.String(reservationID),
		InstanceCount:              aws.Int32(rec.Count),
	}

	// Execute the purchase
	response, err := c.client.PurchaseReservedInstanceOffering(ctx, input)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to purchase OpenSearch RI: %v", err)
		return result
	}

	// Extract purchase information
	if response.ReservedInstanceId != nil {
		result.Success = true
		result.PurchaseID = aws.ToString(response.ReservedInstanceId)
		result.ReservationID = aws.ToString(response.ReservationName)
		result.Message = fmt.Sprintf("Successfully purchased %d OpenSearch instances", rec.Count)
	} else {
		result.Success = false
		result.Message = "Purchase response was empty"
	}

	return result
}

// findOfferingID finds the appropriate Reserved Instance offering ID
func (c *PurchaseClient) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	osDetails, ok := rec.ServiceDetails.(*common.OpenSearchDetails)
	if !ok {
		return "", fmt.Errorf("invalid service details for OpenSearch")
	}

	// Get offerings for the instance type
	input := &opensearch.DescribeReservedInstanceOfferingsInput{
		MaxResults: 100,
	}

	result, err := c.client.DescribeReservedInstanceOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe offerings: %w", err)
	}

	// Find matching offering
	for _, offering := range result.ReservedInstanceOfferings {
		if string(offering.InstanceType) == osDetails.InstanceType {
			// Check payment option and duration match
			if c.matchesPaymentOption(offering.PaymentOption, rec.PaymentOption) &&
				c.matchesDuration(offering.Duration, rec.Term) {
				return aws.ToString(offering.ReservedInstanceOfferingId), nil
			}
		}
	}

	return "", fmt.Errorf("no offerings found for %s", osDetails.InstanceType)
}

// matchesPaymentOption checks if the offering payment option matches our requirement
func (c *PurchaseClient) matchesPaymentOption(offeringOption types.ReservedInstancePaymentOption, required string) bool {
	switch required {
	case "all-upfront":
		return offeringOption == types.ReservedInstancePaymentOptionAllUpfront
	case "partial-upfront":
		return offeringOption == types.ReservedInstancePaymentOptionPartialUpfront
	case "no-upfront":
		return offeringOption == types.ReservedInstancePaymentOptionNoUpfront
	default:
		return false
	}
}

// matchesDuration checks if the offering duration matches our requirement
func (c *PurchaseClient) matchesDuration(offeringDuration int32, requiredMonths int) bool {
	// Convert seconds to months (approximate)
	offeringMonths := (offeringDuration / 2592000) // 30 days in seconds

	// Allow some tolerance for month calculation
	return int(offeringMonths) >= requiredMonths-1 && int(offeringMonths) <= requiredMonths+1
}

// ValidateOffering checks if an offering exists without purchasing
func (c *PurchaseClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	_, err := c.findOfferingID(ctx, rec)
	return err
}

// GetOfferingDetails retrieves detailed information about an offering
func (c *PurchaseClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		return nil, err
	}

	// Get specific offering details
	input := &opensearch.DescribeReservedInstanceOfferingsInput{
		ReservedInstanceOfferingId: aws.String(offeringID),
		MaxResults:                 1,
	}

	result, err := c.client.DescribeReservedInstanceOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedInstanceOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedInstanceOfferings[0]
	osDetails := rec.ServiceDetails.(*common.OpenSearchDetails)

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedInstanceOfferingId),
		InstanceType:  string(offering.InstanceType),
		Engine:        "OpenSearch",
		Duration:      fmt.Sprintf("%d", offering.Duration),
		PaymentOption: string(offering.PaymentOption),
		FixedPrice:    aws.ToFloat64(offering.FixedPrice),
		UsagePrice:    aws.ToFloat64(offering.UsagePrice),
		CurrencyCode:  aws.ToString(offering.CurrencyCode),
		OfferingType:  fmt.Sprintf("%s-%d-nodes", osDetails.InstanceType, osDetails.InstanceCount),
	}

	return details, nil
}

// BatchPurchase purchases multiple OpenSearch RIs with error handling and rate limiting
func (c *PurchaseClient) BatchPurchase(ctx context.Context, recommendations []common.Recommendation, delayBetweenPurchases time.Duration) []common.PurchaseResult {
	return c.BasePurchaseClient.BatchPurchase(ctx, c, recommendations, delayBetweenPurchases)
}

// GetServiceType returns the service type for OpenSearch
func (c *PurchaseClient) GetServiceType() common.ServiceType {
	return common.ServiceOpenSearch
}