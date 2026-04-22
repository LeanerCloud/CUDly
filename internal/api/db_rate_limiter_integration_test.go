//go:build integration
// +build integration

package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRateLimitIntegration spins up a testcontainers Postgres, runs
// the full migration chain (so the rate_limits table from migration
// 000004 is present), and returns the pool + cleanup. Mirrors the
// helper in ri_utilization_cache_integration_test.go.
func setupRateLimitIntegration(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	err = migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", "")
	require.NoError(t, err)
	return container.DB.Pool(), func() { _ = container.Cleanup(ctx) }
}

// TestDBRateLimiter_ConcurrentFirstRequest pins the contract that the
// production bug
//
//	ERROR: duplicate key value violates unique constraint
//	"rate_limits_pkey" (SQLSTATE 23505)
//
// (logged 2026-04-21T18:06:57Z) is fixed: when N goroutines hit the
// same id at once, the atomic upsert gives every caller a successful
// allow/deny return — no INSERT collision, no spurious 500.
func TestDBRateLimiter_ConcurrentFirstRequest(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupRateLimitIntegration(ctx, t)
	defer cleanup()

	rl := NewDBRateLimiter(pool)
	// Set a high MaxAttempts so all N goroutines are allowed and we
	// can assert the count rather than allow/deny mix.
	const N = 20
	rl.SetLimit("test-endpoint", NewRateLimitConfig(N+10, 60))

	var wg sync.WaitGroup
	var allowedCount, errCount atomic.Int32
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, err := rl.Allow(ctx, "IP#1.2.3.4", "test-endpoint")
			if err != nil {
				errCount.Add(1)
				return
			}
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(0), errCount.Load(), "no SQLSTATE 23505 (or any other error) should surface")
	assert.Equal(t, int32(N), allowedCount.Load(), "all N concurrent first-requests must be allowed")

	// Verify the row is present and the count == N.
	var count int
	err := pool.QueryRow(ctx,
		`SELECT count FROM rate_limits WHERE id = 'IP#1.2.3.4#ENDPOINT#test-endpoint'`,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, N, count, "final stored count should equal the number of concurrent calls")
}

// TestDBRateLimiter_WindowExpiry_AtomicReset pins the
// "expired-window resets count to 1" branch of the new ON CONFLICT
// CASE: when a row exists but its reset_time is in the past, the next
// call must reset count to 1 and push reset_time forward.
func TestDBRateLimiter_WindowExpiry_AtomicReset(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupRateLimitIntegration(ctx, t)
	defer cleanup()

	rl := NewDBRateLimiter(pool)
	const winSecs = 60
	rl.SetLimit("expiry-endpoint", NewRateLimitConfig(5, winSecs))

	id := "IP#5.6.7.8#ENDPOINT#expiry-endpoint"
	pastReset := time.Now().Add(-time.Hour)
	now := time.Now()
	_, err := pool.Exec(ctx,
		`INSERT INTO rate_limits (id, count, reset_time, created_at, updated_at)
		 VALUES ($1, 99, $2, $3, $3)`,
		id, pastReset, now,
	)
	require.NoError(t, err)

	allowed, err := rl.Allow(ctx, "IP#5.6.7.8", "expiry-endpoint")
	require.NoError(t, err)
	assert.True(t, allowed, "first call after window expired should be allowed")

	var count int
	var resetTime time.Time
	err = pool.QueryRow(ctx,
		`SELECT count, reset_time FROM rate_limits WHERE id = $1`, id,
	).Scan(&count, &resetTime)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "expired row must be reset to count = 1, not incremented from 99")
	assert.True(t, resetTime.After(now),
		"reset_time must be pushed forward (got %s, now is %s)", resetTime, now)
}

// TestDBRateLimiter_ExceedsLimitDenies sanity-checks the deny path: a
// burst that exceeds MaxAttempts within a single window must produce
// some `false` returns. With the always-increment approach we expect
// MaxAttempts allows followed by deny(s); count may exceed
// MaxAttempts after the burst, which is documented behaviour.
func TestDBRateLimiter_ExceedsLimitDenies(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := setupRateLimitIntegration(ctx, t)
	defer cleanup()

	rl := NewDBRateLimiter(pool)
	const max = 3
	rl.SetLimit("low-limit-endpoint", NewRateLimitConfig(max, 60))

	results := make([]bool, 5)
	for i := range results {
		allowed, err := rl.Allow(ctx, "IP#9.9.9.9", "low-limit-endpoint")
		require.NoError(t, err)
		results[i] = allowed
	}

	// First `max` calls allowed, rest denied.
	for i := 0; i < max; i++ {
		assert.True(t, results[i], "call %d should be allowed (within limit)", i+1)
	}
	for i := max; i < len(results); i++ {
		assert.False(t, results[i], "call %d should be denied (over limit)", i+1)
	}
}
