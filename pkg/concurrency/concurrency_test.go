package concurrency

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

// TestMaxParallelismFromEnv pins the env-knob parser semantics for
// CUDLY_MAX_PARALLELISM.
func TestMaxParallelismFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{"unset returns default", "", DefaultMaxParallelism},
		{"positive integer overrides", "50", 50},
		{"non-numeric falls back to default", "many", DefaultMaxParallelism},
		{"zero falls back to default", "0", DefaultMaxParallelism},
		{"negative falls back to default", "-3", DefaultMaxParallelism},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUDLY_MAX_PARALLELISM", tc.env)
			assert.Equal(t, tc.want, MaxParallelismFromEnv())
		})
	}

	t.Run("explicit unset returns default", func(t *testing.T) {
		os.Unsetenv("CUDLY_MAX_PARALLELISM")
		assert.Equal(t, DefaultMaxParallelism, MaxParallelismFromEnv())
	})
}

// TestSharedSemaphore_NoSemaphoreOnContext verifies Acquire/Release are
// no-ops when no semaphore is attached — the documented contract that lets
// CLI tools and unit tests skip the semaphore entirely without per-call
// branching.
func TestSharedSemaphore_NoSemaphoreOnContext(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, SharedSemaphore(ctx))
	require.NoError(t, Acquire(ctx))
	Release(ctx) // must not panic
}

// TestSharedSemaphore_WithNilSemaphore verifies WithSharedSemaphore returns
// the input ctx unchanged when sem is nil — defensive against accidental
// nil passes.
func TestSharedSemaphore_WithNilSemaphore(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, ctx, WithSharedSemaphore(ctx, nil))
}

// TestSharedSemaphore_BoundsConcurrency is the load-bearing contract test:
// with a cap of 3, 20 goroutines all calling Acquire/work/Release must
// never see more than 3 in-flight concurrently. Asserts peak concurrency
// observed via atomics.
func TestSharedSemaphore_BoundsConcurrency(t *testing.T) {
	const cap = 3
	const goroutines = 20
	sem := semaphore.NewWeighted(cap)
	ctx := WithSharedSemaphore(context.Background(), sem)

	var inflight, peak atomic.Int32
	updatePeak := func(cur int32) {
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				return
			}
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, Acquire(ctx))
			defer Release(ctx)
			cur := inflight.Add(1)
			updatePeak(cur)
			time.Sleep(2 * time.Millisecond) // make overlap observable
			inflight.Add(-1)
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, peak.Load(), int32(cap),
		"peak concurrent in-flight goroutines must not exceed semaphore cap")
	assert.GreaterOrEqual(t, peak.Load(), int32(2),
		"with %d goroutines and cap %d, peak should reach at least 2 (proves goroutines genuinely overlapped)",
		goroutines, cap)
}

// TestSharedSemaphore_AcquireRespectsCancellation verifies Acquire returns
// ctx.Err() when the parent ctx is cancelled while waiting for a slot.
// Without this, a cancelled refresh would leak a goroutine parked
// indefinitely on Acquire.
func TestSharedSemaphore_AcquireRespectsCancellation(t *testing.T) {
	sem := semaphore.NewWeighted(1)
	// Pre-occupy the only slot so the second Acquire must wait.
	require.NoError(t, sem.Acquire(context.Background(), 1))
	defer sem.Release(1)

	ctx, cancel := context.WithCancel(WithSharedSemaphore(context.Background(), sem))
	cancel() // cancel before Acquire even starts

	err := Acquire(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
