// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBRateLimiter provides distributed rate limiting using the database
// This implementation uses a sliding window algorithm with the database as the backend,
// making it suitable for Lambda functions and distributed systems.
type DBRateLimiter struct {
	pool            *pgxpool.Pool
	limits          map[string]RateLimitConfig // endpoint -> config
	limitsMu        sync.RWMutex
	lastCleanup     time.Time
	cleanupMu       sync.Mutex
	cleanupRunning  atomic.Bool
	cleanupInterval time.Duration
}

// Verify that DBRateLimiter implements RateLimiterInterface
var _ RateLimiterInterface = (*DBRateLimiter)(nil)

// NewDBRateLimiter creates a new database-backed rate limiter
func NewDBRateLimiter(pool *pgxpool.Pool) *DBRateLimiter {
	return &DBRateLimiter{
		pool:            pool,
		limits:          getDefaultRateLimits(),
		cleanupInterval: 60 * time.Second, // Only cleanup at most once per minute
	}
}

// SetLimit allows customizing rate limits for specific endpoints
func (rl *DBRateLimiter) SetLimit(endpoint string, config RateLimitConfig) {
	rl.limitsMu.Lock()
	defer rl.limitsMu.Unlock()
	if rl.limits == nil {
		rl.limits = make(map[string]RateLimitConfig)
	}
	rl.limits[endpoint] = config
}

// Allow checks if a request should be allowed based on rate limits.
// The key should be formatted as "IP#{ip}" or "EMAIL#{email}".
// The endpoint identifies which rate limit configuration to use.
//
// Implementation: a single atomic INSERT ... ON CONFLICT DO UPDATE
// statement performs the read-modify-write in one round trip, so two
// concurrent first-requests for the same id can never collide on the
// PK (the older check-then-insert flow hit SQLSTATE 23505 in
// production — see commit 9fa4170a1's sibling note in
// known_issues/05_config_store_postgres.md).
//
// Behaviour: each call increments `count` (or resets to 1 if the
// window has expired). The returned `count` is then compared to
// `config.MaxAttempts` to decide allow/deny. `count` may temporarily
// drift past MaxAttempts under sustained over-limit traffic — the
// rate limiter still denies correctly, and `cleanup()` evicts
// expired rows on its 24-hour cycle. This is a small accounting
// trade for atomicity and is acceptable for rate-limit semantics.
func (rl *DBRateLimiter) Allow(ctx context.Context, key string, endpoint string) (bool, error) {
	if rl == nil || rl.pool == nil {
		return true, nil
	}

	rl.limitsMu.RLock()
	config, exists := rl.limits[endpoint]
	if !exists {
		config = rl.limits["api_general"]
	}
	rl.limitsMu.RUnlock()

	id := fmt.Sprintf("%s#ENDPOINT#%s", key, endpoint)
	now := time.Now()
	newResetTime := now.Add(config.Window)

	var count int
	var resetTime time.Time
	err := rl.pool.QueryRow(ctx, `
		INSERT INTO rate_limits (id, count, reset_time, created_at, updated_at)
		VALUES ($1, 1, $2, $3, $3)
		ON CONFLICT (id) DO UPDATE SET
			count      = CASE WHEN rate_limits.reset_time < $3
			                  THEN 1
			                  ELSE rate_limits.count + 1 END,
			reset_time = CASE WHEN rate_limits.reset_time < $3
			                  THEN EXCLUDED.reset_time
			                  ELSE rate_limits.reset_time END,
			updated_at = $3
		RETURNING count, reset_time
	`, id, newResetTime, now).Scan(&count, &resetTime)
	if err != nil {
		return false, fmt.Errorf("rate limit upsert failed: %w", err)
	}

	allowed := count <= config.MaxAttempts
	// count == 1 means either a fresh insert OR a window-reset; both are
	// good signals that some rows may have aged out and a cleanup pass is
	// worthwhile.
	if allowed && count == 1 {
		rl.maybeCleanup()
	}
	return allowed, nil
}

// maybeCleanup triggers cleanup if enough time has passed since the last cleanup
// This prevents spawning too many goroutines when under high load
func (rl *DBRateLimiter) maybeCleanup() {
	// Quick check without lock - if cleanup is already running, skip
	if rl.cleanupRunning.Load() {
		return
	}

	rl.cleanupMu.Lock()
	// Check if enough time has passed since last cleanup
	if time.Since(rl.lastCleanup) < rl.cleanupInterval {
		rl.cleanupMu.Unlock()
		return
	}

	// Mark cleanup as running and update timestamp
	if !rl.cleanupRunning.CompareAndSwap(false, true) {
		rl.cleanupMu.Unlock()
		return
	}
	rl.lastCleanup = time.Now()
	rl.cleanupMu.Unlock()

	// Run cleanup in background
	go func() {
		defer rl.cleanupRunning.Store(false)
		rl.cleanup()
	}()
}

// cleanup removes expired rate limit entries from the database
// This is called asynchronously and errors are logged but not returned
func (rl *DBRateLimiter) cleanup() {
	if rl.pool == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Delete entries that expired more than 24 hours ago
	result, err := rl.pool.Exec(ctx,
		`DELETE FROM rate_limits WHERE reset_time < NOW() - INTERVAL '24 hours'`,
	)
	if err != nil {
		logging.Warnf("Failed to cleanup expired rate limits: %v", err)
		return
	}

	if result.RowsAffected() > 0 {
		logging.Debugf("Cleaned up %d expired rate limit entries", result.RowsAffected())
	}
}

// AllowWithIP is a convenience method that formats the key as an IP-based key
func (rl *DBRateLimiter) AllowWithIP(ctx context.Context, ip string, endpoint string) (bool, error) {
	key := fmt.Sprintf("IP#%s", ip)
	return rl.Allow(ctx, key, endpoint)
}

// AllowWithEmail is a convenience method that formats the key as an email-based key
func (rl *DBRateLimiter) AllowWithEmail(ctx context.Context, email string, endpoint string) (bool, error) {
	key := fmt.Sprintf("EMAIL#%s", email)
	return rl.Allow(ctx, key, endpoint)
}

// AllowWithUser is a convenience method that formats the key as a user-based key
func (rl *DBRateLimiter) AllowWithUser(ctx context.Context, userID string, endpoint string) (bool, error) {
	key := fmt.Sprintf("USER#%s", userID)
	return rl.Allow(ctx, key, endpoint)
}
