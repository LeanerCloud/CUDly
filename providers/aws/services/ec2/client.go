// Package ec2 provides AWS EC2 Reserved Instances client
package ec2

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// EC2API defines the interface for EC2 operations (enables mocking)
type EC2API interface {
	PurchaseReservedInstancesOffering(ctx context.Context, params *ec2.PurchaseReservedInstancesOfferingInput, optFns ...func(*ec2.Options)) (*ec2.PurchaseReservedInstancesOfferingOutput, error)
	DescribeReservedInstancesOfferings(ctx context.Context, params *ec2.DescribeReservedInstancesOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOfferingsOutput, error)
	DescribeReservedInstances(ctx context.Context, params *ec2.DescribeReservedInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOutput, error)
	DescribeInstanceTypeOfferings(ctx context.Context, params *ec2.DescribeInstanceTypeOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
}

// Client handles AWS EC2 Reserved Instances
type Client struct {
	client EC2API
	region string
}

// NewClient creates a new EC2 client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: ec2.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// SetEC2API sets a custom EC2 API client (for testing)
func (c *Client) SetEC2API(api EC2API) {
	c.client = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceCompute
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as EC2 uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	// EC2 recommendations come from Cost Explorer API via RecommendationsClient
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing EC2 Reserved Instances
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

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
		// Calculate term in months
		duration := aws.ToInt64(ri.Duration)
		termMonths := 12
		if duration == 94608000 { // 3 years in seconds
			termMonths = 36
		}

		commitment := common.Commitment{
			Provider:       common.ProviderAWS,
			CommitmentID:   aws.ToString(ri.ReservedInstancesId),
			CommitmentType: common.CommitmentReservedInstance,
			Service:        common.ServiceEC2,
			Region:         c.region,
			ResourceType:   string(ri.InstanceType),
			Count:          int(aws.ToInt32(ri.InstanceCount)),
			State:          string(ri.State),
			StartDate:      aws.ToTime(ri.Start),
			EndDate:        aws.ToTime(ri.End),
		}

		// Set term string
		if termMonths == 36 {
			commitment.ResourceType = string(ri.InstanceType)
		}

		commitments = append(commitments, commitment)
	}

	return commitments, nil
}

// PurchaseCommitment purchases an EC2 Reserved Instance
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Error = fmt.Errorf("failed to find offering: %w", err)
		return result, result.Error
	}

	// Create the purchase request
	input := &ec2.PurchaseReservedInstancesOfferingInput{
		ReservedInstancesOfferingId: aws.String(offeringID),
		InstanceCount:               aws.Int32(int32(rec.Count)),
	}

	// Execute the purchase
	response, err := c.client.PurchaseReservedInstancesOffering(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase EC2 RI: %w", err)
		return result, result.Error
	}

	// Extract purchase information
	if response.ReservedInstancesId != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedInstancesId)
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// findOfferingID finds the appropriate EC2 Reserved Instance offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	details, ok := rec.Details.(*common.ComputeDetails)
	if !ok || details == nil {
		return "", fmt.Errorf("invalid service details for EC2")
	}

	// Default values if not specified
	platform := details.Platform
	if platform == "" {
		platform = "Linux/UNIX"
	}
	tenancy := details.Tenancy
	if tenancy == "" {
		tenancy = "default"
	}
	scope := details.Scope
	if scope == "" {
		scope = "Region"
	}

	// Prepare filters for the offering search
	filters := []types.Filter{
		{
			Name:   aws.String("instance-type"),
			Values: []string{rec.ResourceType},
		},
		{
			Name:   aws.String("product-description"),
			Values: []string{platform},
		},
		{
			Name:   aws.String("instance-tenancy"),
			Values: []string{tenancy},
		},
		{
			Name:   aws.String("scope"),
			Values: []string{scope},
		},
	}

	// Add duration filter
	durationValue := c.getDurationValue(rec.Term)
	filters = append(filters, types.Filter{
		Name:   aws.String("duration"),
		Values: []string{fmt.Sprintf("%d", durationValue)},
	})

	// Add offering class filter
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
		return "", fmt.Errorf("no offerings found for %s %s %s", rec.ResourceType, platform, tenancy)
	}

	return aws.ToString(result.ReservedInstancesOfferings[0].ReservedInstancesOfferingId), nil
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
		ResourceType:  string(offering.InstanceType),
		Term:          rec.Term,
		PaymentOption: string(offering.OfferingType),
		UpfrontCost:   fixedPrice,
		RecurringCost: float64(aws.ToFloat32(offering.UsagePrice)),
		Currency:      string(offering.CurrencyCode),
	}

	return details, nil
}

// GetValidResourceTypes returns valid EC2 instance types
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	instanceTypesMap := make(map[string]bool)
	var nextToken *string

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

		for _, offering := range result.InstanceTypeOfferings {
			instanceTypesMap[string(offering.InstanceType)] = true
		}

		if result.NextToken == nil || aws.ToString(result.NextToken) == "" {
			break
		}
		nextToken = result.NextToken
	}

	instanceTypes := make([]string, 0, len(instanceTypesMap))
	for instanceType := range instanceTypesMap {
		instanceTypes = append(instanceTypes, instanceType)
	}

	sort.Strings(instanceTypes)
	return instanceTypes, nil
}

// getDurationValue converts term string to seconds for EC2 API
func (c *Client) getDurationValue(term string) int64 {
	if term == "3yr" || term == "3" {
		return 94608000 // 3 years in seconds
	}
	return 31536000 // 1 year in seconds
}

// getOfferingClass converts payment option to EC2 offering class
func (c *Client) getOfferingClass(paymentOption string) string {
	switch paymentOption {
	case "all-upfront":
		return "convertible"
	default:
		return "standard"
	}
}
