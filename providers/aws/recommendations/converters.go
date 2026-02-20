package recommendations

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// getServiceStringForCostExplorer converts service type to Cost Explorer service string
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

// convertPaymentOption converts payment option string to AWS type
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

// convertTermInYears converts term string to AWS type
func convertTermInYears(term string) types.TermInYears {
	if term == "3yr" || term == "3" {
		return types.TermInYearsThreeYears
	}
	return types.TermInYearsOneYear
}

// convertLookbackPeriod converts lookback period string to AWS type
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

// convertSavingsPlansPaymentOption converts payment option for Savings Plans
func convertSavingsPlansPaymentOption(option string) types.PaymentOption {
	return convertPaymentOption(option)
}

// convertSavingsPlansTermInYears converts term for Savings Plans
func convertSavingsPlansTermInYears(term string) types.TermInYears {
	return convertTermInYears(term)
}

// convertSavingsPlansLookbackPeriod converts lookback period for Savings Plans
func convertSavingsPlansLookbackPeriod(period string) types.LookbackPeriodInDays {
	return convertLookbackPeriod(period)
}

// normalizeRegionName converts AWS region display names to region codes
func normalizeRegionName(region string) string {
	// AWS Cost Explorer sometimes returns region names like "US East (N. Virginia)"
	// Convert these to standard region codes
	regionMap := map[string]string{
		"US East (N. Virginia)":     "us-east-1",
		"US East (Ohio)":            "us-east-2",
		"US West (N. California)":   "us-west-1",
		"US West (Oregon)":          "us-west-2",
		"EU (Ireland)":              "eu-west-1",
		"EU (Frankfurt)":            "eu-central-1",
		"EU (London)":               "eu-west-2",
		"EU (Paris)":                "eu-west-3",
		"EU (Stockholm)":            "eu-north-1",
		"Asia Pacific (Singapore)":  "ap-southeast-1",
		"Asia Pacific (Sydney)":     "ap-southeast-2",
		"Asia Pacific (Tokyo)":      "ap-northeast-1",
		"Asia Pacific (Seoul)":      "ap-northeast-2",
		"Asia Pacific (Mumbai)":     "ap-south-1",
		"South America (Sao Paulo)": "sa-east-1",
		"Canada (Central)":          "ca-central-1",
		"Middle East (Bahrain)":     "me-south-1",
		"Africa (Cape Town)":        "af-south-1",
		"Asia Pacific (Hong Kong)":  "ap-east-1",
		"Asia Pacific (Osaka)":      "ap-northeast-3",
		"Asia Pacific (Jakarta)":    "ap-southeast-3",
		"Europe (Milan)":            "eu-south-1",
		"Middle East (UAE)":         "me-central-1",
		"Asia Pacific (Hyderabad)":  "ap-south-2",
		"Europe (Spain)":            "eu-south-2",
		"Europe (Zurich)":           "eu-central-2",
		"Asia Pacific (Melbourne)":  "ap-southeast-4",
		"Israel (Tel Aviv)":         "il-central-1",
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
