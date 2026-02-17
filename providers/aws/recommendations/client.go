// Package recommendations provides AWS Cost Explorer recommendations client
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

// CostExplorerAPI defines the interface for Cost Explorer operations
type CostExplorerAPI interface {
	GetReservationPurchaseRecommendation(ctx context.Context, params *costexplorer.GetReservationPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationPurchaseRecommendationOutput, error)
	GetSavingsPlansPurchaseRecommendation(ctx context.Context, params *costexplorer.GetSavingsPlansPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error)
}

// Client wraps the AWS Cost Explorer client for RI recommendations
type Client struct {
	costExplorerClient CostExplorerAPI
	region             string
	rateLimiter        *RateLimiter
}

// NewClient creates a new recommendations client
func NewClient(cfg aws.Config) *Client {
	// Force Cost Explorer to use us-east-1 with explicit endpoint
	ceConfig := cfg.Copy()
	ceConfig.Region = "us-east-1"
	ceConfig.BaseEndpoint = aws.String("https://ce.us-east-1.amazonaws.com")

	return &Client{
		costExplorerClient: costexplorer.NewFromConfig(ceConfig),
		region:             cfg.Region,
		rateLimiter:        NewRateLimiter(),
	}
}

// NewClientWithAPI creates a new recommendations client with a custom Cost Explorer API (for testing)
func NewClientWithAPI(api CostExplorerAPI, region string) *Client {
	return &Client{
		costExplorerClient: api,
		region:             region,
		rateLimiter:        NewRateLimiter(),
	}
}

// GetRecommendations fetches Reserved Instance recommendations for any service
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	// Handle Savings Plans separately as they use a different API
	if params.Service == common.ServiceSavingsPlans {
		return c.getSavingsPlansRecommendations(ctx, params)
	}

	input := &costexplorer.GetReservationPurchaseRecommendationInput{
		Service:              aws.String(getServiceStringForCostExplorer(params.Service)),
		PaymentOption:        convertPaymentOption(params.PaymentOption),
		TermInYears:          convertTermInYears(params.Term),
		LookbackPeriodInDays: convertLookbackPeriod(params.LookbackPeriod),
		AccountScope:         types.AccountScopeLinked,
	}

	// Implement rate limiting with exponential backoff
	var result *costexplorer.GetReservationPurchaseRecommendationOutput
	var err error

	c.rateLimiter.Reset()
	for {
		if waitErr := c.rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}

		result, err = c.costExplorerClient.GetReservationPurchaseRecommendation(ctx, input)
		if !c.rateLimiter.ShouldRetry(err) {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get RI recommendations after %d retries: %w", c.rateLimiter.GetRetryCount(), err)
	}

	return c.parseRecommendations(result.Recommendations, params)
}

// GetRecommendationsForService fetches recommendations for a specific service (for discovery)
func (c *Client) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	params := common.RecommendationParams{
		Service:        service,
		PaymentOption:  "partial-upfront",
		Term:           "3yr",
		LookbackPeriod: "7d",
		Region:         "",
	}

	return c.GetRecommendations(ctx, params)
}

