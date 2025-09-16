package purchase

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/recommendations"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// Client wraps the AWS RDS client for purchasing Reserved Instances
type Client struct {
	rdsClient *rds.Client
}

// NewClient creates a new purchase client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		rdsClient: rds.NewFromConfig(cfg),
	}
}

// PurchaseRI attempts to purchase a Reserved Instance based on the recommendation
func (c *Client) PurchaseRI(ctx context.Context, rec recommendations.Recommendation) Result {
	result := Result{
		Config:    rec,
		Timestamp: time.Now(),
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to find offering: %v", err)
		return result
	}

	// Create the purchase request
	input := &rds.PurchaseReservedDBInstancesOfferingInput{
		ReservedDBInstancesOfferingId: aws.String(offeringID),
		DBInstanceCount:               aws.Int32(rec.Count),
		Tags:                          c.createPurchaseTags(rec),
	}

	// Execute the purchase
	response, err := c.rdsClient.PurchaseReservedDBInstancesOffering(ctx, input)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to purchase RI: %v", err)
		return result
	}

	// Extract purchase information
	if response.ReservedDBInstance != nil {
		result.Success = true
		result.PurchaseID = aws.ToString(response.ReservedDBInstance.ReservedDBInstanceId)
		result.Message = fmt.Sprintf("Successfully purchased %d instances", rec.Count)
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

// BatchPurchase purchases multiple RIs with error handling and rate limiting
func (c *Client) BatchPurchase(ctx context.Context, recommendations []recommendations.Recommendation, delayBetweenPurchases time.Duration) []Result {
	results := make([]Result, 0, len(recommendations))

	for i, rec := range recommendations {
		result := c.PurchaseRI(ctx, rec)
		results = append(results, result)

		// Add delay between purchases to avoid rate limits (except for the last one)
		if i < len(recommendations)-1 && delayBetweenPurchases > 0 {
			time.Sleep(delayBetweenPurchases)
		}
	}

	return results
}

// findOfferingID finds the appropriate Reserved Instance offering ID
func (c *Client) findOfferingID(ctx context.Context, rec recommendations.Recommendation) (string, error) {
	// Convert recommendation to AWS API parameters
	multiAZ := rec.GetMultiAZ()
	duration := rec.GetDurationString()
	offeringType, err := c.convertPaymentOption(rec.PaymentOption)
	if err != nil {
		return "", fmt.Errorf("invalid payment option: %w", err)
	}

	input := &rds.DescribeReservedDBInstancesOfferingsInput{
		DBInstanceClass:    aws.String(rec.InstanceType),
		ProductDescription: aws.String(rec.Engine),
		MultiAZ:            aws.Bool(multiAZ),
		Duration:           aws.String(duration),
		OfferingType:       aws.String(offeringType),
		MaxRecords:         aws.Int32(100),
	}

	result, err := c.rdsClient.DescribeReservedDBInstancesOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe offerings: %w", err)
	}

	if len(result.ReservedDBInstancesOfferings) == 0 {
		return "", fmt.Errorf("no offerings found for %s %s %s %s",
			rec.InstanceType, rec.Engine, rec.AZConfig, duration)
	}

	// Return the first matching offering ID
	offeringID := aws.ToString(result.ReservedDBInstancesOfferings[0].ReservedDBInstancesOfferingId)
	return offeringID, nil
}

// ValidateOffering checks if an offering exists without purchasing
func (c *Client) ValidateOffering(ctx context.Context, rec recommendations.Recommendation) error {
	_, err := c.findOfferingID(ctx, rec)
	return err
}

// GetOfferingDetails retrieves detailed information about an offering
func (c *Client) GetOfferingDetails(ctx context.Context, rec recommendations.Recommendation) (*OfferingDetails, error) {
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		return nil, err
	}

	input := &rds.DescribeReservedDBInstancesOfferingsInput{
		ReservedDBInstancesOfferingId: aws.String(offeringID),
	}

	result, err := c.rdsClient.DescribeReservedDBInstancesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedDBInstancesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedDBInstancesOfferings[0]

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

	details := &OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedDBInstancesOfferingId),
		InstanceType:  aws.ToString(offering.DBInstanceClass),
		Engine:        aws.ToString(offering.ProductDescription),
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

// convertPaymentOption converts our payment option string to AWS string
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

// createPurchaseTags creates standard tags for the purchase
func (c *Client) createPurchaseTags(rec recommendations.Recommendation) []types.Tag {
	return []types.Tag{
		{
			Key:   aws.String("Purpose"),
			Value: aws.String("Reserved Instance Purchase"),
		},
		{
			Key:   aws.String("Engine"),
			Value: aws.String(rec.Engine),
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
			Value: aws.String(rec.AZConfig),
		},
		{
			Key:   aws.String("PurchaseDate"),
			Value: aws.String(time.Now().Format("2006-01-02")),
		},
		{
			Key:   aws.String("Tool"),
			Value: aws.String("rds-ri-tool"),
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

// EstimateCosts estimates the costs for a list of recommendations
func (c *Client) EstimateCosts(ctx context.Context, recommendations []recommendations.Recommendation) ([]CostEstimate, error) {
	estimates := make([]CostEstimate, 0, len(recommendations))

	for _, rec := range recommendations {
		details, err := c.GetOfferingDetails(ctx, rec)
		if err != nil {
			estimates = append(estimates, CostEstimate{
				Recommendation: rec,
				Error:          err.Error(),
			})
			continue
		}

		estimate := CostEstimate{
			Recommendation:   rec,
			OfferingDetails:  *details,
			TotalFixedCost:   details.FixedPrice * float64(rec.Count),
			MonthlyUsageCost: details.UsagePrice * float64(rec.Count),
		}

		// Calculate total cost over the term
		termMonths := float64(rec.Term)
		estimate.TotalTermCost = estimate.TotalFixedCost + (estimate.MonthlyUsageCost * termMonths)

		estimates = append(estimates, estimate)
	}

	return estimates, nil
}
