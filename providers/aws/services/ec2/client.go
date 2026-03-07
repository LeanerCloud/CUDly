// Package ec2 provides AWS EC2 Reserved Instances client
package ec2

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
)

// EC2API defines the interface for EC2 operations (enables mocking)
type EC2API interface {
	PurchaseReservedInstancesOffering(ctx context.Context, params *ec2.PurchaseReservedInstancesOfferingInput, optFns ...func(*ec2.Options)) (*ec2.PurchaseReservedInstancesOfferingOutput, error)
	DescribeReservedInstancesOfferings(ctx context.Context, params *ec2.DescribeReservedInstancesOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOfferingsOutput, error)
	DescribeReservedInstances(ctx context.Context, params *ec2.DescribeReservedInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOutput, error)
	DescribeInstanceTypeOfferings(ctx context.Context, params *ec2.DescribeInstanceTypeOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
	GetReservedInstancesExchangeQuote(ctx context.Context, params *ec2.GetReservedInstancesExchangeQuoteInput, optFns ...func(*ec2.Options)) (*ec2.GetReservedInstancesExchangeQuoteOutput, error)
	AcceptReservedInstancesExchangeQuote(ctx context.Context, params *ec2.AcceptReservedInstancesExchangeQuoteInput, optFns ...func(*ec2.Options)) (*ec2.AcceptReservedInstancesExchangeQuoteOutput, error)
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
		if duration == ThreeYearSeconds {
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

// buildOfferingFilters constructs the EC2 API filters for finding an RI offering.
func (c *Client) buildOfferingFilters(rec common.Recommendation, details *common.ComputeDetails) []types.Filter {
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

	return []types.Filter{
		{Name: aws.String("instance-type"), Values: []string{rec.ResourceType}},
		{Name: aws.String("product-description"), Values: []string{platform}},
		{Name: aws.String("instance-tenancy"), Values: []string{tenancy}},
		{Name: aws.String("scope"), Values: []string{scope}},
		{Name: aws.String("duration"), Values: []string{fmt.Sprintf("%d", c.getDurationValue(rec.Term))}},
		{Name: aws.String("offering-class"), Values: []string{c.getOfferingClass(rec.PaymentOption)}},
	}
}

// findOfferingID finds the appropriate EC2 Reserved Instance offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	details, ok := rec.Details.(*common.ComputeDetails)
	if !ok || details == nil {
		return "", fmt.Errorf("invalid service details for EC2")
	}

	filters := c.buildOfferingFilters(rec, details)

	var nextToken *string
	for {
		input := &ec2.DescribeReservedInstancesOfferingsInput{
			Filters:            filters,
			IncludeMarketplace: aws.Bool(false),
			MaxResults:         aws.Int32(100),
			NextToken:          nextToken,
		}

		result, err := c.client.DescribeReservedInstancesOfferings(ctx, input)
		if err != nil {
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}

		if len(result.ReservedInstancesOfferings) > 0 {
			return aws.ToString(result.ReservedInstancesOfferings[0].ReservedInstancesOfferingId), nil
		}

		if result.NextToken == nil || aws.ToString(result.NextToken) == "" {
			break
		}
		nextToken = result.NextToken
	}

	return "", fmt.Errorf("no offerings found for %s %s %s", rec.ResourceType, details.Platform, details.Tenancy)
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

// Duration constants for RI term calculations
const (
	OneYearSeconds   = 31536000 // 365 days in seconds
	ThreeYearSeconds = 94608000 // 3 * 365 days in seconds
)

// getDurationValue converts term string to seconds for EC2 API
func (c *Client) getDurationValue(term string) int64 {
	if term == "3yr" || term == "3" {
		return ThreeYearSeconds
	}
	return OneYearSeconds
}

// getOfferingClass returns the EC2 offering class for RI queries.
// Always returns "convertible" — standard RIs are legacy and all modern
// RI purchases should use convertible for exchange flexibility.
func (c *Client) getOfferingClass(_ string) string {
	return "convertible"
}

// ConvertibleRI represents an active convertible Reserved Instance.
type ConvertibleRI struct {
	ReservedInstanceID  string    `json:"reserved_instance_id"`
	InstanceType        string    `json:"instance_type"`
	AvailabilityZone    string    `json:"availability_zone"`
	InstanceCount       int32     `json:"instance_count"`
	Start               time.Time `json:"start"`
	End                 time.Time `json:"end"`
	OfferingType        string    `json:"offering_type"`
	FixedPrice          float64   `json:"fixed_price"`
	UsagePrice          float64   `json:"usage_price"`
	State               string    `json:"state"`
	NormalizationFactor float64   `json:"normalization_factor"`
	ProductDescription  string    `json:"product_description"`
	InstanceTenancy     string    `json:"instance_tenancy"`
	Scope               string    `json:"scope"`
	Duration            int64     `json:"duration"`
}

// ListConvertibleReservedInstances returns all active convertible RIs in the region.
func (c *Client) ListConvertibleReservedInstances(ctx context.Context) ([]ConvertibleRI, error) {
	input := &ec2.DescribeReservedInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("state"),
				Values: []string{"active"},
			},
			{
				Name:   aws.String("offering-class"),
				Values: []string{"convertible"},
			},
		},
	}

	resp, err := c.client.DescribeReservedInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe convertible reserved instances: %w", err)
	}

	result := make([]ConvertibleRI, 0, len(resp.ReservedInstances))
	for _, ri := range resp.ReservedInstances {
		instanceType := string(ri.InstanceType)
		normFactor := normalizationFactorForInstanceType(instanceType)

		result = append(result, ConvertibleRI{
			ReservedInstanceID:  aws.ToString(ri.ReservedInstancesId),
			InstanceType:        instanceType,
			AvailabilityZone:    aws.ToString(ri.AvailabilityZone),
			InstanceCount:       aws.ToInt32(ri.InstanceCount),
			Start:               aws.ToTime(ri.Start),
			End:                 aws.ToTime(ri.End),
			OfferingType:        string(ri.OfferingType),
			FixedPrice:          float64(aws.ToFloat32(ri.FixedPrice)),
			UsagePrice:          float64(aws.ToFloat32(ri.UsagePrice)),
			State:               string(ri.State),
			NormalizationFactor: normFactor,
			ProductDescription:  string(ri.ProductDescription),
			InstanceTenancy:     string(ri.InstanceTenancy),
			Scope:               string(ri.Scope),
			Duration:            aws.ToInt64(ri.Duration),
		})
	}

	return result, nil
}

