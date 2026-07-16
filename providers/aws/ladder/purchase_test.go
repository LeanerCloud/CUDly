package ladder

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/ladder"
)

// ---------------------------------------------------------------------------
// Write-side fakes
// ---------------------------------------------------------------------------

// fakePurchaser is a hermetic riPurchaser / spPurchaser double that records
// the last call. Field order minimizes GC pointer-scan range (fieldalignment).
type fakePurchaser struct {
	err     error
	gotRec  *common.Recommendation
	result  common.PurchaseResult
	gotOpts common.PurchaseOptions
	calls   int
}

func (f *fakePurchaser) PurchaseCommitment(_ context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	f.calls++
	f.gotRec = &rec
	f.gotOpts = opts
	return f.result, f.err
}

// newWiredLadder returns a ladder with the write side wired to the given fakes.
func newWiredLadder(t *testing.T, riP riPurchaser, spP spPurchaser, ex exchangeRunner) *AWSLadder {
	t.Helper()
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	a, err := a.WithWriteSide(riP, spP, ex)
	require.NoError(t, err)
	return a
}

// validRIRec returns a recommendation carrying everything the EC2 RI purchase
// path requires.
func validRIRec() common.Recommendation {
	return common.Recommendation{
		ResourceType:  "m5.large",
		Count:         2,
		Term:          "1yr",
		PaymentOption: "no-upfront",
		Details: &common.ComputeDetails{
			InstanceType: "m5.large",
			Platform:     "linux",
			Tenancy:      "default",
			Scope:        "regional",
		},
	}
}

// validSPRec returns a recommendation carrying everything the Savings Plan
// purchase path requires for the given plan type.
func validSPRec(planType string) common.Recommendation {
	return common.Recommendation{
		Term:          "1yr",
		PaymentOption: "no-upfront",
		Details: &common.SavingsPlanDetails{
			PlanType:         planType,
			HourlyCommitment: 1.50,
		},
	}
}

func validPurchaseOpts() common.PurchaseOptions {
	return common.PurchaseOptions{
		Source:           common.PurchaseSourceWeb,
		IdempotencyToken: "ladder-tok-1",
		ExecutionID:      "exec-1",
	}
}

// ---------------------------------------------------------------------------
// WithWriteSide
// ---------------------------------------------------------------------------

func TestWithWriteSide_NilArgsRejected(t *testing.T) {
	riP := &fakePurchaser{}
	spP := &fakePurchaser{}
	ex := &fakeExchangeRunner{}

	tests := []struct {
		name    string
		riP     riPurchaser
		spP     spPurchaser
		ex      exchangeRunner
		wantErr string
	}{
		{"nil riPurchaser", nil, spP, ex, "riPurchaser must not be nil"},
		{"nil spPurchaser", riP, nil, ex, "spPurchaser must not be nil"},
		{"nil exchangeRunner", riP, spP, nil, "exchangeRunner must not be nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
			_, err := a.WithWriteSide(tt.riP, tt.spP, tt.ex)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestWriteMethods_WithoutWithWriteSide_ReturnErrWriteNotWired(t *testing.T) {
	// Direct coverage of the write methods' own nil-dependency guards: a
	// New()-built ladder that never had WithWriteSide called must reject
	// both write methods with the errWriteNotWired sentinel even when the
	// inputs are otherwise fully valid. (No purchaser/runner exists on such
	// an instance, so a zero-call assertion is implicit — there is nothing
	// wired that could have been invoked.)
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})

	_, err := a.PurchaseLayer(context.Background(), ladder.LayerConvertibleRI, validRIRec(), validPurchaseOpts())
	require.Error(t, err)
	assert.ErrorIs(t, err, errWriteNotWired)

	_, err = a.ReshapeBuffer(context.Background(), testScope(), validReshapeCfg())
	require.Error(t, err)
	assert.ErrorIs(t, err, errWriteNotWired)
}

// ---------------------------------------------------------------------------
// PurchaseLayer dispatch
// ---------------------------------------------------------------------------

func TestPurchaseLayer_DispatchConvertibleRI(t *testing.T) {
	riP := &fakePurchaser{result: common.PurchaseResult{Success: true, CommitmentID: "ri-new-1"}}
	spP := &fakePurchaser{}
	a := newWiredLadder(t, riP, spP, &fakeExchangeRunner{})

	result, err := a.PurchaseLayer(context.Background(), ladder.LayerConvertibleRI, validRIRec(), validPurchaseOpts())
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "ri-new-1", result.CommitmentID)

	assert.Equal(t, 1, riP.calls, "riPurchaser must be called exactly once")
	assert.Equal(t, 0, spP.calls, "spPurchaser must not be called for the RI layer")
	require.NotNil(t, riP.gotRec)
	assert.Equal(t, "m5.large", riP.gotRec.ResourceType)
	assert.Equal(t, "ladder-tok-1", riP.gotOpts.IdempotencyToken)
}

