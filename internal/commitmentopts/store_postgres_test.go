//go:build integration
// +build integration

package commitmentopts_test

import (
	"context"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/commitmentopts"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getMigrationsPath mirrors the helper in internal/config/store_postgres_test.go —
// the migrations directory is a sibling of internal/<package>/.
func getMigrationsPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "database", "postgres", "migrations")
}

// setupStore stands up a fresh Postgres container and returns a store
// bound to it. The container is torn down via t.Cleanup.
func setupStore(ctx context.Context, t *testing.T) *commitmentopts.PostgresStore {
	t.Helper()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	t.Cleanup(func() { container.Cleanup(ctx) })

	require.NoError(t, migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", ""))
	return commitmentopts.NewPostgresStore(container.DB)
}

func TestPostgresStore_HasData_EmptyIsFalse(t *testing.T) {
	ctx := context.Background()
	store := setupStore(ctx, t)

	has, err := store.HasData(ctx)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestPostgresStore_Get_EmptyReturnsFalseBool(t *testing.T) {
	ctx := context.Background()
	store := setupStore(ctx, t)

	opts, has, err := store.Get(ctx)
	require.NoError(t, err)
	assert.False(t, has)
	assert.Empty(t, opts)
}

func TestPostgresStore_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	store := setupStore(ctx, t)

	combos := []commitmentopts.Combo{
		{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"},
		{Provider: "aws", Service: "rds", TermYears: 1, Payment: "no-upfront"},
		{Provider: "aws", Service: "rds", TermYears: 3, Payment: "all-upfront"},
		{Provider: "aws", Service: "elasticache", TermYears: 1, Payment: "partial-upfront"},
	}
	require.NoError(t, store.Save(ctx, combos, "123456789012"))

	has, err := store.HasData(ctx)
	require.NoError(t, err)
	assert.True(t, has)

	opts, has, err := store.Get(ctx)
	require.NoError(t, err)
	assert.True(t, has)
	require.Contains(t, opts, "aws")
	require.Contains(t, opts["aws"], "rds")
	require.Contains(t, opts["aws"], "elasticache")

	rds := opts["aws"]["rds"]
	sort.Slice(rds, func(i, j int) bool {
		if rds[i].TermYears != rds[j].TermYears {
			return rds[i].TermYears < rds[j].TermYears
		}
		return rds[i].Payment < rds[j].Payment
	})
	assert.Equal(t, []commitmentopts.Combo{
		{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"},
		{Provider: "aws", Service: "rds", TermYears: 1, Payment: "no-upfront"},
		{Provider: "aws", Service: "rds", TermYears: 3, Payment: "all-upfront"},
	}, rds)

	assert.Equal(t, []commitmentopts.Combo{
		{Provider: "aws", Service: "elasticache", TermYears: 1, Payment: "partial-upfront"},
	}, opts["aws"]["elasticache"])
}

func TestPostgresStore_Save_IdempotentOnRerun(t *testing.T) {
	ctx := context.Background()
	store := setupStore(ctx, t)

	combos := []commitmentopts.Combo{
		{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"},
	}
	require.NoError(t, store.Save(ctx, combos, "111111111111"))
	// Second call must not fail (singleton PK conflict on the run row,
	// compound PK conflict on the combos row).
	require.NoError(t, store.Save(ctx, combos, "222222222222"))

	opts, has, err := store.Get(ctx)
	require.NoError(t, err)
	assert.True(t, has)
	assert.Equal(t, combos, opts["aws"]["rds"])
}

func TestPostgresStore_Save_EmptyCombosStillMarksRun(t *testing.T) {
	ctx := context.Background()
	store := setupStore(ctx, t)

	// A legitimate "AWS returned no matching offerings" scenario — we
	// persist the run row so the next Get short-circuits via the bool.
	require.NoError(t, store.Save(ctx, nil, "333333333333"))

	has, err := store.HasData(ctx)
	require.NoError(t, err)
	assert.True(t, has)

	opts, has, err := store.Get(ctx)
	require.NoError(t, err)
	assert.True(t, has)
	assert.Empty(t, opts)
}
