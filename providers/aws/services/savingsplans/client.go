// Package savingsplans provides AWS Savings Plans purchase client
package savingsplans

import (
	"context"
	"fmt"
	"log"
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
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
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
		Tags:                  buildSavingsPlanTags(opts.Source),
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
	spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok {
		return "", fmt.Errorf("invalid service details for Savings Plans")
	}

	planType, err := convertPlanType(spDetails.PlanType)
	if err != nil {
		return "", err
	}

	termSeconds := convertTermToSeconds(rec.Term)
	paymentOption := convertPaymentOption(rec.PaymentOption)

	input := &savingsplans.DescribeSavingsPlansOfferingsInput{
		PlanTypes:      []types.SavingsPlanType{planType},
		Durations:      []int64{termSeconds},
		PaymentOptions: []types.SavingsPlanPaymentOption{paymentOption},
	}

	return c.lookupOfferingID(ctx, input)
}

// convertPlanType converts a plan type string to AWS SDK type
func convertPlanType(planType string) (types.SavingsPlanType, error) {
	switch planType {
	case "Compute":
		return types.SavingsPlanTypeCompute, nil
	case "EC2Instance":
		return types.SavingsPlanTypeEc2Instance, nil
	case "SageMaker", "Sagemaker":
		return types.SavingsPlanTypeSagemaker, nil
	case "Database":
		return types.SavingsPlanTypeDatabase, nil
	default:
		return "", fmt.Errorf("unsupported Savings Plan type: %s", planType)
	}
}

// convertTermToSeconds converts a term string to seconds for AWS API
func convertTermToSeconds(term string) int64 {
	if term == "3yr" || term == "3" {
		return 94608000 // 3 years in seconds (365 * 3 * 86400)
	}
	if term != "1yr" && term != "1" && term != "" {
		log.Printf("WARNING: unknown Savings Plans term %q, defaulting to 1 year", term)
	}
	return 31536000 // 1 year in seconds (365 * 86400)
}

// convertPaymentOption converts a payment option string to AWS SDK type
func convertPaymentOption(paymentOption string) types.SavingsPlanPaymentOption {
	switch paymentOption {
	case "All Upfront", "all-upfront":
		return types.SavingsPlanPaymentOptionAllUpfront
	case "Partial Upfront", "partial-upfront":
		return types.SavingsPlanPaymentOptionPartialUpfront
	case "No Upfront", "no-upfront":
		return types.SavingsPlanPaymentOptionNoUpfront
	default:
		log.Printf("WARNING: unknown Savings Plans payment option %q, defaulting to AllUpfront", paymentOption)
		return types.SavingsPlanPaymentOptionAllUpfront
	}
}

// lookupOfferingID performs the actual API call to find the offering ID
func (c *Client) lookupOfferingID(ctx context.Context, input *savingsplans.DescribeSavingsPlansOfferingsInput) (string, error) {
	result, err := c.client.DescribeSavingsPlansOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe Savings Plans offerings: %w", err)
	}

	if len(result.SearchResults) == 0 {
		return "", fmt.Errorf("no Savings Plans offerings found matching criteria")
	}

	firstResult := result.SearchResults[0]
	if firstResult.OfferingId == nil {
		return "", fmt.Errorf("Savings Plans offering has nil ID")
	}

	return *firstResult.OfferingId, nil
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

	spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok {
		return nil, fmt.Errorf("invalid service details for Savings Plans")
	}

	if err := c.validateOffering(ctx, offeringID); err != nil {
		return nil, err
	}

	hoursInTerm := calculateHoursInTerm(rec.Term)
	totalCost := spDetails.HourlyCommitment * hoursInTerm
	upfrontCost, recurringCost := calculatePaymentBreakdown(rec.PaymentOption, totalCost, hoursInTerm)

	return &common.OfferingDetails{
		OfferingID:          offeringID,
		ResourceType:        spDetails.PlanType,
		Term:                normalizeTermString(rec.Term),
		PaymentOption:       rec.PaymentOption,
		UpfrontCost:         upfrontCost,
		RecurringCost:       recurringCost,
		TotalCost:           totalCost,
		EffectiveHourlyRate: spDetails.HourlyCommitment,
		Currency:            "USD",
	}, nil
}

// validateOffering validates that the offering exists
func (c *Client) validateOffering(ctx context.Context, offeringID string) error {
	input := &savingsplans.DescribeSavingsPlansOfferingRatesInput{
		SavingsPlanOfferingIds: []string{offeringID},
	}

	_, err := c.client.DescribeSavingsPlansOfferingRates(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to get offering rates: %w", err)
	}

	return nil
}

// calculateHoursInTerm calculates the number of hours in a commitment term.
// Uses 365 days/year to match AWS billing conventions for RIs and Savings Plans.
func calculateHoursInTerm(term string) float64 {
	if term == "3yr" || term == "3" {
		return 3 * 365 * 24 // 3 years (26280 hours)
	}
	return 365 * 24 // 1 year (8760 hours)
}

// calculatePaymentBreakdown calculates upfront and recurring costs based on payment option
func calculatePaymentBreakdown(paymentOption string, totalCost, hoursInTerm float64) (upfrontCost, recurringCost float64) {
	switch paymentOption {
	case "All Upfront", "all-upfront":
		return totalCost, 0
	case "Partial Upfront", "partial-upfront":
		return totalCost * 0.5, (totalCost * 0.5) / hoursInTerm
	case "No Upfront", "no-upfront":
		return 0, totalCost / hoursInTerm
	default:
		return totalCost, 0
	}
}

// normalizeTermString normalizes a term string to standard format
func normalizeTermString(term string) string {
	if term == "3yr" || term == "3" {
		return "3yr"
	}
	return "1yr"
}

// GetValidResourceTypes returns valid Savings Plan types
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	return []string{
		"Compute",
		"EC2Instance",
		"SageMaker",
		"Database",
	}, nil
}

// buildSavingsPlanTags returns the tag map to stamp onto a newly-created
// Savings Plan. The Tags map on CreateSavingsPlanInput accepts tags at
// purchase time, so no follow-up call is needed. When source is empty the
// purchase-automation tag is skipped rather than writing an empty value.
func buildSavingsPlanTags(source string) map[string]string {
	tags := map[string]string{
		"Tool":         "CUDly",
		"PurchaseDate": time.Now().Format("2006-01-02"),
	}
	if source != "" {
		tags[common.PurchaseTagKey] = source
	}
	return tags
}
