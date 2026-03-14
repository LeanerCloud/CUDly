package recommendations

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// getSavingsPlansRecommendations fetches Savings Plans recommendations
func (c *Client) getSavingsPlansRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	// Build list of plan types to query based on filters
	planTypes := c.getFilteredPlanTypes(params.IncludeSPTypes, params.ExcludeSPTypes)

	if len(planTypes) == 0 {
		return []common.Recommendation{}, nil
	}

	var allRecommendations []common.Recommendation

	for _, planType := range planTypes {
		input := &costexplorer.GetSavingsPlansPurchaseRecommendationInput{
			SavingsPlansType:     planType,
			PaymentOption:        convertSavingsPlansPaymentOption(params.PaymentOption),
			TermInYears:          convertSavingsPlansTermInYears(params.Term),
			LookbackPeriodInDays: convertSavingsPlansLookbackPeriod(params.LookbackPeriod),
			AccountScope:         types.AccountScopeLinked,
		}

		c.rateLimiter.Reset()
		var result *costexplorer.GetSavingsPlansPurchaseRecommendationOutput
		var err error

		for {
			if waitErr := c.rateLimiter.Wait(ctx); waitErr != nil {
				return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
			}

			result, err = c.costExplorerClient.GetSavingsPlansPurchaseRecommendation(ctx, input)
			if !c.rateLimiter.ShouldRetry(err) {
				break
			}
		}

		if err != nil {
			fmt.Printf("Warning: Failed to get %s recommendations: %v\n", planType, err)
			continue
		}

		if result.SavingsPlansPurchaseRecommendation != nil {
			recs := c.parseSavingsPlansRecommendations(result.SavingsPlansPurchaseRecommendation, params, planType)
			allRecommendations = append(allRecommendations, recs...)
		}
	}

	return allRecommendations, nil
}

// parseSavingsPlansRecommendations converts Savings Plans recommendations
func (c *Client) parseSavingsPlansRecommendations(
	spRec *types.SavingsPlansPurchaseRecommendation,
	params common.RecommendationParams,
	planType types.SupportedSavingsPlansType,
) []common.Recommendation {
	var recommendations []common.Recommendation

	for _, detail := range spRec.SavingsPlansPurchaseRecommendationDetails {
		rec := c.parseSavingsPlanDetail(&detail, params, planType)
		if rec != nil {
			recommendations = append(recommendations, *rec)
		}
	}

	return recommendations
}

// parseSavingsPlanDetail converts a single Savings Plan recommendation detail
// parseOptionalFloat parses a string pointer as float64, logging a warning on failure.
// Returns 0 if the pointer is nil.
func parseOptionalFloat(field string, s *string) float64 {
	if s == nil {
		return 0
	}
	val, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		log.Printf("WARNING: failed to parse %s: %v", field, err)
		return 0
	}
	return val
}

func (c *Client) parseSavingsPlanDetail(
	detail *types.SavingsPlansPurchaseRecommendationDetail,
	params common.RecommendationParams,
	planType types.SupportedSavingsPlansType,
) *common.Recommendation {
	hourlyCommitment := parseOptionalFloat("HourlyCommitmentToPurchase", detail.HourlyCommitmentToPurchase)
	monthlySavings := parseOptionalFloat("EstimatedMonthlySavingsAmount", detail.EstimatedMonthlySavingsAmount)
	savingsPercent := parseOptionalFloat("EstimatedSavingsPercentage", detail.EstimatedSavingsPercentage)
	upfrontCost := parseOptionalFloat("UpfrontCost", detail.UpfrontCost)

	planTypeStr := string(planType)
	switch planType {
	case types.SupportedSavingsPlansTypeComputeSp:
		planTypeStr = "Compute"
	case types.SupportedSavingsPlansTypeEc2InstanceSp:
		planTypeStr = "EC2Instance"
	case types.SupportedSavingsPlansTypeSagemakerSp:
		planTypeStr = "SageMaker"
	case types.SupportedSavingsPlansTypeDatabaseSp:
		planTypeStr = "Database"
	}

	accountID := ""
	if detail.AccountId != nil {
		accountID = aws.ToString(detail.AccountId)
	}

	return &common.Recommendation{
		Provider:          common.ProviderAWS,
		Service:           common.ServiceSavingsPlans,
		PaymentOption:     params.PaymentOption,
		Term:              params.Term,
		CommitmentType:    common.CommitmentSavingsPlan,
		Count:             1,
		EstimatedSavings:  monthlySavings,
		SavingsPercentage: savingsPercent,
		CommitmentCost:    upfrontCost,
		Timestamp:         time.Now(),
		Account:           accountID,
		Details: &common.SavingsPlanDetails{
			PlanType:         planTypeStr,
			HourlyCommitment: hourlyCommitment,
			Coverage:         fmt.Sprintf("%.1f%%", savingsPercent),
		},
	}
}

// getFilteredPlanTypes returns the list of Savings Plan types to query based on include/exclude filters
func (c *Client) getFilteredPlanTypes(includeSPTypes, excludeSPTypes []string) []types.SupportedSavingsPlansType {
	// All available plan types
	allPlanTypes := map[string]types.SupportedSavingsPlansType{
		"compute":     types.SupportedSavingsPlansTypeComputeSp,
		"ec2instance": types.SupportedSavingsPlansTypeEc2InstanceSp,
		"sagemaker":   types.SupportedSavingsPlansTypeSagemakerSp,
		"database":    types.SupportedSavingsPlansTypeDatabaseSp,
	}

	// Normalize filter values to lowercase
	normalizeFilters := func(filters []string) map[string]bool {
		result := make(map[string]bool)
		for _, f := range filters {
			result[strings.ToLower(f)] = true
		}
		return result
	}

	includeMap := normalizeFilters(includeSPTypes)
	excludeMap := normalizeFilters(excludeSPTypes)

	var result []types.SupportedSavingsPlansType

	// If include list is specified, only include those types
	if len(includeMap) > 0 {
		for name, planType := range allPlanTypes {
			if includeMap[name] && !excludeMap[name] {
				result = append(result, planType)
			}
		}
	} else {
		// Include all types except those in the exclude list
		for name, planType := range allPlanTypes {
			if !excludeMap[name] {
				result = append(result, planType)
			}
		}
	}

	return result
}