// GetAllRecommendations fetches recommendations for all supported services
func (c *Client) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	services := []common.ServiceType{
		common.ServiceEC2,
		common.ServiceRDS,
		common.ServiceElastiCache,
		common.ServiceOpenSearch,
		common.ServiceRedshift,
	}

	allRecommendations := make([]common.Recommendation, 0)

	for _, service := range services {
		recs, err := c.GetRecommendationsForService(ctx, service)
		if err != nil {
			continue
		}
		allRecommendations = append(allRecommendations, recs...)
		time.Sleep(100 * time.Millisecond)
	}

	return allRecommendations, nil
}

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

	// Parse service-specific details
	switch params.Service {
	case common.ServiceRDS, common.ServiceRelationalDB:
		if err := c.parseRDSDetails(rec, details); err != nil {
			return nil, err
		}
	case common.ServiceElastiCache, common.ServiceCache:
		if err := c.parseElastiCacheDetails(rec, details); err != nil {
			return nil, err
		}
	case common.ServiceEC2, common.ServiceCompute:
		if err := c.parseEC2Details(rec, details); err != nil {
			return nil, err
		}
	case common.ServiceOpenSearch, common.ServiceSearch:
		if err := c.parseOpenSearchDetails(rec, details); err != nil {
			return nil, err
		}
	case common.ServiceRedshift, common.ServiceDataWarehouse:
		if err := c.parseRedshiftDetails(rec, details); err != nil {
			return nil, err
		}
	case common.ServiceMemoryDB:
		if err := c.parseMemoryDBDetails(rec, details); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported service: %s", params.Service)
	}

	return rec, nil
}

// parseRDSDetails extracts RDS-specific details
func (c *Client) parseRDSDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.RDSInstanceDetails == nil {
		return fmt.Errorf("RDS instance details not found")
	}

	rdsDetails := details.InstanceDetails.RDSInstanceDetails
	rdsInfo := &common.DatabaseDetails{}

	if rdsDetails.InstanceType != nil {
		rec.ResourceType = *rdsDetails.InstanceType
	}
	if rdsDetails.DatabaseEngine != nil {
		rdsInfo.Engine = *rdsDetails.DatabaseEngine
	}
	if rdsDetails.Region != nil {
		rec.Region = normalizeRegionName(*rdsDetails.Region)
	}
	if rdsDetails.DeploymentOption != nil {
		if *rdsDetails.DeploymentOption == "Multi-AZ" {
			rdsInfo.AZConfig = "multi-az"
		} else {
			rdsInfo.AZConfig = "single-az"
		}
	} else {
		rdsInfo.AZConfig = "single-az"
	}

	rec.Details = rdsInfo
	return nil
}

// parseElastiCacheDetails extracts ElastiCache-specific details
func (c *Client) parseElastiCacheDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.ElastiCacheInstanceDetails == nil {
		return fmt.Errorf("ElastiCache instance details not found")
	}

	cacheDetails := details.InstanceDetails.ElastiCacheInstanceDetails
	cacheInfo := &common.CacheDetails{}

	if cacheDetails.NodeType != nil {
		rec.ResourceType = *cacheDetails.NodeType
		cacheInfo.NodeType = *cacheDetails.NodeType
	}
	if cacheDetails.ProductDescription != nil {
		cacheInfo.Engine = *cacheDetails.ProductDescription
	}
	if cacheDetails.Region != nil {
		rec.Region = normalizeRegionName(*cacheDetails.Region)
	}

	rec.Details = cacheInfo
	return nil
}

// parseEC2Details extracts EC2-specific details
func (c *Client) parseEC2Details(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.EC2InstanceDetails == nil {
		return fmt.Errorf("EC2 instance details not found")
	}

	ec2Details := details.InstanceDetails.EC2InstanceDetails
	ec2Info := &common.ComputeDetails{}

	if ec2Details.InstanceType != nil {
		rec.ResourceType = *ec2Details.InstanceType
		ec2Info.InstanceType = *ec2Details.InstanceType
	}
	if ec2Details.Platform != nil {
		ec2Info.Platform = *ec2Details.Platform
	}
	if ec2Details.Region != nil {
		rec.Region = normalizeRegionName(*ec2Details.Region)
	}
	if ec2Details.Tenancy != nil {
		ec2Info.Tenancy = *ec2Details.Tenancy
	} else {
		ec2Info.Tenancy = "shared"
	}

	if ec2Details.AvailabilityZone != nil && *ec2Details.AvailabilityZone != "" {
		ec2Info.Scope = "availability-zone"
	} else {
		ec2Info.Scope = "region"
	}

	rec.Details = ec2Info
	return nil
}

