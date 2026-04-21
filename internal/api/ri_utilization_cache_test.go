package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
)

// fakeRIUtilCacheStore is a minimal in-test implementation of
// riUtilizationCacheStore. Keyed by (region, lookbackDays); stores the
// raw JSON payload + fetched_at so the cache layer exercises the same
// marshalling path as the real Postgres store.
type fakeRIUtilCacheStore struct {
	mu      sync.Mutex
	entries map[string]config.RIUtilizationCacheEntry
	getErr  error // if non-nil, GetRIUtilizationCache returns this
}

func newFakeRIUtilCacheStore() *fakeRIUtilCacheStore {
	return &fakeRIUtilCacheStore{entries: map[string]config.RIUtilizationCacheEntry{}}
}

func (f *fakeRIUtilCacheStore) key(region string, lookbackDays int) string {
	return region + "#" + time.Duration(lookbackDays).String()
}

func (f *fakeRIUtilCacheStore) GetRIUtilizationCache(ctx context.Context, region string, lookbackDays int) (*config.RIUtilizationCacheEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	e, ok := f.entries[f.key(region, lookbackDays)]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (f *fakeRIUtilCacheStore) UpsertRIUtilizationCache(ctx context.Context, region string, lookbackDays int, payload []byte, fetchedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[f.key(region, lookbackDays)] = config.RIUtilizationCacheEntry{
		Region:       region,
		LookbackDays: lookbackDays,
		Payload:      payload,
		FetchedAt:    fetchedAt,
	}
	return nil
}

func TestRIUtilizationCache_HitReturnsCachedData(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	c := newRIUtilizationCache(store)
	fetched := []recommendations.RIUtilization{{ReservedInstanceID: "ri-1", UtilizationPercent: 95.0}}
	var calls atomic.Int32

	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return fetched, nil
	}

	// First call: cache miss → fetch → upsert.
	got, err := c.getOrFetch(context.Background(), "us-east-1", 30, 10*time.Second, fetch)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(got) != 1 || got[0].ReservedInstanceID != "ri-1" {
		t.Fatalf("first call returned wrong data: %+v", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 fetch call; got %d", calls.Load())
	}

	// Second call within TTL: cache hit, fetcher NOT invoked again.
	got, err = c.getOrFetch(context.Background(), "us-east-1", 30, 10*time.Second, fetch)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected still 1 fetch call after cache hit; got %d", calls.Load())
	}
	if len(got) != 1 || got[0].ReservedInstanceID != "ri-1" {
		t.Fatalf("second call returned wrong data: %+v", got)
	}
}

func TestRIUtilizationCache_ExpiredEntryRefetches(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	c := newRIUtilizationCache(store)
	var calls atomic.Int32
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return nil, nil
	}

	// TTL of 1ms so entry expires between the two calls.
	_, err := c.getOrFetch(context.Background(), "us-east-1", 30, time.Millisecond, fetch)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	_, err = c.getOrFetch(context.Background(), "us-east-1", 30, time.Millisecond, fetch)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 fetch calls after TTL expiry; got %d", calls.Load())
	}
}

func TestRIUtilizationCache_DistinctKeys(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	c := newRIUtilizationCache(store)
	var calls atomic.Int32
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return nil, nil
	}

	// Same lookback, different regions → distinct cache keys.
	_, _ = c.getOrFetch(context.Background(), "us-east-1", 30, time.Hour, fetch)
	_, _ = c.getOrFetch(context.Background(), "eu-west-1", 30, time.Hour, fetch)
	// Same region, different lookback → distinct too.
	_, _ = c.getOrFetch(context.Background(), "us-east-1", 90, time.Hour, fetch)
	if calls.Load() != 3 {
		t.Fatalf("expected 3 fetch calls for 3 distinct keys; got %d", calls.Load())
	}
}

func TestRIUtilizationCache_FetchErrorNotCached(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	c := newRIUtilizationCache(store)
	var calls atomic.Int32
	sentinel := errors.New("cost explorer down")
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return nil, sentinel
	}

	_, err := c.getOrFetch(context.Background(), "us-east-1", 30, time.Hour, fetch)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error; got %v", err)
	}
	// Retry should hit the fetcher again — we don't want to cache
	// failures and lock the dashboard out for the full TTL window.
	_, err = c.getOrFetch(context.Background(), "us-east-1", 30, time.Hour, fetch)
	if !errors.Is(err, sentinel) {
		t.Fatalf("second call: expected sentinel error; got %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 fetch calls (errors uncached); got %d", calls.Load())
	}
	if len(store.entries) != 0 {
		t.Fatalf("expected no rows written on fetch error; got %d", len(store.entries))
	}
}

func TestRIUtilizationCache_CorruptPayloadFallsThrough(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	// Seed with garbage payload — simulates schema drift or a bad
	// manual row. Cache must log + re-fetch rather than propagate
	// json.Unmarshal as an error and lock the dashboard out.
	store.entries[store.key("us-east-1", 30)] = config.RIUtilizationCacheEntry{
		Region:       "us-east-1",
		LookbackDays: 30,
		Payload:      []byte("not-json"),
		FetchedAt:    time.Now(),
	}
	c := newRIUtilizationCache(store)
	fetched := []recommendations.RIUtilization{{ReservedInstanceID: "ri-fresh"}}
	var calls atomic.Int32
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return fetched, nil
	}

	got, err := c.getOrFetch(context.Background(), "us-east-1", 30, time.Hour, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected a re-fetch after corrupt payload; got %d fetch calls", calls.Load())
	}
	if len(got) != 1 || got[0].ReservedInstanceID != "ri-fresh" {
		t.Fatalf("got wrong data back: %+v", got)
	}
	// Corrupt row should have been overwritten.
	entry := store.entries[store.key("us-east-1", 30)]
	var round []recommendations.RIUtilization
	if err := json.Unmarshal(entry.Payload, &round); err != nil {
		t.Fatalf("stored payload still corrupt after upsert: %v", err)
	}
}
