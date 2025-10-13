package recommendations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

// regionNameToCode maps AWS human-readable region names to region codes
var regionNameToCode = map[string]string{
	"US East (N. Virginia)":     "us-east-1",
	"US East (Ohio)":            "us-east-2",
	"US West (N. California)":   "us-west-1",
	"US West (Oregon)":          "us-west-2",
	"Africa (Cape Town)":        "af-south-1",
	"Asia Pacific (Hong Kong)":  "ap-east-1",
	"Asia Pacific (Hyderabad)":  "ap-south-2",
	"Asia Pacific (Jakarta)":    "ap-southeast-3",
	"Asia Pacific (Melbourne)":  "ap-southeast-4",
	"Asia Pacific (Mumbai)":     "ap-south-1",
	"Asia Pacific (Osaka)":      "ap-northeast-3",
	"Asia Pacific (Seoul)":      "ap-northeast-2",
	"Asia Pacific (Singapore)":  "ap-southeast-1",
	"Asia Pacific (Sydney)":     "ap-southeast-2",
	"Asia Pacific (Tokyo)":      "ap-northeast-1",
	"Canada (Central)":          "ca-central-1",
	"Europe (Frankfurt)":        "eu-central-1",
	"Europe (Ireland)":          "eu-west-1",
	"Europe (London)":           "eu-west-2",
	"Europe (Milan)":            "eu-south-1",
	"Europe (Paris)":            "eu-west-3",
	"Europe (Spain)":            "eu-south-2",
	"Europe (Stockholm)":        "eu-north-1",
	"Europe (Zurich)":           "eu-central-2",
	"Middle East (Bahrain)":     "me-south-1",
	"Middle East (UAE)":         "me-central-1",
	"South America (São Paulo)": "sa-east-1",
	"AWS GovCloud (US-East)":    "us-gov-east-1",
	"AWS GovCloud (US-West)":    "us-gov-west-1",
}

// normalizeRegionName converts human-readable region names to AWS region codes
func normalizeRegionName(regionName string) string {
	if regionName == "" {
		return ""
	}

	// First try exact match
	if code, exists := regionNameToCode[regionName]; exists {
		return code
	}

	// If it's already a region code (lowercase with dashes), return as-is
	if isRegionCode(regionName) {
		return regionName
	}

	// Try case-insensitive match
	for name, code := range regionNameToCode {
		if strings.EqualFold(name, regionName) {
			return code
		}
	}

	// Try partial matching for common variations
	regionLower := strings.ToLower(regionName)

	// Handle common abbreviations and variations
	switch {
	case strings.Contains(regionLower, "virginia") || strings.Contains(regionLower, "n. virginia"):
		return "us-east-1"
	case strings.Contains(regionLower, "ohio"):
		return "us-east-2"
	case strings.Contains(regionLower, "california") || strings.Contains(regionLower, "n. california"):
		return "us-west-1"
	case strings.Contains(regionLower, "oregon"):
		return "us-west-2"
	case strings.Contains(regionLower, "ireland"):
		return "eu-west-1"
	case strings.Contains(regionLower, "frankfurt"):
		return "eu-central-1"
	case strings.Contains(regionLower, "london"):
		return "eu-west-2"
	case strings.Contains(regionLower, "paris"):
		return "eu-west-3"
	case strings.Contains(regionLower, "tokyo"):
		return "ap-northeast-1"
	case strings.Contains(regionLower, "singapore"):
		return "ap-southeast-1"
	case strings.Contains(regionLower, "sydney"):
		return "ap-southeast-2"
	case strings.Contains(regionLower, "mumbai"):
		return "ap-south-1"
	case strings.Contains(regionLower, "seoul"):
		return "ap-northeast-2"
	}

	// If no match found, return the original
	return regionName
}

// isRegionCode checks if a string looks like an AWS region code
func isRegionCode(s string) bool {
	// AWS region codes are typically lowercase, contain dashes, and follow patterns like:
	// us-east-1, eu-west-1, ap-southeast-2, etc.
	return strings.Contains(s, "-") &&
		strings.ToLower(s) == s &&
		!strings.Contains(s, " ") &&
		!strings.Contains(s, "(") &&
		!strings.Contains(s, ")")
}

