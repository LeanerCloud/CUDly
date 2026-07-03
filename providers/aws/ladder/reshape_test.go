package ladder

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/pkg/ladder"
)

// fakeExchangeRunner is a hermetic exchangeRunner double that records the
// config it received. Field order minimizes GC pointer-scan range.
type fakeExchangeRunner struct {
	err    error
	result *exchange.AutoExchangeResult
	gotCfg exchange.RIExchangeConfig
	calls  int
}

func (f *fakeExchangeRunner) RunAutoExchange(_ context.Context, cfg exchange.RIExchangeConfig) (*exchange.AutoExchangeResult, error) {
	f.calls++
	f.gotCfg = cfg
	if f.result == nil && f.err == nil {
		return &exchange.AutoExchangeResult{Mode: cfg.Mode}, nil
	}
	return f.result, f.err
}

// validReshapeCfg returns a BufferReshapeConfig that passes all boundary checks.
func validReshapeCfg() ladder.BufferReshapeConfig {
	return ladder.BufferReshapeConfig{
		MaxPaymentPerExchangeUSD: ptr(100.0),
		MaxPaymentDailyUSD:       ptr(500.0),
		UtilizationThresholdPct:  20.0,
		LookbackDays:             30,
		DryRun:                   false,
	}
}

// ---------------------------------------------------------------------------
// Config mapping
// ---------------------------------------------------------------------------

func TestReshapeBuffer_CapMapping_SetValuesPassedVerbatim(t *testing.T) {
	ex := &fakeExchangeRunner{}
	a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

	_, err := a.ReshapeBuffer(context.Background(), testScope(), validReshapeCfg())
	require.NoError(t, err)
	require.Equal(t, 1, ex.calls)
	assert.InDelta(t, 100.0, ex.gotCfg.MaxPaymentPerExchangeUSD, 1e-9)
	assert.InDelta(t, 500.0, ex.gotCfg.MaxPaymentDailyUSD, 1e-9)
	assert.InDelta(t, 20.0, ex.gotCfg.UtilizationThreshold, 1e-9)
	assert.Equal(t, 30, ex.gotCfg.LookbackDays)
}

func TestReshapeBuffer_CapMapping_NilMeansUnlimited(t *testing.T) {
	ex := &fakeExchangeRunner{}
	a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

	cfg := validReshapeCfg()
	cfg.MaxPaymentPerExchangeUSD = nil
	cfg.MaxPaymentDailyUSD = nil
	_, err := a.ReshapeBuffer(context.Background(), testScope(), cfg)
	require.NoError(t, err)
	assert.Equal(t, unlimitedCapUSD, ex.gotCfg.MaxPaymentPerExchangeUSD,
		"nil per-exchange cap must map to the explicit unlimited constant, never to 0 (0 blocks every exchange)")
	assert.Equal(t, unlimitedCapUSD, ex.gotCfg.MaxPaymentDailyUSD,
		"nil daily cap must map to the explicit unlimited constant, never to 0")
}

func TestReshapeBuffer_CapValidation_ZeroAndBadValuesRejected(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ladder.BufferReshapeConfig)
		wantErr string
	}{
		{"zero per-exchange cap", func(c *ladder.BufferReshapeConfig) { c.MaxPaymentPerExchangeUSD = ptr(0.0) },
			"MaxPaymentPerExchangeUSD must be > 0"},
		{"negative per-exchange cap", func(c *ladder.BufferReshapeConfig) { c.MaxPaymentPerExchangeUSD = ptr(-5.0) },
			"MaxPaymentPerExchangeUSD must be > 0"},
		{"NaN per-exchange cap", func(c *ladder.BufferReshapeConfig) { c.MaxPaymentPerExchangeUSD = ptr(math.NaN()) },
			"MaxPaymentPerExchangeUSD must be finite"},
		{"Inf per-exchange cap", func(c *ladder.BufferReshapeConfig) { c.MaxPaymentPerExchangeUSD = ptr(math.Inf(1)) },
			"MaxPaymentPerExchangeUSD must be finite"},
		{"zero daily cap", func(c *ladder.BufferReshapeConfig) { c.MaxPaymentDailyUSD = ptr(0.0) },
			"MaxPaymentDailyUSD must be > 0"},
		{"negative daily cap", func(c *ladder.BufferReshapeConfig) { c.MaxPaymentDailyUSD = ptr(-1.0) },
			"MaxPaymentDailyUSD must be > 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := &fakeExchangeRunner{}
			a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

			cfg := validReshapeCfg()
			tt.mutate(&cfg)
			_, err := a.ReshapeBuffer(context.Background(), testScope(), cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, 0, ex.calls, "invalid config must never reach the runner")
		})
	}
}