// parseOpenSearchDetails extracts OpenSearch-specific details
func (c *Client) parseOpenSearchDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.ESInstanceDetails == nil {
		return fmt.Errorf("OpenSearch/Elasticsearch instance details not found")
	}

	esDetails := details.InstanceDetails.ESInstanceDetails
	osInfo := &common.SearchDetails{}

	if esDetails.InstanceClass != nil && esDetails.InstanceSize != nil {
		instanceSize := *esDetails.InstanceSize
		// Cost Explorer may return InstanceSize as the full type (e.g. "t3.medium.search");
		// concatenating with InstanceClass would duplicate (e.g. "t3.medium.t3.medium.search").
		if strings.HasSuffix(instanceSize, ".search") {
			rec.ResourceType = instanceSize
		} else {
			rec.ResourceType = fmt.Sprintf("%s.%s.search", *esDetails.InstanceClass, instanceSize)
		}
		osInfo.InstanceType = rec.ResourceType
	}
	if esDetails.Region != nil {
		rec.Region = normalizeRegionName(*esDetails.Region)
	}

	rec.Details = osInfo
	return nil
}

// parseRedshiftDetails extracts Redshift-specific details
func (c *Client) parseRedshiftDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.RedshiftInstanceDetails == nil {
		return fmt.Errorf("Redshift instance details not found")
	}

	rsDetails := details.InstanceDetails.RedshiftInstanceDetails
	rsInfo := &common.DataWarehouseDetails{}

	if rsDetails.NodeType != nil {
		rec.ResourceType = *rsDetails.NodeType
		rsInfo.NodeType = *rsDetails.NodeType
	}
	if rsDetails.Region != nil {
		rec.Region = normalizeRegionName(*rsDetails.Region)
	}

	rsInfo.NumberOfNodes = rec.Count
	if rsInfo.NumberOfNodes == 1 {
		rsInfo.ClusterType = "single-node"
	} else {
		rsInfo.ClusterType = "multi-node"
	}

	rec.Details = rsInfo
	return nil
}

// parseMemoryDBDetails extracts MemoryDB-specific details
func (c *Client) parseMemoryDBDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	// MemoryDB might not have specific details in Cost Explorer yet
	rec.ResourceType = "db.r6gd.xlarge" // Default
	rec.Details = &common.CacheDetails{
		Engine:   "redis",
		NodeType: rec.ResourceType,
	}
	return nil
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
		if intCount, err := strconv.Atoi(qty); err == nil {
			return intCount, nil
		}
		return 0, fmt.Errorf("failed to parse quantity '%s': %w", qty, err)
	}

	return int(count), nil
}

// parseCostInformation extracts cost and savings information
func (c *Client) parseCostInformation(details *types.ReservationPurchaseRecommendationDetail) (float64, float64, error) {
	var estimatedSavings, savingsPercent float64

	if details.EstimatedMonthlySavingsAmount != nil {
		fmt.Sscanf(*details.EstimatedMonthlySavingsAmount, "%f", &estimatedSavings)
	}

	if details.EstimatedMonthlySavingsPercentage != nil {
		fmt.Sscanf(*details.EstimatedMonthlySavingsPercentage, "%f", &savingsPercent)
	}

	return estimatedSavings, savingsPercent, nil
}

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

// Helper functions

func getServiceStringForCostExplorer(service common.ServiceType) string {
	switch service {
	case common.ServiceRDS, common.ServiceRelationalDB:
		return "Amazon Relational Database Service"
	case common.ServiceElastiCache, common.ServiceCache:
		return "Amazon ElastiCache"
	case common.ServiceEC2, common.ServiceCompute:
		return "Amazon Elastic Compute Cloud - Compute"
	case common.ServiceOpenSearch, common.ServiceSearch:
		return "Amazon OpenSearch Service"
	case common.ServiceRedshift, common.ServiceDataWarehouse:
		return "Amazon Redshift"
	case common.ServiceMemoryDB:
		return "Amazon MemoryDB Service"
	default:
		return string(service)
	}
}

