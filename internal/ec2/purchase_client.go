package ec2

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// PurchaseClient wraps the AWS EC2 client for purchasing Reserved Instances
type PurchaseClient struct {
	client EC2API
	common.BasePurchaseClient
}

// NewPurchaseClient creates a new EC2 purchase client
func NewPurchaseClient(cfg aws.Config) *PurchaseClient {
	return &PurchaseClient{
		client: ec2.NewFromConfig(cfg),
		BasePurchaseClient: common.BasePurchaseClient{
			Region: cfg.Region,
		},
	}
}

// PurchaseRI attempts to purchase an EC2 Reserved Instance based on the recommendation
func (c *PurchaseClient) PurchaseRI(ctx context.Context, rec common.Recommendation) common.PurchaseResult {
	result := common.PurchaseResult{
		Config:    rec,
		Timestamp: time.Now(),
	}

	// Validate it's an EC2 recommendation
	if rec.Service != common.ServiceEC2 {
		result.Success = false
		result.Message = "Invalid service type for EC2 purchase"
		return result
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to find offering: %v", err)
		return result
	}

	// Create the purchase request
	input := &ec2.PurchaseReservedInstancesOfferingInput{
		ReservedInstancesOfferingId: aws.String(offeringID),
		InstanceCount:               aws.Int32(rec.Count),
	}

	// Execute the purchase
	response, err := c.client.PurchaseReservedInstancesOffering(ctx, input)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to purchase EC2 RI: %v", err)
		return result
	}

	// Extract purchase information
	if response.ReservedInstancesId != nil {
		result.Success = true
		result.PurchaseID = aws.ToString(response.ReservedInstancesId)
		result.ReservationID = aws.ToString(response.ReservedInstancesId)
		result.Message = fmt.Sprintf("Successfully purchased %d EC2 instances", rec.Count)
	} else {
		result.Success = false
		result.Message = "Purchase response was empty"
	}

	// Note: EC2 RI purchases don't immediately return cost information
	// You'd need to describe the RI to get that info

	return result
}

// findOfferingID finds the appropriate EC2 Reserved Instance offering ID
func (c *PurchaseClient) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	ec2Details, ok := rec.ServiceDetails.(*common.EC2Details)
	if !ok {
		return "", fmt.Errorf("invalid service details for EC2")
	}

	// Prepare filters for the offering search
	filters := []types.Filter{
		{
			Name:   aws.String("instance-type"),
			Values: []string{rec.InstanceType},
		},
		{
			Name:   aws.String("product-description"),
			Values: []string{ec2Details.Platform},
		},
		{
			Name:   aws.String("instance-tenancy"),
			Values: []string{ec2Details.Tenancy},
		},
	}

	// Add scope filter
	if ec2Details.Scope == "availability-zone" {
		// For AZ-scoped RIs, we'd need to specify the AZ
		// This is simplified - in reality you'd need the specific AZ
		filters = append(filters, types.Filter{
			Name:   aws.String("scope"),
			Values: []string{"Availability Zone"},
		})
	} else {
		filters = append(filters, types.Filter{
			Name:   aws.String("scope"),
			Values: []string{"Region"},
		})
	}

	// Add duration filter
	durationValue := c.getDurationValue(rec.Term)
	filters = append(filters, types.Filter{
		Name:   aws.String("duration"),
		Values: []string{fmt.Sprintf("%d", durationValue)},
	})

	// Add offering type filter
	offeringClass := c.getOfferingClass(rec.PaymentOption)
	filters = append(filters, types.Filter{
		Name:   aws.String("offering-class"),
		Values: []string{offeringClass},
	})

	input := &ec2.DescribeReservedInstancesOfferingsInput{
		Filters:            filters,
		IncludeMarketplace: aws.Bool(false),
		MaxResults:         aws.Int32(100),
	}

	result, err := c.client.DescribeReservedInstancesOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe offerings: %w", err)
	}

	if len(result.ReservedInstancesOfferings) == 0 {
		return "", fmt.Errorf("no offerings found for %s %s %s",
			rec.InstanceType, ec2Details.Platform, ec2Details.Tenancy)
	}

	// Return the first matching offering ID
	offeringID := aws.ToString(result.ReservedInstancesOfferings[0].ReservedInstancesOfferingId)
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

	input := &ec2.DescribeReservedInstancesOfferingsInput{
		ReservedInstancesOfferingIds: []string{offeringID},
	}

	result, err := c.client.DescribeReservedInstancesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedInstancesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedInstancesOfferings[0]
	ec2Details := rec.ServiceDetails.(*common.EC2Details)

	// Extract fixed price from pricing details
	var fixedPrice float64
	for _, pricing := range offering.PricingDetails {
		if pricing.Price != nil {
			fixedPrice = *pricing.Price
			break
		}
	}

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedInstancesOfferingId),
		InstanceType:  string(offering.InstanceType),
		Platform:      ec2Details.Platform,
		Duration:      fmt.Sprintf("%d", aws.ToInt64(offering.Duration)),
		PaymentOption: string(offering.OfferingType),
		FixedPrice:    fixedPrice,
		UsagePrice:    float64(aws.ToFloat32(offering.UsagePrice)),
		CurrencyCode:  string(offering.CurrencyCode),
		OfferingType:  string(offering.OfferingType),
	}

	return details, nil
}

