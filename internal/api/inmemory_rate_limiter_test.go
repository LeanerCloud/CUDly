package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInMemoryRateLimiter(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	require.NotNil(t, rl)
	assert.NotNil(t, rl.attempts)
	assert.NotNil(t, rl.limits)
}

func TestInMemoryRateLimiter_SetLimit(t *testing.T) {
	rl := NewInMemoryRateLimiter()

	config := RateLimitConfig{
		MaxAttempts: 100,
		Window:      time.Hour,
	}
	rl.SetLimit("custom_endpoint", config)

	assert.Equal(t, config, rl.limits["custom_endpoint"])
}

func TestInMemoryRateLimiter_SetLimit_NilLimits(t *testing.T) {
	rl := &InMemoryRateLimiter{
		attempts: make(map[string]*inMemoryRateLimitEntry),
		limits:   nil,
	}

	config := RateLimitConfig{
		MaxAttempts: 50,
		Window:      time.Minute,
	}
	rl.SetLimit("test", config)

	assert.NotNil(t, rl.limits)
	assert.Equal(t, config, rl.limits["test"])
}

func TestInMemoryRateLimiter_Allow_NilReceiver(t *testing.T) {
	var rl *InMemoryRateLimiter
	ctx := context.Background()

	allowed, err := rl.Allow(ctx, "test-key", "api_general")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestInMemoryRateLimiter_Allow_Success(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	// First request should be allowed
	allowed, err := rl.Allow(ctx, "test-key", "api_general")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestInMemoryRateLimiter_Allow_RateLimited(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	// Set a very low limit for testing
	rl.SetLimit("test_endpoint", RateLimitConfig{
		MaxAttempts: 2,
		Window:      time.Hour,
	})

	// First two requests should be allowed
	allowed, err := rl.Allow(ctx, "test-key", "test_endpoint")
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, err = rl.Allow(ctx, "test-key", "test_endpoint")
	require.NoError(t, err)
	assert.True(t, allowed)

	// Third request should be blocked
	allowed, err = rl.Allow(ctx, "test-key", "test_endpoint")
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestInMemoryRateLimiter_Allow_WindowExpiry(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	// Set a very short window
	rl.SetLimit("test_endpoint", RateLimitConfig{
		MaxAttempts: 1,
		Window:      10 * time.Millisecond,
	})

	// First request should be allowed
	allowed, err := rl.Allow(ctx, "test-key", "test_endpoint")
	require.NoError(t, err)
	assert.True(t, allowed)

	// Second request should be blocked
	allowed, err = rl.Allow(ctx, "test-key", "test_endpoint")
	require.NoError(t, err)
	assert.False(t, allowed)

	// Wait for window to expire
	time.Sleep(20 * time.Millisecond)

	// Third request should be allowed (window expired)
	allowed, err = rl.Allow(ctx, "test-key", "test_endpoint")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestInMemoryRateLimiter_Allow_DifferentKeys(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	rl.SetLimit("test_endpoint", RateLimitConfig{
		MaxAttempts: 1,
		Window:      time.Hour,
	})

	// First key should be allowed
	allowed, err := rl.Allow(ctx, "key-1", "test_endpoint")
	require.NoError(t, err)
	assert.True(t, allowed)

	// Second key should also be allowed (different key)
	allowed, err = rl.Allow(ctx, "key-2", "test_endpoint")
	require.NoError(t, err)
	assert.True(t, allowed)

	// First key should be blocked
	allowed, err = rl.Allow(ctx, "key-1", "test_endpoint")
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestInMemoryRateLimiter_Allow_FallbackToGeneral(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	// Request with unknown endpoint should use api_general limits
	allowed, err := rl.Allow(ctx, "test-key", "unknown_endpoint")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestInMemoryRateLimiter_AllowWithIP(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	allowed, err := rl.AllowWithIP(ctx, "192.168.1.1", "api_general")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestInMemoryRateLimiter_AllowWithEmail(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	allowed, err := rl.AllowWithEmail(ctx, "user@example.com", "api_general")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestInMemoryRateLimiter_AllowWithUser(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	allowed, err := rl.AllowWithUser(ctx, "user-123", "api_general")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestInMemoryRateLimiter_GarbageCollection(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	// Set very short window for expired entries
	rl.SetLimit("test_endpoint", RateLimitConfig{
		MaxAttempts: 100,
		Window:      1 * time.Millisecond,
	})

	// Create many entries
	for i := 0; i < 1100; i++ {
		key := string(rune('a' + (i % 26)))
		_, err := rl.Allow(ctx, key, "test_endpoint")
		require.NoError(t, err)
	}

	// Wait for entries to expire
	time.Sleep(10 * time.Millisecond)

	// Trigger GC by adding more requests (GC triggers when attempts > 1000)
	// The Allow function should clean up expired entries
	_, err := rl.Allow(ctx, "trigger-gc", "test_endpoint")
	require.NoError(t, err)
}

func TestInMemoryRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	rl.SetLimit("test_endpoint", RateLimitConfig{
		MaxAttempts: 1000,
		Window:      time.Hour,
	})

	// Concurrent access test
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				_, err := rl.Allow(ctx, string(rune('a'+id)), "test_endpoint")
				if err != nil {
					t.Errorf("unexpected error in goroutine: %v", err)
				}
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Regression test for #411: setup_admin must have a dedicated strict rate-limit
// bucket (5/min/IP) and must NOT fall through to the permissive api_general
// config (300/min).
func TestGetDefaultRateLimits_SetupAdminHasDedicatedBucket(t *testing.T) {
	limits := getDefaultRateLimits()

	cfg, ok := limits["setup_admin"]
	if !ok {
		t.Fatal("setup_admin key missing from default rate limits (issue #411)")
	}

	// setup_admin should be far stricter than api_general (300/min)
	apiGeneral := limits["api_general"]
	if cfg.MaxAttempts >= apiGeneral.MaxAttempts {
		t.Errorf("setup_admin limit (%d) is not stricter than api_general (%d); regression of #411",
			cfg.MaxAttempts, apiGeneral.MaxAttempts)
	}

	// Verify the window is present
	if cfg.Window == 0 {
		t.Error("setup_admin rate limit has zero window duration")
	}

	// Verify the window matches the specification from issue #411 (5 per 15 minutes).
	assert.Equal(t, 15*time.Minute, cfg.Window,
		"setup_admin window must be 15 minutes per issue #411")
}

// Regression test for #1016: "register" must have a dedicated strict rate-limit
// bucket and must NOT fall through to the permissive api_general config (300/min).
// An attacker at 300/min can flood the DB with pending-registration rows and
// trigger a synchronous admin-notification email per request (inbox spam + DoS).
func TestGetDefaultRateLimits_RegisterHasDedicatedBucket(t *testing.T) {
	limits := getDefaultRateLimits()

	cfg, ok := limits["register"]
	if !ok {
		t.Fatal("register key missing from default rate limits (issue #1016): add it to getDefaultRateLimits()")
	}

	// Contract from #1016: 5 attempts per 15 minutes.
	assert.Equal(t, 5, cfg.MaxAttempts, "register max attempts must be 5 per issue #1016")
	assert.Equal(t, 15*time.Minute, cfg.Window, "register window must be 15 minutes per issue #1016")

	// register must be far stricter than api_general (300/min).
	apiGeneral := limits["api_general"]
	if cfg.MaxAttempts >= apiGeneral.MaxAttempts {
		t.Errorf("register limit (%d) is not stricter than api_general (%d); regression of #1016",
			cfg.MaxAttempts, apiGeneral.MaxAttempts)
	}

	// Window must be non-zero.
	if cfg.Window == 0 {
		t.Error("register rate limit has zero window duration")
	}
}

// Regression test for #1016: assertRateLimitKeysKnown must panic when an
// unregistered key is supplied, so a future caller typo is caught at startup
// rather than silently falling through to api_general.
func TestAssertRateLimitKeysKnown_PanicsOnUnknownKey(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("assertRateLimitKeysKnown did not panic for an unknown key")
		}
	}()
	assertRateLimitKeysKnown("this_key_does_not_exist_in_the_map")
}

// Regression test for #1016: assertRateLimitKeysKnown must not panic when all
// supplied keys are present in getDefaultRateLimits().
func TestAssertRateLimitKeysKnown_NoopForKnownKeys(t *testing.T) {
	// Should not panic.
	assertRateLimitKeysKnown("login", "setup_admin", "register", "approve_cancel_public")
}

// Regression test for #420: NewInMemoryRateLimiter must be ready at construction
// time (not after a slow lazy init) so Lambda cold-start first requests are protected.
func TestNewInMemoryRateLimiter_ReadyImmediately(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	ctx := context.Background()

	// The limiter must be usable on the very first call without any further setup.
	// Previously, Lambda started with a nil rate limiter that allowed all requests
	// through until the DB connection was established (issue #420).
	allowed, err := rl.AllowWithIP(ctx, "10.0.0.1", "login")
	if err != nil {
		t.Fatalf("AllowWithIP returned unexpected error on first call: %v", err)
	}
	if !allowed {
		t.Fatal("first login attempt should be allowed by a fresh rate limiter")
	}

	// Exhaust the login limit (5 per 15 min)
	loginLimit := getDefaultRateLimits()["login"]
	for i := 1; i < loginLimit.MaxAttempts; i++ {
		_, _ = rl.AllowWithIP(ctx, "10.0.0.1", "login")
	}
	blocked, err := rl.AllowWithIP(ctx, "10.0.0.1", "login")
	if err != nil {
		t.Fatalf("unexpected error after exhausting limit: %v", err)
	}
	if blocked {
		t.Errorf("expected login to be blocked after %d attempts; rate limiter was not enforcing (regression of #420)", loginLimit.MaxAttempts)
	}
}