// Client wraps the AWS Cost Explorer client for RI recommendations
type Client struct {
	costExplorerClient *costexplorer.Client
	region             string
}

// NewClient creates a new recommendations client
func NewClient(cfg aws.Config) *Client {
	// Force Cost Explorer to use us-east-1 with explicit endpoint
	ceConfig := cfg.Copy()
	ceConfig.Region = "us-east-1"

	// Add custom endpoint resolution for Cost Explorer
	ceConfig.BaseEndpoint = aws.String("https://ce.us-east-1.amazonaws.com")

	return &Client{
		costExplorerClient: costexplorer.NewFromConfig(ceConfig),
		region:             cfg.Region,
	}
}

// GetRDSRecommendations fetches RDS Reserved Instance recommendations
func (c *Client) GetRDSRecommendations(ctx context.Context, region string) ([]Recommendation, error) {
	input := &costexplorer.GetReservationPurchaseRecommendationInput{
		Service:              aws.String("Amazon Relational Database Service"),
		PaymentOption:        types.PaymentOptionPartialUpfront,
		TermInYears:          types.TermInYearsThreeYears,
		LookbackPeriodInDays: types.LookbackPeriodInDaysSevenDays,
	}

	result, err := c.costExplorerClient.GetReservationPurchaseRecommendation(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get RI recommendations: %w", err)
	}

	return c.parseRecommendations(result.Recommendations, region)
}

// GetRDSRecommendationsWithParams fetches RDS RI recommendations with custom parameters
func (c *Client) GetRDSRecommendationsWithParams(ctx context.Context, params RecommendationParams) ([]Recommendation, error) {
	input := &costexplorer.GetReservationPurchaseRecommendationInput{
		Service:              aws.String("Amazon Relational Database Service"),
		PaymentOption:        convertPaymentOption(params.PaymentOption),
		TermInYears:          convertTermInYears(params.TermInYears),
		LookbackPeriodInDays: convertLookbackPeriod(params.LookbackPeriodDays),
	}

	// Add account ID filter if specified
	if params.AccountID != "" {
		input.AccountId = aws.String(params.AccountID)
	}

	result, err := c.costExplorerClient.GetReservationPurchaseRecommendation(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get RI recommendations: %w", err)
	}

	return c.parseRecommendations(result.Recommendations, params.Region)
}

