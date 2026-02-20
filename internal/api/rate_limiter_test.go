package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimiter_Allow(t *testing.T) {
	t.Run("nil rate limiter allows all requests", func(t *testing.T) {
		var rl *RateLimiter
		assert.True(t, rl.Allow("test-key", 5, time.Minute))
	})

	t.Run("allows requests within limit", func(t *testing.T) {
		rl := newRateLimiter()
		key := "test-key"
		maxAttempts := 3
		window := time.Minute

		// First three attempts should succeed
		assert.True(t, rl.Allow(key, maxAttempts, window))
		assert.True(t, rl.Allow(key, maxAttempts, window))
		assert.True(t, rl.Allow(key, maxAttempts, window))

		// Fourth attempt should fail
		assert.False(t, rl.Allow(key, maxAttempts, window))
	})

	t.Run("different keys have separate limits", func(t *testing.T) {
		rl := newRateLimiter()
		maxAttempts := 2
		window := time.Minute

		// Key 1 uses up its limit
		assert.True(t, rl.Allow("key1", maxAttempts, window))
		assert.True(t, rl.Allow("key1", maxAttempts, window))
		assert.False(t, rl.Allow("key1", maxAttempts, window))

		// Key 2 should still have its own limit
		assert.True(t, rl.Allow("key2", maxAttempts, window))
		assert.True(t, rl.Allow("key2", maxAttempts, window))
		assert.False(t, rl.Allow("key2", maxAttempts, window))
	})

	t.Run("resets after window expires", func(t *testing.T) {
		rl := newRateLimiter()
		key := "test-key"
		maxAttempts := 2
		window := 10 * time.Millisecond

		// Use up the limit
		assert.True(t, rl.Allow(key, maxAttempts, window))
		assert.True(t, rl.Allow(key, maxAttempts, window))
		assert.False(t, rl.Allow(key, maxAttempts, window))

		// Wait for window to expire
		time.Sleep(20 * time.Millisecond)

		// Should be allowed again
		assert.True(t, rl.Allow(key, maxAttempts, window))
	})

	t.Run("single attempt limit", func(t *testing.T) {
		rl := newRateLimiter()
		key := "test-key"

		assert.True(t, rl.Allow(key, 1, time.Minute))
		assert.False(t, rl.Allow(key, 1, time.Minute))
	})
}

func TestNewRateLimiter(t *testing.T) {
	rl := newRateLimiter()
	assert.NotNil(t, rl)
	assert.NotNil(t, rl.attempts)
}
