package execution

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFanOut_Success(t *testing.T) {
	ids := []string{"a", "b", "c"}
	results := FanOut(context.Background(), ids, func(_ context.Context, id string) (string, error) {
		return "ok-" + id, nil
	})

	require.Len(t, results, 3)

	// Sort for deterministic assertion.
	sort.Slice(results, func(i, j int) bool { return results[i].AccountID < results[j].AccountID })

	assert.Equal(t, "a", results[0].AccountID)
	assert.Equal(t, "ok-a", results[0].Value)
	assert.NoError(t, results[0].Err)
}

func TestFanOut_PartialFailure(t *testing.T) {
	errBoom := errors.New("boom")
	ids := []string{"good", "bad"}
	results := FanOut(context.Background(), ids, func(_ context.Context, id string) (int, error) {
		if id == "bad" {
			return 0, errBoom
		}
		return 1, nil
	})

	require.Len(t, results, 2)

	// Map by account ID for deterministic assertions.
	byID := map[string]Result[int]{}
	for _, r := range results {
		byID[r.AccountID] = r
	}

	assert.NoError(t, byID["good"].Err)
	assert.Equal(t, 1, byID["good"].Value)
	assert.ErrorIs(t, byID["bad"].Err, errBoom)
}

func TestFanOut_Empty(t *testing.T) {
	results := FanOut(context.Background(), nil, func(_ context.Context, _ string) (string, error) {
		return "should not run", nil
	})
	assert.Empty(t, results)
}

func TestFanOut_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	ids := []string{"x"}
	results := FanOut(ctx, ids, func(ctx context.Context, id string) (string, error) {
		return "", ctx.Err()
	})

	require.Len(t, results, 1)
	assert.ErrorIs(t, results[0].Err, context.Canceled)
}

func TestPartition(t *testing.T) {
	results := []Result[string]{
		{AccountID: "a", Value: "v", Err: nil},
		{AccountID: "b", Err: errors.New("fail")},
		{AccountID: "c", Value: "w", Err: nil},
	}

	successes, failures := Partition(results)
	require.Len(t, successes, 2)
	require.Len(t, failures, 1)
	assert.Equal(t, "b", failures[0].AccountID)
}

// TestFanOut_PanicInFn asserts that a panic inside the per-item function is
// caught by the goroutine's deferred recover, converted to an Err on that
// item's result slot, and does NOT propagate up and crash the whole process
// (which would strand the surrounding purchase execution at 'approved' and
// terminate the Lambda invocation abnormally — see #669).
// TestFanOut_ContextCancelled_BlockedSemaphore asserts that when a context is
// canceled while goroutines are already occupying all semaphore slots, the
// remaining queued items record ctx.Err() immediately rather than blocking
// indefinitely on the semaphore (05-H3).
//
// Pre-fix behavior: sem <- struct{}{} was unconditional, so a canceled
// context with maxConcurrency=1 and N>1 ids would block the launch loop on
// the second item until the first goroutine released its slot -- a
// context-deadline timeout would therefore not be respected at the semaphore
// boundary.
func TestFanOut_ContextCancelled_BlockedSemaphore(t *testing.T) {
	// started (buffered=1) signals when the first goroutine holds the slot.
	// release (buffered=1) controls when the first goroutine finishes.
	// Both are buffered so the goroutines never block if the test is already
	// past the receive.
	started := make(chan struct{}, 1)
	release := make(chan struct{}, 1)

	// canceledRecorded is closed by the fan-out launch loop the moment the
	// queued "second" item is recorded as ctx.Canceled at the semaphore
	// boundary (via the semBlockedHookKey hook). This gives a deterministic
	// happens-before edge: the test only releases the blocker AFTER the
	// cancellation has been recorded, so the freed semaphore slot can never
	// race the launch loop into running "second". Without it, cancel() and the
	// slot-release both make the loop's select ready, and Go's uniform-random
	// pick would intermittently launch "second" (recording a nil error).
	canceledRecorded := make(chan struct{})
	ctx, cancel := context.WithCancel(
		context.WithValue(context.Background(), semBlockedHookKey{}, func() {
			close(canceledRecorded)
		}),
	)
	defer cancel()

	// Collect results in a separate goroutine so this test goroutine can
	// drive the cancel/release sequencing without deadlocking on wg.Wait().
	type outcome struct{ results []Result[string] }
	done := make(chan outcome, 1)

	go func() {
		// maxConcurrency=1 so the second item must wait for the semaphore.
		// The first goroutine signals started, then waits on release; during
		// that window we cancel the context. The second item should receive
		// ctx.Err() without waiting for the first goroutine to finish.
		res := FanOutWithConcurrency(ctx, []string{"first", "second"},
			func(ctx context.Context, id string) (string, error) {
				if id == "first" {
					started <- struct{}{} // tell the test we have the slot
					<-release             // wait until the test says go
					return "ok", nil
				}
				return "should-not-run", nil
			}, 1)
		done <- outcome{res}
	}()

	// Wait until the first goroutine holds the semaphore, then cancel. Once
	// canceledRecorded fires, the loop has committed "second" to the
	// <-ctx.Done() branch, so releasing the blocker is now race-free.
	<-started
	cancel()
	select {
	case <-canceledRecorded:
		// Correct behavior: the loop honored ctx cancellation at the
		// semaphore boundary. Fires within microseconds, so the deadline
		// below is never reached on a healthy tree (no flakiness).
	case <-time.After(10 * time.Second):
		// A regression to unconditional `sem <- struct{}{}` would block the
		// launch loop on the second item forever (the blocker never releases
		// because we never send release), so canceledRecorded never fires.
		// Fail fast with a clear message instead of hanging for the whole
		// go-test timeout.
		t.Fatal("timed out waiting for the queued item to be recorded as " +
			"canceled; FanOutWithConcurrency likely blocked on the semaphore " +
			"instead of honoring ctx cancellation")
	}
	release <- struct{}{} // let the first goroutine finish so FanOut can return

	o := <-done
	results := o.results

	require.Len(t, results, 2)
	byID := make(map[string]Result[string], len(results))
	for _, r := range results {
		byID[r.AccountID] = r
	}

	// The first item completes normally (it already had the slot before cancel).
	assert.NoError(t, byID["first"].Err)
	assert.Equal(t, "ok", byID["first"].Value)

	// The second item must carry context.Canceled, not block or succeed.
	require.Error(t, byID["second"].Err)
	assert.ErrorIs(t, byID["second"].Err, context.Canceled)
}

func TestFanOut_PanicInFn(t *testing.T) {
	ids := []string{"a", "panic-me", "c"}
	results := FanOut(context.Background(), ids, func(ctx context.Context, id string) (string, error) {
		if id == "panic-me" {
			panic("synthetic panic for test")
		}
		return "ok:" + id, nil
	})

	require.Len(t, results, 3)
	// Map by AccountID since FanOut order is non-deterministic.
	byID := make(map[string]Result[string], len(results))
	for _, r := range results {
		byID[r.AccountID] = r
	}

	// Non-panicking items still succeed.
	assert.NoError(t, byID["a"].Err)
	assert.Equal(t, "ok:a", byID["a"].Value)
	assert.NoError(t, byID["c"].Err)
	assert.Equal(t, "ok:c", byID["c"].Value)

	// The panicking item is surfaced as an Err containing the panic value.
	require.Error(t, byID["panic-me"].Err)
	assert.Contains(t, byID["panic-me"].Err.Error(), "panic during fan-out")
	assert.Contains(t, byID["panic-me"].Err.Error(), "synthetic panic for test")
	assert.Contains(t, byID["panic-me"].Err.Error(), "panic-me")
}
