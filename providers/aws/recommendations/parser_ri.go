package recommendations

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// parseRecommendations converts AWS recommendations to common.Recommendation format
func (c *Client) parseRecommendations(awsRecs []types.ReservationPurchaseRecommendation, params common.RecommendationParams) ([]common.Recommendation, error) {
	var recommendations []common.Recommendation

	for _, awsRec := range awsRecs {
		for i, details := range awsRec.RecommendationDetails {
			rec, err := c.parseRecommendationDetail(&details, params)
			if err != nil {
				fmt.Printf("Warning: Failed to parse recommendation detail %d: %v\n", i, err)
				continue
			}

			if rec != nil {
				recommendations = append(recommendations, *rec)
			}
		}
	}

	return recommendations, nil
}

// parseRecommendationDetail converts a single AWS recommendation detail
func (c *Client) parseRecommendationDetail(details *types.ReservationPurchaseRecommendationDetail, params common.RecommendationParams) (*common.Recommendation, error) {
	rec := &common.Recommendation{
		Provider:       common.ProviderAWS,
		Service:        params.Service,
		PaymentOption:  params.PaymentOption,
		Term:           params.Term,
		CommitmentType: common.CommitmentReservedInstance,
		Timestamp:      time.Now(),
	}

	// Parse recommended quantity. RecommendedCount preserves AWS's pre-sizing
	// count so the CSV can show what AWS proposed alongside what --coverage /
	// --target-coverage chose; Count is the working value the sizing step
	// mutates.
	count, err := c.parseRecommendedQuantity(details)
	if err != nil {
		return nil, fmt.Errorf("failed to parse recommended quantity: %w", err)
	}
	rec.Count = count
	rec.RecommendedCount = count

	// Parse cost information
	rec.EstimatedSavings, rec.SavingsPercentage, err = c.parseCostInformation(details)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cost information: %w", err)
	}

	// Extract account ID if available
	if details.AccountId != nil {
		rec.Account = aws.ToString(details.AccountId)
	}

	// Parse AWS-provided cost details
	c.parseAWSCostDetails(rec, details)

	// Parse RI utilization signals used by --target-coverage sizing
	c.parseRIUtilizationSignals(rec, details)

	// Parse service-specific details
	if err := c.parseServiceSpecificDetails(rec, details, params.Service); err != nil {
		return nil, err
	}

	return rec, nil
}

// parseRIUtilizationSignals populates AverageInstancesUsedPerHour and
// RecommendedUtilization from the CE response. Both fields are *string in the
// SDK; nil or unparseable values leave the destination at zero, which the
// --target-coverage sizing path treats as "no signal" and skips.
func (c *Client) parseRIUtilizationSignals(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) {
	if details.AverageNumberOfInstancesUsedPerHour != nil {
		if v, err := strconv.ParseFloat(*details.AverageNumberOfInstancesUsedPerHour, 64); err == nil {
			rec.AverageInstancesUsedPerHour = v
		} else {
			log.Printf("WARNING: failed to parse AverageNumberOfInstancesUsedPerHour %q for RI recommendation (service=%s, account=%s): %v", *details.AverageNumberOfInstancesUsedPerHour, rec.Service, rec.Account, err)
		}
	}
	if details.AverageUtilization != nil {
		if v, err := strconv.ParseFloat(*details.AverageUtilization, 64); err == nil {
			rec.RecommendedUtilization = v
		} else {
			log.Printf("WARNING: failed to parse AverageUtilization %q for RI recommendation (service=%s, account=%s): %v", *details.AverageUtilization, rec.Service, rec.Account, err)
		}
	}
}

// parseRecommendedQuantity extracts the recommended quantity from details
func (c *Client) parseRecommendedQuantity(details *types.ReservationPurchaseRecommendationDetail) (int, error) {
	if details.RecommendedNumberOfInstancesToPurchase == nil {
		return 0, fmt.Errorf("recommended quantity not found")
	}

	qty := *details.RecommendedNumberOfInstancesToPurchase

	var count float64
	_, err := fmt.Sscanf(qty, "%f", &count)
	if err != nil {
		if intCount, atoiErr := strconv.Atoi(qty); atoiErr == nil {
			return intCount, nil
		}
		return 0, fmt.Errorf("failed to parse quantity '%s' as float or int", qty)
	}

	return int(math.Round(count)), nil
}

