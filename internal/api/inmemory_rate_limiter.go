// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// inMemoryRateLimitMaxEntries is the hard cap on the number of live entries in
// InMemoryRateLimiter. When reached, expired entries are evicted first; if the
// map is still at or above the cap, the oldest live entries (by resetTime) are
// removed until the count falls to 80% of the cap. This prevents unbounded
// memory growth from rotating attacker source IPs (02-M3).
const inMemoryRateLimitMaxEntries = 500

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

	// evictEntries enforces a hard cap (inMemoryRateLimitMaxEntries) on the map
	// to prevent unbounded memory growth from rotating attacker IPs (02-M3).
	//
	// Step 1: purge already-expired entries (free, always useful).
	// Step 2: if still over cap after step 1, evict the oldest live entries
	//         (smallest resetTime first) until we are back at 80% of cap.
	if len(rl.attempts) >= inMemoryRateLimitMaxEntries {
		for k, v := range rl.attempts {
			if now.After(v.resetTime) {
				delete(rl.attempts, k)
			}
		}
		if len(rl.attempts) >= inMemoryRateLimitMaxEntries {
			// Collect remaining keys sorted by resetTime ascending.
			type kv struct {
				key string
				t   time.Time
			}
			pairs := make([]kv, 0, len(rl.attempts))
			for k, v := range rl.attempts {
				pairs = append(pairs, kv{k, v.resetTime})
			}
			sort.Slice(pairs, func(i, j int) bool {
				return pairs[i].t.Before(pairs[j].t)
			})
			target := inMemoryRateLimitMaxEntries * 4 / 5 // evict down to 80%
			for i := 0; len(rl.attempts) > target && i < len(pairs); i++ {
				delete(rl.attempts, pairs[i].key)
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