func TestPurchaseLayer_DispatchSPLayers(t *testing.T) {
	tests := []struct {
		name         string
		layer        ladder.LayerType
		wantPlanType string
	}{
		{"EC2Instance SP layer", ladder.LayerEC2InstanceSP, spPlanTypeEC2Instance},
		{"Compute SP layer", ladder.LayerComputeSP, spPlanTypeCompute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			riP := &fakePurchaser{}
			spP := &fakePurchaser{result: common.PurchaseResult{Success: true, CommitmentID: "sp-new-1"}}
			a := newWiredLadder(t, riP, spP, &fakeExchangeRunner{})

			result, err := a.PurchaseLayer(context.Background(), tt.layer, validSPRec(tt.wantPlanType), validPurchaseOpts())
			require.NoError(t, err)
			assert.True(t, result.Success)

			assert.Equal(t, 1, spP.calls, "spPurchaser must be called exactly once")
			assert.Equal(t, 0, riP.calls, "riPurchaser must not be called for SP layers")
			require.NotNil(t, spP.gotRec)
			details, ok := spP.gotRec.Details.(*common.SavingsPlanDetails)
			require.True(t, ok)
			assert.Equal(t, tt.wantPlanType, details.PlanType,
				"the dispatched recommendation must carry the layer's plan type")
			assert.Equal(t, "ladder-tok-1", spP.gotOpts.IdempotencyToken)
		})
	}
}

func TestPurchaseLayer_UnknownLayer_ErrorsWithoutCalling(t *testing.T) {
	riP := &fakePurchaser{}
	spP := &fakePurchaser{}
	a := newWiredLadder(t, riP, spP, &fakeExchangeRunner{})

	_, err := a.PurchaseLayer(context.Background(), ladder.LayerType("gcp-cud"), validRIRec(), validPurchaseOpts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a supported AWS ladder layer")
	assert.Equal(t, 0, riP.calls)
	assert.Equal(t, 0, spP.calls)
}

func TestPurchaseLayer_MissingIdempotencyToken_ErrorsWithoutCalling(t *testing.T) {
	for _, layer := range []ladder.LayerType{ladder.LayerConvertibleRI, ladder.LayerEC2InstanceSP, ladder.LayerComputeSP} {
		t.Run(string(layer), func(t *testing.T) {
			riP := &fakePurchaser{}
			spP := &fakePurchaser{}
			a := newWiredLadder(t, riP, spP, &fakeExchangeRunner{})

			opts := validPurchaseOpts()
			opts.IdempotencyToken = ""
			_, err := a.PurchaseLayer(context.Background(), layer, validRIRec(), opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "IdempotencyToken must not be empty")
			assert.Equal(t, 0, riP.calls, "no purchase may happen without an idempotency token")
			assert.Equal(t, 0, spP.calls, "no purchase may happen without an idempotency token")
		})
	}
}

// ---------------------------------------------------------------------------
// PurchaseLayer recommendation validation
// ---------------------------------------------------------------------------

func TestPurchaseLayer_RIRecValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*common.Recommendation)
		wantErr string
	}{
		{"nil details", func(r *common.Recommendation) { r.Details = nil }, "must be *common.ComputeDetails"},
		{"wrong details type", func(r *common.Recommendation) {
			r.Details = &common.SavingsPlanDetails{PlanType: spPlanTypeCompute, HourlyCommitment: 1}
		}, "must be *common.ComputeDetails"},
		{"zero count", func(r *common.Recommendation) { r.Count = 0 }, "Count must be > 0"},
		{"negative count", func(r *common.Recommendation) { r.Count = -1 }, "Count must be > 0"},
		{"empty instance type", func(r *common.Recommendation) {
			r.Details = &common.ComputeDetails{Platform: "linux", Tenancy: "default", Scope: "regional"}
		}, "InstanceType must not be empty"},
		{"empty platform", func(r *common.Recommendation) {
			r.Details = &common.ComputeDetails{InstanceType: "m5.large", Tenancy: "default", Scope: "regional"}
		}, "Platform must not be empty"},
		{"empty tenancy", func(r *common.Recommendation) {
			// The ec2 client silently defaults empty Tenancy to "default"; a
			// dedicated-tenancy rec would buy the wrong product (no-silent-fallback).
			r.Details = &common.ComputeDetails{InstanceType: "m5.large", Platform: "linux", Scope: "regional"}
		}, "Tenancy must not be empty"},
		{"empty scope", func(r *common.Recommendation) {
			// The ec2 client silently defaults empty Scope to "Regional".
			r.Details = &common.ComputeDetails{InstanceType: "m5.large", Platform: "linux", Tenancy: "default"}
		}, "Scope must not be empty"},
		{"empty term", func(r *common.Recommendation) { r.Term = "" }, "Term must not be empty"},
		{"empty payment option", func(r *common.Recommendation) { r.PaymentOption = "" }, "PaymentOption must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			riP := &fakePurchaser{}
			a := newWiredLadder(t, riP, &fakePurchaser{}, &fakeExchangeRunner{})

			rec := validRIRec()
			tt.mutate(&rec)
			_, err := a.PurchaseLayer(context.Background(), ladder.LayerConvertibleRI, rec, validPurchaseOpts())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, 0, riP.calls, "validation failures must prevent the client call")
		})
	}
}

