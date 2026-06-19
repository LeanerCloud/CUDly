package recommendations

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestGetServiceStringForCostExplorer(t *testing.T) {
	tests := []struct {
		name     string
		service  common.ServiceType
		expected string
	}{
		{
			name:     "RDS service",
			service:  common.ServiceRDS,
			expected: "Amazon Relational Database Service",
		},
		{
			name:     "RelationalDB service",
			service:  common.ServiceRelationalDB,
			expected: "Amazon Relational Database Service",
		},
		{
			name:     "ElastiCache service",
			service:  common.ServiceElastiCache,
			expected: "Amazon ElastiCache",
		},
		{
			name:     "Cache service",
			service:  common.ServiceCache,
			expected: "Amazon ElastiCache",
		},
		{
			name:     "EC2 service",
			service:  common.ServiceEC2,
			expected: "Amazon Elastic Compute Cloud - Compute",
		},
		{
			name:     "Compute service",
			service:  common.ServiceCompute,
			expected: "Amazon Elastic Compute Cloud - Compute",
		},
		{
			name:     "OpenSearch service",
			service:  common.ServiceOpenSearch,
			expected: "Amazon OpenSearch Service",
		},
		{
			name:     "Search service",
			service:  common.ServiceSearch,
			expected: "Amazon OpenSearch Service",
		},
		{
			name:     "Redshift service",
			service:  common.ServiceRedshift,
			expected: "Amazon Redshift",
		},
		{
			name:     "DataWarehouse service",
			service:  common.ServiceDataWarehouse,
			expected: "Amazon Redshift",
		},
		{
			name:     "MemoryDB service",
			service:  common.ServiceMemoryDB,
			expected: "Amazon MemoryDB Service",
		},
		{
			name:     "Unknown service returns as-is",
			service:  "unknown-service",
			expected: "unknown-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getServiceStringForCostExplorer(tt.service)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertPaymentOption(t *testing.T) {
	// convertPaymentOption is the legacy wrapper that silently defaults to NoUpfront
	// for unknown values (used by client.go RI path, owned by #865/#1075).
	// This test documents that silent-default behavior; new callers should use
	// convertPaymentOptionE which returns an error on unrecognized values.
	tests := []struct {
		name     string
		option   string
		expected types.PaymentOption
	}{
		{
			name:     "All upfront",
			option:   "all-upfront",
			expected: types.PaymentOptionAllUpfront,
		},
		{
			name:     "Partial upfront",
			option:   "partial-upfront",
			expected: types.PaymentOptionPartialUpfront,
		},
		{
			name:     "No upfront",
			option:   "no-upfront",
			expected: types.PaymentOptionNoUpfront,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertPaymentOption(tt.option)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestConvertPaymentOptionE_FailLoud is the regression test for H3:
// convertPaymentOptionE must return an error on any unrecognized payment option
// instead of silently substituting NoUpfront (the old behavior of the
// convertPaymentOption default branch). Callers on the SP recommendation path
// use this erroring variant so a typo or new/renamed option is caught
// before the wrong recs are queried.
func TestConvertPaymentOptionE_FailLoud(t *testing.T) {
	tests := []struct {
		name        string
		option      string
		expected    types.PaymentOption
		expectError bool
	}{
		{"All upfront", "all-upfront", types.PaymentOptionAllUpfront, false},
		{"Partial upfront", "partial-upfront", types.PaymentOptionPartialUpfront, false},
		{"No upfront", "no-upfront", types.PaymentOptionNoUpfront, false},
		// These must error, not default to NoUpfront (H3 regression guard):
		{"Unknown option errors", "unknown", "", true},
		{"Empty string errors", "", "", true},
		{"Mixed case errors", "All-Upfront", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertPaymentOptionE(tt.option)
			if tt.expectError {
				assert.Error(t, err, "convertPaymentOptionE(%q) must error", tt.option)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// TestConvertTermInYearsE_FailLoud is the regression test for L1:
// convertTermInYearsE must error on unrecognized terms rather than silently
// defaulting to OneYear.
func TestConvertTermInYearsE_FailLoud(t *testing.T) {
	tests := []struct {
		name        string
		term        string
		expected    types.TermInYears
		expectError bool
	}{
		{"1yr", "1yr", types.TermInYearsOneYear, false},
		{"1 numeric", "1", types.TermInYearsOneYear, false},
		{"3yr", "3yr", types.TermInYearsThreeYears, false},
		{"3 numeric", "3", types.TermInYearsThreeYears, false},
		// These must error, not default to OneYear (L1 regression guard):
		{"Unknown term errors", "unknown", "", true},
		{"Empty string errors", "", "", true},
		{"2yr errors", "2yr", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertTermInYearsE(tt.term)
			if tt.expectError {
				assert.Error(t, err, "convertTermInYearsE(%q) must error", tt.term)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// TestConvertLookbackPeriodE_FailLoud is the regression test for L2:
// convertLookbackPeriodE must error on unrecognized periods rather than
// silently defaulting to SevenDays.
func TestConvertLookbackPeriodE_FailLoud(t *testing.T) {
	tests := []struct {
		name        string
		period      string
		expected    types.LookbackPeriodInDays
		expectError bool
	}{
		{"7d", "7d", types.LookbackPeriodInDaysSevenDays, false},
		{"7 numeric", "7", types.LookbackPeriodInDaysSevenDays, false},
		{"30d", "30d", types.LookbackPeriodInDaysThirtyDays, false},
		{"30 numeric", "30", types.LookbackPeriodInDaysThirtyDays, false},
		{"60d", "60d", types.LookbackPeriodInDaysSixtyDays, false},
		{"60 numeric", "60", types.LookbackPeriodInDaysSixtyDays, false},
		// These must error, not default to SevenDays (L2 regression guard):
		{"Unknown period errors", "unknown", "", true},
		{"Empty string errors", "", "", true},
		{"90d errors", "90d", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertLookbackPeriodE(tt.period)
			if tt.expectError {
				assert.Error(t, err, "convertLookbackPeriodE(%q) must error", tt.period)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestConvertTermInYears(t *testing.T) {
	// convertTermInYears is the legacy wrapper used by client.go (RI path);
	// it silently returns OneYear for unrecognized values. This test covers the
	// valid cases only; the fail-loud path is tested by TestConvertTermInYearsE_FailLoud.
	tests := []struct {
		name     string
		term     string
		expected types.TermInYears
	}{
		{
			name:     "3 year with yr suffix",
			term:     "3yr",
			expected: types.TermInYearsThreeYears,
		},
		{
			name:     "3 year numeric only",
			term:     "3",
			expected: types.TermInYearsThreeYears,
		},
		{
			name:     "1 year with yr suffix",
			term:     "1yr",
			expected: types.TermInYearsOneYear,
		},
		{
			name:     "1 year numeric only",
			term:     "1",
			expected: types.TermInYearsOneYear,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertTermInYears(tt.term)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertLookbackPeriod(t *testing.T) {
	// convertLookbackPeriod is the legacy wrapper used by client.go (RI path);
	// it silently returns SevenDays for unrecognized values. This test covers valid
	// cases only; the fail-loud path is tested by TestConvertLookbackPeriodE_FailLoud.
	tests := []struct {
		name     string
		period   string
		expected types.LookbackPeriodInDays
	}{
		{
			name:     "7 days with d suffix",
			period:   "7d",
			expected: types.LookbackPeriodInDaysSevenDays,
		},
		{
			name:     "7 days numeric only",
			period:   "7",
			expected: types.LookbackPeriodInDaysSevenDays,
		},
		{
			name:     "30 days with d suffix",
			period:   "30d",
			expected: types.LookbackPeriodInDaysThirtyDays,
		},
		{
			name:     "30 days numeric only",
			period:   "30",
			expected: types.LookbackPeriodInDaysThirtyDays,
		},
		{
			name:     "60 days with d suffix",
			period:   "60d",
			expected: types.LookbackPeriodInDaysSixtyDays,
		},
		{
			name:     "60 days numeric only",
			period:   "60",
			expected: types.LookbackPeriodInDaysSixtyDays,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertLookbackPeriod(tt.period)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertSavingsPlansPaymentOption(t *testing.T) {
	// SP wrappers now return (value, error); valid options must succeed.
	result, err := convertSavingsPlansPaymentOption("all-upfront")
	assert.NoError(t, err)
	assert.Equal(t, types.PaymentOptionAllUpfront, result)

	result, err = convertSavingsPlansPaymentOption("partial-upfront")
	assert.NoError(t, err)
	assert.Equal(t, types.PaymentOptionPartialUpfront, result)

	// Unknown option must error (not silently default).
	_, err = convertSavingsPlansPaymentOption("bogus")
	assert.Error(t, err, "convertSavingsPlansPaymentOption(bogus) must error")
}

func TestConvertSavingsPlansTermInYears(t *testing.T) {
	result, err := convertSavingsPlansTermInYears("3yr")
	assert.NoError(t, err)
	assert.Equal(t, types.TermInYearsThreeYears, result)

	result, err = convertSavingsPlansTermInYears("1yr")
	assert.NoError(t, err)
	assert.Equal(t, types.TermInYearsOneYear, result)

	// Unknown term must error.
	_, err = convertSavingsPlansTermInYears("bogus")
	assert.Error(t, err, "convertSavingsPlansTermInYears(bogus) must error")
}

func TestConvertSavingsPlansLookbackPeriod(t *testing.T) {
	result, err := convertSavingsPlansLookbackPeriod("7d")
	assert.NoError(t, err)
	assert.Equal(t, types.LookbackPeriodInDaysSevenDays, result)

	result, err = convertSavingsPlansLookbackPeriod("30d")
	assert.NoError(t, err)
	assert.Equal(t, types.LookbackPeriodInDaysThirtyDays, result)

	// Unknown period must error.
	_, err = convertSavingsPlansLookbackPeriod("bogus")
	assert.Error(t, err, "convertSavingsPlansLookbackPeriod(bogus) must error")
}

func TestNormalizeRegionName(t *testing.T) {
	tests := []struct {
		name     string
		region   string
		expected string
	}{
		{
			name:     "US East N. Virginia",
			region:   "US East (N. Virginia)",
			expected: "us-east-1",
		},
		{
			name:     "US East Ohio",
			region:   "US East (Ohio)",
			expected: "us-east-2",
		},
		{
			name:     "US West N. California",
			region:   "US West (N. California)",
			expected: "us-west-1",
		},
		{
			name:     "US West Oregon",
			region:   "US West (Oregon)",
			expected: "us-west-2",
		},
		{
			name:     "EU Ireland",
			region:   "EU (Ireland)",
			expected: "eu-west-1",
		},
		{
			name:     "EU Frankfurt",
			region:   "EU (Frankfurt)",
			expected: "eu-central-1",
		},
		{
			name:     "EU London",
			region:   "EU (London)",
			expected: "eu-west-2",
		},
		{
			name:     "EU Paris",
			region:   "EU (Paris)",
			expected: "eu-west-3",
		},
		{
			name:     "EU Stockholm",
			region:   "EU (Stockholm)",
			expected: "eu-north-1",
		},
		{
			name:     "Asia Pacific Singapore",
			region:   "Asia Pacific (Singapore)",
			expected: "ap-southeast-1",
		},
		{
			name:     "Asia Pacific Sydney",
			region:   "Asia Pacific (Sydney)",
			expected: "ap-southeast-2",
		},
		{
			name:     "Asia Pacific Tokyo",
			region:   "Asia Pacific (Tokyo)",
			expected: "ap-northeast-1",
		},
		{
			name:     "Asia Pacific Seoul",
			region:   "Asia Pacific (Seoul)",
			expected: "ap-northeast-2",
		},
		{
			name:     "Asia Pacific Mumbai",
			region:   "Asia Pacific (Mumbai)",
			expected: "ap-south-1",
		},
		{
			name:     "South America Sao Paulo",
			region:   "South America (Sao Paulo)",
			expected: "sa-east-1",
		},
		{
			name:     "Canada Central",
			region:   "Canada (Central)",
			expected: "ca-central-1",
		},
		{
			name:     "Middle East Bahrain",
			region:   "Middle East (Bahrain)",
			expected: "me-south-1",
		},
		{
			name:     "Africa Cape Town",
			region:   "Africa (Cape Town)",
			expected: "af-south-1",
		},
		{
			name:     "Asia Pacific Hong Kong",
			region:   "Asia Pacific (Hong Kong)",
			expected: "ap-east-1",
		},
		{
			name:     "Asia Pacific Osaka",
			region:   "Asia Pacific (Osaka)",
			expected: "ap-northeast-3",
		},
		{
			name:     "Asia Pacific Jakarta",
			region:   "Asia Pacific (Jakarta)",
			expected: "ap-southeast-3",
		},
		{
			name:     "Europe Milan",
			region:   "Europe (Milan)",
			expected: "eu-south-1",
		},
		{
			name:     "Middle East UAE",
			region:   "Middle East (UAE)",
			expected: "me-central-1",
		},
		{
			name:     "Asia Pacific Hyderabad",
			region:   "Asia Pacific (Hyderabad)",
			expected: "ap-south-2",
		},
		{
			name:     "Europe Spain",
			region:   "Europe (Spain)",
			expected: "eu-south-2",
		},
		{
			name:     "Europe Zurich",
			region:   "Europe (Zurich)",
			expected: "eu-central-2",
		},
		{
			name:     "Asia Pacific Melbourne",
			region:   "Asia Pacific (Melbourne)",
			expected: "ap-southeast-4",
		},
		{
			name:     "Israel Tel Aviv",
			region:   "Israel (Tel Aviv)",
			expected: "il-central-1",
		},
		{
			name:     "Already normalized us-east-1",
			region:   "us-east-1",
			expected: "us-east-1",
		},
		{
			name:     "Already normalized eu-west-2",
			region:   "eu-west-2",
			expected: "eu-west-2",
		},
		{
			name:     "Already normalized ap-southeast-1",
			region:   "ap-southeast-1",
			expected: "ap-southeast-1",
		},
		{
			name:     "Already normalized sa-east-1",
			region:   "sa-east-1",
			expected: "sa-east-1",
		},
		{
			name:     "Already normalized ca-central-1",
			region:   "ca-central-1",
			expected: "ca-central-1",
		},
		{
			name:     "Already normalized me-south-1",
			region:   "me-south-1",
			expected: "me-south-1",
		},
		{
			name:     "Already normalized af-south-1",
			region:   "af-south-1",
			expected: "af-south-1",
		},
		{
			name:     "Already normalized il-central-1",
			region:   "il-central-1",
			expected: "il-central-1",
		},
		{
			name:     "Unknown region returns as-is",
			region:   "Unknown Region",
			expected: "Unknown Region",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeRegionName(tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}
