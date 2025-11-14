package memorydb

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/memorydb/types"
)

// PurchaseClient wraps the AWS MemoryDB client for purchasing Reserved Nodes
type PurchaseClient struct {
	client MemoryDBAPI
	common.BasePurchaseClient
}

// NewPurchaseClient creates a new MemoryDB purchase client
func NewPurchaseClient(cfg aws.Config) *PurchaseClient {
	return &PurchaseClient{
		client: memorydb.NewFromConfig(cfg),
		BasePurchaseClient: common.BasePurchaseClient{
			Region: cfg.Region,
		},
	}
}

// PurchaseRI attempts to purchase a MemoryDB Reserved Node based on the recommendation
func (c *PurchaseClient) PurchaseRI(ctx context.Context, rec common.Recommendation) common.PurchaseResult {
	result := common.PurchaseResult{
		Config:    rec,
		Timestamp: time.Now(),
	}

	// Validate it's a MemoryDB recommendation
	if rec.Service != common.ServiceMemoryDB {
		result.Success = false
		result.Message = "Invalid service type for MemoryDB purchase"
		return result
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to find offering: %v", err)
		return result
	}

	memDetails, ok := rec.ServiceDetails.(*common.MemoryDBDetails)
	if !ok {
		result.Success = false
		result.Message = "Invalid service details for MemoryDB"
		return result
	}

	// Create a unique reservation ID for tracking
	reservationID := common.GenerateReservationID("memorydb", rec.AccountName, "memorydb", rec.InstanceType, rec.Region, rec.Count, rec.Coverage)

	// Create the purchase request
	input := &memorydb.PurchaseReservedNodesOfferingInput{
		ReservedNodesOfferingId: aws.String(offeringID),
		ReservationId:           aws.String(reservationID),
		NodeCount:               aws.Int32(memDetails.NumberOfNodes),
		Tags:                    c.createPurchaseTags(rec),
	}

	// Execute the purchase
	response, err := c.client.PurchaseReservedNodesOffering(ctx, input)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to purchase MemoryDB Reserved Nodes: %v", err)
		return result
	}

	// Extract purchase information
	if response.ReservedNode != nil {
		result.Success = true
		result.PurchaseID = aws.ToString(response.ReservedNode.ReservedNodesOfferingId)
		result.ReservationID = aws.ToString(response.ReservedNode.ReservationId)
		result.Message = fmt.Sprintf("Successfully purchased %d MemoryDB nodes", memDetails.NumberOfNodes)

		// Extract cost information if available
		result.ActualCost = response.ReservedNode.FixedPrice
	} else {
		result.Success = false
		result.Message = "Purchase response was empty"
	}

	return result
}

// findOfferingID finds the appropriate Reserved Node offering ID
func (c *PurchaseClient) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	memDetails, ok := rec.ServiceDetails.(*common.MemoryDBDetails)
	if !ok {
		return "", fmt.Errorf("invalid service details for MemoryDB")
	}

	// Get offerings for the node type
	input := &memorydb.DescribeReservedNodesOfferingsInput{
		NodeType:   aws.String(memDetails.NodeType),
		MaxResults: aws.Int32(100),
	}

	result, err := c.client.DescribeReservedNodesOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe offerings: %w", err)
	}

	// Find matching offering
	for _, offering := range result.ReservedNodesOfferings {
		if offering.NodeType != nil && *offering.NodeType == memDetails.NodeType {
			// Check if duration and payment match
			if c.matchesDuration(offering.Duration, rec.Term) &&
				c.matchesOfferingType(offering.OfferingType, rec.PaymentOption) {
				return aws.ToString(offering.ReservedNodesOfferingId), nil
			}
		}
	}

	return "", fmt.Errorf("no offerings found for %s", memDetails.NodeType)
}

// matchesDuration checks if the offering duration matches our requirement
func (c *PurchaseClient) matchesDuration(offeringDuration int32, requiredMonths int) bool {
	// Duration is in seconds, convert to months
	offeringMonths := offeringDuration / 2592000 // 30 days in seconds

	// Allow some tolerance for month calculation
	return int(offeringMonths) >= requiredMonths-1 && int(offeringMonths) <= requiredMonths+1
}