// parseCostInformation extracts cost and savings information.
//
// EstimatedMonthlySavingsAmount represents the savings from buying the full
// recommended quantity, which AWS CE sizes for ~100% coverage of the account's
// historical on-demand demand. This is the 100%-coverage baseline the dashboard
// scaling in summarizeRecommendationsWithCoverage depends on (issue #215 audit).
func (c *Client) parseCostInformation(details *types.ReservationPurchaseRecommendationDetail) (float64, float64, error) {
	var estimatedSavings, savingsPercent float64

	if details.EstimatedMonthlySavingsAmount != nil {
		val, err := strconv.ParseFloat(*details.EstimatedMonthlySavingsAmount, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse estimated savings %q: %w", *details.EstimatedMonthlySavingsAmount, err)
		}
		estimatedSavings = val
	}

	if details.EstimatedMonthlySavingsPercentage != nil {
		val, err := strconv.ParseFloat(*details.EstimatedMonthlySavingsPercentage, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse savings percentage %q: %w", *details.EstimatedMonthlySavingsPercentage, err)
		}
		savingsPercent = val
	}

	return estimatedSavings, savingsPercent, nil
}

// parseAWSCostDetails extracts upfront, on-demand, and recurring monthly cost from AWS details.
func (c *Client) parseAWSCostDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) {
	if details.UpfrontCost != nil {
		if upfront, err := strconv.ParseFloat(*details.UpfrontCost, 64); err == nil {
			rec.CommitmentCost = upfront
		}
	}
	if details.EstimatedMonthlyOnDemandCost != nil {
		if onDemand, err := strconv.ParseFloat(*details.EstimatedMonthlyOnDemandCost, 64); err == nil {
			rec.OnDemandCost = onDemand
		}
	} else {
		// EstimatedMonthlyOnDemandCost absent from AWS CE response — OnDemandCost
		// will be 0 and the scheduler's nonZeroPtr will store nil, causing the
		// frontend to fall back to the reconstruction formula. Log so operators
		// can detect when the API field is missing. See #321.
		log.Printf("WARNING: EstimatedMonthlyOnDemandCost is nil for RI recommendation (service=%s, account=%s) — Effective %% will use reconstruction fallback", rec.Service, rec.Account)
	}
	// RecurringStandardMonthlyCost is the recurring charge per month for this RI.
	// It is distinct from CommitmentCost (upfront) and EstimatedMonthlySavingsAmount.
	if details.RecurringStandardMonthlyCost != nil {
		if monthly, err := strconv.ParseFloat(*details.RecurringStandardMonthlyCost, 64); err == nil {
			rec.RecurringMonthlyCost = &monthly
		}
	}
}

// serviceParserFunc defines the signature for service-specific parsers
type serviceParserFunc func(*common.Recommendation, *types.ReservationPurchaseRecommendationDetail) error

// parseServiceSpecificDetails routes to the appropriate service parser
func (c *Client) parseServiceSpecificDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail, service common.ServiceType) error {
	// Map of service types to their parser functions
	serviceParsers := map[common.ServiceType]serviceParserFunc{
		common.ServiceRDS:           c.parseRDSDetails,
		common.ServiceRelationalDB:  c.parseRDSDetails,
		common.ServiceElastiCache:   c.parseElastiCacheDetails,
		common.ServiceCache:         c.parseElastiCacheDetails,
		common.ServiceEC2:           c.parseEC2Details,
		common.ServiceCompute:       c.parseEC2Details,
		common.ServiceOpenSearch:    c.parseOpenSearchDetails,
		common.ServiceSearch:        c.parseOpenSearchDetails,
		common.ServiceRedshift:      c.parseRedshiftDetails,
		common.ServiceDataWarehouse: c.parseRedshiftDetails,
		common.ServiceMemoryDB:      c.parseMemoryDBDetails,
	}

	parser, ok := serviceParsers[service]
	if !ok {
		return fmt.Errorf("unsupported service: %s", service)
	}

	return parser(rec, details)
}