func convertPaymentOption(option string) types.PaymentOption {
	switch option {
	case "all-upfront":
		return types.PaymentOptionAllUpfront
	case "partial-upfront":
		return types.PaymentOptionPartialUpfront
	case "no-upfront":
		return types.PaymentOptionNoUpfront
	default:
		return types.PaymentOptionNoUpfront
	}
}

func convertTermInYears(term string) types.TermInYears {
	if term == "3yr" || term == "3" {
		return types.TermInYearsThreeYears
	}
	return types.TermInYearsOneYear
}

func convertLookbackPeriod(period string) types.LookbackPeriodInDays {
	switch period {
	case "7d", "7":
		return types.LookbackPeriodInDaysSevenDays
	case "30d", "30":
		return types.LookbackPeriodInDaysThirtyDays
	case "60d", "60":
		return types.LookbackPeriodInDaysSixtyDays
	default:
		return types.LookbackPeriodInDaysSevenDays
	}
}

func convertSavingsPlansPaymentOption(option string) types.PaymentOption {
	return convertPaymentOption(option)
}

func convertSavingsPlansTermInYears(term string) types.TermInYears {
	return convertTermInYears(term)
}

func convertSavingsPlansLookbackPeriod(period string) types.LookbackPeriodInDays {
	return convertLookbackPeriod(period)
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

func normalizeRegionName(region string) string {
	// AWS Cost Explorer sometimes returns region names like "US East (N. Virginia)"
	// Convert these to standard region codes
	regionMap := map[string]string{
		"US East (N. Virginia)":      "us-east-1",
		"US East (Ohio)":             "us-east-2",
		"US West (N. California)":    "us-west-1",
		"US West (Oregon)":           "us-west-2",
		"EU (Ireland)":               "eu-west-1",
		"EU (Frankfurt)":             "eu-central-1",
		"EU (London)":                "eu-west-2",
		"EU (Paris)":                 "eu-west-3",
		"EU (Stockholm)":             "eu-north-1",
		"Asia Pacific (Singapore)":   "ap-southeast-1",
		"Asia Pacific (Sydney)":      "ap-southeast-2",
		"Asia Pacific (Tokyo)":       "ap-northeast-1",
		"Asia Pacific (Seoul)":       "ap-northeast-2",
		"Asia Pacific (Mumbai)":      "ap-south-1",
		"South America (Sao Paulo)":  "sa-east-1",
		"Canada (Central)":           "ca-central-1",
		"Middle East (Bahrain)":      "me-south-1",
		"Africa (Cape Town)":         "af-south-1",
		"Asia Pacific (Hong Kong)":   "ap-east-1",
		"Asia Pacific (Osaka)":       "ap-northeast-3",
		"Asia Pacific (Jakarta)":     "ap-southeast-3",
		"Europe (Milan)":             "eu-south-1",
		"Middle East (UAE)":          "me-central-1",
		"Asia Pacific (Hyderabad)":   "ap-south-2",
		"Europe (Spain)":             "eu-south-2",
		"Europe (Zurich)":            "eu-central-2",
		"Asia Pacific (Melbourne)":   "ap-southeast-4",
		"Israel (Tel Aviv)":          "il-central-1",
	}

	if normalized, ok := regionMap[region]; ok {
		return normalized
	}

	// If already a region code, return as-is
	if strings.HasPrefix(region, "us-") || strings.HasPrefix(region, "eu-") ||
		strings.HasPrefix(region, "ap-") || strings.HasPrefix(region, "sa-") ||
		strings.HasPrefix(region, "ca-") || strings.HasPrefix(region, "me-") ||
		strings.HasPrefix(region, "af-") || strings.HasPrefix(region, "il-") {
		return region
	}

	return region
}
