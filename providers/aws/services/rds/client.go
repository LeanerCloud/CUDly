// Package rds provides AWS RDS Reserved Instances client
package rds

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// RDSAPI defines the interface for RDS operations (enables mocking)
type RDSAPI interface {
	DescribeReservedDBInstancesOfferings(ctx context.Context, params *rds.DescribeReservedDBInstancesOfferingsInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOfferingsOutput, error)
	PurchaseReservedDBInstancesOffering(ctx context.Context, params *rds.PurchaseReservedDBInstancesOfferingInput, optFns ...func(*rds.Options)) (*rds.PurchaseReservedDBInstancesOfferingOutput, error)
	DescribeReservedDBInstances(ctx context.Context, params *rds.DescribeReservedDBInstancesInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOutput, error)
}

// Client handles AWS RDS Reserved Instances
type Client struct {
	client RDSAPI
	region string
}

// NewClient creates a new RDS client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: rds.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// SetRDSAPI sets a custom RDS API client (for testing)
func (c *Client) SetRDSAPI(api RDSAPI) {
	c.client = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceRelationalDB
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as RDS uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing RDS Reserved Instances
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var marker *string

	for {
		input := &rds.DescribeReservedDBInstancesInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		response, err := c.client.DescribeReservedDBInstances(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe reserved DB instances: %w", err)
		}

		for _, instance := range response.ReservedDBInstances {
			state := aws.ToString(instance.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			duration := aws.ToInt32(instance.Duration)
			termMonths := 12
			if duration == 94608000 {
				termMonths = 36
			}

			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(instance.ReservedDBInstanceId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceRelationalDB,
				Region:         c.region,
				ResourceType:   aws.ToString(instance.DBInstanceClass),
				Engine:         aws.ToString(instance.ProductDescription), // Capture engine for accurate duplicate checking
				Count:          int(aws.ToInt32(instance.DBInstanceCount)),
				State:          state,
				StartDate:      aws.ToTime(instance.StartTime),
				EndDate:        aws.ToTime(instance.StartTime).AddDate(0, termMonths, 0),
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

// PurchaseCommitment purchases an RDS Reserved Instance
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

	// Generate reservation ID (letters, digits, hyphens only; no leading/trailing/double hyphen)
	reservationID := common.SanitizeReservationID(fmt.Sprintf("rds-%s-%d", rec.ResourceType, time.Now().Unix()), "rds-reserved-")

	// Create the purchase request
	input := &rds.PurchaseReservedDBInstancesOfferingInput{
		ReservedDBInstancesOfferingId: aws.String(offeringID),
		ReservedDBInstanceId:          aws.String(reservationID),
		DBInstanceCount:               aws.Int32(int32(rec.Count)),
		Tags:                          c.createPurchaseTags(rec),
	}

	response, err := c.client.PurchaseReservedDBInstancesOffering(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase RDS RI: %w", err)
		return result, result.Error
	}

	if response.ReservedDBInstance != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedDBInstance.ReservedDBInstanceId)
		if response.ReservedDBInstance.FixedPrice != nil {
			result.Cost = *response.ReservedDBInstance.FixedPrice
		}
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// findOfferingID finds the appropriate RDS Reserved Instance offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	details, ok := rec.Details.(*common.DatabaseDetails)
	if !ok || details == nil {
		return "", fmt.Errorf("invalid service details for RDS")
	}

	multiAZ := details.AZConfig == "multi-az"
	duration := c.getDurationString(rec.Term)
	offeringType, err := c.convertPaymentOption(rec.PaymentOption)
	if err != nil {
		return "", fmt.Errorf("invalid payment option: %w", err)
	}

	normalizedEngine := c.normalizeEngineName(details.Engine)

	input := &rds.DescribeReservedDBInstancesOfferingsInput{
		DBInstanceClass:    aws.String(rec.ResourceType),
		ProductDescription: aws.String(normalizedEngine),
		MultiAZ:            aws.Bool(multiAZ),
		Duration:           aws.String(duration),
		OfferingType:       aws.String(offeringType),
		MaxRecords:         aws.Int32(100),
	}

	result, err := c.client.DescribeReservedDBInstancesOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe offerings: %w", err)
	}

	if len(result.ReservedDBInstancesOfferings) == 0 {
		return "", fmt.Errorf("no offerings found for %s %s multi-az=%v %s",
			rec.ResourceType, details.Engine, multiAZ, duration)
	}

	return aws.ToString(result.ReservedDBInstancesOfferings[0].ReservedDBInstancesOfferingId), nil
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

	input := &rds.DescribeReservedDBInstancesOfferingsInput{
		ReservedDBInstancesOfferingId: aws.String(offeringID),
	}

	result, err := c.client.DescribeReservedDBInstancesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedDBInstancesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedDBInstancesOfferings[0]

	var durationStr string
	if offering.Duration != nil {
		durationStr = strconv.Itoa(int(*offering.Duration))
	}

	var offeringTypeStr string
	if offering.OfferingType != nil {
		offeringTypeStr = *offering.OfferingType
	}

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedDBInstancesOfferingId),
		ResourceType:  aws.ToString(offering.DBInstanceClass),
		Term:          durationStr,
		PaymentOption: offeringTypeStr,
		UpfrontCost:   aws.ToFloat64(offering.FixedPrice),
		RecurringCost: aws.ToFloat64(offering.UsagePrice),
		Currency:      aws.ToString(offering.CurrencyCode),
	}

	return details, nil
}

