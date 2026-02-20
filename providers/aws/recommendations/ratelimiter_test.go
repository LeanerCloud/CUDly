package recommendations

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// throttleError implements the errorCoder interface used by isThrottleError
type throttleError struct {
	code string
	msg  string
}

func (e *throttleError) Error() string     { return e.msg }
func (e *throttleError) ErrorCode() string { return e.code }

func newThrottleError() error {
	return &throttleError{code: "Throttling", msg: "rate exceeded"}
}

func newWrappedThrottleError() error {
	return fmt.Errorf("wrapped: %w", newThrottleError())
}

func TestNewRateLimiter(t *testing.T) {
	limiter := NewRateLimiter()

	assert.NotNil(t, limiter)
	assert.Equal(t, 1*time.Second, limiter.baseDelay)
	assert.Equal(t, 30*time.Second, limiter.maxDelay)
	assert.Equal(t, 5, limiter.maxRetries)
	assert.Equal(t, 0, limiter.retryCount)
}

func TestNewRateLimiterWithOptions(t *testing.T) {
	baseDelay := 2 * time.Second
	maxDelay := 60 * time.Second
	maxRetries := 10

	limiter := NewRateLimiterWithOptions(baseDelay, maxDelay, maxRetries)

	assert.NotNil(t, limiter)
	assert.Equal(t, baseDelay, limiter.baseDelay)
	assert.Equal(t, maxDelay, limiter.maxDelay)
	assert.Equal(t, maxRetries, limiter.maxRetries)
	assert.Equal(t, 0, limiter.retryCount)
}

func TestWait_FirstAttemptNoDelay(t *testing.T) {
	limiter := NewRateLimiter()
	ctx := context.Background()

	start := time.Now()
	err := limiter.Wait(ctx)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	// First attempt should have minimal delay
	assert.Less(t, elapsed, 100*time.Millisecond)
}

func TestWait_ExponentialBackoff(t *testing.T) {
	limiter := NewRateLimiterWithOptions(100*time.Millisecond, 10*time.Second, 5)
	ctx := context.Background()

	// First retry (retryCount = 1)
	limiter.retryCount = 1
	start := time.Now()
	err := limiter.Wait(ctx)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	// Should have base delay with jitter: 2^0 * 100ms = 100ms + jitter
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	assert.Less(t, elapsed, 200*time.Millisecond)
}

func TestWait_MaxDelayRespected(t *testing.T) {
	limiter := NewRateLimiterWithOptions(100*time.Millisecond, 500*time.Millisecond, 10)
	ctx := context.Background()

	// Set a high retry count that would exceed max delay
	limiter.retryCount = 10
	start := time.Now()
	err := limiter.Wait(ctx)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	// Should be capped at max delay plus jitter
	assert.Less(t, elapsed, 700*time.Millisecond)
}

