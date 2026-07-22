package tools

import (
	"testing"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
)

func TestValidatePaymentOption(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    PaymentOption
		wantErr bool
	}{
		{"all-upfront", "all-upfront", PaymentOptionAllUpfront, false},
		{"partial-upfront", "partial-upfront", PaymentOptionPartialUpfront, false},
		{"no-upfront", "no-upfront", PaymentOptionNoUpfront, false},
		{"empty", "", "", true},
		{"unknown", "some-upfront", "", true},
		{"case sensitive", "All-Upfront", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidatePaymentOption(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidateTermYears(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      int
		want    TermYears
		wantErr bool
	}{
		{"one year", 1, TermOneYear, false},
		{"three year", 3, TermThreeYear, false},
		{"zero", 0, 0, true},
		{"two years unsupported", 2, 0, true},
		{"negative", -1, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateTermYears(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTermYearsRecommendationTerm(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "1yr", TermOneYear.RecommendationTerm())
	assert.Equal(t, "3yr", TermThreeYear.RecommendationTerm())
}

func TestValidateSPType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    SPType
		wantErr bool
	}{
		{"compute", "Compute", SPTypeCompute, false},
		{"ec2instance", "EC2Instance", SPTypeEC2Instance, false},
		{"sagemaker", "SageMaker", SPTypeSageMaker, false},
		{"database", "Database", SPTypeDatabase, false},
		{"lowercase rejected", "compute", "", true},
		{"empty", "", "", true},
		{"unknown", "Storage", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateSPType(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidateAZConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    AZConfig
		wantErr bool
	}{
		{"single-az", "single-az", AZConfigSingleAZ, false},
		{"multi-az", "multi-az", AZConfigMultiAZ, false},
		{"empty refuses to guess", "", "", true},
		{"unknown", "triple-az", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateAZConfig(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidatePlatform(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    ec2types.RIProductDescription
		wantErr bool
	}{
		{"linux", "Linux/UNIX", ec2types.RIProductDescriptionLinuxUnix, false},
		{"linux vpc", "Linux/UNIX (Amazon VPC)", ec2types.RIProductDescriptionLinuxUnixAmazonVpc, false},
		{"windows", "Windows", ec2types.RIProductDescriptionWindows, false},
		{"windows vpc", "Windows (Amazon VPC)", ec2types.RIProductDescriptionWindowsAmazonVpc, false},
		{"lowercase rejected", "linux", "", true},
		{"empty", "", "", true},
		{"unknown os", "MacOS", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidatePlatform(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidateTenancy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    Tenancy
		wantErr bool
	}{
		{"default", "default", TenancyDefault, false},
		{"dedicated", "dedicated", TenancyDedicated, false},
		{"empty", "", "", true},
		{"host unsupported", "host", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateTenancy(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidateCacheEngine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    CacheEngine
		wantErr bool
	}{
		{"redis", "redis", CacheEngineRedis, false},
		{"memcached", "memcached", CacheEngineMemcached, false},
		{"empty", "", "", true},
		{"unknown", "postgres", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateCacheEngine(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidateScope(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    Scope
		wantErr bool
	}{
		{"region", "region", ScopeRegion, false},
		{"availability-zone", "availability-zone", ScopeAvailabilityZone, false},
		{"empty", "", "", true},
		{"sdk casing rejected", "Region", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateScope(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