// parseRecommendations converts AWS recommendations to our internal format
func (c *Client) parseRecommendations(awsRecs []types.ReservationPurchaseRecommendation, targetRegion string) ([]Recommendation, error) {
	var recommendations []Recommendation

	for _, awsRec := range awsRecs {
		// Process ALL recommendation details, not just the first one
		for i, details := range awsRec.RecommendationDetails {
			rec, err := c.parseRecommendationDetail(awsRec, &details, targetRegion)
			if err != nil {
				// Log error but continue processing other recommendations
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

// parseRecommendationDetail converts a single AWS recommendation detail to our format
func (c *Client) parseRecommendationDetail(awsRec types.ReservationPurchaseRecommendation, details *types.ReservationPurchaseRecommendationDetail, targetRegion string) (*Recommendation, error) {
	// Extract instance details
	instanceType, engine, region, azConfig, err := c.extractInstanceDetails(details)
	if err != nil {
		return nil, fmt.Errorf("failed to extract instance details: %w", err)
	}

	// Filter by region if specified
	if targetRegion != "" && region != targetRegion {
		return nil, nil // Skip this recommendation
	}

	// Parse recommended quantity
	count, err := c.parseRecommendedQuantity(details)
	if err != nil {
		return nil, fmt.Errorf("failed to parse recommended quantity: %w", err)
	}

	// Parse cost information from the detail-level data
	estimatedCost, savingsPercent, err := c.parseCostInformationFromDetail(details)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cost information: %w", err)
	}

	// Extract account ID if available (from organization-level recommendations)
	accountID := ""
	if details.AccountId != nil {
		accountID = aws.ToString(details.AccountId)
	}

	rec := &Recommendation{
		Region:         region,
		InstanceType:   instanceType,
		Engine:         engine,
		AZConfig:       azConfig,
		PaymentOption:  "partial-upfront", // Default from our query
		Term:           36,                // 3 years from our query
		Count:          count,
		EstimatedCost:  estimatedCost,
		SavingsPercent: savingsPercent,
		Timestamp:      time.Now(),
		AccountID:      accountID,
	}

	rec.Description = rec.GenerateDescription()
	return rec, nil
}

// parseCostInformationFromDetail extracts cost info from individual recommendation details
func (c *Client) parseCostInformationFromDetail(details *types.ReservationPurchaseRecommendationDetail) (float64, float64, error) {
	var estimatedCost, savingsPercent float64

	// Parse monthly savings amount
	if details.EstimatedMonthlySavingsAmount != nil {
		fmt.Sscanf(*details.EstimatedMonthlySavingsAmount, "%f", &estimatedCost)
	}

	// Parse savings percentage
	if details.EstimatedMonthlySavingsPercentage != nil {
		fmt.Sscanf(*details.EstimatedMonthlySavingsPercentage, "%f", &savingsPercent)
	}

	return estimatedCost, savingsPercent, nil
}

// parseRecommendation converts a single AWS recommendation to our format
func (c *Client) parseRecommendation(awsRec types.ReservationPurchaseRecommendation, targetRegion string) (*Recommendation, error) {
	if len(awsRec.RecommendationDetails) == 0 {
		return nil, fmt.Errorf("recommendation details are missing")
	}

	// Get the first recommendation detail (AWS can return multiple details)
	details := &awsRec.RecommendationDetails[0]

	// Extract instance details - this varies by service type
	instanceType, engine, region, azConfig, err := c.extractInstanceDetails(details)
	if err != nil {
		return nil, fmt.Errorf("failed to extract instance details: %w", err)
	}

	// Filter by region if specified
	if targetRegion != "" && region != targetRegion {
		return nil, nil // Skip this recommendation
	}

	// Parse recommended quantity
	count, err := c.parseRecommendedQuantity(details)
	if err != nil {
		return nil, fmt.Errorf("failed to parse recommended quantity: %w", err)
	}

	// Parse cost information
	estimatedCost, savingsPercent, err := c.parseCostInformation(awsRec)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cost information: %w", err)
	}

	rec := &Recommendation{
		Region:         region,
		InstanceType:   instanceType,
		Engine:         engine,
		AZConfig:       azConfig,
		PaymentOption:  "partial-upfront", // Default from our query
		Term:           36,                // 3 years from our query
		Count:          count,
		EstimatedCost:  estimatedCost,
		SavingsPercent: savingsPercent,
		Timestamp:      time.Now(),
	}

	rec.Description = rec.GenerateDescription()
	return rec, nil
}

// extractInstanceDetails extracts instance type, engine, region, and AZ config from recommendation details
func (c *Client) extractInstanceDetails(details *types.ReservationPurchaseRecommendationDetail) (string, string, string, string, error) {
	var instanceType, engine, region, azConfig string

	// Extract from InstanceDetails if available
	if details.InstanceDetails != nil && details.InstanceDetails.RDSInstanceDetails != nil {
		rdsDetails := details.InstanceDetails.RDSInstanceDetails

		if rdsDetails.InstanceType != nil {
			instanceType = *rdsDetails.InstanceType
		}
		if rdsDetails.DatabaseEngine != nil {
			engine = *rdsDetails.DatabaseEngine
		}
		if rdsDetails.Region != nil {
			// Normalize the region name to AWS region code
			rawRegion := *rdsDetails.Region
			region = normalizeRegionName(rawRegion)

			// Log the mapping for debugging
			if region != rawRegion {
				fmt.Printf("Debug: Mapped region '%s' to '%s'\n", rawRegion, region)
			}
		}
		if rdsDetails.DeploymentOption != nil {
			if *rdsDetails.DeploymentOption == "Multi-AZ" {
				azConfig = "multi-az"
			} else {
				azConfig = "single-az"
			}
		}
	}

	// Validate required fields
	if instanceType == "" {
		return "", "", "", "", fmt.Errorf("instance type not found")
	}
	if engine == "" {
		return "", "", "", "", fmt.Errorf("engine not found")
	}
	if region == "" {
		region = normalizeRegionName(c.region) // Use client's default region
	}
	if azConfig == "" {
		azConfig = "single-az" // Default to single-az
	}

	return instanceType, engine, region, azConfig, nil
}

// parseRecommendedQuantity extracts the recommended quantity from details
func (c *Client) parseRecommendedQuantity(details *types.ReservationPurchaseRecommendationDetail) (int32, error) {
	if details.RecommendedNumberOfInstancesToPurchase == nil {
		return 0, fmt.Errorf("recommended quantity not found")
	}

	// AWS returns this as a string, we need to parse it
	qty := *details.RecommendedNumberOfInstancesToPurchase

	// Parse the quantity string (e.g., "5.0" -> 5)
	var count float64
	_, err := fmt.Sscanf(qty, "%f", &count)
	if err != nil {
		return 0, fmt.Errorf("failed to parse quantity '%s': %w", qty, err)
	}

	return int32(count), nil
}

// parseCostInformation extracts cost and savings information
func (c *Client) parseCostInformation(awsRec types.ReservationPurchaseRecommendation) (float64, float64, error) {
	var estimatedCost, savingsPercent float64

	if awsRec.RecommendationSummary != nil {
		summary := awsRec.RecommendationSummary

		// Parse total cost
		if summary.TotalEstimatedMonthlySavingsAmount != nil {
			fmt.Sscanf(*summary.TotalEstimatedMonthlySavingsAmount, "%f", &estimatedCost)
		}

		// Parse savings percentage
		if summary.TotalEstimatedMonthlySavingsPercentage != nil {
			fmt.Sscanf(*summary.TotalEstimatedMonthlySavingsPercentage, "%f", &savingsPercent)
		}
	}

	return estimatedCost, savingsPercent, nil
}

// Helper functions to convert between our types and AWS types

func convertPaymentOption(option string) types.PaymentOption {
	switch option {
	case "all-upfront":
		return types.PaymentOptionAllUpfront
	case "partial-upfront":
		return types.PaymentOptionPartialUpfront
	case "no-upfront":
		return types.PaymentOptionNoUpfront
	default:
		return types.PaymentOptionPartialUpfront
	}
}

func convertTermInYears(years int) types.TermInYears {
	switch years {
	case 1:
		return types.TermInYearsOneYear
	case 3:
		return types.TermInYearsThreeYears
	default:
		return types.TermInYearsThreeYears
	}
}

func convertLookbackPeriod(days int) types.LookbackPeriodInDays {
	switch days {
	case 7:
		return types.LookbackPeriodInDaysSevenDays
	case 30:
		return types.LookbackPeriodInDaysThirtyDays
	case 60:
		return types.LookbackPeriodInDaysSixtyDays
	default:
		return types.LookbackPeriodInDaysSevenDays
	}
}

// GetRDSRecommendationsForDiscovery fetches RDS RI recommendations without region filtering
// This is used for auto-discovering regions that have recommendations
func (c *Client) GetRDSRecommendationsForDiscovery(ctx context.Context) ([]Recommendation, error) {
	input := &costexplorer.GetReservationPurchaseRecommendationInput{
		Service:              aws.String("Amazon Relational Database Service"),
		PaymentOption:        types.PaymentOptionPartialUpfront,
		TermInYears:          types.TermInYearsThreeYears,
		LookbackPeriodInDays: types.LookbackPeriodInDaysSevenDays,
	}

	result, err := c.costExplorerClient.GetReservationPurchaseRecommendation(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get RI recommendations: %w", err)
	}

	// Parse all recommendations without region filtering (pass empty string)
	return c.parseRecommendations(result.Recommendations, "")
}
