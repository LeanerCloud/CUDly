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
