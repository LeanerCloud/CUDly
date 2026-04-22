package retry

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastConfig is a Config tuned for quick test iteration: ms-scale
// delays so tests don't sleep multiple seconds, no jitter so timing
// assertions are deterministic.
func fastConfig(maxAttempts int) Config {
	return Config{
		MaxAttempts: maxAttempts,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}
}

func TestDo_SucceedsOnFirstAttempt(t *testing.T) {
	t.Parallel()

	var calls int32
	err := Do(context.Background(), fastConfig(3), func(_ context.Context, attempt int) error {
		atomic.AddInt32(&calls, 1)
		assert.Equal(t, 1, attempt, "first attempt should be 1-indexed")
		return nil
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, calls, "should not retry on success")
}

func TestDo_SucceedsAfterRetries(t *testing.T) {
	t.Parallel()

	var calls int32
	err := Do(context.Background(), fastConfig(5), func(_ context.Context, attempt int) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return fmt.Errorf("transient on attempt %d", attempt)
		}
		return nil
	})
	require.NoError(t, err)
	assert.EqualValues(t, 3, calls, "should stop retrying once op succeeds")
}

func TestDo_ExhaustsBudget(t *testing.T) {
	t.Parallel()

	var calls int32
	wantErr := errors.New("always fails")
	err := Do(context.Background(), fastConfig(3), func(_ context.Context, _ int) error {
		atomic.AddInt32(&calls, 1)
		return wantErr
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr, "wrapped error must be unwrappable to the original")
	assert.Contains(t, err.Error(), "exhausted 3 attempts")
	assert.EqualValues(t, 3, calls)
}

func TestDo_PermanentErrorShortCircuits(t *testing.T) {
	t.Parallel()

	var calls int32
	sdkErr := errors.New("auth failed")
	err := Do(context.Background(), fastConfig(5), func(_ context.Context, _ int) error {
		atomic.AddInt32(&calls, 1)
		return fmt.Errorf("%w: %w", ErrPermanent, sdkErr)
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPermanent, "ErrPermanent must be in the error chain")
	assert.ErrorIs(t, err, sdkErr, "underlying SDK error must also be in the chain")
	assert.EqualValues(t, 1, calls, "permanent error should not retry")
}

func TestDo_ContextCancelledMidBackoff(t *testing.T) {
	t.Parallel()

	cfg := Config{
		MaxAttempts: 5,
		BaseDelay:   50 * time.Millisecond,
		MaxDelay:    100 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int32
	go func() {
		time.Sleep(20 * time.Millisecond) // cancel during the first backoff sleep
		cancel()
	}()

	err := Do(ctx, cfg, func(_ context.Context, _ int) error {
		atomic.AddInt32(&calls, 1)
		return errors.New("fail to trigger backoff")
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.LessOrEqual(t, calls, int32(2), "should not run further attempts after ctx cancellation")
}

func TestDo_PerAttemptTimeoutFires(t *testing.T) {
	t.Parallel()

	cfg := Config{
		MaxAttempts:       3,
		BaseDelay:         1 * time.Millisecond,
		MaxDelay:          5 * time.Millisecond,
		PerAttemptTimeout: 20 * time.Millisecond,
	}

	var calls int32
	err := Do(context.Background(), cfg, func(perAttemptCtx context.Context, _ int) error {
		atomic.AddInt32(&calls, 1)
		// Simulate a hung op that ignores the per-attempt context
		// being cancelled — it returns the ctx.Err() once the deadline
		// fires.
		select {
		case <-perAttemptCtx.Done():
			return perAttemptCtx.Err()
		case <-time.After(200 * time.Millisecond):
			t.Fatal("per-attempt context should have fired before 200ms")
			return nil
		}
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exhausted 3 attempts")
	assert.EqualValues(t, 3, calls, "every attempt should run under its own deadline")
}

func TestDo_OnAttemptCallback(t *testing.T) {
	t.Parallel()

	type call struct {
		attempt int
		hadErr  bool
	}
	var seen []call
	cfg := fastConfig(3)
	cfg.OnAttempt = func(attempt int, prevErr error) {
		seen = append(seen, call{attempt: attempt, hadErr: prevErr != nil})
	}

	var calls int32
	err := Do(context.Background(), cfg, func(_ context.Context, _ int) error {
		atomic.AddInt32(&calls, 1)
		return errors.New("fail")
	})
	require.Error(t, err)
	require.Len(t, seen, 3)
	assert.Equal(t, call{attempt: 1, hadErr: false}, seen[0], "first invocation: prevErr is nil")
	assert.Equal(t, call{attempt: 2, hadErr: true}, seen[1])
	assert.Equal(t, call{attempt: 3, hadErr: true}, seen[2])
}

func TestConfig_Validate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"zero attempts", Config{MaxAttempts: 0, BaseDelay: time.Second, MaxDelay: time.Second}, "MaxAttempts"},
		{"negative attempts", Config{MaxAttempts: -1, BaseDelay: time.Second, MaxDelay: time.Second}, "MaxAttempts"},
		{"zero base delay", Config{MaxAttempts: 3, BaseDelay: 0, MaxDelay: time.Second}, "BaseDelay"},
		{"max < base", Config{MaxAttempts: 3, BaseDelay: 5 * time.Second, MaxDelay: time.Second}, "MaxDelay"},
		{"valid", Config{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Second}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestBackoffFor_GrowsAndCaps(t *testing.T) {
	t.Parallel()

	cfg := Config{BaseDelay: 1 * time.Second, MaxDelay: 4 * time.Second}
	// Attempt 2 → BaseDelay (1s), attempt 3 → 2s, attempt 4 → 4s, attempt 5 → 4s (capped).
	assert.Equal(t, 1*time.Second, backoffFor(2, cfg))
	assert.Equal(t, 2*time.Second, backoffFor(3, cfg))
	assert.Equal(t, 4*time.Second, backoffFor(4, cfg))
	assert.Equal(t, 4*time.Second, backoffFor(5, cfg))
	assert.Equal(t, 4*time.Second, backoffFor(20, cfg), "must not overflow with huge attempt numbers")
}
