package recommendations

import (
	"fmt"
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

// convertPaymentOption converts payment option string to AWS type.
//
// Deprecated: this function silently defaults to NoUpfront for unrecognized
// values and is retained only for the legacy RI recommendation-fetch path in
// client.go (owned by #865/#1075). New callers must use convertPaymentOptionE
// and propagate the error. See open-questions/fix-aws-converters.md OQ-1.
func convertPaymentOption(option string) types.PaymentOption {
	v, _ := convertPaymentOptionE(option)
	return v
}

// convertPaymentOptionE converts a payment option string to the AWS Cost Explorer
// typed enum, returning an error for any value not in the known set.
// Known values: "all-upfront", "partial-upfront", "no-upfront".
func convertPaymentOptionE(option string) (types.PaymentOption, error) {
	switch option {
	case "all-upfront":
		return types.PaymentOptionAllUpfront, nil
	case "partial-upfront":
		return types.PaymentOptionPartialUpfront, nil
	case "no-upfront":
		return types.PaymentOptionNoUpfront, nil
	default:
		return "", fmt.Errorf("unsupported payment option %q: must be one of all-upfront, partial-upfront, no-upfront", option)
	}
}

// convertTermInYearsE converts a term string to the AWS Cost Explorer typed enum,
// returning an error for any value not in the known set.
// Known values: "1yr", "1", "3yr", "3".
func convertTermInYearsE(term string) (types.TermInYears, error) {
	switch term {
	case "1yr", "1":
		return types.TermInYearsOneYear, nil
	case "3yr", "3":
		return types.TermInYearsThreeYears, nil
	default:
		return "", fmt.Errorf("unsupported term %q: must be one of 1yr, 1, 3yr, 3", term)
	}
}

// convertTermInYears converts term string to AWS type.
//
// Deprecated: this function silently defaults to OneYear for unrecognized
// values and is retained only for the legacy RI recommendation-fetch path in
// client.go (owned by #865/#1075). New callers must use convertTermInYearsE
// and propagate the error. See open-questions/fix-aws-converters.md OQ-1.
func convertTermInYears(term string) types.TermInYears {
	v, _ := convertTermInYearsE(term)
	return v
}

// convertLookbackPeriodE converts a lookback period string to the AWS Cost Explorer
// typed enum, returning an error for any value not in the known set.
// Known values: "7d", "7", "30d", "30", "60d", "60".
func convertLookbackPeriodE(period string) (types.LookbackPeriodInDays, error) {
	switch period {
	case "7d", "7":
		return types.LookbackPeriodInDaysSevenDays, nil
	case "30d", "30":
		return types.LookbackPeriodInDaysThirtyDays, nil
	case "60d", "60":
		return types.LookbackPeriodInDaysSixtyDays, nil
	default:
		return "", fmt.Errorf("unsupported lookback period %q: must be one of 7d, 30d, 60d", period)
	}
}

// convertLookbackPeriod converts lookback period string to AWS type.
//
// Deprecated: this function silently defaults to SevenDays for unrecognized
// values and is retained only for the legacy RI recommendation-fetch path in
// client.go (owned by #865/#1075). New callers must use convertLookbackPeriodE
// and propagate the error. See open-questions/fix-aws-converters.md OQ-1.
func convertLookbackPeriod(period string) types.LookbackPeriodInDays {
	v, _ := convertLookbackPeriodE(period)
	return v
}

// convertSavingsPlansPaymentOption converts payment option for Savings Plans,
// returning an error for unrecognized values. This is the fail-loud variant
// used by the SP recommendation path.
func convertSavingsPlansPaymentOption(option string) (types.PaymentOption, error) {
	return convertPaymentOptionE(option)
}

// convertSavingsPlansTermInYears converts term for Savings Plans,
// returning an error for unrecognized values.
func convertSavingsPlansTermInYears(term string) (types.TermInYears, error) {
	return convertTermInYearsE(term)
}

// convertSavingsPlansLookbackPeriod converts lookback period for Savings Plans,
// returning an error for unrecognized values.
func convertSavingsPlansLookbackPeriod(period string) (types.LookbackPeriodInDays, error) {
	return convertLookbackPeriodE(period)
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
