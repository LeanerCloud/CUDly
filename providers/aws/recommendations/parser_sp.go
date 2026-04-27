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

// getSavingsPlansRecommendations fetches Savings Plans recommendations.
//
// Resolution order for which plan types to query, in order of precedence:
//  1. params.Service is one of the four per-plan-type slugs
//     (e.g. ServiceSavingsPlansSageMaker) — query just that plan type. This
//     is the path the AWS provider's GetServiceClient dispatch takes after
//     the per-plan-type split: each registered SP service makes its own
//     Cost Explorer call with its own term/payment defaults.
//  2. Otherwise, fall back to the legacy IncludeSPTypes/ExcludeSPTypes
//     filter mechanism (for callers passing the umbrella ServiceSavingsPlans
//     slug or for direct CLI invocations that haven't been migrated yet).
func (c *Client) getSavingsPlansRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	planTypes := planTypesForParams(params)

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
			// When the caller scoped the request to one plan type
			// (post-issue-#22 split), a Cost Explorer failure means an
			// entire SP service collection returns nothing — silently
			// dropping that as "0 recommendations" hides real outages.
			// Propagate. The umbrella iterate-all path keeps logging
			// and continuing so a transient failure on one plan type
			// doesn't poison the others.
			if len(planTypes) == 1 {
				return nil, fmt.Errorf("failed to get %s recommendations: %w", planType, err)
			}
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
		Service:           serviceSlugForPlanType(planType),
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

// planTypesForParams resolves which AWS Cost Explorer plan types to query
// for a given RecommendationParams. When params.Service is one of the four
// per-plan-type slugs the result is a single-element slice for that type;
// otherwise it falls back to the legacy IncludeSPTypes/ExcludeSPTypes filter.
// See the getSavingsPlansRecommendations docstring for the full resolution
// order.
func planTypesForParams(params common.RecommendationParams) []types.SupportedSavingsPlansType {
	if pt, ok := planTypeForServiceSlug(params.Service); ok {
		return []types.SupportedSavingsPlansType{pt}
	}
	return getFilteredPlanTypes(params.IncludeSPTypes, params.ExcludeSPTypes)
}

// planTypeForServiceSlug maps a per-plan-type SP service slug to its
// Cost Explorer plan-type enum. Returns false for non-SP slugs and for the
// legacy umbrella ServiceSavingsPlans (which still triggers the iterate-all
// fallback inside planTypesForParams).
func planTypeForServiceSlug(s common.ServiceType) (types.SupportedSavingsPlansType, bool) {
	switch s {
	case common.ServiceSavingsPlansCompute:
		return types.SupportedSavingsPlansTypeComputeSp, true
	case common.ServiceSavingsPlansEC2Instance:
		return types.SupportedSavingsPlansTypeEc2InstanceSp, true
	case common.ServiceSavingsPlansSageMaker:
		return types.SupportedSavingsPlansTypeSagemakerSp, true
	case common.ServiceSavingsPlansDatabase:
		return types.SupportedSavingsPlansTypeDatabaseSp, true
	}
	return "", false
}

// serviceSlugForPlanType is the inverse of planTypeForServiceSlug. Used by
// parseSavingsPlanDetail to tag each Recommendation with the per-plan-type
// slug rather than the legacy umbrella, so downstream stats/filters can
// distinguish Compute SP from SageMaker SP recommendations.
func serviceSlugForPlanType(pt types.SupportedSavingsPlansType) common.ServiceType {
	switch pt {
	case types.SupportedSavingsPlansTypeComputeSp:
		return common.ServiceSavingsPlansCompute
	case types.SupportedSavingsPlansTypeEc2InstanceSp:
		return common.ServiceSavingsPlansEC2Instance
	case types.SupportedSavingsPlansTypeSagemakerSp:
		return common.ServiceSavingsPlansSageMaker
	case types.SupportedSavingsPlansTypeDatabaseSp:
		return common.ServiceSavingsPlansDatabase
	}
	return common.ServiceSavingsPlans
}

// getFilteredPlanTypes returns the list of Savings Plan types to query based
// on include/exclude filters. Iterates a fixed-order slice rather than a map
// so the returned order is deterministic — downstream "first plan type wins"
// behaviour and test assertions can rely on it.
func getFilteredPlanTypes(includeSPTypes, excludeSPTypes []string) []types.SupportedSavingsPlansType {
	allPlanTypes := []struct {
		name string
		typ  types.SupportedSavingsPlansType
	}{
		{"compute", types.SupportedSavingsPlansTypeComputeSp},
		{"ec2instance", types.SupportedSavingsPlansTypeEc2InstanceSp},
		{"sagemaker", types.SupportedSavingsPlansTypeSagemakerSp},
		{"database", types.SupportedSavingsPlansTypeDatabaseSp},
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
		for _, item := range allPlanTypes {
			if includeMap[item.name] && !excludeMap[item.name] {
				result = append(result, item.typ)
			}
		}
	} else {
		// Include all types except those in the exclude list
		for _, item := range allPlanTypes {
			if !excludeMap[item.name] {
				result = append(result, item.typ)
			}
		}
	}

	return result
}
