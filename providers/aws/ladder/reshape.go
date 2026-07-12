package ladder

import (
	"context"
	"fmt"
	"math"

	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/pkg/ladder"
)

// unlimitedCapUSD is the explicit "no cap" value passed to
// exchange.RIExchangeConfig when a BufferReshapeConfig cap is nil.
//
// Why not something cleaner: RIExchangeConfig has no nil/absent
// representation for its float64 caps, and 0 there is maximally RESTRICTIVE
// (RunAutoExchange skips any exchange whose payment exceeds the cap, so a
// zero cap blocks everything) — mapping nil to 0 would silently invert the
// caller's intent. +Inf is not usable either: big.Rat.SetFloat64(+Inf)
// returns nil and the comparison in RunAutoExchange would panic.
// math.MaxFloat64 is finite (exactly representable in big.Rat) and exceeds
// any real exchange payment, making it a faithful "no cap".
const unlimitedCapUSD = math.MaxFloat64

// ReshapeBuffer runs the automated RI exchange flow over the buffer layer
// (convertible RIs), delegating to the injected exchangeRunner. AWSLadder
// only maps the configuration and the outcome; the runner owns the exchange
// store, quote/execute client, offering lookup, and RI/utilization inventory
// (the same wiring internal/server.executeRIExchangeReshape performs).
//
// WARNING — store-wide side effect: the underlying exchange.RunAutoExchange
// begins by unconditionally canceling ALL pending exchange records in the
// store (CancelAllPendingExchanges), including pendings created by the
// standalone ri_exchange_reshape scheduled task. Callers MUST NOT run
// ReshapeBuffer concurrently with that task against the same store; the
// pipeline phase that invokes this method must coordinate with (or disable)
// the standalone scheduler.
//
// DryRun is NOT supported: there is no true simulation mode in pkg/exchange
// yet (tracked upstream), and mapping DryRun onto the exchange flow's manual
// mode would be a false simulation — manual mode persists pending
// ExchangeRecords with live approval tokens (actionable money instruments)
// and still triggers the store-wide pending cancellation above. cfg.DryRun
// therefore fails loud; the engine previews reshapes via ActionReshape
// rationales without calling ReshapeBuffer.
//
// Config mapping (BufferReshapeConfig -> exchange.RIExchangeConfig):
//
//   - MaxPaymentPerExchangeUSD / MaxPaymentDailyUSD: nil means no cap and maps
//     to unlimitedCapUSD (see that constant for why); non-nil values must be
//     finite and > 0 — zero is rejected loudly because RunAutoExchange treats
//     the cap as a skip threshold and a zero cap would silently block every
//     exchange (almost certainly a config bug, not an intent).
//   - UtilizationThresholdPct must be in (0, 100]; LookbackDays must be > 0.
//   - Mode is always exchange.ExchangeModeAuto: exchanges execute immediately,
//     subject to the per-exchange and daily caps.
//
// Outcome mapping (exchange.AutoExchangeResult -> ladder.ReshapeSummary):
//
//   - Analyzed = Completed + Pending + Failed + Skipped: the number of reshape
//     recommendations processed. NOTE: this is not the total RI inventory size
//     (the thin runner seam does not expose it); it counts the commitments the
//     exchange analysis flagged and processed.
//   - Reshaped = Completed only (exchanges actually executed).
//   - Skipped  = Skipped only: below the utilization threshold, no matching
//     offering, invalid quote, or over the PER-EXCHANGE cap. A DAILY-cap stop
//     is NOT in this bucket: pkg/exchange classifies it as Failed
//     (saveFailedRecord + result.Failed), so a routine daily-cap policy stop
//     currently surfaces from this method as "N of M failed" plus a non-nil
//     error. Upstream reclassification of daily-cap stops as skips is
//     tracked in #1348.
//     Failed attempts are never counted as "skipped"; they surface in
//     Details AND as a non-nil error (money-path failures must never be
//     silently absorbed into a success-looking summary).
//
// Partial failures: when the runner reports failed exchange attempts, the
// populated summary is returned TOGETHER with a non-nil error so callers get
// both the audit detail and a loud failure signal.
func (a *AWSLadder) ReshapeBuffer(ctx context.Context, scope ladder.Scope, cfg ladder.BufferReshapeConfig) (ladder.ReshapeSummary, error) {
	if a.exchange == nil {
		return ladder.ReshapeSummary{}, fmt.Errorf("ReshapeBuffer: %w", errWriteNotWired)
	}
	if err := a.validateScope(scope); err != nil {
		return ladder.ReshapeSummary{}, err
	}
	if cfg.DryRun {
		// Fail loud beats false simulation: the exchange flow's manual mode
		// persists actionable approval records and cancels unrelated pendings
		// store-wide — neither is a dry run. See the godoc warning above.
		return ladder.ReshapeSummary{}, fmt.Errorf(
			"ReshapeBuffer: dry-run is not supported by the AWS exchange flow yet (a true simulation mode in pkg/exchange is tracked upstream); the engine previews reshapes via ActionReshape rationales without calling ReshapeBuffer")
	}

	runCfg, err := buildRIExchangeConfig(cfg)
	if err != nil {
		return ladder.ReshapeSummary{}, fmt.Errorf("ReshapeBuffer: %w", err)
	}

	result, err := a.exchange.RunAutoExchange(ctx, runCfg)
	if err != nil {
		return ladder.ReshapeSummary{}, fmt.Errorf("ReshapeBuffer: auto exchange run failed: %w", err)
	}
	if result == nil {
		return ladder.ReshapeSummary{}, fmt.Errorf("ReshapeBuffer: exchange runner returned a nil result without an error (runner contract violation)")
	}

	return summarizeExchangeResult(result)
}

