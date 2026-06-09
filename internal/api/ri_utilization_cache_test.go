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
	getErr  error
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

// seedStale writes a cache row with fetchedAt set in the past so the
// caller can control exactly how "stale" the row is relative to soft
// / hard TTLs.
func (f *fakeRIUtilCacheStore) seedStale(t *testing.T, region string, lookbackDays int, data []recommendations.RIUtilization, age time.Duration) {
	t.Helper()
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("seedStale marshal: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[f.key(region, lookbackDays)] = config.RIUtilizationCacheEntry{
		Region:       region,
		LookbackDays: lookbackDays,
		Payload:      payload,
		FetchedAt:    time.Now().Add(-age),
	}
}

func (f *fakeRIUtilCacheStore) latest(region string, lookbackDays int) ([]recommendations.RIUtilization, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[f.key(region, lookbackDays)]
	if !ok {
		return nil, false
	}
	var out []recommendations.RIUtilization
	_ = json.Unmarshal(e.Payload, &out)
	return out, true
}

func TestRIUtilizationCache_HitReturnsCachedData(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	c := newRIUtilizationCache(store, false)
	fetched := []recommendations.RIUtilization{{ReservedInstanceID: "ri-1", UtilizationPercent: 95.0}}
	var calls atomic.Int32

	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return fetched, nil
	}

	got, err := c.getOrFetch(context.Background(), "us-east-1", 30, 10*time.Second, 30*time.Second, fetch)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(got) != 1 || got[0].ReservedInstanceID != "ri-1" {
		t.Fatalf("first call returned wrong data: %+v", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 fetch call; got %d", calls.Load())
	}

	// Second call within soft TTL: cache hit, fetcher NOT invoked.
	_, err = c.getOrFetch(context.Background(), "us-east-1", 30, 10*time.Second, 30*time.Second, fetch)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected still 1 fetch call after cache hit; got %d", calls.Load())
	}
}

func TestRIUtilizationCache_StaleOnNonLambdaServesStaleAndKicksRefresh(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	staleData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-stale"}}
	freshData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-fresh"}}
	// Seed a row whose age is between the soft TTL (50ms) and hard TTL (1h).
	store.seedStale(t, "us-east-1", 30, staleData, 100*time.Millisecond)

	c := newRIUtilizationCache(store, false) // non-Lambda

	fetchCalled := make(chan struct{}, 1)
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		fetchCalled <- struct{}{}
		return freshData, nil
	}

	start := time.Now()
	got, err := c.getOrFetch(context.Background(), "us-east-1", 30, 50*time.Millisecond, time.Hour, fetch)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// SWR must return quickly with the stale payload, not wait for
	// the fetcher.
	if elapsed > 50*time.Millisecond {
		t.Fatalf("getOrFetch took %s — should have returned the stale copy synchronously", elapsed)
	}
	if len(got) != 1 || got[0].ReservedInstanceID != "ri-stale" {
		t.Fatalf("expected stale copy, got %+v", got)
	}

	// Background fetch should fire within a short window.
	select {
	case <-fetchCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("background fetch never fired")
	}

	// Give the storePayload write a beat to land.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		latest, ok := store.latest("us-east-1", 30)
		if ok && len(latest) == 1 && latest[0].ReservedInstanceID == "ri-fresh" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background refresh did not update the stored row")
}

