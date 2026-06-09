package exchange

import (
	"context"
	"math/big"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// fakeEC2 is a test double for EC2ExchangeAPI that records inputs and
// returns caller-supplied outputs. Enough for verifying request shape —
// tests that need live EC2 behaviour go in the sanity-test suite.
type fakeEC2 struct {
	quoteInput   *ec2.GetReservedInstancesExchangeQuoteInput
	quoteOutput  *ec2.GetReservedInstancesExchangeQuoteOutput
	quoteErr     error
	acceptInput  *ec2.AcceptReservedInstancesExchangeQuoteInput
	acceptOutput *ec2.AcceptReservedInstancesExchangeQuoteOutput
	acceptErr    error
}

func (f *fakeEC2) GetReservedInstancesExchangeQuote(_ context.Context, in *ec2.GetReservedInstancesExchangeQuoteInput, _ ...func(*ec2.Options)) (*ec2.GetReservedInstancesExchangeQuoteOutput, error) {
	f.quoteInput = in
	return f.quoteOutput, f.quoteErr
}

func (f *fakeEC2) AcceptReservedInstancesExchangeQuote(_ context.Context, in *ec2.AcceptReservedInstancesExchangeQuoteInput, _ ...func(*ec2.Options)) (*ec2.AcceptReservedInstancesExchangeQuoteOutput, error) {
	f.acceptInput = in
	return f.acceptOutput, f.acceptErr
}

func validQuoteOutput(paymentDue string) *ec2.GetReservedInstancesExchangeQuoteOutput {
	return &ec2.GetReservedInstancesExchangeQuoteOutput{
		IsValidExchange: sdkaws.Bool(true),
		PaymentDue:      sdkaws.String(paymentDue),
	}
}

func TestGetQuote_SingleTargetLegacyAlias(t *testing.T) {
	t.Parallel()
	f := &fakeEC2{quoteOutput: validQuoteOutput("10.00")}
	c := NewExchangeClientFromAPI(f)

	_, err := c.GetQuote(context.Background(), ExchangeQuoteRequest{
		ReservedIDs:      []string{"ri-1"},
		TargetOfferingID: "off-A",
		TargetCount:      2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.quoteInput == nil || len(f.quoteInput.TargetConfigurations) != 1 {
		t.Fatalf("expected 1 target; got %+v", f.quoteInput)
	}
	tc := f.quoteInput.TargetConfigurations[0]
	if sdkaws.ToString(tc.OfferingId) != "off-A" || sdkaws.ToInt32(tc.InstanceCount) != 2 {
		t.Fatalf("legacy alias not propagated; got %+v", tc)
	}
}

func TestGetQuote_MultiTargetOverridesLegacy(t *testing.T) {
	t.Parallel()
	f := &fakeEC2{quoteOutput: validQuoteOutput("25.00")}
	c := NewExchangeClientFromAPI(f)

	_, err := c.GetQuote(context.Background(), ExchangeQuoteRequest{
		ReservedIDs: []string{"ri-1", "ri-2"},
		Targets: []TargetConfig{
			{OfferingID: "off-A", Count: 3},
			{OfferingID: "off-B", Count: 1},
		},
		// When Targets is set, these legacy fields must be ignored —
		// silently preferring one over the other would otherwise leak
		// surprising behaviour to callers that populate both.
		TargetOfferingID: "off-LEGACY",
		TargetCount:      99,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(f.quoteInput.TargetConfigurations); got != 2 {
		t.Fatalf("expected 2 targets; got %d", got)
	}
	t0, t1 := f.quoteInput.TargetConfigurations[0], f.quoteInput.TargetConfigurations[1]
	if sdkaws.ToString(t0.OfferingId) != "off-A" || sdkaws.ToInt32(t0.InstanceCount) != 3 {
		t.Fatalf("target[0] wrong: %+v", t0)
	}
	if sdkaws.ToString(t1.OfferingId) != "off-B" || sdkaws.ToInt32(t1.InstanceCount) != 1 {
		t.Fatalf("target[1] wrong: %+v", t1)
	}
}

func TestGetQuote_EmptyTargetOfferingRejected(t *testing.T) {
	t.Parallel()
	f := &fakeEC2{}
	c := NewExchangeClientFromAPI(f)
	_, err := c.GetQuote(context.Background(), ExchangeQuoteRequest{
		ReservedIDs: []string{"ri-1"},
		Targets: []TargetConfig{
			{OfferingID: "", Count: 1},
		},
	})
	if err == nil {
		t.Fatalf("expected validation error for empty offering_id")
	}
}

func TestExecute_MultiTargetAppliesTotalSpendCap(t *testing.T) {
	t.Parallel()
	// Quote returns a total payment of $30.00 that is the aggregate
	// across both targets. The spend cap is $25 — we expect the
	// exchange to be refused because the TOTAL exceeds the cap, even
	// though each individual target's payment is unknown to us. This
	// locks in the intended "cap is a total, not per-target" semantic.
	f := &fakeEC2{quoteOutput: validQuoteOutput("30.00")}
	c := NewExchangeClientFromAPI(f)

	_, _, err := c.Execute(context.Background(), ExchangeExecuteRequest{
		ReservedIDs: []string{"ri-1"},
		Targets: []TargetConfig{
			{OfferingID: "off-A", Count: 1},
			{OfferingID: "off-B", Count: 1},
		},
		MaxPaymentDueUSD: new(big.Rat).SetInt64(25),
	})
	if err == nil {
		t.Fatalf("expected spend-cap refusal for paymentDue 30 > cap 25")
	}
	// Accept must not have been called — guardrail refused before.
	if f.acceptInput != nil {
		t.Fatalf("Accept was called despite cap refusal")
	}
}

func TestExecute_MultiTargetPassesSliceToAccept(t *testing.T) {
	t.Parallel()
	f := &fakeEC2{
		quoteOutput:  validQuoteOutput("10.00"),
		acceptOutput: &ec2.AcceptReservedInstancesExchangeQuoteOutput{ExchangeId: sdkaws.String("exch-123")},
	}
	c := NewExchangeClientFromAPI(f)

	exchangeID, _, err := c.Execute(context.Background(), ExchangeExecuteRequest{
		ReservedIDs: []string{"ri-1"},
		Targets: []TargetConfig{
			{OfferingID: "off-A", Count: 2},
			{OfferingID: "off-B", Count: 4},
		},
		MaxPaymentDueUSD: new(big.Rat).SetInt64(100),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exchangeID != "exch-123" {
		t.Fatalf("wrong exchange ID: %q", exchangeID)
	}
	if f.acceptInput == nil || len(f.acceptInput.TargetConfigurations) != 2 {
		t.Fatalf("Accept input didn't carry both targets: %+v", f.acceptInput)
	}
	if sdkaws.ToString(f.acceptInput.TargetConfigurations[1].OfferingId) != "off-B" {
		t.Fatalf("second target offering mismatch: %+v", f.acceptInput.TargetConfigurations[1])
	}
}

func TestExecute_LegacyAliasStillWorks(t *testing.T) {
	t.Parallel()
	f := &fakeEC2{
		quoteOutput:  validQuoteOutput("5.00"),
		acceptOutput: &ec2.AcceptReservedInstancesExchangeQuoteOutput{ExchangeId: sdkaws.String("exch-legacy")},
	}
	c := NewExchangeClientFromAPI(f)
	id, _, err := c.Execute(context.Background(), ExchangeExecuteRequest{
		ReservedIDs:      []string{"ri-1"},
		TargetOfferingID: "off-LEGACY",
		TargetCount:      1,
		MaxPaymentDueUSD: new(big.Rat).SetInt64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "exch-legacy" {
		t.Fatalf("wrong exchange id: %q", id)
	}
	if f.acceptInput == nil || len(f.acceptInput.TargetConfigurations) != 1 {
		t.Fatalf("expected 1-target Accept call; got %+v", f.acceptInput)
	}
}
