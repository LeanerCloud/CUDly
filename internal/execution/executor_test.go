package execution

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeAccount(id string) config.CloudAccount {
	return config.CloudAccount{ID: id, Name: "Account " + id, Provider: "aws", ExternalID: id}
}

func TestRunForAccounts_Success(t *testing.T) {
	accounts := []config.CloudAccount{makeAccount("acct-1"), makeAccount("acct-2")}

	results := RunForAccounts(context.Background(), accounts, func(_ context.Context, a config.CloudAccount) (string, error) {
		return a.ID + "-result", nil
	})

	require.Len(t, results, 2)

	// Sort for deterministic assertions.
	sort.Slice(results, func(i, j int) bool { return results[i].AccountID < results[j].AccountID })

	assert.Equal(t, "acct-1", results[0].AccountID)
	assert.Equal(t, "acct-1-result", results[0].Value)
	assert.NoError(t, results[0].Err)

	assert.Equal(t, "acct-2", results[1].AccountID)
	assert.Equal(t, "acct-2-result", results[1].Value)
	assert.NoError(t, results[1].Err)
}

func TestRunForAccounts_PartialFailure(t *testing.T) {
	errBoom := errors.New("boom")
	accounts := []config.CloudAccount{makeAccount("acct-1"), makeAccount("acct-2")}

	results := RunForAccounts(context.Background(), accounts, func(_ context.Context, a config.CloudAccount) (string, error) {
		if a.ID == "acct-2" {
			return "", errBoom
		}
		return a.ID + "-ok", nil
	})

	require.Len(t, results, 2)

	byID := map[string]Result[string]{}
	for _, r := range results {
		byID[r.AccountID] = r
	}

	assert.NoError(t, byID["acct-1"].Err)
	assert.Equal(t, "acct-1-ok", byID["acct-1"].Value)
	assert.ErrorIs(t, byID["acct-2"].Err, errBoom)
}

func TestCollect(t *testing.T) {
	results := []Result[string]{
		{AccountID: "a", Value: "v1", Err: nil},
		{AccountID: "b", Err: errors.New("fail")},
		{AccountID: "c", Value: "v2", Err: nil},
	}

	values := Collect(results)
	require.Len(t, values, 2)
	assert.Contains(t, values, "v1")
	assert.Contains(t, values, "v2")
}

func TestCollectErrors(t *testing.T) {
	errA := errors.New("error-a")
	errC := errors.New("error-c")
	results := []Result[int]{
		{AccountID: "a", Err: errA},
		{AccountID: "b", Value: 1, Err: nil},
		{AccountID: "c", Err: errC},
	}

	errs := CollectErrors(results)
	require.Len(t, errs, 2)
	assert.ErrorIs(t, errs[0], errA)
	assert.ErrorIs(t, errs[1], errC)
}

func TestFirstError(t *testing.T) {
	t.Run("returns first error", func(t *testing.T) {
		errX := errors.New("x")
		results := []Result[string]{
			{AccountID: "a", Value: "ok", Err: nil},
			{AccountID: "b", Err: errX},
			{AccountID: "c", Err: errors.New("y")},
		}
		assert.ErrorIs(t, FirstError(results), errX)
	})

	t.Run("returns nil when all succeed", func(t *testing.T) {
		results := []Result[string]{
			{AccountID: "a", Value: "ok1"},
			{AccountID: "b", Value: "ok2"},
		}
		assert.NoError(t, FirstError(results))
	})
}
