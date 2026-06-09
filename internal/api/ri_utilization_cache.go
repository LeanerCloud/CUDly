package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	"golang.org/x/sync/singleflight"
)

// defaultRIUtilizationCacheTTL is the soft-freshness window: reads
// within this window serve the cached row and issue NO Cost Explorer
// call. 15 minutes matches CE's hourly upstream refresh cadence with
// headroom for dashboard users to expect recent numbers.
const defaultRIUtilizationCacheTTL = 15 * time.Minute

// defaultRIUtilizationCacheStaleTTL is the hard-expiry window: rows
// older than this are considered unusable and force a synchronous
// refetch even on non-Lambda. 30 minutes (2× soft) gives the SWR path
// enough room to kick a background refresh without stale reads
// dragging on indefinitely if the refresh fails.
const defaultRIUtilizationCacheStaleTTL = 30 * time.Minute

// riUtilizationCacheTTL + riUtilizationCacheStaleTTL are resolved once
// from env vars via time.ParseDuration. Declared as vars (not consts)
// so tests can overwrite them. Invalid values fall back to the
// defaults with a log line.
var (
	riUtilizationCacheTTL      = resolveDurationEnv("CUDLY_RI_UTILIZATION_CACHE_TTL", defaultRIUtilizationCacheTTL)
	riUtilizationCacheStaleTTL = resolveDurationEnv("CUDLY_RI_UTILIZATION_CACHE_STALE_TTL", defaultRIUtilizationCacheStaleTTL)
)

func resolveDurationEnv(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		fmt.Fprintf(os.Stderr, "%s invalid (%q); using default %s\n", name, v, def)
		return def
	}
	return d
}

// riUtilizationCacheStore is the narrow slice of config.StoreInterface
// that the cache needs. Declared here (and satisfied structurally) so
// tests can inject a fake without depending on the full store.
type riUtilizationCacheStore interface {
	GetRIUtilizationCache(ctx context.Context, region string, lookbackDays int) (*config.RIUtilizationCacheEntry, error)
	UpsertRIUtilizationCache(ctx context.Context, region string, lookbackDays int, payload []byte, fetchedAt time.Time) error
}

// riUtilizationFetcher is the signature of the function that retrieves
// fresh data from Cost Explorer. Parameterising lets tests inject a
// fake.
type riUtilizationFetcher func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error)

// riUtilizationCache wraps a Postgres-backed store with stale-while-
// revalidate semantics on non-Lambda runtimes. Lambda containers can't
// safely run background goroutines (they freeze between invocations)
// so on Lambda the cache falls back to synchronous fetch-on-stale —
// today's behaviour. Non-Lambda runtimes get SWR: stale rows are
// served immediately while a detached goroutine refreshes the row for
// the next reader.
//
// The singleflight.Group ensures that N concurrent stale-reads for the
// same (region, lookback) tuple collapse to exactly one background
// refresh, avoiding a thundering-herd CE fan-out.
type riUtilizationCache struct {
	store    riUtilizationCacheStore
	isLambda bool
	sf       singleflight.Group
}

func newRIUtilizationCache(store riUtilizationCacheStore, isLambda bool) *riUtilizationCache {
	return &riUtilizationCache{store: store, isLambda: isLambda}
}

// getOrFetch returns cached utilization when fresh. On non-Lambda, a
// stale-but-reusable entry ([softTTL, hardTTL)) is returned
// immediately while a detached goroutine refreshes the row. Rows
// older than hardTTL (or any staleness on Lambda) force a synchronous
// refetch.
//
// Fetch errors are NOT cached — a transient CE 5xx must not lock the
// dashboard out for the full TTL. A cache-write error after a fresh
// fetch is logged but not returned — the caller already has data.
func (c *riUtilizationCache) getOrFetch(
	ctx context.Context,
	region string,
	lookbackDays int,
	softTTL, hardTTL time.Duration,
	fetch riUtilizationFetcher,
) ([]recommendations.RIUtilization, error) {
	key := fmt.Sprintf("%s#%d", region, lookbackDays)

	entry, err := c.store.GetRIUtilizationCache(ctx, region, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("failed to read ri utilization cache: %w", err)
	}

	if entry != nil {
		age := time.Since(entry.FetchedAt)
		data, unmarshalErr := unmarshalRIUtilization(entry.Payload)
		if unmarshalErr != nil {
			// Corrupt payload — log and fall through to a fresh
			// fetch so one bad row can't lock the dashboard out
			// for an entire TTL window.
			logging.Warnf("ri_utilization_cache: corrupt payload for region=%q lookback=%d: %v", region, lookbackDays, unmarshalErr)
		} else {
			switch {
			case age < softTTL:
				return data, nil
			case age < hardTTL:
				if c.isLambda {
					logging.Debugf("ri_utilization_cache: Lambda runtime — skipping SWR (key=%s age=%s)", key, age)
					// Fall through to synchronous refetch.
					break
				}
				logging.Infof("ri_utilization_cache: serving stale + kicking background refresh (key=%s age=%s)", key, age)
				c.kickBackgroundRefresh(key, region, lookbackDays, fetch)
				return data, nil
			default:
				logging.Infof("ri_utilization_cache: entry beyond hard expiry; synchronous refetch (key=%s age=%s)", key, age)
				// Fall through to synchronous refetch.
			}
		}
	}

	data, err := fetch(ctx, lookbackDays)
	if err != nil {
		return nil, err
	}

	c.storePayload(ctx, region, lookbackDays, data)
	return data, nil
}

// kickBackgroundRefresh runs a single-flighted refetch in a detached
// goroutine. sf.Do with the same key collapses concurrent calls to
// one in-flight refresh. The refresh uses a fresh context (not the
// caller's) because the caller's ctx may be cancelled when the HTTP
// response completes, which would abort the refresh prematurely.
func (c *riUtilizationCache) kickBackgroundRefresh(key, region string, lookbackDays int, fetch riUtilizationFetcher) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logging.Errorf("ri_utilization_cache: background refresh panic (key=%s): %v", key, r)
			}
		}()

		_, _, _ = c.sf.Do(key, func() (any, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			data, err := fetch(ctx, lookbackDays)
			if err != nil {
				logging.Warnf("ri_utilization_cache: background refresh failed (key=%s): %v", key, err)
				return nil, err
			}
			c.storePayload(ctx, region, lookbackDays, data)
			return nil, nil
		})
	}()
}

// storePayload marshals and upserts the fresh data. Errors are logged
// but not returned — callers have the data in hand either way.
func (c *riUtilizationCache) storePayload(ctx context.Context, region string, lookbackDays int, data []recommendations.RIUtilization) {
	payload, err := json.Marshal(data)
	if err != nil {
		logging.Warnf("ri_utilization_cache: failed to marshal payload for region=%q lookback=%d: %v", region, lookbackDays, err)
		return
	}
	if err := c.store.UpsertRIUtilizationCache(ctx, region, lookbackDays, payload, time.Now()); err != nil {
		logging.Warnf("ri_utilization_cache: failed to upsert row for region=%q lookback=%d: %v", region, lookbackDays, err)
	}
}

func unmarshalRIUtilization(payload []byte) ([]recommendations.RIUtilization, error) {
	var data []recommendations.RIUtilization
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	return data, nil
}