// GetValidResourceTypes returns valid RDS instance types
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	instanceTypesMap := make(map[string]bool)
	var marker *string

	for {
		input := &rds.DescribeReservedDBInstancesOfferingsInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		result, err := c.client.DescribeReservedDBInstancesOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe RDS offerings: %w", err)
		}

		for _, offering := range result.ReservedDBInstancesOfferings {
			if offering.DBInstanceClass != nil {
				instanceTypesMap[*offering.DBInstanceClass] = true
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

// getDurationString converts term string to duration string for RDS API
func (c *Client) getDurationString(term string) string {
	if term == "3yr" || term == "3" {
		return "94608000" // 3 years in seconds
	}
	return "31536000" // 1 year in seconds
}

// convertPaymentOption converts payment option to AWS string
func (c *Client) convertPaymentOption(option string) (string, error) {
	switch option {
	case "all-upfront":
		return "All Upfront", nil
	case "partial-upfront":
		return "Partial Upfront", nil
	case "no-upfront":
		return "No Upfront", nil
	default:
		return "", fmt.Errorf("unsupported payment option: %s", option)
	}
}

// normalizeEngineName converts engine names to AWS API format
func (c *Client) normalizeEngineName(engine string) string {
	engineLower := strings.ToLower(engine)

	if strings.Contains(engineLower, "aurora") {
		if strings.Contains(engineLower, "mysql") {
			return "aurora-mysql"
		}
		if strings.Contains(engineLower, "postgres") {
			return "aurora-postgresql"
		}
		return "aurora-mysql"
	}

	if strings.Contains(engineLower, "mysql") {
		return "mysql"
	}
	if strings.Contains(engineLower, "postgres") {
		return "postgresql"
	}
	if strings.Contains(engineLower, "mariadb") {
		return "mariadb"
	}
	if strings.Contains(engineLower, "oracle") {
		return "oracle-se2"
	}
	if strings.Contains(engineLower, "sqlserver") || strings.Contains(engineLower, "sql-server") {
		return "sqlserver-se"
	}

	return engineLower
}

// createPurchaseTags creates standard tags for the purchase
func (c *Client) createPurchaseTags(rec common.Recommendation) []types.Tag {
	return []types.Tag{
		{
			Key:   aws.String("Purpose"),
			Value: aws.String("Reserved Instance Purchase"),
		},
		{
			Key:   aws.String("ResourceType"),
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