func TestPurchaseLayer_SPRecValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*common.Recommendation)
		wantErr string
	}{
		{"nil details", func(r *common.Recommendation) { r.Details = nil }, "must be *common.SavingsPlanDetails"},
		{"wrong details type", func(r *common.Recommendation) {
			r.Details = &common.ComputeDetails{InstanceType: "m5.large"}
		}, "must be *common.SavingsPlanDetails"},
		{"plan type mismatch", func(r *common.Recommendation) {
			r.Details = &common.SavingsPlanDetails{PlanType: spPlanTypeCompute, HourlyCommitment: 1}
		}, "does not match the dispatched layer's plan type"},
		{"zero hourly commitment", func(r *common.Recommendation) {
			r.Details = &common.SavingsPlanDetails{PlanType: spPlanTypeEC2Instance, HourlyCommitment: 0}
		}, "HourlyCommitment must be a positive finite value"},
		{"negative hourly commitment", func(r *common.Recommendation) {
			r.Details = &common.SavingsPlanDetails{PlanType: spPlanTypeEC2Instance, HourlyCommitment: -0.5}
		}, "HourlyCommitment must be a positive finite value"},
		{"NaN hourly commitment", func(r *common.Recommendation) {
			r.Details = &common.SavingsPlanDetails{PlanType: spPlanTypeEC2Instance, HourlyCommitment: math.NaN()}
		}, "HourlyCommitment must be a positive finite value"},
		{"+Inf hourly commitment", func(r *common.Recommendation) {
			r.Details = &common.SavingsPlanDetails{PlanType: spPlanTypeEC2Instance, HourlyCommitment: math.Inf(1)}
		}, "HourlyCommitment must be a positive finite value"},
		{"empty term", func(r *common.Recommendation) { r.Term = "" }, "Term must not be empty"},
		{"empty payment option", func(r *common.Recommendation) { r.PaymentOption = "" }, "PaymentOption must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spP := &fakePurchaser{}
			a := newWiredLadder(t, &fakePurchaser{}, spP, &fakeExchangeRunner{})

			rec := validSPRec(spPlanTypeEC2Instance)
			tt.mutate(&rec)
			_, err := a.PurchaseLayer(context.Background(), ladder.LayerEC2InstanceSP, rec, validPurchaseOpts())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, 0, spP.calls, "validation failures must prevent the client call")
		})
	}
}

