// Package execution provides parallel multi-account execution primitives.
package execution

import (
	"context"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// AccountExecutor is a function that performs an operation for a single account.
// It receives the full CloudAccount (with auth metadata) and returns a result.
type AccountExecutor[T any] func(ctx context.Context, account config.CloudAccount) (T, error)

// RunForAccounts is a convenience wrapper over FanOut that takes CloudAccount
// values directly, using account.ID as the key in each Result.
func RunForAccounts[T any](
	ctx context.Context,
	accounts []config.CloudAccount,
	fn AccountExecutor[T],
) []Result[T] {
	ids := make([]string, len(accounts))
	byID := make(map[string]config.CloudAccount, len(accounts))
	for i, a := range accounts {
		ids[i] = a.ID
		byID[a.ID] = a
	}
	return FanOut(ctx, ids, func(ctx context.Context, id string) (T, error) {
		return fn(ctx, byID[id])
	})
}

// RunForAccountsWithConcurrency is like RunForAccounts but with an explicit
// concurrency limit.
func RunForAccountsWithConcurrency[T any](
	ctx context.Context,
	accounts []config.CloudAccount,
	fn AccountExecutor[T],
	maxConcurrency int,
) []Result[T] {
	ids := make([]string, len(accounts))
	byID := make(map[string]config.CloudAccount, len(accounts))
	for i, a := range accounts {
		ids[i] = a.ID
		byID[a.ID] = a
	}
	return FanOutWithConcurrency(ctx, ids, func(ctx context.Context, id string) (T, error) {
		return fn(ctx, byID[id])
	}, maxConcurrency)
}