func TestReshapeBuffer_ThresholdAndLookbackValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ladder.BufferReshapeConfig)
		wantErr string
	}{
		{"zero threshold", func(c *ladder.BufferReshapeConfig) { c.UtilizationThresholdPct = 0 }, "UtilizationThresholdPct must be in (0, 100]"},
		{"negative threshold", func(c *ladder.BufferReshapeConfig) { c.UtilizationThresholdPct = -5 }, "UtilizationThresholdPct must be in (0, 100]"},
		{"threshold above 100", func(c *ladder.BufferReshapeConfig) { c.UtilizationThresholdPct = 101 }, "UtilizationThresholdPct must be in (0, 100]"},
		{"NaN threshold", func(c *ladder.BufferReshapeConfig) { c.UtilizationThresholdPct = math.NaN() }, "UtilizationThresholdPct must be in (0, 100]"},
		{"zero lookback", func(c *ladder.BufferReshapeConfig) { c.LookbackDays = 0 }, "LookbackDays must be > 0"},
		{"negative lookback", func(c *ladder.BufferReshapeConfig) { c.LookbackDays = -7 }, "LookbackDays must be > 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := &fakeExchangeRunner{}
			a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

			cfg := validReshapeCfg()
			tt.mutate(&cfg)
			_, err := a.ReshapeBuffer(context.Background(), testScope(), cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, 0, ex.calls)
		})
	}
}

