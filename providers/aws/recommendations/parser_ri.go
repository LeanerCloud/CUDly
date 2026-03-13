package recommendations

import (
	"fmt"
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

	// Parse recommended quantity
	count, err := c.parseRecommendedQuantity(details)
	if err != nil {
		return nil, fmt.Errorf("failed to parse recommended quantity: %w", err)
	}
	rec.Count = count

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

	// Parse service-specific details
	if err := c.parseServiceSpecificDetails(rec, details, params.Service); err != nil {
		return nil, err
	}

	return rec, nil
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

// parseCostInformation extracts cost and savings information
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

// parseAWSCostDetails extracts upfront and on-demand cost from AWS details
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