// BatchPurchase purchases multiple EC2 RIs with error handling and rate limiting
func (c *PurchaseClient) BatchPurchase(ctx context.Context, recommendations []common.Recommendation, delayBetweenPurchases time.Duration) []common.PurchaseResult {
	return c.BasePurchaseClient.BatchPurchase(ctx, c, recommendations, delayBetweenPurchases)
}

// GetServiceType returns the service type for EC2
func (c *PurchaseClient) GetServiceType() common.ServiceType {
	return common.ServiceEC2
}

// getDurationValue converts term months to seconds for EC2 API
func (c *PurchaseClient) getDurationValue(termMonths int) int64 {
	switch termMonths {
	case 12:
		return 31536000 // 1 year in seconds
	case 36:
		return 94608000 // 3 years in seconds
	default:
		return 94608000 // Default to 3 years
	}
}

// getOfferingClass converts payment option to EC2 offering class
func (c *PurchaseClient) getOfferingClass(paymentOption string) string {
	// EC2 uses different terminology than other services
	// For simplicity, return convertible for all-upfront, standard for others
	switch paymentOption {
	case "all-upfront":
		return "convertible"
	default:
		return "standard"
	}
}

// getOfferingType converts payment option to EC2 offering type
func (c *PurchaseClient) getOfferingType(paymentOption string) types.OfferingTypeValues {
	switch paymentOption {
	case "all-upfront":
		return types.OfferingTypeValuesAllUpfront
	case "partial-upfront":
		return types.OfferingTypeValuesPartialUpfront
	case "no-upfront":
		return types.OfferingTypeValuesNoUpfront
	default:
		return types.OfferingTypeValuesPartialUpfront
	}
}

// GetExistingReservedInstances retrieves existing EC2 reserved instances
func (c *PurchaseClient) GetExistingReservedInstances(ctx context.Context) ([]common.ExistingRI, error) {
	var existingRIs []common.ExistingRI

	input := &ec2.DescribeReservedInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("state"),
				Values: []string{"active", "payment-pending"},
			},
		},
	}

	response, err := c.client.DescribeReservedInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe reserved instances: %w", err)
	}

	for _, ri := range response.ReservedInstances {
		// Extract platform from product description
		platform := string(ri.ProductDescription)

		// Calculate term in months
		duration := aws.ToInt64(ri.Duration)
		termMonths := 12
		if duration == 94608000 { // 3 years in seconds
			termMonths = 36
		}

		existingRI := common.ExistingRI{
			ReservationID: aws.ToString(ri.ReservedInstancesId),
			InstanceType:  string(ri.InstanceType),
			Engine:        platform, // For EC2, we use platform as "engine"
			Region:        c.Region,
			Count:         aws.ToInt32(ri.InstanceCount),
			State:         string(ri.State),
			StartTime:     aws.ToTime(ri.Start),
			EndTime:       aws.ToTime(ri.End),
			PaymentOption: string(ri.OfferingType),
			Term:          termMonths,
		}

		existingRIs = append(existingRIs, existingRI)
	}

	return existingRIs, nil
}
// GetValidInstanceTypes returns a list of valid instance types for EC2 using the DescribeInstanceTypeOfferings API
func (c *PurchaseClient) GetValidInstanceTypes(ctx context.Context) ([]string, error) {
	instanceTypesMap := make(map[string]bool)
	var nextToken *string

	// Query all available EC2 instance types
	for {
		input := &ec2.DescribeInstanceTypeOfferingsInput{
			LocationType: types.LocationTypeRegion,
			NextToken:    nextToken,
			MaxResults:   aws.Int32(1000),
		}

		result, err := c.client.DescribeInstanceTypeOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe EC2 instance type offerings: %w", err)
		}

		// Extract unique instance types
		for _, offering := range result.InstanceTypeOfferings {
			instanceTypesMap[string(offering.InstanceType)] = true
		}

		// Check if there are more results
		if result.NextToken == nil || aws.ToString(result.NextToken) == "" {
			break
		}
		nextToken = result.NextToken
	}

	// Convert map to sorted slice
	instanceTypes := make([]string, 0, len(instanceTypesMap))
	for instanceType := range instanceTypesMap {
		instanceTypes = append(instanceTypes, instanceType)
	}

	// Sort for consistent output
	sort.Strings(instanceTypes)
	return instanceTypes, nil
}