// matchesOfferingType checks if the offering type matches our payment option
func (c *PurchaseClient) matchesOfferingType(offeringType *string, paymentOption string) bool {
	if offeringType == nil {
		return false
	}

	// Map payment options to MemoryDB offering types
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
	memDetails := rec.ServiceDetails.(*common.MemoryDBDetails)

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedNodesOfferingId),
		NodeType:      aws.ToString(offering.NodeType),
		Duration:      fmt.Sprintf("%d", offering.Duration),
		PaymentOption: aws.ToString(offering.OfferingType),
		FixedPrice:    offering.FixedPrice,
		CurrencyCode:  "USD", // MemoryDB doesn't have currency in API
		OfferingType:  fmt.Sprintf("%s-%d-nodes-%d-shards", memDetails.NodeType, memDetails.NumberOfNodes, memDetails.ShardCount),
	}

	// Calculate recurring charges
	for _, charge := range offering.RecurringCharges {
		if charge.RecurringChargeFrequency != nil {
			if aws.ToString(charge.RecurringChargeFrequency) == "Hourly" {
				details.UsagePrice = charge.RecurringChargeAmount
			}
		}
	}

	return details, nil
}

// BatchPurchase purchases multiple MemoryDB Reserved Nodes with error handling and rate limiting
func (c *PurchaseClient) BatchPurchase(ctx context.Context, recommendations []common.Recommendation, delayBetweenPurchases time.Duration) []common.PurchaseResult {
	return c.BasePurchaseClient.BatchPurchase(ctx, c, recommendations, delayBetweenPurchases)
}

// GetServiceType returns the service type for MemoryDB
func (c *PurchaseClient) GetServiceType() common.ServiceType {
	return common.ServiceMemoryDB
}

// createPurchaseTags creates standard tags for the purchase
func (c *PurchaseClient) createPurchaseTags(rec common.Recommendation) []types.Tag {
	memDetails := rec.ServiceDetails.(*common.MemoryDBDetails)

	return []types.Tag{
		{
			Key:   aws.String("Purpose"),
			Value: aws.String("Reserved Node Purchase"),
		},
		{
			Key:   aws.String("NodeType"),
			Value: aws.String(memDetails.NodeType),
		},
		{
			Key:   aws.String("NumberOfNodes"),
			Value: aws.String(fmt.Sprintf("%d", memDetails.NumberOfNodes)),
		},
		{
			Key:   aws.String("ShardCount"),
			Value: aws.String(fmt.Sprintf("%d", memDetails.ShardCount)),
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

// GetExistingReservedInstances retrieves existing reserved nodes
func (c *PurchaseClient) GetExistingReservedInstances(ctx context.Context) ([]common.ExistingRI, error) {
	var existingRIs []common.ExistingRI
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
			// Only include active or payment-pending reservations
			state := aws.ToString(node.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			// Calculate term in months from duration (in seconds)
			termMonths := common.GetTermMonthsFromDuration(node.Duration)

			existingRI := common.ExistingRI{
				ReservationID: aws.ToString(node.ReservationId),
				InstanceType:  aws.ToString(node.NodeType),
				Engine:        "memorydb", // MemoryDB is Redis-compatible
				Region:        c.Region,
				Count:         node.NodeCount,
				State:         state,
				StartDate:     aws.ToTime(node.StartTime),
				PaymentOption: aws.ToString(node.OfferingType),
				Term:          termMonths,
			}

			// Calculate end time based on start time and term
			existingRI.EndDate = existingRI.StartDate.AddDate(0, termMonths, 0)

			existingRIs = append(existingRIs, existingRI)
		}

		// Check if there are more results
		if response.NextToken == nil || aws.ToString(response.NextToken) == "" {
			break
		}
		nextToken = response.NextToken
	}

	return existingRIs, nil
}
// GetValidInstanceTypes returns the static list of valid instance types for memorydb
func (c *PurchaseClient) GetValidInstanceTypes(ctx context.Context) ([]string, error) {
	// Return static list as these services don't have a describe offerings API that's as comprehensive
	return common.GetStaticInstanceTypes(common.ServiceMemoryDB), nil
}
