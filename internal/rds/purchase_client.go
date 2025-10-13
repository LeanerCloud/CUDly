package rds

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// PurchaseClient wraps the AWS RDS client for purchasing Reserved Instances
type PurchaseClient struct {
	client RDSClientInterface
	common.BasePurchaseClient
}

// NewPurchaseClient creates a new RDS purchase client
func NewPurchaseClient(cfg aws.Config) *PurchaseClient {
	return &PurchaseClient{
		client: rds.NewFromConfig(cfg),
		BasePurchaseClient: common.BasePurchaseClient{
			Region: cfg.Region,
		},
	}
}

// PurchaseRI attempts to purchase an RDS Reserved Instance based on the recommendation
func (c *PurchaseClient) PurchaseRI(ctx context.Context, rec common.Recommendation) common.PurchaseResult {
	result := common.PurchaseResult{
		Config:    rec,
		Timestamp: time.Now(),
	}

	// Validate it's an RDS recommendation
	if rec.Service != common.ServiceRDS {
		result.Success = false
		result.Message = "Invalid service type for RDS purchase"
		return result
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to find offering: %v", err)
		return result
	}

	// Create a descriptive reservation ID with account alias
	rdsDetails, _ := rec.ServiceDetails.(*common.RDSDetails)
	engine := "unknown"
	if rdsDetails != nil {
		engine = rdsDetails.Engine
	}
	reservationID := common.GenerateReservationID("rds", rec.AccountName, engine, rec.InstanceType, rec.Region, rec.Count, rec.Coverage)

	// Create the purchase request
	input := &rds.PurchaseReservedDBInstancesOfferingInput{
		ReservedDBInstancesOfferingId: aws.String(offeringID),
		ReservedDBInstanceId:          aws.String(reservationID),
		DBInstanceCount:               aws.Int32(rec.Count),
		Tags:                          c.createPurchaseTags(rec),
	}

	// Log what we're about to purchase
	common.AppLogger.Printf("    🔸 RDS API Call: Purchasing %d instances (OfferingID: %s, ReservationID: %s)\n", rec.Count, offeringID, reservationID)

	// Execute the purchase
	response, err := c.client.PurchaseReservedDBInstancesOffering(ctx, input)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to purchase RDS RI: %v", err)
		return result
	}

	// Extract purchase information
	if response.ReservedDBInstance != nil {
		result.Success = true
		result.PurchaseID = aws.ToString(response.ReservedDBInstance.ReservedDBInstanceId)
		result.Message = fmt.Sprintf("Successfully purchased %d RDS instances", rec.Count)
		result.ReservationID = aws.ToString(response.ReservedDBInstance.ReservedDBInstanceId)

		// Extract cost information if available
		if response.ReservedDBInstance.FixedPrice != nil {
			result.ActualCost = *response.ReservedDBInstance.FixedPrice
		}
	} else {
		result.Success = false
		result.Message = "Purchase response was empty"
	}

	return result
}

// findOfferingID finds the appropriate Reserved Instance offering ID
func (c *PurchaseClient) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	rdsDetails, ok := rec.ServiceDetails.(*common.RDSDetails)
	if !ok {
		return "", fmt.Errorf("invalid service details for RDS")
	}

	// Convert recommendation to AWS API parameters
	multiAZ := rdsDetails.AZConfig == "multi-az"
	duration := c.getDurationString(rec.Term)
	offeringType, err := c.convertPaymentOption(rec.PaymentOption)
	if err != nil {
		return "", fmt.Errorf("invalid payment option: %w", err)
	}

	// Normalize engine name for AWS API
	normalizedEngine := c.normalizeEngineName(rdsDetails.Engine)

	input := &rds.DescribeReservedDBInstancesOfferingsInput{
		DBInstanceClass:    aws.String(rec.InstanceType),
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
		return "", fmt.Errorf("no offerings found for %s %s %s %s",
			rec.InstanceType, rdsDetails.Engine, rdsDetails.AZConfig, duration)
	}

	// Return the first matching offering ID
	offeringID := aws.ToString(result.ReservedDBInstancesOfferings[0].ReservedDBInstancesOfferingId)
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
	rdsDetails := rec.ServiceDetails.(*common.RDSDetails)

	// Convert duration from int32 to string
	var durationStr string
	if offering.Duration != nil {
		durationStr = strconv.Itoa(int(*offering.Duration))
	}

	// Get offering type as string
	var offeringTypeStr string
	if offering.OfferingType != nil {
		offeringTypeStr = *offering.OfferingType
	}

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedDBInstancesOfferingId),
		InstanceType:  aws.ToString(offering.DBInstanceClass),
		Engine:        rdsDetails.Engine,
		Duration:      durationStr,
		PaymentOption: offeringTypeStr,
		MultiAZ:       aws.ToBool(offering.MultiAZ),
		FixedPrice:    aws.ToFloat64(offering.FixedPrice),
		UsagePrice:    aws.ToFloat64(offering.UsagePrice),
		CurrencyCode:  aws.ToString(offering.CurrencyCode),
		OfferingType:  offeringTypeStr,
	}

	return details, nil
}

