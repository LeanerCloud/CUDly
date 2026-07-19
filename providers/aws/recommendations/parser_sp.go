package recommendations

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/concurrency"
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
//     filter mechanism (for callers passing the umbrella ServiceSavingsPlansAll
//     slug or for direct CLI invocations that haven't been migrated yet).
func (c *Client) getSavingsPlansRecommendations(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	planTypes := planTypesForParams(params)

	if len(planTypes) == 0 {
		return []common.Recommendation{}, nil
	}

	var allRecommendations []common.Recommendation

	for _, planType := range planTypes {
		paymentOption, err := convertSavingsPlansPaymentOption(params.PaymentOption)
		if err != nil {
			return nil, fmt.Errorf("invalid payment option for Savings Plans recommendation: %w", err)
		}
		termInYears, err := convertSavingsPlansTermInYears(params.Term)
		if err != nil {
			return nil, fmt.Errorf("invalid term for Savings Plans recommendation: %w", err)
		}
		lookbackPeriod, err := convertSavingsPlansLookbackPeriod(params.LookbackPeriod)
		if err != nil {
			return nil, fmt.Errorf("invalid lookback period for Savings Plans recommendation: %w", err)
		}
		input := &costexplorer.GetSavingsPlansPurchaseRecommendationInput{
			SavingsPlansType:     planType,
			PaymentOption:        paymentOption,
			TermInYears:          termInYears,
			LookbackPeriodInDays: lookbackPeriod,
			AccountScope:         types.AccountScopeLinked,
		}

		recs, err := c.fetchSPAllPages(ctx, input, params, planType)
		if err != nil {
			// When the caller scoped the request to one plan type
			// (post-issue-#22 split), a Cost Explorer failure means an
			// entire SP service collection returns nothing -- silently
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

		allRecommendations = append(allRecommendations, recs...)
	}

	return allRecommendations, nil
}

// fetchSPAllPages paginates over all pages of SP recommendations for a single
// plan type. ctx.Err() is checked at the top of each iteration so cancellation
// is terminal (per feedback_ctx_cancel_terminal.md, issue #692).
func (c *Client) fetchSPAllPages(
	ctx context.Context,
	input *costexplorer.GetSavingsPlansPurchaseRecommendationInput,
	params *common.RecommendationParams,
	planType types.SupportedSavingsPlansType,
) ([]common.Recommendation, error) {
	var allRecs []common.Recommendation
	var nextPageToken *string

	for pageIdx := 0; ; pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pageIdx >= maxRecommendationPages {
			return nil, fmt.Errorf(
				"pagination cap reached after %d pages for SP %s (issue #692)",
				maxRecommendationPages, planType,
			)
		}
		input.NextPageToken = nextPageToken

		result, err := c.fetchSPPageWithRetry(ctx, input)
		if err != nil {
			return nil, err
		}
		if result == nil {
			break
		}

		if result.SavingsPlansPurchaseRecommendation != nil {
			recs := c.parseSavingsPlansRecommendations(result.SavingsPlansPurchaseRecommendation, params, planType)
			allRecs = append(allRecs, recs...)
		}

		if result.NextPageToken == nil || aws.ToString(result.NextPageToken) == "" {
			break
		}
		nextPageToken = result.NextPageToken
	}

	return allRecs, nil
}

// fetchSPPageWithRetry executes a single GetSavingsPlansPurchaseRecommendation
// call with rate-limiter exponential back-off. Extracted so the pagination loop
// in fetchSPAllPages stays below the gocyclo cap.
func (c *Client) fetchSPPageWithRetry(
	ctx context.Context,
	input *costexplorer.GetSavingsPlansPurchaseRecommendationInput,
) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	rateLimiter := c.rateLimiter.newOperation()
	var result *costexplorer.GetSavingsPlansPurchaseRecommendationOutput
	var err error

	for {
		if waitErr := rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}

		if acqErr := concurrency.Acquire(ctx); acqErr != nil {
			return nil, fmt.Errorf("concurrency acquire failed: %w", acqErr)
		}
		result, err = c.costExplorerClient.GetSavingsPlansPurchaseRecommendation(ctx, input)
		concurrency.Release(ctx)
		if !rateLimiter.ShouldRetry(err) {
			break
		}
	}

	if err != nil {
		return nil, err
	}

	return result, nil
}

