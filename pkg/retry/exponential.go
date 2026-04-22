// Package retry provides a shared exponential-backoff helper used by
// retry sites that previously open-coded the same loop with slightly
// different shapes. Today's call-sites:
//
//   - internal/database/connection.go::createConnectionPoolWithRetry
//     bounds Lambda cold-start RDS Ping retries with a per-attempt
//     deadline so a single hung TCP SYN doesn't burn the full retry
//     budget.
//   - providers/gcp/services/computeengine/client.go::CreateCommitment
//     retries on RESOURCE_EXHAUSTED with a fixed 1s/2s/4s sequence.
//
// The AWS rate limiter at providers/aws/recommendations/ratelimiter.go
// is intentionally NOT migrated to this helper — its Wait/ShouldRetry/
// Reset state-machine is structurally incompatible with the closure-
// based Do shape and rewriting its callers would be a separate change.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// Config bundles the exponential-backoff knobs.
type Config struct {
	// MaxAttempts is the total number of attempts (NOT retries). 1 = no
	// retry, 5 = first attempt + 4 retries.
	MaxAttempts int
	// BaseDelay is the delay before the SECOND attempt; subsequent
	// delays double until MaxDelay caps them.
	BaseDelay time.Duration
	// MaxDelay caps the per-iteration backoff after exponential growth.
	MaxDelay time.Duration
	// PerAttemptTimeout, if > 0, wraps each op invocation in a
	// context.WithTimeout independent of the outer ctx so a single hung
	// attempt fails fast and the retry budget continues. Zero disables.
	PerAttemptTimeout time.Duration
	// Jitter, if true, adds ±25% noise to each backoff via math/rand/v2
	// (lock-free per-goroutine source, no global RNG mutex contention
	// under concurrent retries). math/rand/v2 is sufficient for backoff
	// jitter; cryptographic randomness is not needed.
	Jitter bool
	// OnAttempt, if non-nil, is invoked once per attempt — see OnAttemptFn.
	OnAttempt OnAttemptFn
}

// OnAttemptFn is called BEFORE each attempt (after any backoff
// delay has elapsed, just before op runs). It receives the attempt
// number (1-indexed) and the error from the previous attempt
// (nil on the first call). Used for per-call-site logging that
// the shared helper can't know about.
//
// Why "before" not "after": call-sites today log either while
// about-to-wait ("retrying after Nms") or right after a failure.
// The shared callback fires before each attempt and gets prevErr,
// so call-sites guard on `prevErr != nil` to log only on retries
// (matching today's behaviour: no log on the first/only attempt).
// The first-attempt invocation with prevErr=nil is a no-op for
// most call-sites.
type OnAttemptFn func(attempt int, prevErr error)

// ErrPermanent is a sentinel callers wrap into the error they return
// from op to short-circuit retries. The shared Do checks via
// errors.Is(err, ErrPermanent) after every attempt.
//
// Go 1.20+ supports multiple %w verbs in fmt.Errorf so the underlying
// SDK error stays unwrappable via errors.As:
//
//	if !shouldRetry(awsErr) {
//	    return fmt.Errorf("%w: %w", retry.ErrPermanent, awsErr)
//	}
//
// Callers can then errors.Is(err, retry.ErrPermanent) AND
// errors.As(err, &awsSpecificErr) on the same returned value.
var ErrPermanent = errors.New("retry: permanent error, do not retry")

// Validate returns an error if MaxAttempts <= 0, BaseDelay <= 0, or
// MaxDelay < BaseDelay. Do calls Validate on entry and returns its
// error verbatim if any.
func (c Config) Validate() error {
	if c.MaxAttempts <= 0 {
		return fmt.Errorf("retry: MaxAttempts must be > 0, got %d", c.MaxAttempts)
	}
	if c.BaseDelay <= 0 {
		return fmt.Errorf("retry: BaseDelay must be > 0, got %s", c.BaseDelay)
	}
	if c.MaxDelay < c.BaseDelay {
		return fmt.Errorf("retry: MaxDelay (%s) must be >= BaseDelay (%s)", c.MaxDelay, c.BaseDelay)
	}
	return nil
}

// Do runs op until it returns nil or the attempt budget is exhausted.
// The per-attempt context (if PerAttemptTimeout > 0) is independent
// of ctx so a slow attempt fails fast and the outer retry budget
// continues. Errors marked with errors.Is(err, ErrPermanent)
// short-circuit retries.
//
// Returned error: nil on success, ctx.Err() if the outer context is
// cancelled mid-backoff, the unwrapped op error if it short-circuits
// via ErrPermanent, or a wrapped "after N attempts: …" of the last
// op error if the budget exhausts.
func Do(ctx context.Context, cfg Config, op func(ctx context.Context, attempt int) error) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if attempt > 1 {
			delay := backoffFor(attempt, cfg)
			select {
			case <-ctx.Done():
				return fmt.Errorf("retry: cancelled mid-backoff after %d attempts: %w", attempt-1, ctx.Err())
			case <-time.After(delay):
			}
		}

		if cfg.OnAttempt != nil {
			cfg.OnAttempt(attempt, lastErr)
		}

		err := runAttempt(ctx, cfg, attempt, op)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrPermanent) {
			return err
		}
		lastErr = err
	}

	return fmt.Errorf("retry: exhausted %d attempts: %w", cfg.MaxAttempts, lastErr)
}

// runAttempt invokes op with a per-attempt context if configured.
// Pulled out as its own function so `defer cancel()` doesn't leak a
// context per loop iteration.
func runAttempt(ctx context.Context, cfg Config, attempt int, op func(ctx context.Context, attempt int) error) error {
	if cfg.PerAttemptTimeout <= 0 {
		return op(ctx, attempt)
	}
	perAttemptCtx, cancel := context.WithTimeout(ctx, cfg.PerAttemptTimeout)
	defer cancel()
	return op(perAttemptCtx, attempt)
}

// backoffFor returns the delay before attempt N (N >= 2). The base
// shape is BaseDelay * 2^(attempt-2) capped at MaxDelay; if Jitter is
// enabled, ±25% noise is layered on top. Attempt 2 uses BaseDelay
// directly (2^0 = 1).
func backoffFor(attempt int, cfg Config) time.Duration {
	shift := attempt - 2
	if shift < 0 {
		shift = 0
	}
	// Use multiplication rather than 1<<shift to avoid signed-int
	// overflow at huge attempt counts; capped immediately by MaxDelay.
	delay := cfg.BaseDelay
	for i := 0; i < shift; i++ {
		delay *= 2
		if delay >= cfg.MaxDelay {
			delay = cfg.MaxDelay
			break
		}
	}
	if delay > cfg.MaxDelay {
		delay = cfg.MaxDelay
	}
	if cfg.Jitter {
		// ±25% noise: rand.Float64() ∈ [0, 1) → noise factor ∈ [0.75, 1.25).
		factor := 0.75 + rand.Float64()*0.5
		delay = time.Duration(float64(delay) * factor)
	}
	return delay
}