func TestWait_ContextCancellation(t *testing.T) {
	limiter := NewRateLimiterWithOptions(5*time.Second, 30*time.Second, 5)
	ctx, cancel := context.WithCancel(context.Background())

	// Set retry count to trigger delay
	limiter.retryCount = 1

	// Cancel context immediately
	cancel()

	err := limiter.Wait(ctx)

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestWait_ContextTimeout(t *testing.T) {
	limiter := NewRateLimiterWithOptions(5*time.Second, 30*time.Second, 5)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Set retry count to trigger a long delay
	limiter.retryCount = 5

	err := limiter.Wait(ctx)

	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}

func TestShouldRetry_NoError(t *testing.T) {
	limiter := NewRateLimiter()

	shouldRetry := limiter.ShouldRetry(nil)

	assert.False(t, shouldRetry)
	assert.Equal(t, 0, limiter.retryCount)
}

func TestShouldRetry_NonThrottleError(t *testing.T) {
	limiter := NewRateLimiter()
	err := errors.New("some non-throttle error")

	shouldRetry := limiter.ShouldRetry(err)

	assert.False(t, shouldRetry)
	assert.Equal(t, 0, limiter.retryCount)
}

func TestShouldRetry_ThrottleError(t *testing.T) {
	limiter := NewRateLimiter()
	err := newThrottleError()

	shouldRetry := limiter.ShouldRetry(err)

	assert.True(t, shouldRetry)
	assert.Equal(t, 1, limiter.retryCount)
}

func TestShouldRetry_WrappedThrottleError(t *testing.T) {
	limiter := NewRateLimiter()
	err := newWrappedThrottleError()

	shouldRetry := limiter.ShouldRetry(err)

	assert.True(t, shouldRetry)
	assert.Equal(t, 1, limiter.retryCount)
}

func TestShouldRetry_MaxRetriesExceeded(t *testing.T) {
	limiter := NewRateLimiterWithOptions(100*time.Millisecond, 30*time.Second, 3)
	err := newThrottleError()

	// First 3 retries should succeed
	for i := 0; i < 3; i++ {
		shouldRetry := limiter.ShouldRetry(err)
		assert.True(t, shouldRetry, "Retry %d should be allowed", i+1)
		assert.Equal(t, i+1, limiter.retryCount)
	}

	// 4th retry should fail (exceeds maxRetries)
	shouldRetry := limiter.ShouldRetry(err)
	assert.False(t, shouldRetry)
	assert.Equal(t, 3, limiter.retryCount)
}

func TestShouldRetry_ResetsOnSuccess(t *testing.T) {
	limiter := NewRateLimiter()
	err := newThrottleError()

	// Trigger a few retries
	limiter.ShouldRetry(err)
	limiter.ShouldRetry(err)
	assert.Equal(t, 2, limiter.retryCount)

	// Success should reset
	shouldRetry := limiter.ShouldRetry(nil)
	assert.False(t, shouldRetry)
	assert.Equal(t, 0, limiter.retryCount)
}

func TestReset(t *testing.T) {
	limiter := NewRateLimiter()
	err := newThrottleError()

	// Trigger some retries
	limiter.ShouldRetry(err)
	limiter.ShouldRetry(err)
	limiter.ShouldRetry(err)
	assert.Equal(t, 3, limiter.retryCount)

	// Reset should clear retry count
	limiter.Reset()
	assert.Equal(t, 0, limiter.retryCount)
}

func TestGetRetryCount(t *testing.T) {
	limiter := NewRateLimiter()
	err := newThrottleError()

	assert.Equal(t, 0, limiter.GetRetryCount())

	limiter.ShouldRetry(err)
	assert.Equal(t, 1, limiter.GetRetryCount())

	limiter.ShouldRetry(err)
	assert.Equal(t, 2, limiter.GetRetryCount())

	limiter.Reset()
	assert.Equal(t, 0, limiter.GetRetryCount())
}

func TestRateLimiter_FullRetryFlow(t *testing.T) {
	limiter := NewRateLimiterWithOptions(10*time.Millisecond, 100*time.Millisecond, 3)
	ctx := context.Background()
	throttleErr := newThrottleError()

	attempts := 0
	for {
		attempts++

		// Wait for rate limiter
		waitErr := limiter.Wait(ctx)
		require.NoError(t, waitErr)

		// Simulate an API call that might fail
		var callErr error
		if attempts < 3 {
			callErr = throttleErr // Fail first 2 attempts with throttle
		} else {
			callErr = nil // Succeed on 3rd attempt
		}

		// Check if we should retry
		if !limiter.ShouldRetry(callErr) {
			break
		}
	}

	assert.Equal(t, 3, attempts)
	assert.Equal(t, 0, limiter.GetRetryCount()) // Should be reset after success
}

func TestRateLimiter_ExhaustsRetries(t *testing.T) {
	limiter := NewRateLimiterWithOptions(10*time.Millisecond, 100*time.Millisecond, 2)
	ctx := context.Background()
	throttleErr := newThrottleError()

	attempts := 0
	var lastErr error
	for {
		attempts++

		// Wait for rate limiter
		waitErr := limiter.Wait(ctx)
		require.NoError(t, waitErr)

		// Simulate an API call that always fails with throttling
		lastErr = throttleErr

		// Check if we should retry
		if !limiter.ShouldRetry(lastErr) {
			break
		}
	}

	// Should attempt once + maxRetries (2) = 3 total attempts
	assert.Equal(t, 3, attempts)
	assert.Equal(t, 2, limiter.GetRetryCount())
	assert.Error(t, lastErr)
}
