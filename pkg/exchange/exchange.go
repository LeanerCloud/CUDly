// Package exchange provides AWS Convertible Reserved Instance exchange operations.
// It wraps the EC2 GetReservedInstancesExchangeQuote and AcceptReservedInstancesExchangeQuote
// APIs with input validation and spend-cap guardrails.
package exchange

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// ExchangeQuoteSummary is a small, stable summary we can log/guard on.
type ExchangeQuoteSummary struct {
	IsValidExchange         bool
	ValidationFailureReason string
	CurrencyCode            string

	PaymentDueRaw    string   // as returned by AWS (string)
	PaymentDueUSD    *big.Rat `json:"-"`                         // internal use only, not serializable
	PaymentDueUSDStr string   `json:"payment_due_usd,omitempty"` // parsed decimal for JSON consumers

	OutputReservedInstancesExp string // formatted date string (YYYY-MM-DD), empty if not set

	// Rollups (strings in AWS response)
	SourceHourlyPriceRaw      string
	SourceRemainingUpfrontRaw string
	SourceRemainingTotalRaw   string
	TargetHourlyPriceRaw      string
	TargetRemainingUpfrontRaw string
	TargetRemainingTotalRaw   string
}

// ParseDecimalRat parses AWS decimal strings like "123.45" or "-0.018000" into big.Rat.
func ParseDecimalRat(s string) (*big.Rat, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty decimal string")
	}
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return nil, fmt.Errorf("invalid decimal: %q", s)
	}
	return r, nil
}

// TargetConfig is a single target offering in an exchange: a Convertible
// RI offering to buy and how many of it. AWS accepts multiple targets
// per exchange (AcceptReservedInstancesExchangeQuote is all-or-nothing
// across the whole TargetConfigurations slice), which lets callers
// redistribute RI value across several shapes in one atomic operation.
type TargetConfig struct {
	OfferingID string
	Count      int32
}

// ExchangeQuoteRequest holds parameters for requesting an exchange quote.
type ExchangeQuoteRequest struct {
	Region          string
	ExpectedAccount string // optional safety check
	ReservedIDs     []string

	// Targets is the preferred path for multi-target exchanges. When
	// non-empty, TargetOfferingID / TargetCount are ignored.
	Targets []TargetConfig

	// TargetOfferingID + TargetCount are the legacy single-target
	// fields, retained so pre-existing callers (HTTP handlers, sanity
	// tests, serialized PurchasePlans) don't need a flag-day change.
	// New code should populate Targets instead.
	TargetOfferingID string
	TargetCount      int32

	// DryRun here uses the AWS API DryRun parameter (permission check).
	// The quote call itself never performs an exchange.
	DryRun bool
}

// ExchangeExecuteRequest holds parameters for executing an exchange.
type ExchangeExecuteRequest struct {
	Region          string
	ExpectedAccount string // optional safety check
	ReservedIDs     []string

	// Targets is the preferred path for multi-target exchanges. When
	// non-empty, TargetOfferingID / TargetCount are ignored.
	Targets []TargetConfig

	// Legacy single-target alias. Prefer Targets for new code.
	TargetOfferingID string
	TargetCount      int32

	// Guardrail: require PaymentDue <= MaxPaymentDueUSD to execute.
	// AWS returns a single aggregated PaymentDue across all targets,
	// so for multi-target requests this guardrail naturally becomes a
	// total cap rather than a per-target cap.
	// If nil, execution is refused.
	MaxPaymentDueUSD *big.Rat
}

// targetConfigs returns the ec2types slice to pass to the EC2 API.
// Prefers r.Targets when set; otherwise falls back to the legacy
// TargetOfferingID / TargetCount singleton. Empty or negative Count
// values default to 1 so a caller that forgets to populate Count still
// gets a usable request — matches the prior singleton behaviour.
func (r *ExchangeQuoteRequest) targetConfigs() []ec2types.TargetConfigurationRequest {
	return buildTargetConfigs(r.Targets, r.TargetOfferingID, r.TargetCount)
}

func (r *ExchangeExecuteRequest) targetConfigs() []ec2types.TargetConfigurationRequest {
	return buildTargetConfigs(r.Targets, r.TargetOfferingID, r.TargetCount)
}

func buildTargetConfigs(targets []TargetConfig, legacyOfferingID string, legacyCount int32) []ec2types.TargetConfigurationRequest {
	if len(targets) > 0 {
		out := make([]ec2types.TargetConfigurationRequest, 0, len(targets))
		for _, t := range targets {
			count := t.Count
			if count <= 0 {
				count = 1
			}
			out = append(out, ec2types.TargetConfigurationRequest{
				OfferingId:    sdkaws.String(t.OfferingID),
				InstanceCount: sdkaws.Int32(count),
			})
		}
		return out
	}
	count := legacyCount
	if count <= 0 {
		count = 1
	}
	return []ec2types.TargetConfigurationRequest{{
		OfferingId:    sdkaws.String(legacyOfferingID),
		InstanceCount: sdkaws.Int32(count),
	}}
}