// parseSavingsPlansRecommendations converts Savings Plans recommendations.
func (c *Client) parseSavingsPlansRecommendations(
	spRec *types.SavingsPlansPurchaseRecommendation,
	params *common.RecommendationParams,
	planType types.SupportedSavingsPlansType,
) []common.Recommendation {
	var recommendations []common.Recommendation

	for _, detail := range spRec.SavingsPlansPurchaseRecommendationDetails {
		rec, err := c.parseSavingsPlanDetail(&detail, params, planType)
		if err != nil {
			// present-but-unparseable money field: drop this recommendation
			// rather than forwarding a corrupt $0 to the scheduler/frontend.
			// Mirrors the RI path (parseAWSCostDetails) which errors on the
			// same class. Log so operators can detect malformed CE responses.
			log.Printf("WARNING: skipping SP recommendation (planType=%s): %v", planType, err)
			continue
		}
		if rec != nil {
			recommendations = append(recommendations, *rec)
		}
	}

	return recommendations
}

// parseOptionalFloat parses a *string pointer as float64.
//   - nil pointer (field absent from API response): returns (0, nil) — absent
//     is acceptable; callers treat 0 as "not available".
//   - non-nil pointer that fails ParseFloat (present but unparseable): returns
//     (0, error) — mirrors the RI path (parseAWSCostDetails) which errors on
//     the same class rather than silently substituting 0. Callers must propagate
//     this error and drop the recommendation so a corrupt money figure never
//     reaches the scheduler or frontend.
//   - non-nil pointer that parses to NaN or ±Inf: returns (0, error). Go's
//     strconv.ParseFloat accepts "NaN", "Inf", "+Inf", "-Inf" (and Infinity
//     variants) as valid with a nil error, so without this guard a corrupt
//     non-finite money value would flow straight through to the scheduler and
//     frontend. Reject non-finite values the same way as unparseable ones.
//   - non-nil pointer that parses to a negative value: returns (0, error). Every
//     field this parses (SP/RI cost, commitment, savings, savings %, utilization)
//     is non-negative by construction; a negative is corrupt in the same way a
//     non-finite value is. Mirrors the repo's stricter parseSPFloat helper.
func parseOptionalFloat(field string, s *string) (float64, error) {
	if s == nil {
		return 0, nil
	}
	val, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %s %q: %w", field, *s, err)
	}
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, fmt.Errorf("%s %q is not a finite number", field, *s)
	}
	if val < 0 {
		return 0, fmt.Errorf("%s %q is negative, which is invalid for a financial metric", field, *s)
	}
	return val, nil
}

// parseOptionalFloatOrWarn parses a *string as float64 but treats present-but-
// unparseable as a non-fatal warning (logs and returns 0). Use ONLY for
// non-money fields (utilization percentages, averages) where a bad value
// degrades gracefully. For money fields use parseOptionalFloat and propagate
// the error.
func parseOptionalFloatOrWarn(field string, s *string) float64 {
	val, err := parseOptionalFloat(field, s)
	if err != nil {
		log.Printf("WARNING: %v — treating as 0", err)
		return 0
	}
	return val
}

// hoursPerMonth is the standard AWS billing constant for monthly cost calculations.
const hoursPerMonth = 730.0

// ec2SPFields holds the Cost Explorer SavingsPlansDetails fields that are
// specific to EC2Instance Savings Plans. Extracted from parseSavingsPlanDetail
// to keep that function's cyclomatic complexity under the gocyclo-10 threshold.
type ec2SPFields struct {
	instanceFamily string
	region         string
	offeringID     string
}

// extractEC2SPFields reads InstanceFamily, Region, and OfferingId from the
// CE SavingsPlansDetails nested struct for EC2Instance plan recommendations.
// Returns a zero-value struct (all empty strings) for non-EC2Instance plan
// types and when SavingsPlansDetails is nil, so callers do not need a nil guard.
func extractEC2SPFields(planType types.SupportedSavingsPlansType, detail *types.SavingsPlansPurchaseRecommendationDetail) ec2SPFields {
	if planType != types.SupportedSavingsPlansTypeEc2InstanceSp || detail.SavingsPlansDetails == nil {
		return ec2SPFields{}
	}
	return ec2SPFields{
		instanceFamily: aws.ToString(detail.SavingsPlansDetails.InstanceFamily),
		region:         aws.ToString(detail.SavingsPlansDetails.Region),
		offeringID:     aws.ToString(detail.SavingsPlansDetails.OfferingId),
	}
}

