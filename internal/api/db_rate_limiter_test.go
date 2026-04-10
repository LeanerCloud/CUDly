package api

// db_rate_limiter_test.go — tests for DBRateLimiter that don't require a real
// database connection. Only nil-pool and in-memory paths are exercised.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDBRateLimiter(t *testing.T) {
	rl := NewDBRateLimiter(nil)
	assert.NotNil(t, rl)
	assert.NotNil(t, rl.limits)
}

func TestDBRateLimiter_SetLimit(t *testing.T) {
	rl := NewDBRateLimiter(nil)
	cfg := NewRateLimitConfig(10, 30)
	rl.SetLimit("test-endpoint", cfg)

	rl.limitsMu.RLock()
	stored := rl.limits["test-endpoint"]
	rl.limitsMu.RUnlock()

	assert.Equal(t, 10, stored.MaxAttempts)
}

func TestDBRateLimiter_SetLimit_NilLimitsMap(t *testing.T) {
	// Simulate a limiter whose limits map was nilled out
	rl := &DBRateLimiter{}
	cfg := NewRateLimitConfig(5, 60)
	rl.SetLimit("endpoint", cfg)

	assert.Equal(t, 5, rl.limits["endpoint"].MaxAttempts)
}

func TestDBRateLimiter_Allow_NilLimiter(t *testing.T) {
	var rl *DBRateLimiter
	// A nil receiver should be safe (fail-open)
	allowed, err := rl.Allow(context.Background(), "IP#1.2.3.4", "login")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestDBRateLimiter_Allow_NilPool(t *testing.T) {
	rl := NewDBRateLimiter(nil)
	allowed, err := rl.Allow(context.Background(), "IP#1.2.3.4", "login")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestDBRateLimiter_AllowWithIP_NilPool(t *testing.T) {
	rl := NewDBRateLimiter(nil)
	allowed, err := rl.AllowWithIP(context.Background(), "1.2.3.4", "login")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestDBRateLimiter_AllowWithEmail_NilPool(t *testing.T) {
	rl := NewDBRateLimiter(nil)
	allowed, err := rl.AllowWithEmail(context.Background(), "user@example.com", "forgot_password")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestDBRateLimiter_AllowWithUser_NilPool(t *testing.T) {
	rl := NewDBRateLimiter(nil)
	allowed, err := rl.AllowWithUser(context.Background(), "user-id-123", "admin")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestDBRateLimiter_cleanup_NilPool(t *testing.T) {
	rl := &DBRateLimiter{pool: nil}
	// Should not panic
	rl.cleanup()
}

func TestDBRateLimiter_maybeCleanup_AlreadyRunning(t *testing.T) {
	rl := NewDBRateLimiter(nil)
	rl.cleanupRunning.Store(true)
	// Should return immediately without panicking
	rl.maybeCleanup()
	rl.cleanupRunning.Store(false)
}

func TestDBRateLimiter_maybeCleanup_TooSoon(t *testing.T) {
	rl := NewDBRateLimiter(nil)
	rl.cleanupInterval = time.Hour
	rl.lastCleanup = time.Now() // Just ran cleanup

	// Should return without triggering cleanup
	rl.maybeCleanup()
}

func TestDBRateLimiter_maybeCleanup_ReadyToCleanup(t *testing.T) {
	rl := NewDBRateLimiter(nil)
	rl.cleanupInterval = time.Millisecond
	rl.lastCleanup = time.Now().Add(-time.Minute) // Long ago

	// Should trigger cleanup (no-op because pool is nil)
	rl.maybeCleanup()
	// Brief wait for the goroutine to complete
	time.Sleep(20 * time.Millisecond)
}
