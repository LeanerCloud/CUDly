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
// This implementation should NOT be used for Lambda (multi-instance) - use DBRateLimiter instead.
type InMemoryRateLimiter struct {
	attempts map[string]*inMemoryRateLimitEntry
	limits   map[string]RateLimitConfig
	mu       sync.Mutex
}

type inMemoryRateLimitEntry struct {
	resetTime time.Time
	count     int
}

// Verify that InMemoryRateLimiter implements RateLimiterInterface.
var _ RateLimiterInterface = (*InMemoryRateLimiter)(nil)

// NewInMemoryRateLimiter creates a new in-memory rate limiter for single-instance deployments.
func NewInMemoryRateLimiter() *InMemoryRateLimiter {
	return &InMemoryRateLimiter{
		attempts: make(map[string]*inMemoryRateLimitEntry),
		limits:   getDefaultRateLimits(),
	}
}

// SetLimit allows customizing rate limits for specific endpoints.
func (rl *InMemoryRateLimiter) SetLimit(endpoint string, config RateLimitConfig) {
	if rl.limits == nil {
		rl.limits = make(map[string]RateLimitConfig)
	}
	rl.limits[endpoint] = config
}

// configFor returns the rate limit configuration for an endpoint, falling back
// to the api_general bucket when no specific entry exists.
func (rl *InMemoryRateLimiter) configFor(endpoint string) RateLimitConfig {
	if cfg, ok := rl.limits[endpoint]; ok {
		return cfg
	}
	return rl.limits["api_general"]
}

// evictIfAtCap enforces the hard cap on the attempts map (02-M3).
//
// Step 1: purge already-expired entries (free, always useful).
// Step 2: if still over cap, evict the oldest live entries (smallest
// resetTime first) until the count falls to 80% of the cap.
//
// Must be called with rl.mu held.
func (rl *InMemoryRateLimiter) evictIfAtCap(now time.Time) {
	if len(rl.attempts) < inMemoryRateLimitMaxEntries {
		return
	}
	for k, v := range rl.attempts {
		if now.After(v.resetTime) {
			delete(rl.attempts, k)
		}
	}
	if len(rl.attempts) < inMemoryRateLimitMaxEntries {
		return
	}
	rl.evictOldest()
}

// evictOldest removes the oldest live entries until the map is at 80% of cap.
// Must be called with rl.mu held.
func (rl *InMemoryRateLimiter) evictOldest() {
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
	target := inMemoryRateLimitMaxEntries * 4 / 5
	for i := 0; len(rl.attempts) > target && i < len(pairs); i++ {
		delete(rl.attempts, pairs[i].key)
	}
}

// Allow checks if a request should be allowed based on rate limits.
// The key should be formatted as "IP#{ip}" or "EMAIL#{email}".
// The endpoint identifies which rate limit configuration to use.
func (rl *InMemoryRateLimiter) Allow(ctx context.Context, key string, endpoint string) (bool, error) { //nolint:gocritic // paramTypeCombine: explicit types aid readability
	if rl == nil {
		return true, nil
	}

	config := rl.configFor(endpoint)
	id := fmt.Sprintf("%s#ENDPOINT#%s", key, endpoint)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rl.evictIfAtCap(now)

	entry, exists := rl.attempts[id]
	if !exists || now.After(entry.resetTime) {
		rl.attempts[id] = &inMemoryRateLimitEntry{
			count:     1,
			resetTime: now.Add(config.Window),
		}
		return true, nil
	}

	if entry.count >= config.MaxAttempts {
		return false, nil
	}

	entry.count++
	return true, nil
}

// AllowWithIP is a convenience method that formats the key as an IP-based key.
func (rl *InMemoryRateLimiter) AllowWithIP(ctx context.Context, ip string, endpoint string) (bool, error) { //nolint:gocritic // paramTypeCombine: explicit types aid readability
	key := fmt.Sprintf("IP#%s", ip)
	return rl.Allow(ctx, key, endpoint)
}

// AllowWithEmail is a convenience method that formats the key as an email-based key.
func (rl *InMemoryRateLimiter) AllowWithEmail(ctx context.Context, email string, endpoint string) (bool, error) { //nolint:gocritic // paramTypeCombine: explicit types aid readability
	key := fmt.Sprintf("EMAIL#%s", email)
	return rl.Allow(ctx, key, endpoint)
}

// AllowWithUser is a convenience method that formats the key as a user-based key.
func (rl *InMemoryRateLimiter) AllowWithUser(ctx context.Context, userID string, endpoint string) (bool, error) { //nolint:gocritic // paramTypeCombine: explicit types aid readability
	key := fmt.Sprintf("USER#%s", userID)
	return rl.Allow(ctx, key, endpoint)
}