// spPlanTypeDisplayString converts a SupportedSavingsPlansType to a
// human-readable plan-type label used in SavingsPlanDetails.PlanType.
// Returns the raw SDK string for unrecognised types (forward-compat).
func spPlanTypeDisplayString(pt types.SupportedSavingsPlansType) string {
	switch pt {
	case types.SupportedSavingsPlansTypeComputeSp:
		return "Compute"
	case types.SupportedSavingsPlansTypeEc2InstanceSp:
		return "EC2Instance"
	case types.SupportedSavingsPlansTypeSagemakerSp:
		return "SageMaker"
	case types.SupportedSavingsPlansTypeDatabaseSp:
		return "Database"
	}
	return string(pt)
}

// parseSavingsPlanDetail converts a single Savings Plan recommendation detail
// into a *common.Recommendation. Returns (nil, error) when any money field
// (HourlyCommitmentToPurchase, EstimatedMonthlySavingsAmount, UpfrontCost) is
// present in the API response but cannot be parsed as float64 — mirroring the
// RI path (parseAWSCostDetails) which errors on the same class rather than
// silently substituting 0. Non-money fields (percentages, utilization) degrade
// to 0 with a log warning.
func (c *Client) parseSavingsPlanDetail(
	detail *types.SavingsPlansPurchaseRecommendationDetail,
	params *common.RecommendationParams,
	planType types.SupportedSavingsPlansType,
) (*common.Recommendation, error) {
	hourlyCommitment, err := parseOptionalFloat("HourlyCommitmentToPurchase", detail.HourlyCommitmentToPurchase)
	if err != nil {
		return nil, err
	}
	monthlySavings, err := parseOptionalFloat("EstimatedMonthlySavingsAmount", detail.EstimatedMonthlySavingsAmount)
	if err != nil {
		return nil, err
	}
	upfrontCost, err := parseOptionalFloat("UpfrontCost", detail.UpfrontCost)
	if err != nil {
		return nil, err
	}

	// Non-money fields: present-but-unparseable degrades to 0 with a warning
	// rather than dropping the whole recommendation. The RI path uses the same
	// two-tier treatment (hard error on cost, warn-and-continue on percentages).
	savingsPercent := parseOptionalFloatOrWarn("EstimatedSavingsPercentage", detail.EstimatedSavingsPercentage)
	// EstimatedAverageUtilization carries the "if you buy exactly this commitment,
	// what % of it will AWS expect to be used" signal. Used by --target-coverage
	// sizing in cmd/helpers.go; zero (nil pointer or parse failure) means "no signal"
	// and the sizing path leaves the recommendation unchanged.
	recommendedUtilization := parseOptionalFloatOrWarn("EstimatedAverageUtilization", detail.EstimatedAverageUtilization)
	// onDemandCost is the canonical monthly on-demand baseline for this SP
	// recommendation. AWS Cost Explorer returns the average hourly on-demand
	// spend over the lookback period in CurrentAverageHourlyOnDemandSpend;
	// multiplying by hoursPerMonth gives the monthly equivalent, which is the
	// denominator AWS uses internally when computing EstimatedSavingsPercentage.
	// We surface it as OnDemandCost so the frontend can use the provider-
	// supplied value directly instead of reconstructing from
	// monthly_cost + savings + amortized (which is less accurate for SP rows
	// where monthly_cost reflects only the no-upfront recurring charge, not
	// the full on-demand baseline). See #303.
	onDemandCost := parseOptionalFloatOrWarn("CurrentAverageHourlyOnDemandSpend", detail.CurrentAverageHourlyOnDemandSpend) * hoursPerMonth
	if detail.CurrentAverageHourlyOnDemandSpend == nil {
		// CurrentAverageHourlyOnDemandSpend absent from AWS CE response — onDemandCost
		// will be 0 and the scheduler's nonZeroPtr will store nil, causing the
		// frontend to fall back to the reconstruction formula. Log so operators
		// can detect when the API field is missing. See #321.
		log.Printf("WARNING: CurrentAverageHourlyOnDemandSpend is nil for SP recommendation (planType=%s, account=%s) — Effective %% will use reconstruction fallback", planType, aws.ToString(detail.AccountId))
	}

	planTypeStr := spPlanTypeDisplayString(planType)

	accountID := ""
	if detail.AccountId != nil {
		accountID = aws.ToString(detail.AccountId)
	}

	// Extract EC2Instance-specific routing data from CE's SavingsPlansDetails.
	// See extractEC2SPFields for the extraction logic and field semantics.
	ec2Fields := extractEC2SPFields(planType, detail)

	// RecurringMonthlyCost is the portion of the SP commitment that appears
	// on monthly bills (i.e. excludes upfront payments).
	//
	//   - all-upfront: recurring = 0 (everything was paid upfront; monthly
	//     bills show no further charge). Use explicit 0, not nil, so the
	//     frontend can distinguish "known-zero" from "data not provided".
	//   - partial-upfront / no-upfront: recurring ≈ HourlyCommitmentToPurchase
	//     × 730. For partial-upfront this slightly over-counts (it includes the
	//     amortized upfront portion), but AWS CE does not expose the recurring-
	//     only hourly rate directly, so this is the best available approximation.
	//   - nil: HourlyCommitmentToPurchase was absent from the API response
	//     (should not happen for well-formed CE responses, but handled defensively).
	var recurringMonthlyCost *float64
	if detail.HourlyCommitmentToPurchase != nil {
		if params.PaymentOption == "all-upfront" {
			zero := 0.0
			recurringMonthlyCost = &zero
		} else {
			monthly := hourlyCommitment * hoursPerMonth
			recurringMonthlyCost = &monthly
		}
	}

	return &common.Recommendation{
		Provider:               common.ProviderAWS,
		Service:                serviceSlugForPlanType(planType),
		PaymentOption:          params.PaymentOption,
		Term:                   params.Term,
		CommitmentType:         common.CommitmentSavingsPlan,
		Count:                  1,
		EstimatedSavings:       monthlySavings,
		SavingsPercentage:      savingsPercent,
		CommitmentCost:         upfrontCost,
		OnDemandCost:           onDemandCost,
		RecurringMonthlyCost:   recurringMonthlyCost,
		RecommendedUtilization: recommendedUtilization,
		Timestamp:              time.Now(),
		Account:                accountID,
		Details: &common.SavingsPlanDetails{
			PlanType:         planTypeStr,
			HourlyCommitment: hourlyCommitment,
			Coverage:         fmt.Sprintf("%.1f%%", savingsPercent),
			InstanceFamily:   ec2Fields.instanceFamily,
			Region:           ec2Fields.region,
			OfferingID:       ec2Fields.offeringID,
		},
	}, nil
}