// FindConvertibleOfferingParams holds the parameters for finding a convertible RI offering.
type FindConvertibleOfferingParams struct {
	InstanceType       string
	ProductDescription string
	Tenancy            string
	Scope              string
	Duration           int64
}

// FindConvertibleOffering finds a convertible RI offering ID for the given parameters.
func (c *Client) FindConvertibleOffering(ctx context.Context, params FindConvertibleOfferingParams) (string, error) {
	tenancy := params.Tenancy
	if tenancy == "" {
		tenancy = "default"
	}
	scope := params.Scope
	if scope == "" {
		scope = "Region"
	}
	duration := params.Duration
	if duration == 0 {
		duration = OneYearSeconds
	}
	productDesc := params.ProductDescription
	if productDesc == "" {
		productDesc = "Linux/UNIX"
	}

	filters := []types.Filter{
		{Name: aws.String("instance-type"), Values: []string{params.InstanceType}},
		{Name: aws.String("product-description"), Values: []string{productDesc}},
		{Name: aws.String("instance-tenancy"), Values: []string{tenancy}},
		{Name: aws.String("scope"), Values: []string{scope}},
		{Name: aws.String("duration"), Values: []string{fmt.Sprintf("%d", duration)}},
		{Name: aws.String("offering-class"), Values: []string{"convertible"}},
	}

	input := &ec2.DescribeReservedInstancesOfferingsInput{
		Filters:            filters,
		IncludeMarketplace: aws.Bool(false),
		MaxResults:         aws.Int32(20),
	}

	result, err := c.client.DescribeReservedInstancesOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe convertible offerings: %w", err)
	}

	if len(result.ReservedInstancesOfferings) == 0 {
		return "", fmt.Errorf("no convertible offering found for %s (%s, %s, %s)", params.InstanceType, productDesc, tenancy, scope)
	}

	return aws.ToString(result.ReservedInstancesOfferings[0].ReservedInstancesOfferingId), nil
}

// normalizationFactorForInstanceType extracts the size from an instance type
// (e.g., "m5.xlarge" → "xlarge") and returns the AWS normalization factor.
func normalizationFactorForInstanceType(instanceType string) float64 {
	parts := strings.SplitN(instanceType, ".", 2)
	if len(parts) != 2 {
		return 0
	}
	return exchange.NormalizationFactorForSize(parts[1])
}
