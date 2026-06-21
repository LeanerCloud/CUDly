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

// QuoteSummary is a small, stable summary we can log/guard on.
type QuoteSummary struct {
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

	IsValidExchange bool
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

// QuoteRequest holds parameters for requesting an exchange quote.
type QuoteRequest struct {
	// TargetOfferingID + TargetCount are the legacy single-target
	// fields, retained so pre-existing callers (HTTP handlers, sanity
	// tests, serialized PurchasePlans) don't need a flag-day change.
	// New code should populate Targets instead.
	TargetOfferingID string
	Region           string
	ExpectedAccount  string // optional safety check
	ReservedIDs      []string

	// Targets is the preferred path for multi-target exchanges. When
	// non-empty, TargetOfferingID / TargetCount are ignored.
	Targets     []TargetConfig
	TargetCount int32

	// DryRun here uses the AWS API DryRun parameter (permission check).
	// The quote call itself never performs an exchange.
	DryRun bool
}

// ExecuteRequest holds parameters for executing an exchange.
type ExecuteRequest struct {
	// Guardrail: require PaymentDue <= MaxPaymentDueUSD to execute.
	// AWS returns a single aggregated PaymentDue across all targets,
	// so for multi-target requests this guardrail naturally becomes a
	// total cap rather than a per-target cap.
	// If nil, execution is refused.
	MaxPaymentDueUSD *big.Rat

	// Legacy single-target alias. Prefer Targets for new code.
	TargetOfferingID string
	Region           string
	ExpectedAccount  string // optional safety check
	ReservedIDs      []string

	// Targets is the preferred path for multi-target exchanges. When
	// non-empty, TargetOfferingID / TargetCount are ignored.
	Targets     []TargetConfig
	TargetCount int32
}

// targetConfigs returns the ec2types slice to pass to the EC2 API.
// Prefers r.Targets when set; otherwise falls back to the legacy
// TargetOfferingID / TargetCount singleton.
func (r *QuoteRequest) targetConfigs() []ec2types.TargetConfigurationRequest {
	return buildTargetConfigs(r.Targets, r.TargetOfferingID, r.TargetCount)
}

func (r *ExecuteRequest) targetConfigs() []ec2types.TargetConfigurationRequest {
	return buildTargetConfigs(r.Targets, r.TargetOfferingID, r.TargetCount)
}

func buildTargetConfigs(targets []TargetConfig, legacyOfferingID string, legacyCount int32) []ec2types.TargetConfigurationRequest {
	if len(targets) > 0 {
		out := make([]ec2types.TargetConfigurationRequest, 0, len(targets))
		for _, t := range targets {
			out = append(out, ec2types.TargetConfigurationRequest{
				OfferingId:    sdkaws.String(t.OfferingID),
				InstanceCount: sdkaws.Int32(t.Count),
			})
		}
		return out
	}
	return []ec2types.TargetConfigurationRequest{{
		OfferingId:    sdkaws.String(legacyOfferingID),
		InstanceCount: sdkaws.Int32(legacyCount),
	}}
}

// validateTargets returns a non-nil error if neither the Targets slice
// nor the legacy singleton fields carry a usable target offering, or if
// any target has a non-positive Count.
func validateTargets(targets []TargetConfig, legacyOfferingID string, legacyCount int32) error {
	if len(targets) > 0 {
		for i, t := range targets {
			if strings.TrimSpace(t.OfferingID) == "" {
				return fmt.Errorf("targets[%d].offering_id must be non-empty", i)
			}
			if t.Count <= 0 {
				return fmt.Errorf("targets[%d].count must be >= 1, got %d", i, t.Count)
			}
		}
		return nil
	}
	if strings.TrimSpace(legacyOfferingID) == "" {
		return fmt.Errorf("must provide target offering ID (either targets[] or target_offering_id)")
	}
	if legacyCount <= 0 {
		return fmt.Errorf("target_count must be >= 1, got %d", legacyCount)
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

// Client wraps an EC2ExchangeAPI for dependency-injected exchange
// operations. Use NewClient to construct one.
type Client struct {
	ec2 EC2ExchangeAPI
}

// NewClient creates an Client from an AWS config.
func NewClient(cfg *sdkaws.Config) *Client {
	return &Client{ec2: ec2.NewFromConfig(*cfg)}
}

// NewClientFromAPI creates an Client from an existing
// EC2ExchangeAPI implementation (useful for testing).
func NewClientFromAPI(api EC2ExchangeAPI) *Client {
	return &Client{ec2: api}
}

// GetQuote retrieves an exchange quote using the injected EC2 client.
func (c *Client) GetQuote(ctx context.Context, req *QuoteRequest) (*QuoteSummary, error) {
	return getQuoteWithAPI(ctx, c.ec2, req)
}

// Execute performs a convertible RI exchange with a spend-cap guardrail
// using the injected EC2 client.
func (c *Client) Execute(ctx context.Context, req *ExecuteRequest) (string, *QuoteSummary, error) {
	return executeWithAPI(ctx, c.ec2, req)
}

func loadCfg(ctx context.Context, region string) (sdkaws.Config, error) {
	if strings.TrimSpace(region) == "" {
		return sdkaws.Config{}, fmt.Errorf("region must be specified explicitly; refusing to default to us-east-1 on an RI exchange path")
	}
	return config.LoadDefaultConfig(ctx, config.WithRegion(region))
}

func assertAccount(ctx context.Context, cfg *sdkaws.Config, expected string) error {
	if expected == "" {
		return nil
	}
	out, err := sts.NewFromConfig(*cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}
	if sdkaws.ToString(out.Account) != expected {
		return fmt.Errorf("unexpected AWS account: got %s want %s", sdkaws.ToString(out.Account), expected)
	}
	return nil
}

// GetExchangeQuote retrieves an exchange quote from the EC2 API.
func GetExchangeQuote(ctx context.Context, req *QuoteRequest) (*QuoteSummary, error) {
	cfg, err := loadCfg(ctx, req.Region)
	if err != nil {
		return nil, err
	}
	err = assertAccount(ctx, &cfg, req.ExpectedAccount)
	if err != nil {
		return nil, err
	}
	return getQuoteWithAPI(ctx, ec2.NewFromConfig(cfg), req)
}

// getQuoteWithAPI performs the quote call using an EC2ExchangeAPI,
// allowing ExecuteExchange to reuse the same client for both quote and accept.
func getQuoteWithAPI(ctx context.Context, client EC2ExchangeAPI, req *QuoteRequest) (*QuoteSummary, error) {
	if len(req.ReservedIDs) == 0 {
		return nil, fmt.Errorf("must provide at least one reserved instance ID")
	}
	if err := validateTargets(req.Targets, req.TargetOfferingID, req.TargetCount); err != nil {
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

	s := &QuoteSummary{
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
func ExecuteExchange(ctx context.Context, req *ExecuteRequest) (exchangeID string, quote *QuoteSummary, err error) {
	if req.MaxPaymentDueUSD == nil {
		return "", nil, fmt.Errorf("refusing to execute without max-payment-due-usd guardrail")
	}

	cfg, err := loadCfg(ctx, req.Region)
	if err != nil {
		return "", nil, err
	}
	err = assertAccount(ctx, &cfg, req.ExpectedAccount)
	if err != nil {
		return "", nil, err
	}

	return executeWithAPI(ctx, ec2.NewFromConfig(cfg), req)
}

// resolvePaymentDue returns the quote's PaymentDueUSD, treating nil as zero.
// AWS may omit PaymentDue for zero-cost exchanges (e.g., same-RI-type conversions).
func resolvePaymentDue(q *QuoteSummary) *big.Rat {
	if q.PaymentDueUSD != nil {
		return q.PaymentDueUSD
	}
	return new(big.Rat)
}

// checkInitialQuote returns an error if the quote is invalid or exceeds the spend cap.
func checkInitialQuote(q *QuoteSummary, maxPayment *big.Rat) error {
	if !q.IsValidExchange {
		return fmt.Errorf("exchange is not valid: %s", q.ValidationFailureReason)
	}
	paymentDue := resolvePaymentDue(q)
	if paymentDue.Cmp(maxPayment) == 1 {
		return fmt.Errorf("paymentDue %s exceeds max %s", paymentDue.FloatString(2), maxPayment.FloatString(2))
	}
	return nil
}

// checkReQuote returns an error if the pre-accept re-quote is invalid or exceeds the cap.
// It is called immediately before AcceptReservedInstancesExchangeQuote to narrow the
// race window between pricing changes.
func checkReQuote(q *QuoteSummary, maxPayment *big.Rat) error {
	if !q.IsValidExchange {
		return fmt.Errorf("exchange no longer valid at accept time: %s", q.ValidationFailureReason)
	}
	paymentDue := resolvePaymentDue(q)
	if paymentDue.Cmp(maxPayment) == 1 {
		return fmt.Errorf(
			"aborting exchange: re-quoted payment %s USD exceeds cap %s USD (pricing changed between initial quote and accept)",
			paymentDue.FloatString(2),
			maxPayment.FloatString(2),
		)
	}
	return nil
}

// executeWithAPI performs the exchange using an injected EC2ExchangeAPI.
func executeWithAPI(ctx context.Context, client EC2ExchangeAPI, req *ExecuteRequest) (string, *QuoteSummary, error) {
	if req.MaxPaymentDueUSD == nil {
		return "", nil, fmt.Errorf("refusing to execute without max-payment-due-usd guardrail")
	}

	quoteReq := QuoteRequest{
		Region:           req.Region,
		ExpectedAccount:  req.ExpectedAccount,
		ReservedIDs:      req.ReservedIDs,
		Targets:          req.Targets,
		TargetOfferingID: req.TargetOfferingID,
		TargetCount:      req.TargetCount,
		DryRun:           false,
	}

	q, err := getQuoteWithAPI(ctx, client, &quoteReq)
	if err != nil {
		return "", nil, err
	}
	err = checkInitialQuote(q, req.MaxPaymentDueUSD)
	if err != nil {
		return "", q, err
	}

	// Re-quote immediately before accepting to narrow the window between quote and
	// accept. The AWS API has no atomic quote+accept operation, so server-side
	// pricing can change between the two calls. This second quote reduces -- but
	// does not eliminate -- the race window. If the fresh quote now exceeds the
	// cap, abort before the irreversible accept call.
	freshQ, err := getQuoteWithAPI(ctx, client, &quoteReq)
	if err != nil {
		return "", q, fmt.Errorf("pre-accept re-quote failed: %w", err)
	}
	err = checkReQuote(freshQ, req.MaxPaymentDueUSD)
	if err != nil {
		return "", freshQ, err
	}

	out, err := client.AcceptReservedInstancesExchangeQuote(ctx, &ec2.AcceptReservedInstancesExchangeQuoteInput{
		ReservedInstanceIds:  req.ReservedIDs,
		TargetConfigurations: req.targetConfigs(),
	})
	if err != nil {
		return "", freshQ, err
	}

	return sdkaws.ToString(out.ExchangeId), freshQ, nil
}
