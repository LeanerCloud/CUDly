//go:build integration
// +build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrations_FullStackIdempotent verifies that calling RunMigrations on an
// already-fully-migrated DB is a clean no-op: m.Up() returns ErrNoChange
// (swallowed by RunMigrations), the DB version is unchanged, and the
// schema_migrations row is not set dirty.
//
// SCOPE LIMITATION: this test does NOT verify per-migration idempotency (i.e.
// that re-running a single migration on a DB that already has its effects does
// not fail or corrupt data). The second call to RunMigrations hits ErrNoChange
// from golang-migrate and returns without executing any migration SQL -- so
// individual migration bodies are never actually re-run here.
//
// The conservative auto-heal in maybeAutoHealDirty no longer relies on
// per-migration idempotency: instead of silently Force-clearing the dirty flag
// (which risked recording a rolled-back migration as applied), it now fails
// loud and defers to an operator to confirm the schema state via
// CUDLY_FORCE_MIGRATION_VERSION. Per-migration re-run safety is therefore not
// an invariant that auto-heal depends on, and is tracked separately.
func TestMigrations_FullStackIdempotent(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// First run: migrate to head on a fresh DB.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""),
		"first migration run to head should succeed on a fresh DB")

	versionAfterFirst, dirtyAfterFirst, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
	require.NoError(t, err)
	require.False(t, dirtyAfterFirst, "DB must not be dirty after the first run")
	require.Greater(t, versionAfterFirst, uint(0), "head version should be > 0")

	// Second run: m.Up() returns ErrNoChange (no SQL executed), which
	// RunMigrations swallows. The version must be unchanged and the dirty flag
	// must not be set.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""),
		"second migration run on a fully-migrated DB must be a clean no-op (ErrNoChange)")

	versionAfterSecond, dirtyAfterSecond, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
	require.NoError(t, err)
	assert.False(t, dirtyAfterSecond, "DB must not be dirty after the second (no-op) run")
	assert.Equal(t, versionAfterFirst, versionAfterSecond,
		"version must be unchanged after a no-op second run")
}
