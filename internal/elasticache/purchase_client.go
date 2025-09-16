package elasticache

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticache/types"
)

// PurchaseClient wraps the AWS ElastiCache client for purchasing Reserved Cache Nodes
type PurchaseClient struct {
	client *elasticache.Client
	common.BasePurchaseClient
}

// NewPurchaseClient creates a new ElastiCache purchase client
func NewPurchaseClient(cfg aws.Config) *PurchaseClient {
	return &PurchaseClient{
		client: elasticache.NewFromConfig(cfg),
		BasePurchaseClient: common.BasePurchaseClient{
			Region: cfg.Region,
		},
	}
}

// PurchaseRI attempts to purchase a Reserved Cache Node based on the recommendation
func (c *PurchaseClient) PurchaseRI(ctx context.Context, rec common.Recommendation) common.PurchaseResult {
	result := common.PurchaseResult{
		Config:    rec,
		Timestamp: time.Now(),
	}

	// Validate it's an ElastiCache recommendation
	if rec.Service != common.ServiceElastiCache {
		result.Success = false
		result.Message = "Invalid service type for ElastiCache purchase"
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
	reservationID := fmt.Sprintf("elasticache-ri-%s-%d", rec.Region, time.Now().Unix())

	// Create the purchase request
	input := &elasticache.PurchaseCacheNodesOfferingInput{
		CacheNodesOfferingId: aws.String(offeringID),
		CacheNodeCount:       aws.Int32(rec.Count),
		ReservationId:        aws.String(reservationID),
		Tags:                 c.createPurchaseTags(rec),
	}

	// Execute the purchase
	response, err := c.client.PurchaseCacheNodesOffering(ctx, input)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to purchase Reserved Cache Node: %v", err)
		return result
	}

	// Extract purchase information
	if response.ReservedCacheNode != nil {
		result.Success = true
		result.PurchaseID = aws.ToString(response.ReservedCacheNode.ReservedCacheNodeId)
		result.ReservationID = aws.ToString(response.ReservedCacheNode.ReservationId)
		result.Message = fmt.Sprintf("Successfully purchased %d cache nodes", rec.Count)

		// Extract cost information if available
		if response.ReservedCacheNode.FixedPrice != nil {
			result.ActualCost = *response.ReservedCacheNode.FixedPrice
		}
	} else {
		result.Success = false
		result.Message = "Purchase response was empty"
	}

	return result
}

// findOfferingID finds the appropriate Reserved Cache Node offering ID
func (c *PurchaseClient) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	cacheDetails, ok := rec.ServiceDetails.(*common.ElastiCacheDetails)
	if !ok {
		return "", fmt.Errorf("invalid service details for ElastiCache")
	}

	// Convert recommendation to AWS API parameters
	duration := c.getDurationString(rec.Term)
	offeringType := common.ConvertPaymentOptionToString(rec.PaymentOption)

	input := &elasticache.DescribeCacheNodesOfferingsInput{
		CacheNodeType:      aws.String(rec.InstanceType),
		ProductDescription: aws.String(cacheDetails.Engine),
		Duration:           aws.String(duration),
		OfferingType:       aws.String(offeringType),
		MaxRecords:         aws.Int32(100),
	}

	result, err := c.client.DescribeCacheNodesOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe offerings: %w", err)
	}

	if len(result.CacheNodesOfferings) == 0 {
		return "", fmt.Errorf("no offerings found for %s %s %s",
			rec.InstanceType, cacheDetails.Engine, duration)
	}

	// Return the first matching offering ID
	offeringID := aws.ToString(result.CacheNodesOfferings[0].CacheNodesOfferingId)
	return offeringID, nil
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

	input := &elasticache.DescribeCacheNodesOfferingsInput{
		CacheNodesOfferingId: aws.String(offeringID),
	}

	result, err := c.client.DescribeCacheNodesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.CacheNodesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.CacheNodesOfferings[0]
	cacheDetails := rec.ServiceDetails.(*common.ElastiCacheDetails)

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.CacheNodesOfferingId),
		InstanceType:  aws.ToString(offering.CacheNodeType),
		Engine:        cacheDetails.Engine,
		Duration:      aws.ToString(offering.Duration),
		PaymentOption: aws.ToString(offering.OfferingType),
		FixedPrice:    aws.ToFloat64(offering.FixedPrice),
		UsagePrice:    aws.ToFloat64(offering.UsagePrice),
		CurrencyCode:  aws.ToString(offering.RecurringCharges[0].RecurringChargeFrequency),
		OfferingType:  aws.ToString(offering.OfferingType),
	}

	return details, nil
}

// BatchPurchase purchases multiple Reserved Cache Nodes with error handling and rate limiting
func (c *PurchaseClient) BatchPurchase(ctx context.Context, recommendations []common.Recommendation, delayBetweenPurchases time.Duration) []common.PurchaseResult {
	return c.BasePurchaseClient.BatchPurchase(ctx, c, recommendations, delayBetweenPurchases)
}

// getDurationString converts term months to duration string for ElastiCache API
func (c *PurchaseClient) getDurationString(termMonths int) string {
	switch termMonths {
	case 12:
		return "31536000" // 1 year in seconds
	case 36:
		return "94608000" // 3 years in seconds
	default:
		return "94608000" // Default to 3 years
	}
}

// createPurchaseTags creates standard tags for the purchase
func (c *PurchaseClient) createPurchaseTags(rec common.Recommendation) []types.Tag {
	cacheDetails := rec.ServiceDetails.(*common.ElastiCacheDetails)

	return []types.Tag{
		{
			Key:   aws.String("Purpose"),
			Value: aws.String("Reserved Cache Node Purchase"),
		},
		{
			Key:   aws.String("Engine"),
			Value: aws.String(cacheDetails.Engine),
		},
		{
			Key:   aws.String("NodeType"),
			Value: aws.String(rec.InstanceType),
		},
		{
			Key:   aws.String("Region"),
			Value: aws.String(rec.Region),
		},
		{
			Key:   aws.String("PurchaseDate"),
			Value: aws.String(time.Now().Format("2006-01-02")),
		},
		{
			Key:   aws.String("Tool"),
			Value: aws.String("ri-helper-tool"),
		},
		{
			Key:   aws.String("PaymentOption"),
			Value: aws.String(rec.PaymentOption),
		},
		{
			Key:   aws.String("Term"),
			Value: aws.String(fmt.Sprintf("%d-months", rec.Term)),
		},
	}
}