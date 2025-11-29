// Package savingsplans provides AWS Savings Plans purchase client
package savingsplans

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// SavingsPlansAPI defines the interface for Savings Plans operations (enables mocking)
type SavingsPlansAPI interface {
	CreateSavingsPlan(ctx context.Context, params *savingsplans.CreateSavingsPlanInput, optFns ...func(*savingsplans.Options)) (*savingsplans.CreateSavingsPlanOutput, error)
	DescribeSavingsPlans(ctx context.Context, params *savingsplans.DescribeSavingsPlansInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOutput, error)
	DescribeSavingsPlansOfferings(ctx context.Context, params *savingsplans.DescribeSavingsPlansOfferingsInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOfferingsOutput, error)
	DescribeSavingsPlansOfferingRates(ctx context.Context, params *savingsplans.DescribeSavingsPlansOfferingRatesInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOfferingRatesOutput, error)
}

// Client handles AWS Savings Plans
type Client struct {
	client SavingsPlansAPI
	region string
}

// NewClient creates a new Savings Plans client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: savingsplans.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// SetSavingsPlansAPI sets a custom Savings Plans API client (for testing)
func (c *Client) SetSavingsPlansAPI(api SavingsPlansAPI) {
	c.client = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceSavingsPlans
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as Savings Plans uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing Savings Plans
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	input := &savingsplans.DescribeSavingsPlansInput{
		States: []types.SavingsPlanState{
			types.SavingsPlanStateActive,
			types.SavingsPlanStatePendingReturn,
			types.SavingsPlanStateQueued,
		},
	}

	result, err := c.client.DescribeSavingsPlans(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe Savings Plans: %w", err)
	}

	commitments := make([]common.Commitment, 0, len(result.SavingsPlans))

	for _, sp := range result.SavingsPlans {
		if sp.SavingsPlanId == nil {
			continue
		}

		commitment := common.Commitment{
			Provider:       common.ProviderAWS,
			CommitmentID:   *sp.SavingsPlanId,
			CommitmentType: common.CommitmentSavingsPlan,
			Service:        common.ServiceSavingsPlans,
			Region:         aws.ToString(sp.Region),
			ResourceType:   string(sp.SavingsPlanType),
			Count:          1, // Savings Plans don't have a count
			State:          string(sp.State),
		}

		if sp.Start != nil {
			if startTime, err := time.Parse(time.RFC3339, *sp.Start); err == nil {
				commitment.StartDate = startTime
			}
		}
		if sp.End != nil {
			if endTime, err := time.Parse(time.RFC3339, *sp.End); err == nil {
				commitment.EndDate = endTime
			}
		}

		commitments = append(commitments, commitment)
	}

	return commitments, nil
}

// PurchaseCommitment purchases a Savings Plan
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	spDetails, ok := rec.Details.(common.SavingsPlanDetails)
	if !ok {
		result.Error = fmt.Errorf("invalid service details for Savings Plans")
		return result, result.Error
	}

	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Error = fmt.Errorf("failed to find Savings Plans offering: %w", err)
		return result, result.Error
	}

	input := &savingsplans.CreateSavingsPlanInput{
		SavingsPlanOfferingId: aws.String(offeringID),
		Commitment:            aws.String(fmt.Sprintf("%.2f", spDetails.HourlyCommitment)),
		UpfrontPaymentAmount:  nil, // AWS calculates this based on payment option
		PurchaseTime:          aws.Time(time.Now()),
	}

	response, err := c.client.CreateSavingsPlan(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase Savings Plan: %w", err)
		return result, result.Error
	}

	if response.SavingsPlanId != nil {
		result.Success = true
		result.CommitmentID = *response.SavingsPlanId
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// findOfferingID finds the appropriate Savings Plans offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	spDetails, ok := rec.Details.(common.SavingsPlanDetails)
	if !ok {
		return "", fmt.Errorf("invalid service details for Savings Plans")
	}

	// Convert plan type
	var planType types.SavingsPlanType
	switch spDetails.PlanType {
	case "Compute":
		planType = types.SavingsPlanTypeCompute
	case "EC2Instance":
		planType = types.SavingsPlanTypeEc2Instance
	case "SageMaker", "Sagemaker":
		planType = types.SavingsPlanTypeSagemaker
	default:
		return "", fmt.Errorf("unsupported Savings Plan type: %s", spDetails.PlanType)
	}

	// Convert term to months
	termMonths := int64(12)
	if rec.Term == "3yr" || rec.Term == "3" {
		termMonths = 36
	}

	// Convert payment option
	paymentOption := types.SavingsPlanPaymentOptionAllUpfront
	switch rec.PaymentOption {
	case "All Upfront", "all-upfront":
		paymentOption = types.SavingsPlanPaymentOptionAllUpfront
	case "Partial Upfront", "partial-upfront":
		paymentOption = types.SavingsPlanPaymentOptionPartialUpfront
	case "No Upfront", "no-upfront":
		paymentOption = types.SavingsPlanPaymentOptionNoUpfront
	}

	input := &savingsplans.DescribeSavingsPlansOfferingsInput{
		PlanTypes:      []types.SavingsPlanType{planType},
		Durations:      []int64{termMonths},
		PaymentOptions: []types.SavingsPlanPaymentOption{paymentOption},
	}

	result, err := c.client.DescribeSavingsPlansOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe Savings Plans offerings: %w", err)
	}

	if len(result.SearchResults) == 0 {
		return "", fmt.Errorf("no Savings Plans offerings found matching criteria")
	}

	return *result.SearchResults[0].OfferingId, nil
}

// ValidateOffering checks if a Savings Plans offering exists
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

	spDetails, ok := rec.Details.(common.SavingsPlanDetails)
	if !ok {
		return nil, fmt.Errorf("invalid service details for Savings Plans")
	}

	// Get offering rates
	input := &savingsplans.DescribeSavingsPlansOfferingRatesInput{
		SavingsPlanOfferingIds: []string{offeringID},
	}

	_, err = c.client.DescribeSavingsPlansOfferingRates(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering rates: %w", err)
	}

	// Calculate costs based on payment option
	var upfrontCost, recurringCost, totalCost float64

	// Total cost is hourly commitment * hours in term
	hoursInTerm := 8760.0 // 1 year
	if rec.Term == "3yr" || rec.Term == "3" {
		hoursInTerm = 26280.0 // 3 years
	}
	totalCost = spDetails.HourlyCommitment * hoursInTerm

	switch rec.PaymentOption {
	case "All Upfront", "all-upfront":
		upfrontCost = totalCost
		recurringCost = 0
	case "Partial Upfront", "partial-upfront":
		upfrontCost = totalCost * 0.5
		recurringCost = (totalCost * 0.5) / hoursInTerm
	case "No Upfront", "no-upfront":
		upfrontCost = 0
		recurringCost = totalCost / hoursInTerm
	}

	termStr := "1yr"
	if rec.Term == "3yr" || rec.Term == "3" {
		termStr = "3yr"
	}

	return &common.OfferingDetails{
		OfferingID:          offeringID,
		ResourceType:        spDetails.PlanType,
		Term:                termStr,
		PaymentOption:       rec.PaymentOption,
		UpfrontCost:         upfrontCost,
		RecurringCost:       recurringCost,
		TotalCost:           totalCost,
		EffectiveHourlyRate: spDetails.HourlyCommitment,
		Currency:            "USD",
	}, nil
}

// GetValidResourceTypes returns valid Savings Plan types
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	return []string{
		"Compute",
		"EC2Instance",
		"SageMaker",
	}, nil
}
