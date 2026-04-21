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
)

// defaultRIUtilizationCacheTTL is how long a cached utilization row stays
// fresh before we re-fetch from Cost Explorer. 15 minutes because CE
// data updates hourly at best and the user-facing dashboard doesn't
// need sub-minute freshness; every cache hit avoids a paid CE API call.
const defaultRIUtilizationCacheTTL = 15 * time.Minute

// riUtilizationCacheTTL is resolved once from CUDLY_RI_UTILIZATION_CACHE_TTL
// via time.ParseDuration. Declared as a var (not const) so tests can
// overwrite it. Invalid values fall back to the default with a log line.
var riUtilizationCacheTTL = resolveRIUtilizationCacheTTL()

func resolveRIUtilizationCacheTTL() time.Duration {
	v := os.Getenv("CUDLY_RI_UTILIZATION_CACHE_TTL")
	if v == "" {
		return defaultRIUtilizationCacheTTL
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		fmt.Fprintf(os.Stderr, "CUDLY_RI_UTILIZATION_CACHE_TTL invalid (%q); using default %s\n", v, defaultRIUtilizationCacheTTL)
		return defaultRIUtilizationCacheTTL
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
// fresh data from Cost Explorer. Parameterising lets tests inject a fake.
type riUtilizationFetcher func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error)

// riUtilizationCache wraps a Postgres-backed store with TTL semantics
// and marshalling. Postgres (not in-memory) because Lambda containers
// are short-lived and each cold container would start with an empty
// in-memory cache — effectively no cache. The shared table lets every
// warm and cold container benefit from one CE call per TTL window.
type riUtilizationCache struct {
	store riUtilizationCacheStore
}

func newRIUtilizationCache(store riUtilizationCacheStore) *riUtilizationCache {
	return &riUtilizationCache{store: store}
}

// getOrFetch returns cached utilization when fresh (< ttl old), otherwise
// invokes fetch() and writes the result back to the store. A fetch
// error bypasses the cache write so transient Cost Explorer failures
// don't overwrite a still-useful row with garbage. A cache-write error
// is logged but not returned — the caller already has fresh data.
//
// Concurrent callers for the SAME (region, lookbackDays) may duplicate
// a fetch during a cold-cache window; that's acceptable vs. the
// complexity of a distributed lock or per-key singleflight on top of
// Postgres. The cost of one extra CE call once every TTL window is
// negligible.
func (c *riUtilizationCache) getOrFetch(
	ctx context.Context,
	region string,
	lookbackDays int,
	ttl time.Duration,
	fetch riUtilizationFetcher,
) ([]recommendations.RIUtilization, error) {
	entry, err := c.store.GetRIUtilizationCache(ctx, region, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("failed to read ri utilization cache: %w", err)
	}
	if entry != nil && time.Since(entry.FetchedAt) < ttl {
		var data []recommendations.RIUtilization
		if err := json.Unmarshal(entry.Payload, &data); err != nil {
			// Corrupt payload — log and fall through to a fresh
			// fetch so one bad row can't lock the dashboard out
			// for an entire TTL window.
			logging.Warnf("ri_utilization_cache: corrupt payload for region=%q lookback=%d: %v", region, lookbackDays, err)
		} else {
			return data, nil
		}
	}

	data, err := fetch(ctx, lookbackDays)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(data)
	if err != nil {
		// Shouldn't happen for a plain struct; log and return the
		// fresh data anyway so the caller isn't blocked.
		logging.Warnf("ri_utilization_cache: failed to marshal payload for region=%q lookback=%d: %v", region, lookbackDays, err)
		return data, nil
	}
	if err := c.store.UpsertRIUtilizationCache(ctx, region, lookbackDays, payload, time.Now()); err != nil {
		logging.Warnf("ri_utilization_cache: failed to upsert row for region=%q lookback=%d: %v", region, lookbackDays, err)
	}

	return data, nil
}
