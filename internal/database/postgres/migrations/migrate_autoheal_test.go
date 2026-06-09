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

// markDirty forces schema_migrations into the dirty state at the given version,
// reproducing what golang-migrate leaves behind when a migration is interrupted
// mid-run (Lambda timeout, ENI drop, etc.). golang-migrate keeps exactly one
// row in this table.
func markDirty(ctx context.Context, t *testing.T, container *testhelpers.PostgresContainer, version uint) {
	t.Helper()
	_, err := container.DB.Exec(ctx,
		`UPDATE schema_migrations SET version = $1, dirty = true`, version)
	require.NoError(t, err, "failed to mark schema_migrations dirty")
}

// TestMigrations_AutoHealDirty covers the DEFAULT-ON CUDLY_MIGRATION_AUTOHEAL
// behavior from migrate.go:
//
//   - BY DEFAULT (flag unset), a dirty DB is healed: Force(current) clears the
//     dirty flag and the subsequent Up() reaches head clean -- a cold start
//     self-recovers without operator intervention.
//   - With CUDLY_MIGRATION_AUTOHEAL=false, auto-heal is disabled and a dirty DB
//     still returns an error with the dirty flag left intact (escape hatch).
//
// In both cases the Force target is the CURRENT recorded version (never lower),
// which is the only safe shape -- forcing below already-applied migrations
// would re-run guarded seed migrations that raise on a second run.
func TestMigrations_AutoHealDirty(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	t.Run("by default a dirty DB self-heals and head is reached", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Migrate to head, then simulate an interrupted migration by forcing
		// the dirty flag at the head version.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		headVersion, _, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		require.Greater(t, headVersion, uint(0))
		markDirty(ctx, t, container, headVersion)

		// Sanity: confirm the DB is genuinely dirty before healing.
		_, dirtyBefore, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		require.True(t, dirtyBefore, "precondition: DB must be dirty before the heal run")

		// Flag explicitly unset: auto-heal is default-on, so the re-run must
		// self-recover. t.Setenv auto-restores and forbids t.Parallel().
		t.Setenv("CUDLY_MIGRATION_AUTOHEAL", "")
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""),
			"default-on auto-heal should clear the dirty flag and reach head without error")

		versionAfter, dirtyAfter, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		assert.False(t, dirtyAfter, "auto-heal must clear the dirty flag")
		assert.Equal(t, headVersion, versionAfter, "auto-heal must leave the DB at head")
	})

	t.Run("with CUDLY_MIGRATION_AUTOHEAL=true a dirty DB self-heals", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		headVersion, _, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		markDirty(ctx, t, container, headVersion)

		t.Setenv("CUDLY_MIGRATION_AUTOHEAL", "true")
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""),
			"explicit true should also clear the dirty flag and reach head")

		versionAfter, dirtyAfter, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		assert.False(t, dirtyAfter, "auto-heal must clear the dirty flag")
		assert.Equal(t, headVersion, versionAfter, "auto-heal must leave the DB at head")
	})

	t.Run("CUDLY_MIGRATION_AUTOHEAL=false disables auto-heal and a dirty DB errors", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		headVersion, _, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		markDirty(ctx, t, container, headVersion)

		// Escape hatch: the falsey value disables auto-heal, so the dirty error
		// must surface (the caller fail-opens on it; the app still starts).
		t.Setenv("CUDLY_MIGRATION_AUTOHEAL", "false")
		err = migrations.RunMigrations(ctx, pool, migrationsPath, "", "")
		require.Error(t, err, "CUDLY_MIGRATION_AUTOHEAL=false must disable auto-heal so a dirty DB errors")

		_, dirtyAfter, verr := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, verr)
		assert.True(t, dirtyAfter, "with auto-heal disabled, the dirty flag must be left intact")
	})
}
