// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"time"
)

// RateLimitConfig defines the rate limiting parameters for a specific endpoint/operation
type RateLimitConfig struct {
	MaxAttempts int           // Maximum number of attempts allowed
	WindowSecs  int           // Time window in seconds
	Window      time.Duration // Computed time window (for convenience)
}

// NewRateLimitConfig creates a new RateLimitConfig
func NewRateLimitConfig(maxAttempts int, windowSecs int) RateLimitConfig {
	return RateLimitConfig{
		MaxAttempts: maxAttempts,
		WindowSecs:  windowSecs,
		Window:      time.Duration(windowSecs) * time.Second,
	}
}

// getDefaultRateLimits returns default rate limit configurations
func getDefaultRateLimits() map[string]RateLimitConfig {
	return map[string]RateLimitConfig{
		"login":           NewRateLimitConfig(5, 15*60),  // 5 attempts / 15 minutes / IP
		"forgot_password": NewRateLimitConfig(10, 5*60),  // 10 attempts / 5 minutes / email
		"reset_password":  NewRateLimitConfig(10, 15*60), // 10 attempts / 15 minutes / IP
		"change_password": NewRateLimitConfig(10, 15*60), // 10 attempts / 15 minutes / IP
		"api_general":     NewRateLimitConfig(300, 60),   // 300 requests / minute / IP
		"admin":           NewRateLimitConfig(30, 60),    // 30 / minute / user
	}
}

// newRateLimiter creates a new rate limiter
func newRateLimiter() *RateLimiter {
	return &RateLimiter{
		attempts: make(map[string]*rateLimitEntry),
	}
}

// Allow checks if a request should be allowed based on rate limits
func (rl *RateLimiter) Allow(key string, maxAttempts int, window time.Duration) bool {
	// Handle nil RateLimiter (for testing or when not configured)
	if rl == nil {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, exists := rl.attempts[key]

	// Clean up expired entries periodically
	if len(rl.attempts) > 1000 {
		for k, v := range rl.attempts {
			if now.After(v.resetTime) {
				delete(rl.attempts, k)
			}
		}
	}

	if !exists || now.After(entry.resetTime) {
		rl.attempts[key] = &rateLimitEntry{
			count:     1,
			resetTime: now.Add(window),
		}
		return true
	}

	if entry.count >= maxAttempts {
		return false
	}

	entry.count++
	return true
}