// validateTargets returns a non-nil error if neither the Targets slice
// nor the legacy singleton fields carry a usable target offering.
func validateTargets(targets []TargetConfig, legacyOfferingID string) error {
	if len(targets) > 0 {
		for i, t := range targets {
			if strings.TrimSpace(t.OfferingID) == "" {
				return fmt.Errorf("targets[%d].offering_id must be non-empty", i)
			}
		}
		return nil
	}
	if strings.TrimSpace(legacyOfferingID) == "" {
		return fmt.Errorf("must provide target offering ID (either targets[] or target_offering_id)")
	}
	return nil
}

// EC2ExchangeAPI defines the EC2 API methods used by exchange operations.
// Satisfied by *ec2.Client; accept this interface to enable testing without
// real AWS credentials.
type EC2ExchangeAPI interface {
	GetReservedInstancesExchangeQuote(ctx context.Context, params *ec2.GetReservedInstancesExchangeQuoteInput, optFns ...func(*ec2.Options)) (*ec2.GetReservedInstancesExchangeQuoteOutput, error)
	AcceptReservedInstancesExchangeQuote(ctx context.Context, params *ec2.AcceptReservedInstancesExchangeQuoteInput, optFns ...func(*ec2.Options)) (*ec2.AcceptReservedInstancesExchangeQuoteOutput, error)
}

// ExchangeClient wraps an EC2ExchangeAPI for dependency-injected exchange
// operations. Use NewExchangeClient to construct one.
type ExchangeClient struct {
	ec2 EC2ExchangeAPI
}

// NewExchangeClient creates an ExchangeClient from an AWS config.
func NewExchangeClient(cfg sdkaws.Config) *ExchangeClient {
	return &ExchangeClient{ec2: ec2.NewFromConfig(cfg)}
}

// NewExchangeClientFromAPI creates an ExchangeClient from an existing
// EC2ExchangeAPI implementation (useful for testing).
func NewExchangeClientFromAPI(api EC2ExchangeAPI) *ExchangeClient {
	return &ExchangeClient{ec2: api}
}

// GetQuote retrieves an exchange quote using the injected EC2 client.
func (c *ExchangeClient) GetQuote(ctx context.Context, req ExchangeQuoteRequest) (*ExchangeQuoteSummary, error) {
	return getQuoteWithAPI(ctx, c.ec2, req)
}

// Execute performs a convertible RI exchange with a spend-cap guardrail
// using the injected EC2 client.
func (c *ExchangeClient) Execute(ctx context.Context, req ExchangeExecuteRequest) (string, *ExchangeQuoteSummary, error) {
	return executeWithAPI(ctx, c.ec2, req)
}

func loadCfg(ctx context.Context, region string) (sdkaws.Config, error) {
	if region == "" {
		region = "us-east-1"
	}
	return config.LoadDefaultConfig(ctx, config.WithRegion(region))
}

func assertAccount(ctx context.Context, cfg sdkaws.Config, expected string) error {
	if expected == "" {
		return nil
	}
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}
	if sdkaws.ToString(out.Account) != expected {
		return fmt.Errorf("unexpected AWS account: got %s want %s", sdkaws.ToString(out.Account), expected)
	}
	return nil
}

// GetExchangeQuote retrieves an exchange quote from the EC2 API.
func GetExchangeQuote(ctx context.Context, req ExchangeQuoteRequest) (*ExchangeQuoteSummary, error) {
	cfg, err := loadCfg(ctx, req.Region)
	if err != nil {
		return nil, err
	}
	if err := assertAccount(ctx, cfg, req.ExpectedAccount); err != nil {
		return nil, err
	}
	return getQuoteWithAPI(ctx, ec2.NewFromConfig(cfg), req)
}

