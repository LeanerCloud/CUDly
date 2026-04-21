// Package execution provides parallel multi-account execution primitives.
package execution

import (
	"context"
	"os"
	"strconv"
	"sync"
)

// ConcurrencyFromEnv reads the CUDLY_MAX_ACCOUNT_PARALLELISM env var and
// returns its value when it's a positive integer, otherwise
// DefaultMaxConcurrency. Shared between the purchase manager (which drives
// live cloud API calls) and the scheduler (per-account recommendations
// collection) so both honour the same operator-level override.
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
	AccountID string
	Value     T
	Err       error
}

// DefaultMaxConcurrency is the default cap on parallel account goroutines.
// Override with FanOutWithConcurrency when a different limit is needed.
const DefaultMaxConcurrency = 20

// FanOut runs fn concurrently for each accountID in the supplied slice and
// collects all results. Cancellation of ctx is respected: inflight goroutines
// see the cancelled context but all launched goroutines are still awaited so
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
		wg.Add(1)
		sem <- struct{}{} // acquire slot
		go func(idx int, accountID string) {
			defer wg.Done()
			defer func() { <-sem }() // release slot
			val, err := fn(ctx, accountID)
			results[idx] = Result[T]{AccountID: accountID, Value: val, Err: err}
		}(i, id)
	}

	wg.Wait()
	return results
}

// Partition splits a Result slice into successes and failures.
func Partition[T any](results []Result[T]) (successes []Result[T], failures []Result[T]) {
	for _, r := range results {
		if r.Err != nil {
			failures = append(failures, r)
		} else {
			successes = append(successes, r)
		}
	}
	return successes, failures
}
