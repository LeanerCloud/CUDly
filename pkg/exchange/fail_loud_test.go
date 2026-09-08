package exchange

// Regression tests for the fail-loud hardening of the RI exchange path.
// Each test documents the finding it guards:
//   - M4: over-cap re-quote aborts before accept
//   - M3: empty region returns an error (no us-east-1 default)
//   - L2: Count <= 0 returns an error (no silent rewrite to 1)
//   - #1964 / A09-004: a quote with no PaymentDue refuses before accept; an explicit zero proceeds

import (
	"context"
	"math/big"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// sequentialFakeEC2 returns successive quoteOutputs for each GetQuote call
// so we can simulate pricing increasing between the first and second quote.
type sequentialFakeEC2 struct {
	quoteOutputs []*ec2.GetReservedInstancesExchangeQuoteOutput
	quoteErrors  []error
	quoteCall    int

	acceptInput  *ec2.AcceptReservedInstancesExchangeQuoteInput
	acceptOutput *ec2.AcceptReservedInstancesExchangeQuoteOutput
	acceptErr    error
}

func (f *sequentialFakeEC2) GetReservedInstancesExchangeQuote(_ context.Context, _ *ec2.GetReservedInstancesExchangeQuoteInput, _ ...func(*ec2.Options)) (*ec2.GetReservedInstancesExchangeQuoteOutput, error) {
	i := f.quoteCall
	f.quoteCall++
	if i < len(f.quoteOutputs) {
		return f.quoteOutputs[i], f.quoteErrors[i]
	}
	// Repeat the last output if called more times than expected.
	last := len(f.quoteOutputs) - 1
	return f.quoteOutputs[last], f.quoteErrors[last]
}

func (f *sequentialFakeEC2) AcceptReservedInstancesExchangeQuote(_ context.Context, in *ec2.AcceptReservedInstancesExchangeQuoteInput, _ ...func(*ec2.Options)) (*ec2.AcceptReservedInstancesExchangeQuoteOutput, error) {
	f.acceptInput = in
	return f.acceptOutput, f.acceptErr
}

func seqQuoteOut(paymentDue string) *ec2.GetReservedInstancesExchangeQuoteOutput {
	return &ec2.GetReservedInstancesExchangeQuoteOutput{
		IsValidExchange: sdkaws.Bool(true),
		PaymentDue:      sdkaws.String(paymentDue),
	}
}

// TestExecute_OverCapReQuoteAbortsBeforeAccept (M4):
// The initial quote is within cap, but the pre-accept re-quote exceeds the cap.
// The exchange must be aborted with an explicit error; Accept must not be called.
func TestExecute_OverCapReQuoteAbortsBeforeAccept(t *testing.T) {
	t.Parallel()

	cap := new(big.Rat).SetInt64(50)

	// First quote (initial check): $40 -- within cap.
	// Second quote (pre-accept re-quote): $60 -- exceeds cap.
	f := &sequentialFakeEC2{
		quoteOutputs: []*ec2.GetReservedInstancesExchangeQuoteOutput{
			seqQuoteOut("40.00"),
			seqQuoteOut("60.00"),
		},
		quoteErrors:  []error{nil, nil},
		acceptOutput: &ec2.AcceptReservedInstancesExchangeQuoteOutput{ExchangeId: sdkaws.String("should-not-be-called")},
	}
	c := NewExchangeClientFromAPI(f)

	_, _, err := c.Execute(context.Background(), ExchangeExecuteRequest{
		ReservedIDs:      []string{"ri-1"},
		TargetOfferingID: "off-A",
		TargetCount:      1,
		MaxPaymentDueUSD: cap,
	})

	if err == nil {
		t.Fatal("expected error when re-quoted payment exceeds cap, got nil")
	}
	if f.acceptInput != nil {
		t.Fatalf("Accept was called despite re-quoted payment exceeding cap; accept input: %+v", f.acceptInput)
	}
	// Error must surface both the actual and cap amounts.
	if !strings.Contains(err.Error(), "60") {
		t.Errorf("error should contain the re-quoted amount (60); got: %v", err)
	}
	if !strings.Contains(err.Error(), "50") {
		t.Errorf("error should contain the cap amount (50); got: %v", err)
	}
}

// TestExecute_ReQuoteWithinCapProceedsToAccept (M4 happy path):
// Both quotes are within cap; Accept must be called.
func TestExecute_ReQuoteWithinCapProceedsToAccept(t *testing.T) {
	t.Parallel()

	cap := new(big.Rat).SetInt64(100)

	f := &sequentialFakeEC2{
		quoteOutputs: []*ec2.GetReservedInstancesExchangeQuoteOutput{
			seqQuoteOut("40.00"),
			seqQuoteOut("45.00"), // re-quote: higher but still within cap
		},
		quoteErrors:  []error{nil, nil},
		acceptOutput: &ec2.AcceptReservedInstancesExchangeQuoteOutput{ExchangeId: sdkaws.String("exch-ok")},
	}
	c := NewExchangeClientFromAPI(f)

	exchangeID, _, err := c.Execute(context.Background(), ExchangeExecuteRequest{
		ReservedIDs:      []string{"ri-1"},
		TargetOfferingID: "off-A",
		TargetCount:      1,
		MaxPaymentDueUSD: cap,
	})

	if err != nil {
		t.Fatalf("unexpected error when re-quoted payment is within cap: %v", err)
	}
	if exchangeID != "exch-ok" {
		t.Fatalf("expected exchange ID 'exch-ok', got %q", exchangeID)
	}
	if f.acceptInput == nil {
		t.Fatal("Accept was not called despite both quotes being within cap")
	}
}

// seqQuoteOutNoPayment models a valid quote whose PaymentDue field is absent.
func seqQuoteOutNoPayment() *ec2.GetReservedInstancesExchangeQuoteOutput {
	return &ec2.GetReservedInstancesExchangeQuoteOutput{IsValidExchange: sdkaws.Bool(true)}
}

// TestExecute_MissingPaymentDueOnInitialQuote_RefusesBeforeAccept (#1964,
// A09-004): a valid initial quote with no PaymentDue must be refused; Accept
// must not be called and the re-quote must not even be attempted.
func TestExecute_MissingPaymentDueOnInitialQuote_RefusesBeforeAccept(t *testing.T) {
	t.Parallel()

	f := &sequentialFakeEC2{
		quoteOutputs: []*ec2.GetReservedInstancesExchangeQuoteOutput{seqQuoteOutNoPayment()},
		quoteErrors:  []error{nil},
		acceptOutput: &ec2.AcceptReservedInstancesExchangeQuoteOutput{ExchangeId: sdkaws.String("should-not-be-called")},
	}
	c := NewExchangeClientFromAPI(f)

	_, _, err := c.Execute(context.Background(), ExchangeExecuteRequest{
		ReservedIDs:      []string{"ri-1"},
		TargetOfferingID: "off-A",
		TargetCount:      1,
		MaxPaymentDueUSD: new(big.Rat).SetInt64(50),
	})

	if err == nil {
		t.Fatal("expected error when the initial quote carries no PaymentDue, got nil")
	}
	if !strings.Contains(err.Error(), "no PaymentDue") {
		t.Errorf("error should name the missing PaymentDue; got: %v", err)
	}
	if f.acceptInput != nil {
		t.Fatalf("Accept was called despite the quote carrying no PaymentDue; accept input: %+v", f.acceptInput)
	}
	if f.quoteCall != 1 {
		t.Errorf("expected exactly one quote call before refusal, got %d", f.quoteCall)
	}
}

// TestExecute_MissingPaymentDueOnReQuote_RefusesBeforeAccept (#1964,
// A09-004): the initial quote is priced and within cap, but the pre-accept
// re-quote carries no PaymentDue. The exchange must abort before Accept.
func TestExecute_MissingPaymentDueOnReQuote_RefusesBeforeAccept(t *testing.T) {
	t.Parallel()

	f := &sequentialFakeEC2{
		quoteOutputs: []*ec2.GetReservedInstancesExchangeQuoteOutput{
			seqQuoteOut("40.00"),
			seqQuoteOutNoPayment(),
		},
		quoteErrors:  []error{nil, nil},
		acceptOutput: &ec2.AcceptReservedInstancesExchangeQuoteOutput{ExchangeId: sdkaws.String("should-not-be-called")},
	}
	c := NewExchangeClientFromAPI(f)

	_, _, err := c.Execute(context.Background(), ExchangeExecuteRequest{
		ReservedIDs:      []string{"ri-1"},
		TargetOfferingID: "off-A",
		TargetCount:      1,
		MaxPaymentDueUSD: new(big.Rat).SetInt64(50),
	})

	if err == nil {
		t.Fatal("expected error when the re-quote carries no PaymentDue, got nil")
	}
	if !strings.Contains(err.Error(), "no PaymentDue") {
		t.Errorf("error should name the missing PaymentDue; got: %v", err)
	}
	if !strings.Contains(err.Error(), "accept time") {
		t.Errorf("error should say the refusal happened at accept time; got: %v", err)
	}
	if f.acceptInput != nil {
		t.Fatalf("Accept was called despite the re-quote carrying no PaymentDue; accept input: %+v", f.acceptInput)
	}
	if f.quoteCall != 2 {
		t.Errorf("expected two quote calls (initial + re-quote) before refusal, got %d", f.quoteCall)
	}
}

// TestExecute_ZeroPaymentDueProceedsToAccept is the positive control for
// #1964: an explicit zero true-up cost is a real amount within any cap and
// must not be confused with an absent PaymentDue.
func TestExecute_ZeroPaymentDueProceedsToAccept(t *testing.T) {
	t.Parallel()

	f := &sequentialFakeEC2{
		quoteOutputs: []*ec2.GetReservedInstancesExchangeQuoteOutput{
			seqQuoteOut("0.000000"),
			seqQuoteOut("0.000000"),
		},
		quoteErrors:  []error{nil, nil},
		acceptOutput: &ec2.AcceptReservedInstancesExchangeQuoteOutput{ExchangeId: sdkaws.String("exch-zero")},
	}
	c := NewExchangeClientFromAPI(f)

	exchangeID, freshQ, err := c.Execute(context.Background(), ExchangeExecuteRequest{
		ReservedIDs:      []string{"ri-1"},
		TargetOfferingID: "off-A",
		TargetCount:      1,
		MaxPaymentDueUSD: new(big.Rat).SetInt64(50),
	})

	if err != nil {
		t.Fatalf("unexpected error for a zero-cost exchange: %v", err)
	}
	if exchangeID != "exch-zero" {
		t.Fatalf("expected exchange ID 'exch-zero', got %q", exchangeID)
	}
	if f.acceptInput == nil {
		t.Fatal("Accept was not called for a zero-cost exchange")
	}
	if freshQ == nil || freshQ.PaymentDueUSDStr != "0.000000" {
		t.Fatalf("fresh quote must carry the explicit zero amount, got %+v", freshQ)
	}
}

// TestLoadCfg_EmptyRegionErrors (M3):
// loadCfg must return an error when region is empty; it must not default to us-east-1.
func TestLoadCfg_EmptyRegionErrors(t *testing.T) {
	t.Parallel()

	_, err := loadCfg(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty region, got nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "us-east-1") {
		// Error must explain why, not mention the forbidden default as an alternative.
		t.Logf("error message: %v", err)
	}
	// Confirm the error message references region or explicit.
	if !strings.Contains(err.Error(), "region") {
		t.Errorf("error should mention 'region'; got: %v", err)
	}
}

// TestLoadCfg_WhitespaceOnlyRegionErrors (M3):
// Whitespace-only region must also be rejected.
func TestLoadCfg_WhitespaceOnlyRegionErrors(t *testing.T) {
	t.Parallel()

	_, err := loadCfg(context.Background(), "   ")
	if err == nil {
		t.Fatal("expected error for whitespace-only region, got nil")
	}
}

// TestValidateTargets_ZeroCountErrors (L2):
// A target with Count <= 0 must produce a validation error; it must not be
// silently rewritten to 1.
func TestValidateTargets_ZeroCountErrors(t *testing.T) {
	t.Parallel()

	err := validateTargets([]TargetConfig{{OfferingID: "off-A", Count: 0}}, "", 0)
	if err == nil {
		t.Fatal("expected error for Count=0, got nil")
	}
	if !strings.Contains(err.Error(), "count") {
		t.Errorf("error should mention 'count'; got: %v", err)
	}
}

// TestValidateTargets_NegativeCountErrors (L2):
// A target with Count < 0 must produce a validation error.
func TestValidateTargets_NegativeCountErrors(t *testing.T) {
	t.Parallel()

	err := validateTargets([]TargetConfig{{OfferingID: "off-B", Count: -3}}, "", 0)
	if err == nil {
		t.Fatal("expected error for Count=-3, got nil")
	}
}

// TestValidateTargets_LegacyZeroCountErrors (L2):
// The legacy singleton path must also reject Count <= 0.
func TestValidateTargets_LegacyZeroCountErrors(t *testing.T) {
	t.Parallel()

	err := validateTargets(nil, "off-A", 0)
	if err == nil {
		t.Fatal("expected error for legacy TargetCount=0, got nil")
	}
}

// TestValidateTargets_LegacyNegativeCountErrors (L2):
// The legacy singleton path must also reject negative Count.
func TestValidateTargets_LegacyNegativeCountErrors(t *testing.T) {
	t.Parallel()

	err := validateTargets(nil, "off-A", -1)
	if err == nil {
		t.Fatal("expected error for legacy TargetCount=-1, got nil")
	}
}

// TestGetQuote_ZeroCountRejected (L2):
// GetQuote via ExchangeClient must reject a zero-count target before calling AWS.
func TestGetQuote_ZeroCountRejected(t *testing.T) {
	t.Parallel()

	f := &fakeEC2{}
	c := NewExchangeClientFromAPI(f)

	_, err := c.GetQuote(context.Background(), ExchangeQuoteRequest{
		ReservedIDs:      []string{"ri-1"},
		TargetOfferingID: "off-A",
		TargetCount:      0,
	})
	if err == nil {
		t.Fatal("expected error for TargetCount=0, got nil")
	}
	if f.quoteInput != nil {
		t.Fatal("AWS GetReservedInstancesExchangeQuote was called despite invalid Count=0; should have been rejected before reaching AWS")
	}
}
