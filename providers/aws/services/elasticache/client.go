// Package elasticache provides AWS ElastiCache Reserved Cache Nodes client
package elasticache

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// ElastiCacheAPI defines the interface for ElastiCache operations (enables mocking)
type ElastiCacheAPI interface {
	DescribeReservedCacheNodesOfferings(ctx context.Context, params *elasticache.DescribeReservedCacheNodesOfferingsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error)
	PurchaseReservedCacheNodesOffering(ctx context.Context, params *elasticache.PurchaseReservedCacheNodesOfferingInput, optFns ...func(*elasticache.Options)) (*elasticache.PurchaseReservedCacheNodesOfferingOutput, error)
	DescribeReservedCacheNodes(ctx context.Context, params *elasticache.DescribeReservedCacheNodesInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOutput, error)
}

// Client handles AWS ElastiCache Reserved Cache Nodes
type Client struct {
	client ElastiCacheAPI
	region string
}

// NewClient creates a new ElastiCache client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: elasticache.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// SetElastiCacheAPI sets a custom ElastiCache API client (for testing)
func (c *Client) SetElastiCacheAPI(api ElastiCacheAPI) {
	c.client = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceCache
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as ElastiCache uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing ElastiCache Reserved Cache Nodes
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var marker *string

	for {
		input := &elasticache.DescribeReservedCacheNodesInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		response, err := c.client.DescribeReservedCacheNodes(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe reserved cache nodes: %w", err)
		}

		for _, node := range response.ReservedCacheNodes {
			state := aws.ToString(node.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			duration := aws.ToInt32(node.Duration)
			termMonths := 12
			if duration == 94608000 {
				termMonths = 36
			}

			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(node.ReservedCacheNodeId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceCache,
				Region:         c.region,
				ResourceType:   aws.ToString(node.CacheNodeType),
				Count:          int(aws.ToInt32(node.CacheNodeCount)),
				State:          state,
				StartDate:      aws.ToTime(node.StartTime),
				EndDate:        aws.ToTime(node.StartTime).AddDate(0, termMonths, 0),
			}

			commitments = append(commitments, commitment)
		}

		if response.Marker == nil || aws.ToString(response.Marker) == "" {
			break
		}
		marker = response.Marker
	}

	return commitments, nil
}

// PurchaseCommitment purchases an ElastiCache Reserved Cache Node
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Error = fmt.Errorf("failed to find offering: %w", err)
		return result, result.Error
	}

	reservationID := common.SanitizeReservationID(fmt.Sprintf("elasticache-%s-%d", rec.ResourceType, time.Now().Unix()), "elasticache-reserved-")

	input := &elasticache.PurchaseReservedCacheNodesOfferingInput{
		ReservedCacheNodesOfferingId: aws.String(offeringID),
		CacheNodeCount:               aws.Int32(int32(rec.Count)),
		ReservedCacheNodeId:          aws.String(reservationID),
		Tags:                         c.createPurchaseTags(rec),
	}

	response, err := c.client.PurchaseReservedCacheNodesOffering(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase Reserved Cache Node: %w", err)
		return result, result.Error
	}

	if response.ReservedCacheNode != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedCacheNode.ReservedCacheNodeId)
		if response.ReservedCacheNode.FixedPrice != nil {
			result.Cost = *response.ReservedCacheNode.FixedPrice
		}
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// findOfferingID finds the appropriate Reserved Cache Node offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	details, ok := rec.Details.(*common.CacheDetails)
	if !ok || details == nil {
		return "", fmt.Errorf("invalid service details for ElastiCache")
	}

	duration := c.getDurationString(rec.Term)
	offeringType := c.convertPaymentOption(rec.PaymentOption)

	input := &elasticache.DescribeReservedCacheNodesOfferingsInput{
		CacheNodeType:      aws.String(rec.ResourceType),
		ProductDescription: aws.String(details.Engine),
		Duration:           aws.String(duration),
		OfferingType:       aws.String(offeringType),
		MaxRecords:         aws.Int32(100),
	}

	result, err := c.client.DescribeReservedCacheNodesOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe offerings: %w", err)
	}

	if len(result.ReservedCacheNodesOfferings) == 0 {
		return "", fmt.Errorf("no offerings found for %s %s %s",
			rec.ResourceType, details.Engine, duration)
	}

	return aws.ToString(result.ReservedCacheNodesOfferings[0].ReservedCacheNodesOfferingId), nil
}

// ValidateOffering checks if an offering exists without purchasing
func (c *Client) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	_, err := c.findOfferingID(ctx, rec)
	return err
}

// GetOfferingDetails retrieves offering details
func (c *Client) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		return nil, err
	}

	input := &elasticache.DescribeReservedCacheNodesOfferingsInput{
		ReservedCacheNodesOfferingId: aws.String(offeringID),
	}

	result, err := c.client.DescribeReservedCacheNodesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedCacheNodesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedCacheNodesOfferings[0]

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedCacheNodesOfferingId),
		ResourceType:  aws.ToString(offering.CacheNodeType),
		Term:          fmt.Sprintf("%d", aws.ToInt32(offering.Duration)),
		PaymentOption: aws.ToString(offering.OfferingType),
		UpfrontCost:   aws.ToFloat64(offering.FixedPrice),
		RecurringCost: aws.ToFloat64(offering.UsagePrice),
		Currency:      "USD",
	}

	return details, nil
}

// GetValidResourceTypes returns valid ElastiCache node types
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	instanceTypesMap := make(map[string]bool)
	var marker *string

	for {
		input := &elasticache.DescribeReservedCacheNodesOfferingsInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		result, err := c.client.DescribeReservedCacheNodesOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe ElastiCache offerings: %w", err)
		}

		for _, offering := range result.ReservedCacheNodesOfferings {
			if offering.CacheNodeType != nil {
				instanceTypesMap[*offering.CacheNodeType] = true
			}
		}

		if result.Marker == nil || aws.ToString(result.Marker) == "" {
			break
		}
		marker = result.Marker
	}

	instanceTypes := make([]string, 0, len(instanceTypesMap))
	for instanceType := range instanceTypesMap {
		instanceTypes = append(instanceTypes, instanceType)
	}

	sort.Strings(instanceTypes)
	return instanceTypes, nil
}

// getDurationString converts term string to duration string
func (c *Client) getDurationString(term string) string {
	if term == "3yr" || term == "3" {
		return "94608000"
	}
	return "31536000"
}

// convertPaymentOption converts payment option to AWS string
func (c *Client) convertPaymentOption(option string) string {
	switch option {
	case "all-upfront":
		return "All Upfront"
	case "partial-upfront":
		return "Partial Upfront"
	case "no-upfront":
		return "No Upfront"
	default:
		return "Partial Upfront"
	}
}

// createPurchaseTags creates standard tags for the purchase
func (c *Client) createPurchaseTags(rec common.Recommendation) []types.Tag {
	return []types.Tag{
		{
			Key:   aws.String("Purpose"),
			Value: aws.String("Reserved Cache Node Purchase"),
		},
		{
			Key:   aws.String("NodeType"),
			Value: aws.String(rec.ResourceType),
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
			Value: aws.String("CUDly"),
		},
	}
}
