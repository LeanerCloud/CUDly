package redshift

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
)

// PurchaseClient wraps the AWS Redshift client for purchasing Reserved Nodes
type PurchaseClient struct {
	client RedshiftAPI
	common.BasePurchaseClient
}

// NewPurchaseClient creates a new Redshift purchase client
func NewPurchaseClient(cfg aws.Config) *PurchaseClient {
	return &PurchaseClient{
		client: redshift.NewFromConfig(cfg),
		BasePurchaseClient: common.BasePurchaseClient{
			Region: cfg.Region,
		},
	}
}

// PurchaseRI attempts to purchase a Redshift Reserved Node based on the recommendation
func (c *PurchaseClient) PurchaseRI(ctx context.Context, rec common.Recommendation) common.PurchaseResult {
	result := common.PurchaseResult{
		Config:    rec,
		Timestamp: time.Now(),
	}

	// Validate it's a Redshift recommendation
	if rec.Service != common.ServiceRedshift {
		result.Success = false
		result.Message = "Invalid service type for Redshift purchase"
		return result
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to find offering: %v", err)
		return result
	}

	rsDetails, ok := rec.ServiceDetails.(*common.RedshiftDetails)
	if !ok {
		result.Success = false
		result.Message = "Invalid service details for Redshift"
		return result
	}

	// Create the purchase request
	input := &redshift.PurchaseReservedNodeOfferingInput{
		ReservedNodeOfferingId: aws.String(offeringID),
		NodeCount:              aws.Int32(rsDetails.NumberOfNodes),
	}

	// Execute the purchase
	response, err := c.client.PurchaseReservedNodeOffering(ctx, input)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to purchase Redshift Reserved Node: %v", err)
		return result
	}

	// Extract purchase information
	if response.ReservedNode != nil {
		result.Success = true
		result.PurchaseID = aws.ToString(response.ReservedNode.ReservedNodeId)
		result.ReservationID = aws.ToString(response.ReservedNode.ReservedNodeOfferingId)
		result.Message = fmt.Sprintf("Successfully purchased %d Redshift nodes", rsDetails.NumberOfNodes)

		// Extract cost information if available
		if response.ReservedNode.FixedPrice != nil {
			result.ActualCost = *response.ReservedNode.FixedPrice
		}
	} else {
		result.Success = false
		result.Message = "Purchase response was empty"
	}

	return result
}

// findOfferingID finds the appropriate Reserved Node offering ID
func (c *PurchaseClient) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	rsDetails, ok := rec.ServiceDetails.(*common.RedshiftDetails)
	if !ok {
		return "", fmt.Errorf("invalid service details for Redshift")
	}

	// Get offerings for the node type
	input := &redshift.DescribeReservedNodeOfferingsInput{
		MaxRecords: aws.Int32(100),
	}

	result, err := c.client.DescribeReservedNodeOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe offerings: %w", err)
	}

	// Find matching offering
	for _, offering := range result.ReservedNodeOfferings {
		if offering.NodeType != nil && *offering.NodeType == rsDetails.NodeType {
			// Check if duration and payment match
			if c.matchesDuration(offering.Duration, rec.Term) &&
				c.matchesOfferingType(string(offering.ReservedNodeOfferingType), rec.PaymentOption) {
				return aws.ToString(offering.ReservedNodeOfferingId), nil
			}
		}
	}

	return "", fmt.Errorf("no offerings found for %s", rsDetails.NodeType)
}

// matchesDuration checks if the offering duration matches our requirement
func (c *PurchaseClient) matchesDuration(offeringDuration *int32, requiredMonths int) bool {
	if offeringDuration == nil {
		return false
	}

	// Duration is in seconds, convert to months
	offeringMonths := *offeringDuration / 2592000 // 30 days in seconds

	return int(offeringMonths) == requiredMonths
}

// matchesOfferingType checks if the offering type matches our payment option
func (c *PurchaseClient) matchesOfferingType(offeringType string, paymentOption string) bool {
	// Redshift uses offering types like "Regular" and "Upgradable"
	// For now, we accept both types regardless of payment option
	// In production, you would need to examine the offering's RecurringCharges
	// to determine the actual payment structure (all-upfront has no recurring charges,
	// partial-upfront has reduced recurring charges, no-upfront has full recurring charges)
	_ = paymentOption // Mark as intentionally unused for now
	return offeringType == "Regular" || offeringType == "Upgradable"
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
	input := &redshift.DescribeReservedNodeOfferingsInput{
		ReservedNodeOfferingId: aws.String(offeringID),
		MaxRecords:             aws.Int32(1),
	}

	result, err := c.client.DescribeReservedNodeOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedNodeOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedNodeOfferings[0]
	rsDetails := rec.ServiceDetails.(*common.RedshiftDetails)

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedNodeOfferingId),
		NodeType:      aws.ToString(offering.NodeType),
		Duration:      fmt.Sprintf("%d", aws.ToInt32(offering.Duration)),
		PaymentOption: string(offering.ReservedNodeOfferingType),
		FixedPrice:    aws.ToFloat64(offering.FixedPrice),
		UsagePrice:    aws.ToFloat64(offering.UsagePrice),
		CurrencyCode:  aws.ToString(offering.CurrencyCode),
		OfferingType:  fmt.Sprintf("%s-%d-nodes", rsDetails.NodeType, rsDetails.NumberOfNodes),
	}

	// Calculate recurring charges
	for _, charge := range offering.RecurringCharges {
		if charge.RecurringChargeAmount != nil && charge.RecurringChargeFrequency != nil {
			if *charge.RecurringChargeFrequency == "Hourly" {
				details.UsagePrice = *charge.RecurringChargeAmount
			}
		}
	}

	return details, nil
}

// BatchPurchase purchases multiple Redshift Reserved Nodes with error handling and rate limiting
func (c *PurchaseClient) BatchPurchase(ctx context.Context, recommendations []common.Recommendation, delayBetweenPurchases time.Duration) []common.PurchaseResult {
	return c.BasePurchaseClient.BatchPurchase(ctx, c, recommendations, delayBetweenPurchases)
}

// GetServiceType returns the service type for Redshift
func (c *PurchaseClient) GetServiceType() common.ServiceType {
	return common.ServiceRedshift
}

// GetExistingReservedInstances retrieves existing reserved nodes
func (c *PurchaseClient) GetExistingReservedInstances(ctx context.Context) ([]common.ExistingRI, error) {
	var existingRIs []common.ExistingRI
	var marker *string

	for {
		input := &redshift.DescribeReservedNodesInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
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
			termMonths := common.GetTermMonthsFromDuration(aws.ToInt32(node.Duration))

			existingRI := common.ExistingRI{
				ReservationID: aws.ToString(node.ReservedNodeId),
				InstanceType:  aws.ToString(node.NodeType),
				Engine:        "redshift",
				Region:        c.Region,
				Count:         aws.ToInt32(node.NodeCount),
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
		if response.Marker == nil || aws.ToString(response.Marker) == "" {
			break
		}
		marker = response.Marker
	}

	return existingRIs, nil
}
// GetValidInstanceTypes returns the static list of valid instance types for redshift
func (c *PurchaseClient) GetValidInstanceTypes(ctx context.Context) ([]string, error) {
	// Return static list as these services don't have a describe offerings API that's as comprehensive
	return common.GetStaticInstanceTypes(common.ServiceRedshift), nil
}
