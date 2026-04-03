// Package execution provides parallel multi-account execution primitives.
package execution

import (
	"context"
	"sync"
)

// Result holds the outcome of running an operation against a single account.
type Result[T any] struct {
	AccountID string
	Value     T
	Err       error
}

// FanOut runs fn concurrently for each accountID in the supplied slice and
// collects all results. Cancellation of ctx is respected: inflight goroutines
// see the cancelled context but all launched goroutines are still awaited so
// the caller receives a full result slice (some entries may carry ctx.Err()).
//
// The order of results is non-deterministic.
func FanOut[T any](
	ctx context.Context,
	accountIDs []string,
	fn func(ctx context.Context, accountID string) (T, error),
) []Result[T] {
	results := make([]Result[T], len(accountIDs))
	var wg sync.WaitGroup

	for i, id := range accountIDs {
		wg.Add(1)
		go func(idx int, accountID string) {
			defer wg.Done()
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
