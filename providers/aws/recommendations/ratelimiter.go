package recommendations

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strings"
	"time"
)

// throttleErrorCodes contains AWS API error codes that indicate throttling
var throttleErrorCodes = map[string]struct{}{
	"Throttling":                             {},
	"ThrottlingException":                    {},
	"ThrottledException":                     {},
	"RequestThrottledException":              {},
	"TooManyRequestsException":               {},
	"ProvisionedThroughputExceededException": {},
	"RequestLimitExceeded":                   {},
	"BandwidthLimitExceeded":                 {},
	"LimitExceededException":                 {},
	"RequestThrottled":                       {},
	"SlowDown":                               {},
	"EC2ThrottledException":                  {},
}

// RateLimiter provides rate limiting with exponential backoff
type RateLimiter struct {
	// Base delay between requests
	baseDelay time.Duration
	// Maximum delay for exponential backoff
	maxDelay time.Duration
	// Current retry attempt
	retryCount int
	// Maximum number of retries
	maxRetries int
}

// NewRateLimiter creates a new rate limiter with default settings
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		baseDelay:  1 * time.Second,
		maxDelay:   30 * time.Second,
		maxRetries: 5,
		retryCount: 0,
	}
}

// NewRateLimiterWithOptions creates a rate limiter with custom settings
func NewRateLimiterWithOptions(baseDelay, maxDelay time.Duration, maxRetries int) *RateLimiter {
	return &RateLimiter{
		baseDelay:  baseDelay,
		maxDelay:   maxDelay,
		maxRetries: maxRetries,
		retryCount: 0,
	}
}

// Wait implements exponential backoff delay
func (r *RateLimiter) Wait(ctx context.Context) error {
	if r.retryCount == 0 {
		// No delay for first attempt
		return nil
	}

	// Calculate exponential backoff with jitter
	backoffSeconds := math.Pow(2, float64(r.retryCount-1))
	delay := time.Duration(backoffSeconds) * r.baseDelay

	// Cap at maximum delay
	if delay > r.maxDelay {
		delay = r.maxDelay
	}

	// Add jitter (up to 20% of delay). Math/rand is intentional: jitter only
	// needs uniform distribution, not cryptographic randomness.
	jitter := time.Duration(float64(delay) * 0.2 * rand.Float64()) // #nosec G404
	delay += jitter

	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ShouldRetry checks if we should retry based on error type and retry count.
// Only throttling and transient errors are retried.
func (r *RateLimiter) ShouldRetry(err error) bool {
	if err == nil {
		r.Reset()
		return false
	}

	// Check if we've exceeded max retries
	if r.retryCount >= r.maxRetries {
		return false
	}

	// Only retry on throttling or transient errors
	if isThrottleError(err) {
		r.retryCount++
		return true
	}

	return false
}

// isThrottleError checks if an error is an AWS throttling error
func isThrottleError(err error) bool {
	// Check for AWS API errors that implement ErrorCode()
	type errorCoder interface {
		ErrorCode() string
	}
	var apiErr errorCoder
	if errors.As(err, &apiErr) {
		_, isThrottle := throttleErrorCodes[apiErr.ErrorCode()]
		return isThrottle
	}

	// Fallback: check error message for common throttle indicators
	errMsg := err.Error()
	return strings.Contains(errMsg, "Throttling") ||
		strings.Contains(errMsg, "Rate exceeded") ||
		strings.Contains(errMsg, "TooManyRequests")
}

// Reset resets the retry counter
func (r *RateLimiter) Reset() {
	r.retryCount = 0
}

// GetRetryCount returns the current retry count
func (r *RateLimiter) GetRetryCount() int {
	return r.retryCount
}
