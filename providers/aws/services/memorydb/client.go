// Package memorydb provides AWS MemoryDB Reserved Nodes client
package memorydb

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/memorydb/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// MemoryDBAPI defines the interface for MemoryDB operations (enables mocking)
type MemoryDBAPI interface {
	PurchaseReservedNodesOffering(ctx context.Context, params *memorydb.PurchaseReservedNodesOfferingInput, optFns ...func(*memorydb.Options)) (*memorydb.PurchaseReservedNodesOfferingOutput, error)
	DescribeReservedNodesOfferings(ctx context.Context, params *memorydb.DescribeReservedNodesOfferingsInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOfferingsOutput, error)
	DescribeReservedNodes(ctx context.Context, params *memorydb.DescribeReservedNodesInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOutput, error)
}

// Client handles AWS MemoryDB Reserved Nodes
type Client struct {
	client MemoryDBAPI
	region string
}

// NewClient creates a new MemoryDB client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: memorydb.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// SetMemoryDBAPI sets a custom MemoryDB API client (for testing)
func (c *Client) SetMemoryDBAPI(api MemoryDBAPI) {
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

// GetRecommendations returns empty as MemoryDB uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing MemoryDB Reserved Nodes
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var nextToken *string

	for {
		input := &memorydb.DescribeReservedNodesInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(100),
		}

		response, err := c.client.DescribeReservedNodes(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe reserved nodes: %w", err)
		}

		for _, node := range response.ReservedNodes {
			state := aws.ToString(node.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			termMonths := getTermMonthsFromDuration(node.Duration)

			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(node.ReservationId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceMemoryDB,
				Region:         c.region,
				ResourceType:   aws.ToString(node.NodeType),
				Count:          int(node.NodeCount),
				State:          state,
				StartDate:      aws.ToTime(node.StartTime),
				EndDate:        aws.ToTime(node.StartTime).AddDate(0, termMonths, 0),
			}

			commitments = append(commitments, commitment)
		}

		if response.NextToken == nil || aws.ToString(response.NextToken) == "" {
			break
		}
		nextToken = response.NextToken
	}

	return commitments, nil
}

// PurchaseCommitment purchases a MemoryDB Reserved Node
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

	reservationID := common.SanitizeReservationID(fmt.Sprintf("memorydb-%s-%d", rec.ResourceType, time.Now().Unix()), "memorydb-reserved-")

	input := &memorydb.PurchaseReservedNodesOfferingInput{
		ReservedNodesOfferingId: aws.String(offeringID),
		ReservationId:           aws.String(reservationID),
		NodeCount:               aws.Int32(int32(rec.Count)),
		Tags:                    c.createPurchaseTags(rec),
	}

	response, err := c.client.PurchaseReservedNodesOffering(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase MemoryDB Reserved Nodes: %w", err)
		return result, result.Error
	}

	if response.ReservedNode != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedNode.ReservationId)
		result.Cost = response.ReservedNode.FixedPrice
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// findOfferingID finds the appropriate Reserved Node offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	input := &memorydb.DescribeReservedNodesOfferingsInput{
		NodeType:   aws.String(rec.ResourceType),
		MaxResults: aws.Int32(100),
	}

	result, err := c.client.DescribeReservedNodesOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe offerings: %w", err)
	}

	requiredMonths := c.getTermMonthsFromString(rec.Term)
	for _, offering := range result.ReservedNodesOfferings {
		if offering.NodeType != nil && *offering.NodeType == rec.ResourceType {
			if c.matchesDuration(offering.Duration, requiredMonths) &&
				c.matchesOfferingType(offering.OfferingType, rec.PaymentOption) {
				return aws.ToString(offering.ReservedNodesOfferingId), nil
			}
		}
	}

	return "", fmt.Errorf("no offerings found for %s", rec.ResourceType)
}

// matchesDuration checks if the offering duration matches
func (c *Client) matchesDuration(offeringDuration int32, requiredMonths int) bool {
	offeringMonths := offeringDuration / 2592000
	return int(offeringMonths) >= requiredMonths-1 && int(offeringMonths) <= requiredMonths+1
}

// matchesOfferingType checks if the offering type matches
func (c *Client) matchesOfferingType(offeringType *string, paymentOption string) bool {
	if offeringType == nil {
		return false
	}

	switch paymentOption {
	case "all-upfront":
		return *offeringType == "All Upfront"
	case "partial-upfront":
		return *offeringType == "Partial Upfront"
	case "no-upfront":
		return *offeringType == "No Upfront"
	default:
		return false
	}
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

	input := &memorydb.DescribeReservedNodesOfferingsInput{
		ReservedNodesOfferingId: aws.String(offeringID),
		MaxResults:              aws.Int32(1),
	}

	result, err := c.client.DescribeReservedNodesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedNodesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedNodesOfferings[0]

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedNodesOfferingId),
		ResourceType:  aws.ToString(offering.NodeType),
		Term:          fmt.Sprintf("%d", offering.Duration),
		PaymentOption: aws.ToString(offering.OfferingType),
		UpfrontCost:   offering.FixedPrice,
		Currency:      "USD",
	}

	for _, charge := range offering.RecurringCharges {
		if charge.RecurringChargeFrequency != nil {
			if aws.ToString(charge.RecurringChargeFrequency) == "Hourly" {
				details.RecurringCost = charge.RecurringChargeAmount
			}
		}
	}

	return details, nil
}

// GetValidResourceTypes returns valid MemoryDB node types (static list)
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	return []string{
		"db.t4g.small",
		"db.t4g.medium",
		"db.r6g.large",
		"db.r6g.xlarge",
		"db.r6g.2xlarge",
		"db.r6g.4xlarge",
		"db.r6g.8xlarge",
		"db.r6g.12xlarge",
		"db.r6g.16xlarge",
		"db.r7g.large",
		"db.r7g.xlarge",
		"db.r7g.2xlarge",
		"db.r7g.4xlarge",
		"db.r7g.8xlarge",
		"db.r7g.12xlarge",
		"db.r7g.16xlarge",
	}, nil
}

// createPurchaseTags creates standard tags for the purchase
func (c *Client) createPurchaseTags(rec common.Recommendation) []types.Tag {
	return []types.Tag{
		{
			Key:   aws.String("Purpose"),
			Value: aws.String("Reserved Node Purchase"),
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

// getTermMonthsFromDuration converts duration in seconds to months
func getTermMonthsFromDuration(duration int32) int {
	offeringMonths := duration / 2592000
	if offeringMonths >= 30 {
		return 36
	}
	return 12
}

// getTermMonthsFromString converts term string to months
func (c *Client) getTermMonthsFromString(term string) int {
	switch term {
	case "3yr", "3", "36":
		return 36
	default:
		return 12
	}
}