// buildRIExchangeConfig validates cfg at the boundary and maps it to the
// exchange package's runtime configuration. See ReshapeBuffer's godoc for the
// full mapping rationale.
func buildRIExchangeConfig(cfg ladder.BufferReshapeConfig) (exchange.RIExchangeConfig, error) {
	perExchangeCap, err := capOrUnlimited("MaxPaymentPerExchangeUSD", cfg.MaxPaymentPerExchangeUSD)
	if err != nil {
		return exchange.RIExchangeConfig{}, err
	}
	dailyCap, err := capOrUnlimited("MaxPaymentDailyUSD", cfg.MaxPaymentDailyUSD)
	if err != nil {
		return exchange.RIExchangeConfig{}, err
	}
	if math.IsNaN(cfg.UtilizationThresholdPct) || cfg.UtilizationThresholdPct <= 0 || cfg.UtilizationThresholdPct > 100 {
		return exchange.RIExchangeConfig{}, fmt.Errorf(
			"UtilizationThresholdPct must be in (0, 100], got %g", cfg.UtilizationThresholdPct)
	}
	if cfg.LookbackDays <= 0 {
		return exchange.RIExchangeConfig{}, fmt.Errorf("LookbackDays must be > 0, got %d", cfg.LookbackDays)
	}

	// Mode is always auto: ReshapeBuffer rejects DryRun before this point
	// (no true simulation mode exists in pkg/exchange; manual mode is not a
	// simulation — see the ReshapeBuffer godoc warning).
	return exchange.RIExchangeConfig{
		Mode:                     string(exchange.ExchangeModeAuto),
		UtilizationThreshold:     cfg.UtilizationThresholdPct,
		MaxPaymentPerExchangeUSD: perExchangeCap,
		MaxPaymentDailyUSD:       dailyCap,
		LookbackDays:             cfg.LookbackDays,
	}, nil
}

// capOrUnlimited maps an optional money cap to the float64 the exchange
// config requires: nil -> unlimitedCapUSD (no cap); non-nil values must be
// finite and > 0 (see the unlimitedCapUSD comment for why zero is rejected).
func capOrUnlimited(name string, capUSD *float64) (float64, error) {
	if capUSD == nil {
		return unlimitedCapUSD, nil
	}
	v := *capUSD
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s must be finite, got %g", name, v)
	}
	if v <= 0 {
		return 0, fmt.Errorf("%s must be > 0 when set (a zero cap would block every exchange; use nil for no cap), got %g", name, v)
	}
	return v, nil
}

// summarizeExchangeResult maps the runner outcome to a ReshapeSummary.
// Index-based loops avoid copying the large outcome structs (rangeValCopy).
func summarizeExchangeResult(result *exchange.AutoExchangeResult) (ladder.ReshapeSummary, error) {
	summary := ladder.ReshapeSummary{
		Analyzed: len(result.Completed) + len(result.Pending) + len(result.Failed) + len(result.Skipped),
		Reshaped: len(result.Completed),
		Skipped:  len(result.Skipped),
		Details: make([]string, 0,
			len(result.Completed)+len(result.Pending)+len(result.Failed)+len(result.Skipped)),
	}

	for i := range result.Completed {
		o := &result.Completed[i]
		summary.Details = append(summary.Details, fmt.Sprintf(
			"reshaped: %s (%s) -> %s x%d, payment $%s, exchange %s",
			o.SourceRIID, o.SourceInstanceType, o.TargetInstanceType, o.TargetCount, o.PaymentDue, o.ExchangeID))
	}
	for i := range result.Pending {
		o := &result.Pending[i]
		summary.Details = append(summary.Details, fmt.Sprintf(
			"pending approval (not executed): %s (%s) -> %s x%d, payment $%s",
			o.SourceRIID, o.SourceInstanceType, o.TargetInstanceType, o.TargetCount, o.PaymentDue))
	}
	for i := range result.Failed {
		o := &result.Failed[i]
		summary.Details = append(summary.Details, fmt.Sprintf(
			"failed: %s (%s) -> %s: %s",
			o.SourceRIID, o.SourceInstanceType, o.TargetInstanceType, o.Error))
	}
	for i := range result.Skipped {
		s := &result.Skipped[i]
		summary.Details = append(summary.Details, fmt.Sprintf(
			"skipped: %s (%s): %s", s.SourceRIID, s.SourceInstanceType, s.Reason))
	}

	if len(result.Failed) > 0 {
		// Denominate by actual exchange ATTEMPTS (completed + failed), not
		// summary.Analyzed: pending and skipped items were never attempted,
		// and an inflated denominator would misread during an incident.
		attempts := len(result.Completed) + len(result.Failed)
		return summary, fmt.Errorf(
			"ReshapeBuffer: %d of %d exchange attempt(s) failed (first: %s: %s); see summary details for the full list",
			len(result.Failed), attempts, result.Failed[0].SourceRIID, result.Failed[0].Error)
	}
	return summary, nil
}
