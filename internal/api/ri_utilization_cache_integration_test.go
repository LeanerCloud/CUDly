//go:build integration
// +build integration

package api

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	"github.com/stretchr/testify/require"
)

// createRIUtilizationCacheTable creates only the schema this test
// suite needs. We deliberately skip the full migration set because
// migration 000027 is non-idempotent on a fresh DB (tries to ADD
// PRIMARY KEY that migration 000018 already added) — that's a
// pre-existing bug tracked separately and outside this commit's
// scope. By creating the table directly we keep the integration
// test for commit A/B's cache behaviour self-contained and not
// blocked on the unrelated migration-order issue.
func createRIUtilizationCacheTable(ctx context.Context, t *testing.T, db *database.Connection) {
	t.Helper()
	_, err := db.Pool().Exec(ctx, `
		CREATE TABLE IF NOT EXISTS ri_utilization_cache (
		    region        TEXT NOT NULL,
		    lookback_days INT  NOT NULL,
		    payload       JSONB NOT NULL,
		    fetched_at    TIMESTAMPTZ NOT NULL,
		    PRIMARY KEY (region, lookback_days)
		);
	`)
	require.NoError(t, err)
}

func setupRICacheIntegration(ctx context.Context, t *testing.T) (*config.PostgresStore, func()) {
	t.Helper()
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	createRIUtilizationCacheTable(ctx, t, container.DB)
	store := config.NewPostgresStore(container.DB)
	return store, func() { _ = container.Cleanup(ctx) }
}

// TestRIUtilizationCache_Integration_ColdReadFetchesAndPersists verifies
// that the first read with no existing row triggers the fetcher and
// persists the result in ri_utilization_cache.
func TestRIUtilizationCache_Integration_ColdReadFetchesAndPersists(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRICacheIntegration(ctx, t)
	defer cleanup()

	cache := newRIUtilizationCache(store, false)
	fetched := []recommendations.RIUtilization{
		{ReservedInstanceID: "ri-integration-1", UtilizationPercent: 90.0, PurchasedHours: 720, TotalActualHours: 648},
	}
	var calls atomic.Int32
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return fetched, nil
	}

	got, err := cache.getOrFetch(ctx, "us-east-1", 30, time.Hour, 2*time.Hour, fetch)
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())
	require.Len(t, got, 1)
	require.Equal(t, "ri-integration-1", got[0].ReservedInstanceID)

	// Postgres row must be present with the correct payload.
	entry, err := store.GetRIUtilizationCache(ctx, "us-east-1", 30)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, "us-east-1", entry.Region)
	require.Equal(t, 30, entry.LookbackDays)
	var round []recommendations.RIUtilization
	require.NoError(t, json.Unmarshal(entry.Payload, &round))
	require.Equal(t, fetched, round)
}

// TestRIUtilizationCache_Integration_FreshHitNoFetch verifies that a
// second read within the soft TTL serves from Postgres without
// invoking the fetcher.
func TestRIUtilizationCache_Integration_FreshHitNoFetch(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRICacheIntegration(ctx, t)
	defer cleanup()

	cache := newRIUtilizationCache(store, false)
	fetched := []recommendations.RIUtilization{{ReservedInstanceID: "ri-fresh-1"}}
	var calls atomic.Int32
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return fetched, nil
	}

	// Prime the cache.
	_, err := cache.getOrFetch(ctx, "us-east-1", 30, time.Hour, 2*time.Hour, fetch)
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())

	// Second read: well within soft TTL.
	_, err = cache.getOrFetch(ctx, "us-east-1", 30, time.Hour, 2*time.Hour, fetch)
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load(), "fresh hit must not trigger a fetch")
}

// TestRIUtilizationCache_Integration_StaleNonLambdaServesStaleAndRefreshes
// verifies SWR end-to-end: stale row served synchronously, background
// refresh fires, Postgres row is updated within a short window.
func TestRIUtilizationCache_Integration_StaleNonLambdaServesStaleAndRefreshes(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRICacheIntegration(ctx, t)
	defer cleanup()

	staleData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-stale"}}
	freshData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-fresh"}}

	// Seed a row whose age is between soft (50ms) and hard (1h).
	stalePayload, err := json.Marshal(staleData)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRIUtilizationCache(ctx, "us-east-1", 30, stalePayload, time.Now().Add(-200*time.Millisecond)))

	cache := newRIUtilizationCache(store, false) // non-Lambda
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		return freshData, nil
	}

	start := time.Now()
	got, err := cache.getOrFetch(ctx, "us-east-1", 30, 50*time.Millisecond, time.Hour, fetch)
	require.NoError(t, err)
	require.Less(t, time.Since(start), 100*time.Millisecond, "stale-read must return synchronously, not wait for background fetch")
	require.Equal(t, "ri-stale", got[0].ReservedInstanceID, "first call must return the stale copy")

	// Wait for the background refresh to land the fresh data.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entry, err := store.GetRIUtilizationCache(ctx, "us-east-1", 30)
		if err == nil && entry != nil {
			var round []recommendations.RIUtilization
			_ = json.Unmarshal(entry.Payload, &round)
			if len(round) == 1 && round[0].ReservedInstanceID == "ri-fresh" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background refresh did not update the ri_utilization_cache row within 2s")
}

// TestRIUtilizationCache_Integration_HardExpiredSyncRefetch verifies
// that rows beyond the hard TTL force a synchronous refetch even on
// non-Lambda: the caller blocks until fresh data arrives.
func TestRIUtilizationCache_Integration_HardExpiredSyncRefetch(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRICacheIntegration(ctx, t)
	defer cleanup()

	staleData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-stale"}}
	freshData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-fresh"}}

	stalePayload, err := json.Marshal(staleData)
	require.NoError(t, err)
	// Age 2h exceeds both soft (15m) and hard (1h).
	require.NoError(t, store.UpsertRIUtilizationCache(ctx, "us-east-1", 30, stalePayload, time.Now().Add(-2*time.Hour)))

	cache := newRIUtilizationCache(store, false)
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		return freshData, nil
	}

	got, err := cache.getOrFetch(ctx, "us-east-1", 30, 15*time.Minute, time.Hour, fetch)
	require.NoError(t, err)
	require.Equal(t, "ri-fresh", got[0].ReservedInstanceID, "hard-expired entry must force sync refetch")

	// Postgres row should have the fresh fetched_at timestamp, not
	// the 2h-old one we seeded.
	entry, err := store.GetRIUtilizationCache(ctx, "us-east-1", 30)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Less(t, time.Since(entry.FetchedAt), 5*time.Second, "fetched_at must be updated after sync refetch")
}