func TestReshapeBuffer_DryRun_NotSupported_FailsLoudWithoutCallingRunner(t *testing.T) {
	// DryRun must NOT be mapped onto the exchange flow's manual mode: manual
	// mode persists pending ExchangeRecords with live approval tokens and
	// RunAutoExchange unconditionally cancels ALL pending records store-wide
	// first — neither is a simulation. The decided behavior is a loud error.
	ex := &fakeExchangeRunner{}
	a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

	cfg := validReshapeCfg()
	cfg.DryRun = true
	_, err := a.ReshapeBuffer(context.Background(), testScope(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
	assert.Equal(t, 0, ex.calls, "a dry run must never reach the runner (it would cancel unrelated pending exchanges)")
}

func TestReshapeBuffer_LiveRun_AlwaysAutoMode(t *testing.T) {
	ex := &fakeExchangeRunner{}
	a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

	cfg := validReshapeCfg()
	cfg.DryRun = false
	_, err := a.ReshapeBuffer(context.Background(), testScope(), cfg)
	require.NoError(t, err)
	require.Equal(t, 1, ex.calls)
	assert.Equal(t, string(exchange.ExchangeModeAuto), ex.gotCfg.Mode)
}

// ---------------------------------------------------------------------------
// Outcome mapping
// ---------------------------------------------------------------------------

func TestReshapeBuffer_SummaryMapping(t *testing.T) {
	ex := &fakeExchangeRunner{result: &exchange.AutoExchangeResult{
		Mode: string(exchange.ExchangeModeAuto),
		Completed: []exchange.ExchangeOutcome{
			{SourceRIID: "ri-1", SourceInstanceType: "m5.large", TargetInstanceType: "m5.xlarge", TargetCount: 1, PaymentDue: "12.34", ExchangeID: "ex-1"},
		},
		Pending: []exchange.ExchangeOutcome{
			{SourceRIID: "ri-2", SourceInstanceType: "c5.large", TargetInstanceType: "c5.xlarge", TargetCount: 2, PaymentDue: "0"},
		},
		Skipped: []exchange.SkippedRecommendation{
			{SourceRIID: "ri-3", SourceInstanceType: "r5.large", Reason: "exceeds per-exchange cap"},
		},
	}}
	a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

	summary, err := a.ReshapeBuffer(context.Background(), testScope(), validReshapeCfg())
	require.NoError(t, err)

	assert.Equal(t, 3, summary.Analyzed, "Analyzed = completed + pending + failed + skipped")
	assert.Equal(t, 1, summary.Reshaped, "only executed exchanges count as reshaped")
	assert.Equal(t, 1, summary.Skipped)
	require.Len(t, summary.Details, 3)
	assert.Contains(t, summary.Details[0], "reshaped: ri-1")
	assert.Contains(t, summary.Details[0], "ex-1")
	assert.Contains(t, summary.Details[1], "pending approval (not executed): ri-2")
	assert.Contains(t, summary.Details[2], "skipped: ri-3")
	assert.Contains(t, summary.Details[2], "exceeds per-exchange cap")
}

func TestReshapeBuffer_PartialFailure_SummaryPlusError(t *testing.T) {
	// Includes a skipped item so the error's denominator provably counts
	// actual ATTEMPTS (completed + failed = 2), not Analyzed (3): skipped
	// and pending items were never attempted.
	ex := &fakeExchangeRunner{result: &exchange.AutoExchangeResult{
		Mode: string(exchange.ExchangeModeAuto),
		Completed: []exchange.ExchangeOutcome{
			{SourceRIID: "ri-ok", SourceInstanceType: "m5.large", TargetInstanceType: "m5.xlarge", TargetCount: 1},
		},
		Failed: []exchange.ExchangeOutcome{
			{SourceRIID: "ri-bad", SourceInstanceType: "c5.large", TargetInstanceType: "c5.xlarge", Error: "AWS exchange rejected"},
		},
		Skipped: []exchange.SkippedRecommendation{
			{SourceRIID: "ri-skip", SourceInstanceType: "r5.large", Reason: "exceeds per-exchange cap"},
		},
	}}
	a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

	summary, err := a.ReshapeBuffer(context.Background(), testScope(), validReshapeCfg())
	require.Error(t, err, "partial failures on a money path must surface as an error, not be absorbed")
	assert.Contains(t, err.Error(), "1 of 2 exchange attempt(s) failed",
		"denominator must be completed+failed attempts, not Analyzed")
	assert.Contains(t, err.Error(), "ri-bad")

	// The summary is still populated for audit alongside the error.
	assert.Equal(t, 3, summary.Analyzed)
	assert.Equal(t, 1, summary.Reshaped)
	assert.Equal(t, 1, summary.Skipped, "failed attempts are not counted as skipped")
	require.Len(t, summary.Details, 3)
	assert.Contains(t, summary.Details[1], "failed: ri-bad")
	assert.Contains(t, summary.Details[2], "skipped: ri-skip")
}

func TestReshapeBuffer_RunnerError_Propagates(t *testing.T) {
	runnerErr := errors.New("exchange store unavailable")
	ex := &fakeExchangeRunner{err: runnerErr}
	a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

	_, err := a.ReshapeBuffer(context.Background(), testScope(), validReshapeCfg())
	require.Error(t, err)
	assert.ErrorIs(t, err, runnerErr)
	assert.Contains(t, err.Error(), "auto exchange run failed")
}

func TestReshapeBuffer_NilResultWithoutError_IsContractViolation(t *testing.T) {
	// The fake returns a synthetic result when both fields are zero, so force
	// the nil-result path with a sentinel: result nil, err nil is only
	// reachable when the runner violates its contract.
	ex := &nilResultRunner{}
	a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

	_, err := a.ReshapeBuffer(context.Background(), testScope(), validReshapeCfg())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil result")
}

// nilResultRunner deliberately violates the runner contract for the guard test.
type nilResultRunner struct{}

func (n *nilResultRunner) RunAutoExchange(_ context.Context, _ exchange.RIExchangeConfig) (*exchange.AutoExchangeResult, error) {
	return nil, nil
}

func TestReshapeBuffer_WrongScope_ReturnsError(t *testing.T) {
	ex := &fakeExchangeRunner{}
	a := newWiredLadder(t, &fakePurchaser{}, &fakePurchaser{}, ex)

	badScope := ladder.Scope{Provider: common.ProviderAWS, AccountID: "999"}
	_, err := a.ReshapeBuffer(context.Background(), badScope, validReshapeCfg())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match configured account")
	assert.Equal(t, 0, ex.calls)
}
