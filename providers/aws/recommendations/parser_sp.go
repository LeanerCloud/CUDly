package recommendations

import (
	"context"
	"fmt"
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
func (c *Client) parseSavingsPlanDetail(
	detail *types.SavingsPlansPurchaseRecommendationDetail,
	params common.RecommendationParams,
	planType types.SupportedSavingsPlansType,
) *common.Recommendation {
	var hourlyCommitment, monthlySavings, savingsPercent, upfrontCost float64

	if detail.HourlyCommitmentToPurchase != nil {
		hourlyCommitment, _ = strconv.ParseFloat(*detail.HourlyCommitmentToPurchase, 64)
	}
	if detail.EstimatedMonthlySavingsAmount != nil {
		monthlySavings, _ = strconv.ParseFloat(*detail.EstimatedMonthlySavingsAmount, 64)
	}
	if detail.EstimatedSavingsPercentage != nil {
		savingsPercent, _ = strconv.ParseFloat(*detail.EstimatedSavingsPercentage, 64)
	}
	if detail.UpfrontCost != nil {
		upfrontCost, _ = strconv.ParseFloat(*detail.UpfrontCost, 64)
	}

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