// planTypesForParams resolves which AWS Cost Explorer plan types to query
// for a given RecommendationParams. When params.Service is one of the four
// per-plan-type slugs the result is a single-element slice for that type;
// otherwise it falls back to the legacy IncludeSPTypes/ExcludeSPTypes filter.
// See the getSavingsPlansRecommendations docstring for the full resolution
// order.
func planTypesForParams(params *common.RecommendationParams) []types.SupportedSavingsPlansType {
	if pt, ok := planTypeForServiceSlug(params.Service); ok {
		return []types.SupportedSavingsPlansType{pt}
	}
	return getFilteredPlanTypes(params.IncludeSPTypes, params.ExcludeSPTypes)
}

// planTypeForServiceSlug maps a per-plan-type SP service slug to its
// Cost Explorer plan-type enum. Returns false for non-SP slugs and for the
// umbrella sentinel ServiceSavingsPlansAll (which still triggers the iterate-all
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
// slug rather than the umbrella sentinel, so downstream stats/filters can
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
	return common.ServiceSavingsPlansAll
}

// normalizeFilterSet lowercases each entry in filters and returns them as a
// set (map[string]bool). Extracted from getFilteredPlanTypes so the function
// literal does not capture outer variables (gocritic unlambda).
func normalizeFilterSet(filters []string) map[string]bool {
	result := make(map[string]bool, len(filters))
	for _, f := range filters {
		result[strings.ToLower(f)] = true
	}
	return result
}

// getFilteredPlanTypes returns the list of Savings Plan types to query based
// on include/exclude filters. Iterates a fixed-order slice rather than a map
// so the returned order is deterministic — downstream "first plan type wins"
// behavior and test assertions can rely on it.
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

	includeMap := normalizeFilterSet(includeSPTypes)
	excludeMap := normalizeFilterSet(excludeSPTypes)

	var result []types.SupportedSavingsPlansType

	// If include list is specified, only include those types.
	if len(includeMap) > 0 {
		for _, item := range allPlanTypes {
			if includeMap[item.name] && !excludeMap[item.name] {
				result = append(result, item.typ)
			}
		}
	} else {
		// Include all types except those in the exclude list.
		for _, item := range allPlanTypes {
			if !excludeMap[item.name] {
				result = append(result, item.typ)
			}
		}
	}

	return result
}