// BatchPurchase purchases multiple RDS RIs with error handling and rate limiting
func (c *PurchaseClient) BatchPurchase(ctx context.Context, recommendations []common.Recommendation, delayBetweenPurchases time.Duration) []common.PurchaseResult {
	return c.BasePurchaseClient.BatchPurchase(ctx, c, recommendations, delayBetweenPurchases)
}

// getDurationString converts term months to a duration string for RDS API
func (c *PurchaseClient) getDurationString(termMonths int) string {
	years := termMonths / 12
	if years == 1 {
		return "31536000" // 1 year in seconds
	}
	return "94608000" // 3 years in seconds
}

// convertPaymentOption converts our payment option string to AWS string
func (c *PurchaseClient) convertPaymentOption(option string) (string, error) {
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

// normalizeEngineName converts human-readable engine names to AWS API format
func (c *PurchaseClient) normalizeEngineName(engine string) string {
	// Convert to lowercase for comparison
	engineLower := strings.ToLower(engine)

	// Handle Aurora variants
	if strings.Contains(engineLower, "aurora") {
		if strings.Contains(engineLower, "mysql") {
			return "aurora-mysql"
		}
		if strings.Contains(engineLower, "postgres") {
			return "aurora-postgresql"
		}
		return "aurora-mysql" // Default Aurora to MySQL
	}

	// Handle standard RDS engines
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
		return "oracle-se2" // Most common Oracle edition
	}
	if strings.Contains(engineLower, "sqlserver") || strings.Contains(engineLower, "sql-server") {
		return "sqlserver-se" // Standard edition by default
	}

	// If already in correct format, return as-is
	return engineLower
}

// createPurchaseTags creates standard tags for the purchase
func (c *PurchaseClient) createPurchaseTags(rec common.Recommendation) []types.Tag {
	rdsDetails := rec.ServiceDetails.(*common.RDSDetails)

	return []types.Tag{
		{
			Key:   aws.String("Purpose"),
			Value: aws.String("Reserved Instance Purchase"),
		},
		{
			Key:   aws.String("Engine"),
			Value: aws.String(rdsDetails.Engine),
		},
		{
			Key:   aws.String("InstanceType"),
			Value: aws.String(rec.InstanceType),
		},
		{
			Key:   aws.String("Region"),
			Value: aws.String(rec.Region),
		},
		{
			Key:   aws.String("AZConfig"),
			Value: aws.String(rdsDetails.AZConfig),
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

// GetExistingReservedInstances retrieves existing reserved DB instances
func (c *PurchaseClient) GetExistingReservedInstances(ctx context.Context) ([]common.ExistingRI, error) {
	var existingRIs []common.ExistingRI
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
			// Only include active or payment-pending reservations
			state := aws.ToString(instance.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			// Extract engine from product description
			engine := aws.ToString(instance.ProductDescription)

			// Calculate term in months based on duration
			duration := aws.ToInt32(instance.Duration)
			termMonths := 12
			if duration == 94608000 { // 3 years in seconds
				termMonths = 36
			}

			existingRI := common.ExistingRI{
				ReservationID: aws.ToString(instance.ReservedDBInstanceId),
				InstanceType:  aws.ToString(instance.DBInstanceClass),
				Engine:        engine,
				Region:        c.Region,
				Count:         aws.ToInt32(instance.DBInstanceCount),
				State:         state,
				StartTime:     aws.ToTime(instance.StartTime),
				PaymentOption: aws.ToString(instance.OfferingType),
				Term:          termMonths,
			}

			// Calculate end time based on start time and term
			existingRI.EndTime = existingRI.StartTime.AddDate(0, termMonths, 0)

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

// GetValidInstanceTypes returns a list of valid instance types for RDS by querying offerings
func (c *PurchaseClient) GetValidInstanceTypes(ctx context.Context) ([]string, error) {
	instanceTypesMap := make(map[string]bool)
	var marker *string

	// Query all available RDS reserved instance offerings to extract instance types
	for {
		input := &rds.DescribeReservedDBInstancesOfferingsInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		result, err := c.client.DescribeReservedDBInstancesOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe RDS offerings: %w", err)
		}

		// Extract unique instance types
		for _, offering := range result.ReservedDBInstancesOfferings {
			if offering.DBInstanceClass != nil {
				instanceTypesMap[*offering.DBInstanceClass] = true
			}
		}

		// Check if there are more results
		if result.Marker == nil || aws.ToString(result.Marker) == "" {
			break
		}
		marker = result.Marker
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