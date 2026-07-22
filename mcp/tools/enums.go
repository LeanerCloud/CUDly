// Package tools implements the individual MCP tool handlers exposed by the
// CUDly MCP server (mcp/server.go), plus the shared validation and purchase
// harness they all build on.
package tools

import (
	"fmt"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// PaymentOption is the AWS/Azure/GCP-agnostic reserved-capacity payment
// schedule. It is validated at the MCP tool boundary before being copied
// onto common.Recommendation.PaymentOption (which stays a bare string there
// for backward compatibility with existing CSV/DB rows -- see
// pkg/common/types.go) so a caller can never smuggle an unrecognised payment
// term into a purchase.
type PaymentOption string

const (
	PaymentOptionAllUpfront     PaymentOption = "all-upfront"
	PaymentOptionPartialUpfront PaymentOption = "partial-upfront"
	PaymentOptionNoUpfront      PaymentOption = "no-upfront"
)

// ValidatePaymentOption returns the typed PaymentOption for s, or an explicit
// error when s is not one of the three allowed values. There is no default:
// an empty or unknown payment option is always an error, never silently
// coerced to a fallback term (feedback_no_silent_fallbacks).
func ValidatePaymentOption(s string) (PaymentOption, error) {
	switch PaymentOption(s) {
	case PaymentOptionAllUpfront, PaymentOptionPartialUpfront, PaymentOptionNoUpfront:
		return PaymentOption(s), nil
	default:
		return "", fmt.Errorf("invalid payment_option %q: must be one of %s, %s, %s",
			s, PaymentOptionAllUpfront, PaymentOptionPartialUpfront, PaymentOptionNoUpfront)
	}
}

// TermYears is the reserved-capacity commitment length, in years.
type TermYears int

const (
	TermOneYear   TermYears = 1
	TermThreeYear TermYears = 3
)

// ValidateTermYears returns the typed TermYears for n, or an explicit error
// when n is not 1 or 3 (the only terms AWS/Azure/GCP reserved-capacity
// products offer).
func ValidateTermYears(n int) (TermYears, error) {
	switch TermYears(n) {
	case TermOneYear, TermThreeYear:
		return TermYears(n), nil
	default:
		return 0, fmt.Errorf("invalid term_years %d: must be %d or %d", n, TermOneYear, TermThreeYear)
	}
}

// RecommendationTerm renders t in the "1yr"/"3yr" vocabulary that
// common.Recommendation.Term and the provider clients expect.
func (t TermYears) RecommendationTerm() string {
	return fmt.Sprintf("%dyr", int(t))
}

// SPType is the AWS Savings Plans product family (--include-sp-types in the
// CLI, cmd/main.go:112).
type SPType string

const (
	SPTypeCompute     SPType = "Compute"
	SPTypeEC2Instance SPType = "EC2Instance"
	SPTypeSageMaker   SPType = "SageMaker"
	SPTypeDatabase    SPType = "Database"
)

// ValidateSPType returns the typed SPType for s, or an explicit error when s
// is not one of the four AWS Savings Plans product families.
func ValidateSPType(s string) (SPType, error) {
	switch SPType(s) {
	case SPTypeCompute, SPTypeEC2Instance, SPTypeSageMaker, SPTypeDatabase:
		return SPType(s), nil
	default:
		return "", fmt.Errorf("invalid sp_type %q: must be one of %s, %s, %s, %s",
			s, SPTypeCompute, SPTypeEC2Instance, SPTypeSageMaker, SPTypeDatabase)
	}
}

// AZConfig is the RDS deployment topology (single-AZ vs multi-AZ), which
// carries a different price and offering catalogue per
// providers/aws/services/rds/client.go:314-322.
type AZConfig string

const (
	AZConfigSingleAZ AZConfig = "single-az"
	AZConfigMultiAZ  AZConfig = "multi-az"
)

// ValidateAZConfig returns the typed AZConfig for s, or an explicit error
// when s is not single-az or multi-az. RDS's own client refuses to guess this
// value (see the comment at providers/aws/services/rds/client.go:306-322), so
// the MCP boundary must not default it either.
func ValidateAZConfig(s string) (AZConfig, error) {
	switch AZConfig(s) {
	case AZConfigSingleAZ, AZConfigMultiAZ:
		return AZConfig(s), nil
	default:
		return "", fmt.Errorf("invalid az_config %q: must be %s or %s", s, AZConfigSingleAZ, AZConfigMultiAZ)
	}
}

// ValidatePlatform returns the AWS SDK's own ec2types.RIProductDescription
// enum member for s, or an explicit error when s does not match one of the
// four values that DescribeReservedInstancesOfferings accepts as
// ProductDescription (providers/aws/services/ec2/client.go:419). Reusing the
// SDK's own enum constants -- rather than inventing a "linux"/"windows"
// vocabulary -- means an outbound offering lookup can never carry a bare
// string literal that drifts from what the SDK actually recognises
// (feedback_sdk_enum_string_literals).
func ValidatePlatform(s string) (ec2types.RIProductDescription, error) {
	switch ec2types.RIProductDescription(s) {
	case ec2types.RIProductDescriptionLinuxUnix,
		ec2types.RIProductDescriptionLinuxUnixAmazonVpc,
		ec2types.RIProductDescriptionWindows,
		ec2types.RIProductDescriptionWindowsAmazonVpc:
		return ec2types.RIProductDescription(s), nil
	default:
		return "", fmt.Errorf("invalid platform %q: must be one of %s, %s, %s, %s", s,
			ec2types.RIProductDescriptionLinuxUnix, ec2types.RIProductDescriptionLinuxUnixAmazonVpc,
			ec2types.RIProductDescriptionWindows, ec2types.RIProductDescriptionWindowsAmazonVpc)
	}
}

// Tenancy is the EC2 RI tenancy dimension. Values match ec2types.Tenancy
// (providers/aws/services/ec2/client.go:309-318 canonicalises them further,
// but "default"/"dedicated" already pass through unchanged).
type Tenancy string

const (
	TenancyDefault   Tenancy = Tenancy(ec2types.TenancyDefault)
	TenancyDedicated Tenancy = Tenancy(ec2types.TenancyDedicated)
)

// ValidateTenancy returns the typed Tenancy for s, or an explicit error when
// s is neither default nor dedicated.
func ValidateTenancy(s string) (Tenancy, error) {
	switch Tenancy(s) {
	case TenancyDefault, TenancyDedicated:
		return Tenancy(s), nil
	default:
		return "", fmt.Errorf("invalid tenancy %q: must be %s or %s", s, TenancyDefault, TenancyDedicated)
	}
}

// Scope is the EC2 RI applicability dimension. Values are the lowercase,
// hyphenated form that providers/aws/services/ec2/client.go:330-339
// (canonicalizeEC2Scope) recognises and normalises to the SDK's
// ec2types.Scope casing ("Region" / "Availability Zone").
type Scope string

const (
	ScopeRegion           Scope = "region"
	ScopeAvailabilityZone Scope = "availability-zone"
)

// ValidateScope returns the typed Scope for s, or an explicit error when s is
// neither region nor availability-zone.
func ValidateScope(s string) (Scope, error) {
	switch Scope(s) {
	case ScopeRegion, ScopeAvailabilityZone:
		return Scope(s), nil
	default:
		return "", fmt.Errorf("invalid scope %q: must be %s or %s", s, ScopeRegion, ScopeAvailabilityZone)
	}
}
