// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// InMemoryRateLimiter provides in-memory rate limiting for single-instance deployments (Fargate, ECS)
// This implementation should NOT be used for Lambda (multi-instance) - use DBRateLimiter instead
type InMemoryRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*inMemoryRateLimitEntry
	limits   map[string]RateLimitConfig // endpoint -> config
}

type inMemoryRateLimitEntry struct {
	count     int
	resetTime time.Time
}

// Verify that InMemoryRateLimiter implements RateLimiterInterface
var _ RateLimiterInterface = (*InMemoryRateLimiter)(nil)

// NewInMemoryRateLimiter creates a new in-memory rate limiter for single-instance deployments
func NewInMemoryRateLimiter() *InMemoryRateLimiter {
	return &InMemoryRateLimiter{
		attempts: make(map[string]*inMemoryRateLimitEntry),
		limits:   getDefaultRateLimits(),
	}
}

// SetLimit allows customizing rate limits for specific endpoints
func (rl *InMemoryRateLimiter) SetLimit(endpoint string, config RateLimitConfig) {
	if rl.limits == nil {
		rl.limits = make(map[string]RateLimitConfig)
	}
	rl.limits[endpoint] = config
}

// Allow checks if a request should be allowed based on rate limits
// The key should be formatted as "IP#{ip}" or "EMAIL#{email}"
// The endpoint identifies which rate limit configuration to use
func (rl *InMemoryRateLimiter) Allow(ctx context.Context, key string, endpoint string) (bool, error) {
	// Handle nil rate limiter (for testing or when not configured)
	if rl == nil {
		return true, nil
	}

	// Get the rate limit configuration for this endpoint
	config, exists := rl.limits[endpoint]
	if !exists {
		// Default to general API limits if endpoint not specifically configured
		config = rl.limits["api_general"]
	}

	// Create the unique identifier combining the key and endpoint
	id := fmt.Sprintf("%s#ENDPOINT#%s", key, endpoint)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, exists := rl.attempts[id]

	// Clean up expired entries periodically (simple garbage collection)
	if len(rl.attempts) > 1000 {
		for k, v := range rl.attempts {
			if now.After(v.resetTime) {
				delete(rl.attempts, k)
			}
		}
	}

	if !exists || now.After(entry.resetTime) {
		// No entry or window expired - create new/reset
		rl.attempts[id] = &inMemoryRateLimitEntry{
			count:     1,
			resetTime: now.Add(config.Window),
		}
		return true, nil
	}

	// Window is still active, check if limit exceeded
	if entry.count >= config.MaxAttempts {
		// Limit exceeded
		return false, nil
	}

	// Increment the counter
	entry.count++
	return true, nil
}

// AllowWithIP is a convenience method that formats the key as an IP-based key
func (rl *InMemoryRateLimiter) AllowWithIP(ctx context.Context, ip string, endpoint string) (bool, error) {
	key := fmt.Sprintf("IP#%s", ip)
	return rl.Allow(ctx, key, endpoint)
}

// AllowWithEmail is a convenience method that formats the key as an email-based key
func (rl *InMemoryRateLimiter) AllowWithEmail(ctx context.Context, email string, endpoint string) (bool, error) {
	key := fmt.Sprintf("EMAIL#%s", email)
	return rl.Allow(ctx, key, endpoint)
}

// AllowWithUser is a convenience method that formats the key as a user-based key
func (rl *InMemoryRateLimiter) AllowWithUser(ctx context.Context, userID string, endpoint string) (bool, error) {
	key := fmt.Sprintf("USER#%s", userID)
	return rl.Allow(ctx, key, endpoint)
}
