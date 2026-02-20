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
	if rl.limits == nil {
		rl.limits = make(map[string]RateLimitConfig)
	}
	rl.limits[endpoint] = config
}

// Allow checks if a request should be allowed based on rate limits
// The key should be formatted as "IP#{ip}" or "EMAIL#{email}"
// The endpoint identifies which rate limit configuration to use
func (rl *DBRateLimiter) Allow(ctx context.Context, key string, endpoint string) (bool, error) {
	// Handle nil rate limiter (for testing or when not configured)
	if rl == nil || rl.pool == nil {
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

	now := time.Now()
	resetTime := now.Add(config.Window)

	// Use transaction to ensure atomic read-modify-write
	tx, err := rl.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) // Will be no-op if committed

	// Try to get existing rate limit entry with row lock
	var count int
	var existingResetTime time.Time
	err = tx.QueryRow(ctx,
		`SELECT count, reset_time FROM rate_limits WHERE id = $1 FOR UPDATE`,
		id,
	).Scan(&count, &existingResetTime)

	if err != nil && err.Error() == "no rows in result set" {
		// No existing entry, create a new one
		_, err = tx.Exec(ctx,
			`INSERT INTO rate_limits (id, count, reset_time, created_at, updated_at)
			 VALUES ($1, 1, $2, $3, $3)`,
			id, resetTime, now,
		)
		if err != nil {
			return false, fmt.Errorf("failed to create rate limit entry: %w", err)
		}

		if err = tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("failed to commit transaction: %w", err)
		}

		// Periodically clean up old entries (throttled, non-blocking)
		rl.maybeCleanup()

		return true, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to query rate limit entry: %w", err)
	}

	// Check if the window has expired
	if now.After(existingResetTime) {
		// Window expired, reset the counter
		_, err = tx.Exec(ctx,
			`UPDATE rate_limits SET count = 1, reset_time = $1, updated_at = $2 WHERE id = $3`,
			resetTime, now, id,
		)
		if err != nil {
			return false, fmt.Errorf("failed to reset rate limit entry: %w", err)
		}

		if err = tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("failed to commit transaction: %w", err)
		}

		return true, nil
	}

	// Window is still active, check if limit exceeded
	if count >= config.MaxAttempts {
		// Limit exceeded - commit to release lock
		if err = tx.Commit(ctx); err != nil {
			logging.Warnf("Failed to commit after rate limit exceeded: %v", err)
		}
		return false, nil
	}

	// Increment the counter
	newCount := count + 1
	_, err = tx.Exec(ctx,
		`UPDATE rate_limits SET count = $1, updated_at = $2 WHERE id = $3`,
		newCount, now, id,
	)
	if err != nil {
		return false, fmt.Errorf("failed to increment rate limit counter: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return true, nil
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