// getQuoteWithAPI performs the quote call using an EC2ExchangeAPI,
// allowing ExecuteExchange to reuse the same client for both quote and accept.
func getQuoteWithAPI(ctx context.Context, client EC2ExchangeAPI, req ExchangeQuoteRequest) (*ExchangeQuoteSummary, error) {
	if len(req.ReservedIDs) == 0 {
		return nil, fmt.Errorf("must provide at least one reserved instance ID")
	}
	if err := validateTargets(req.Targets, req.TargetOfferingID); err != nil {
		return nil, err
	}

	in := &ec2.GetReservedInstancesExchangeQuoteInput{
		DryRun:               sdkaws.Bool(req.DryRun),
		ReservedInstanceIds:  req.ReservedIDs,
		TargetConfigurations: req.targetConfigs(),
	}

	out, err := client.GetReservedInstancesExchangeQuote(ctx, in)
	if err != nil {
		return nil, err
	}

	s := &ExchangeQuoteSummary{
		IsValidExchange:         sdkaws.ToBool(out.IsValidExchange),
		ValidationFailureReason: sdkaws.ToString(out.ValidationFailureReason),
		CurrencyCode:            sdkaws.ToString(out.CurrencyCode),
		PaymentDueRaw:           sdkaws.ToString(out.PaymentDue),
	}

	if out.OutputReservedInstancesWillExpireAt != nil {
		s.OutputReservedInstancesExp = out.OutputReservedInstancesWillExpireAt.Format("2006-01-02")
	}

	if s.PaymentDueRaw != "" {
		p, perr := ParseDecimalRat(s.PaymentDueRaw)
		if perr != nil {
			return nil, fmt.Errorf("quote returned invalid paymentDue %q: %w", s.PaymentDueRaw, perr)
		}
		s.PaymentDueUSD = p
		s.PaymentDueUSDStr = p.FloatString(6)
	}

	// Rollups (optional but useful for debugging)
	if out.ReservedInstanceValueRollup != nil {
		s.SourceHourlyPriceRaw = sdkaws.ToString(out.ReservedInstanceValueRollup.HourlyPrice)
		s.SourceRemainingUpfrontRaw = sdkaws.ToString(out.ReservedInstanceValueRollup.RemainingUpfrontValue)
		s.SourceRemainingTotalRaw = sdkaws.ToString(out.ReservedInstanceValueRollup.RemainingTotalValue)
	}
	if out.TargetConfigurationValueRollup != nil {
		s.TargetHourlyPriceRaw = sdkaws.ToString(out.TargetConfigurationValueRollup.HourlyPrice)
		s.TargetRemainingUpfrontRaw = sdkaws.ToString(out.TargetConfigurationValueRollup.RemainingUpfrontValue)
		s.TargetRemainingTotalRaw = sdkaws.ToString(out.TargetConfigurationValueRollup.RemainingTotalValue)
	}

	return s, nil
}

// ExecuteExchange performs a convertible RI exchange with a spend-cap guardrail.
// This is a convenience wrapper that creates its own AWS client from default config.
func ExecuteExchange(ctx context.Context, req ExchangeExecuteRequest) (exchangeID string, quote *ExchangeQuoteSummary, err error) {
	if req.MaxPaymentDueUSD == nil {
		return "", nil, fmt.Errorf("refusing to execute without max-payment-due-usd guardrail")
	}

	cfg, err := loadCfg(ctx, req.Region)
	if err != nil {
		return "", nil, err
	}
	if err := assertAccount(ctx, cfg, req.ExpectedAccount); err != nil {
		return "", nil, err
	}

	return executeWithAPI(ctx, ec2.NewFromConfig(cfg), req)
}

// executeWithAPI performs the exchange using an injected EC2ExchangeAPI.
func executeWithAPI(ctx context.Context, client EC2ExchangeAPI, req ExchangeExecuteRequest) (string, *ExchangeQuoteSummary, error) {
	if req.MaxPaymentDueUSD == nil {
		return "", nil, fmt.Errorf("refusing to execute without max-payment-due-usd guardrail")
	}

	q, err := getQuoteWithAPI(ctx, client, ExchangeQuoteRequest{
		Region:           req.Region,
		ExpectedAccount:  req.ExpectedAccount,
		ReservedIDs:      req.ReservedIDs,
		Targets:          req.Targets,
		TargetOfferingID: req.TargetOfferingID,
		TargetCount:      req.TargetCount,
		DryRun:           false,
	})
	if err != nil {
		return "", nil, err
	}

	if !q.IsValidExchange {
		return "", q, fmt.Errorf("exchange is not valid: %s", q.ValidationFailureReason)
	}

	// AWS may return an empty PaymentDue for zero-cost exchanges (e.g., same-RI-type
	// conversions). Treat nil as zero cost so valid zero-cost exchanges are not refused.
	paymentDue := q.PaymentDueUSD
	if paymentDue == nil {
		paymentDue = new(big.Rat)
	}

	// paymentDue > max => refuse
	if paymentDue.Cmp(req.MaxPaymentDueUSD) == 1 {
		return "", q, fmt.Errorf("paymentDue %s exceeds max %s", paymentDue.FloatString(2), req.MaxPaymentDueUSD.FloatString(2))
	}

	// NOTE: The AWS RI exchange API has no atomic quote+accept operation. The
	// AcceptReservedInstancesExchangeQuote call re-evaluates pricing server-side,
	// so in rare cases the actual payment could differ from the quoted amount
	// (e.g., due to RI valuation changes between GetQuote and Accept).
	// This is a known API limitation and not something we can prevent here.
	out, err := client.AcceptReservedInstancesExchangeQuote(ctx, &ec2.AcceptReservedInstancesExchangeQuoteInput{
		ReservedInstanceIds:  req.ReservedIDs,
		TargetConfigurations: req.targetConfigs(),
	})
	if err != nil {
		return "", q, err
	}

	return sdkaws.ToString(out.ExchangeId), q, nil
}
