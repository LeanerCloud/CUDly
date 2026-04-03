package execution

import (
	"context"
	"errors"
	"sort"
	"testing"

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
	cancel() // already cancelled

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
