// Package concurrency provides a shared global parallelism cap for the
// recommendations-collection fan-out tree.
//
// The fan-out has up to four nested levels (provider → account → service|region
// → per-region service). Each level was independently capped, so peak goroutine
// counts multiplied through the tree (3 providers × 20 accounts × 30 regions ×
// 2 services = thousands of in-flight gRPC/HTTP clients). On a 512 MB Lambda
// that exhausted memory before the work could finish.
//
// A single semaphore stashed on the context lets every leaf goroutine — the
// goroutine that issues the actual cloud-API call — acquire one slot before
// doing IO and release it after, so the aggregate concurrent IO count is hard-
// bounded regardless of nesting depth. Intermediate dispatchers (provider,
// account, GCP region) do NOT acquire — they only launch sub-goroutines — so
// no goroutine can deadlock by holding a permit while waiting for sub-permits.
//
// If no semaphore is attached to the context (e.g. unit tests, ambient calls
// from CLI tools), Acquire and Release are no-ops; callers don't need to
// branch on whether the semaphore is set.
package concurrency

import (
	"context"
	"os"
	"strconv"

	"golang.org/x/sync/semaphore"
)

// DefaultMaxParallelism is the default cap on aggregate concurrent leaf
// goroutines across the recommendations-collection fan-out tree. Override at
// runtime with CUDLY_MAX_PARALLELISM.
const DefaultMaxParallelism = 20

// MaxParallelismFromEnv reads CUDLY_MAX_PARALLELISM and returns its
// positive-integer value, falling back to DefaultMaxParallelism on unset /
// invalid / non-positive values.
func MaxParallelismFromEnv() int {
	if v := os.Getenv("CUDLY_MAX_PARALLELISM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxParallelism
}

type ctxKey struct{}

// WithSharedSemaphore returns a context carrying sem. Goroutines spawned from
// this context (or any descendant) can acquire/release slots via Acquire and
// Release. If sem is nil the context is returned unchanged.
func WithSharedSemaphore(ctx context.Context, sem *semaphore.Weighted) context.Context {
	if sem == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, sem)
}

// SharedSemaphore returns the semaphore stashed in ctx, or nil if none.
func SharedSemaphore(ctx context.Context) *semaphore.Weighted {
	sem, _ := ctx.Value(ctxKey{}).(*semaphore.Weighted)
	return sem
}

// Acquire blocks until a slot is available on the shared semaphore in ctx and
// returns nil. Returns ctx.Err() if the wait is cancelled. If no semaphore is
// attached to ctx, Acquire is a no-op and returns nil immediately — leaf
// callers can use it unconditionally without checking.
func Acquire(ctx context.Context) error {
	sem := SharedSemaphore(ctx)
	if sem == nil {
		return nil
	}
	return sem.Acquire(ctx, 1)
}

// Release returns one slot to the shared semaphore in ctx. Always pair with a
// successful Acquire (return value nil); calling Release after a cancelled
// Acquire would corrupt the slot count. If no semaphore is attached to ctx,
// Release is a no-op.
func Release(ctx context.Context) {
	sem := SharedSemaphore(ctx)
	if sem == nil {
		return
	}
	sem.Release(1)
}
