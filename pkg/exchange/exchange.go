// Package exchange provides AWS Convertible Reserved Instance exchange operations.
// It wraps the EC2 GetReservedInstancesExchangeQuote and AcceptReservedInstancesExchangeQuote
// APIs with input validation and spend-cap guardrails.
package exchange

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

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

	PaymentDueRaw string   // as returned by AWS (string)
	PaymentDueUSD *big.Rat // parsed numeric (optional)

	OutputReservedInstancesExp *time.Time

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

// ExchangeQuoteRequest holds parameters for requesting an exchange quote.
type ExchangeQuoteRequest struct {
	Region          string
	ExpectedAccount string // optional safety check
	ReservedIDs     []string

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

	TargetOfferingID string
	TargetCount      int32

	// Guardrail: require PaymentDue <= MaxPaymentDueUSD to execute.
	// If nil, execution is refused.
	MaxPaymentDueUSD *big.Rat
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
	return getQuoteWithClient(ctx, ec2.NewFromConfig(cfg), req)
}

// getQuoteWithClient performs the quote call using a pre-configured EC2 client,
// allowing ExecuteExchange to reuse the same client for both quote and accept.
func getQuoteWithClient(ctx context.Context, client *ec2.Client, req ExchangeQuoteRequest) (*ExchangeQuoteSummary, error) {
	if len(req.ReservedIDs) == 0 {
		return nil, fmt.Errorf("must provide at least one reserved instance ID")
	}
	if strings.TrimSpace(req.TargetOfferingID) == "" {
		return nil, fmt.Errorf("must provide target offering ID")
	}
	if req.TargetCount <= 0 {
		req.TargetCount = 1
	}

	in := &ec2.GetReservedInstancesExchangeQuoteInput{
		DryRun:              sdkaws.Bool(req.DryRun),
		ReservedInstanceIds: req.ReservedIDs,
		TargetConfigurations: []ec2types.TargetConfigurationRequest{
			{
				OfferingId:    sdkaws.String(req.TargetOfferingID),
				InstanceCount: sdkaws.Int32(req.TargetCount),
			},
		},
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
		t := *out.OutputReservedInstancesWillExpireAt
		s.OutputReservedInstancesExp = &t
	}

	if s.PaymentDueRaw != "" {
		p, perr := ParseDecimalRat(s.PaymentDueRaw)
		if perr != nil {
			return nil, fmt.Errorf("quote returned invalid paymentDue %q: %w", s.PaymentDueRaw, perr)
		}
		s.PaymentDueUSD = p
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

	client := ec2.NewFromConfig(cfg)

	q, err := getQuoteWithClient(ctx, client, ExchangeQuoteRequest{
		Region:           req.Region,
		ExpectedAccount:  req.ExpectedAccount,
		ReservedIDs:      req.ReservedIDs,
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

	if q.PaymentDueUSD == nil {
		return "", q, fmt.Errorf("quote did not return a parseable paymentDue; refusing to execute without cost verification")
	}

	// paymentDue > max => refuse
	if q.PaymentDueUSD.Cmp(req.MaxPaymentDueUSD) == 1 {
		return "", q, fmt.Errorf("paymentDue %s exceeds max %s", q.PaymentDueUSD.FloatString(2), req.MaxPaymentDueUSD.FloatString(2))
	}

	out, err := client.AcceptReservedInstancesExchangeQuote(ctx, &ec2.AcceptReservedInstancesExchangeQuoteInput{
		ReservedInstanceIds: req.ReservedIDs,
		TargetConfigurations: []ec2types.TargetConfigurationRequest{
			{
				OfferingId:    sdkaws.String(req.TargetOfferingID),
				InstanceCount: sdkaws.Int32(req.TargetCount),
			},
		},
	})
	if err != nil {
		return "", q, err
	}

	return sdkaws.ToString(out.ExchangeId), q, nil
}
