// Package execution provides parallel multi-account execution primitives.
package execution

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// ConcurrencyFromEnv reads the CUDLY_MAX_ACCOUNT_PARALLELISM env var and
// returns its value when it's a positive integer, otherwise
// DefaultMaxConcurrency. Shared between the purchase manager (which drives
// live cloud API calls) and the scheduler (per-account recommendations
// collection) so both honor the same operator-level override.
func ConcurrencyFromEnv() int {
	if v := os.Getenv("CUDLY_MAX_ACCOUNT_PARALLELISM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxConcurrency
}

// Result holds the outcome of running an operation against a single account.
type Result[T any] struct {
	Value     T
	Err       error
	AccountID string
}

// DefaultMaxConcurrency is the default cap on parallel account goroutines.
// Override with FanOutWithConcurrency when a different limit is needed.
const DefaultMaxConcurrency = 20

// FanOut runs fn concurrently for each accountID in the supplied slice and
// collects all results. Cancellation of ctx is respected: inflight goroutines
// see the canceled context but all launched goroutines are still awaited so
// the caller receives a full result slice (some entries may carry ctx.Err()).
//
// Concurrency is capped at DefaultMaxConcurrency to avoid overwhelming AWS
// STS rate limits and Lambda memory budgets on large organizations.
// The order of results is non-deterministic.
func FanOut[T any](
	ctx context.Context,
	accountIDs []string,
	fn func(ctx context.Context, accountID string) (T, error),
) []Result[T] {
	return FanOutWithConcurrency(ctx, accountIDs, fn, DefaultMaxConcurrency)
}

// FanOutWithConcurrency is like FanOut but with an explicit concurrency limit.
func FanOutWithConcurrency[T any](
	ctx context.Context,
	accountIDs []string,
	fn func(ctx context.Context, accountID string) (T, error),
	maxConcurrency int,
) []Result[T] {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	results := make([]Result[T], len(accountIDs))
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	for i, id := range accountIDs {
		// Acquire a semaphore slot before launching the goroutine.
		// Use select so a canceled/expired context is not held up by a
		// full semaphore: if ctx is done while waiting for a slot, record
		// ctx.Err() on the result slot and skip launching the goroutine.
		// Without this, a large fan-out on an already-canceled context
		// (e.g. Lambda deadline exceeded partway through) would block
		// indefinitely here rather than draining quickly.
		wg.Add(1)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i] = Result[T]{AccountID: id, Err: ctx.Err()}
			wg.Done()
			continue
		}
		go func(idx int, accountID string) {
			defer wg.Done()
			defer func() { <-sem }() // release slot
			// recover() guard so a panic inside fn (nil deref, type
			// assertion failure, slice OOB, etc.) doesn't crash the
			// whole Lambda process and strand the purchase execution
			// at 'approved' with no way to debug post-mortem (issue
			// #669). Surface the panic as an Err on the result slot
			// so the parent aggregator records it on the execution
			// row exactly like a regular fn-returned error, and log
			// the goroutine stack at Error level for diagnosis.
			defer func() {
				if r := recover(); r != nil {
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)
					logging.Errorf("fan-out goroutine panic (account=%s): %v\n%s", accountID, r, buf[:n])
					results[idx] = Result[T]{
						AccountID: accountID,
						Err:       fmt.Errorf("panic during fan-out (account=%s): %v", accountID, r),
					}
				}
			}()
			t0 := time.Now()
			logging.Infof("purchase[fan-out]: goroutine starting for account=%s", accountID)
			val, err := fn(ctx, accountID)
			elapsed := time.Since(t0)
			if err != nil {
				logging.Errorf("purchase[fan-out]: goroutine for account=%s failed after %s: %v",
					accountID, elapsed, err)
			} else {
				logging.Infof("purchase[fan-out]: goroutine for account=%s completed in %s",
					accountID, elapsed)
			}
			results[idx] = Result[T]{AccountID: accountID, Value: val, Err: err}
		}(i, id)
	}

	wg.Wait()
	return results
}

// Partition splits a Result slice into successes and failures.
func Partition[T any](results []Result[T]) (successes []Result[T], failures []Result[T]) { //nolint:gocritic // paramTypeCombine: explicit types aid readability
	for _, r := range results {
		if r.Err != nil {
			failures = append(failures, r)
		} else {
			successes = append(successes, r)
		}
	}
	return successes, failures
}