func TestRIUtilizationCache_StaleOnLambdaBlocksForSyncRefetch(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	staleData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-stale"}}
	freshData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-fresh"}}
	store.seedStale(t, "us-east-1", 30, staleData, 100*time.Millisecond)

	c := newRIUtilizationCache(store, true) // Lambda mode

	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		return freshData, nil
	}

	got, err := c.getOrFetch(context.Background(), "us-east-1", 30, 50*time.Millisecond, time.Hour, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Lambda must return the FRESH data synchronously (today's behaviour).
	if len(got) != 1 || got[0].ReservedInstanceID != "ri-fresh" {
		t.Fatalf("Lambda mode should synchronously refetch; got %+v", got)
	}
}

func TestRIUtilizationCache_HardExpirySyncRefetch(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	staleData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-stale"}}
	freshData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-fresh"}}
	// Age (2h) exceeds both soft (15m) and hard (1h).
	store.seedStale(t, "us-east-1", 30, staleData, 2*time.Hour)

	c := newRIUtilizationCache(store, false) // non-Lambda, but hard-expired

	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		return freshData, nil
	}

	got, err := c.getOrFetch(context.Background(), "us-east-1", 30, 15*time.Minute, time.Hour, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Past hard TTL: caller must block for fresh data, not get the stale copy.
	if len(got) != 1 || got[0].ReservedInstanceID != "ri-fresh" {
		t.Fatalf("hard-expired entry should force sync refetch; got %+v", got)
	}
}

func TestRIUtilizationCache_SingleflightCollapsesConcurrentRefreshes(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	staleData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-stale"}}
	freshData := []recommendations.RIUtilization{{ReservedInstanceID: "ri-fresh"}}
	store.seedStale(t, "us-east-1", 30, staleData, 100*time.Millisecond)

	c := newRIUtilizationCache(store, false)

	var calls atomic.Int32
	// Gate the fetcher so concurrent calls all race to enter the
	// refresh — singleflight should collapse them.
	release := make(chan struct{})
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		<-release
		return freshData, nil
	}

	// Spawn 10 concurrent readers.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.getOrFetch(context.Background(), "us-east-1", 30, 50*time.Millisecond, time.Hour, fetch)
			if err != nil {
				t.Errorf("getOrFetch: %v", err)
			}
		}()
	}
	wg.Wait()

	// All 10 getOrFetch calls returned the stale copy. The 10
	// background-refresh goroutines are now racing to enter sf.Do —
	// under CPU pressure (full test suite with -race) they can lag
	// behind the getOrFetch returns, and a straggler that arrives
	// after the first singleflight batch finishes would open a
	// second batch and bump calls to 2. Give the scheduler enough
	// runway for all 10 to reach sf.Do (where they'll block on
	// release) before we unblock the in-flight fetch.
	time.Sleep(250 * time.Millisecond)

	// Unblock the single in-flight fetch and wait for the collapsed
	// batch's storePayload to complete so calls.Load() reflects the
	// final state.
	close(release)
	time.Sleep(200 * time.Millisecond)

	if n := calls.Load(); n != 1 {
		t.Fatalf("expected exactly 1 fetch call under singleflight; got %d", n)
	}
}

func TestRIUtilizationCache_FetchErrorNotCached(t *testing.T) {
	store := newFakeRIUtilCacheStore()
	c := newRIUtilizationCache(store, true) // Lambda so we hit the sync path
	var calls atomic.Int32
	sentinel := errors.New("cost explorer down")
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return nil, sentinel
	}

	_, err := c.getOrFetch(context.Background(), "us-east-1", 30, time.Hour, time.Hour, fetch)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error; got %v", err)
	}
	// Retry should hit the fetcher again — we don't want to cache
	// failures and lock the dashboard out for the full TTL window.
	_, err = c.getOrFetch(context.Background(), "us-east-1", 30, time.Hour, time.Hour, fetch)
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
	store.entries[store.key("us-east-1", 30)] = config.RIUtilizationCacheEntry{
		Region:       "us-east-1",
		LookbackDays: 30,
		Payload:      []byte("not-json"),
		FetchedAt:    time.Now(),
	}
	c := newRIUtilizationCache(store, true)
	fetched := []recommendations.RIUtilization{{ReservedInstanceID: "ri-fresh"}}
	var calls atomic.Int32
	fetch := func(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
		calls.Add(1)
		return fetched, nil
	}

	got, err := c.getOrFetch(context.Background(), "us-east-1", 30, time.Hour, time.Hour, fetch)
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