func TestPurchaseLayer_SPPlanTypeMismatch_InverseDirection(t *testing.T) {
	// The mismatch table above covers Compute details dispatched to the
	// EC2Instance layer; this covers the inverse: EC2Instance details
	// dispatched to the Compute layer must be rejected the same way.
	spP := &fakePurchaser{}
	a := newWiredLadder(t, &fakePurchaser{}, spP, &fakeExchangeRunner{})

	_, err := a.PurchaseLayer(context.Background(), ladder.LayerComputeSP, validSPRec(spPlanTypeEC2Instance), validPurchaseOpts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match the dispatched layer's plan type")
	assert.Equal(t, 0, spP.calls, "a mismatched plan type must never reach the client")
}

// ---------------------------------------------------------------------------
// PurchaseLayer error propagation
// ---------------------------------------------------------------------------

func TestPurchaseLayer_ClientError_WrappedWithLayerContext(t *testing.T) {
	clientErr := errors.New("AWS API throttled")
	clientResult := common.PurchaseResult{Success: false, Error: clientErr}
	riP := &fakePurchaser{err: clientErr, result: clientResult}
	a := newWiredLadder(t, riP, &fakePurchaser{}, &fakeExchangeRunner{})

	result, err := a.PurchaseLayer(context.Background(), ladder.LayerConvertibleRI, validRIRec(), validPurchaseOpts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(ladder.LayerConvertibleRI))
	assert.Contains(t, err.Error(), "EC2 convertible RI purchase failed")
	assert.ErrorIs(t, err, clientErr, "the client error must remain unwrappable")
	assert.False(t, result.Success, "the client's result must be passed through for audit")
}

func TestPurchaseLayer_NotSupportedSentinel_PassesThrough(t *testing.T) {
	wrapped := fmt.Errorf("savings plans: %w", common.ErrCommitmentPurchaseNotSupported)
	spP := &fakePurchaser{err: wrapped}
	a := newWiredLadder(t, &fakePurchaser{}, spP, &fakeExchangeRunner{})

	_, err := a.PurchaseLayer(context.Background(), ladder.LayerComputeSP, validSPRec(spPlanTypeCompute), validPurchaseOpts())
	require.Error(t, err)
	assert.ErrorIs(t, err, common.ErrCommitmentPurchaseNotSupported,
		"engine callers detect permanent inability via errors.Is; wrapping must preserve it")
}

// ---------------------------------------------------------------------------
// Kill-switch (WireWriteSideDisabled / WireWriteSide) tests
// ---------------------------------------------------------------------------

// TestWireWriteSideDisabled_PurchaseLayer_ReturnsErrLadderExecutionDisabled verifies
// that a ladder wired via WireWriteSideDisabled returns ErrLadderExecutionDisabled
// (not the unwired errWriteNotWired) on PurchaseLayer, and never calls any AWS API.
func TestWireWriteSideDisabled_PurchaseLayer_ReturnsErrLadderExecutionDisabled(t *testing.T) {
	t.Parallel()
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	wired, err := WireWriteSideDisabled(a)
	require.NoError(t, err)

	_, purchaseErr := wired.PurchaseLayer(context.Background(), ladder.LayerConvertibleRI, validRIRec(), validPurchaseOpts())
	require.Error(t, purchaseErr)
	assert.ErrorIs(t, purchaseErr, ErrLadderExecutionDisabled,
		"kill-switch must surface as ErrLadderExecutionDisabled, not errWriteNotWired")
}

// TestWireWriteSideDisabled_ReshapeBuffer_ReturnsErrLadderExecutionDisabled verifies
// the same kill-switch behavior for ReshapeBuffer.
func TestWireWriteSideDisabled_ReshapeBuffer_ReturnsErrLadderExecutionDisabled(t *testing.T) {
	t.Parallel()
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	wired, err := WireWriteSideDisabled(a)
	require.NoError(t, err)

	_, reshapeErr := wired.ReshapeBuffer(context.Background(), testScope(), validReshapeCfg())
	require.Error(t, reshapeErr)
	assert.ErrorIs(t, reshapeErr, ErrLadderExecutionDisabled,
		"kill-switch must surface as ErrLadderExecutionDisabled, not errWriteNotWired")
}

// TestWireWriteSide_EmptyAWSConfig_WiresWithoutPanic verifies that WireWriteSide
// does not panic or error at construction time with an empty aws.Config. AWS
// SDK client constructors are lazy (no credentials or network calls at init).
func TestWireWriteSide_EmptyAWSConfig_WiresWithoutPanic(t *testing.T) {
	t.Parallel()
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	wired, err := WireWriteSide(a, aws.Config{}, &fakeExchangeRunner{})
	require.NoError(t, err)
	require.NotNil(t, wired, "WireWriteSide must return a non-nil ladder")
}
