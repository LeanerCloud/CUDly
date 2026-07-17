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
//   - BY DEFAULT (flag unset), a dirty DB fails loud with a descriptive error
//     that includes recovery instructions (CUDLY_FORCE_MIGRATION_VERSION). The
//     dirty flag is left intact. The app still starts (fail-open via ensureDB),
//     and the CloudWatch alarm fires. Operators inspect the actual schema to
//     choose the correct force target.
//   - With CUDLY_MIGRATION_AUTOHEAL=false, auto-heal is bypassed and a dirty DB
//     surfaces the generic Up() error ("migration: N is dirty") without recovery
//     guidance.
//
// WHY THE BEHAVIOR CHANGED FROM "AUTO-CLEAR" TO "FAIL-LOUD": golang-migrate
// sets dirty=true BEFORE running migration N's SQL. If the migration is
// interrupted (Lambda timeout, etc.) the SQL transaction is rolled back but the
// dirty flag persists -- N's effects are ABSENT. The previous Force(N)+Up() path
// treated the rolled-back migration as applied and skipped its DDL forever
// (000074 divergence class). There is also a narrow race where N commits but
// the dirty=false update fails, leaving dirty=true WITH effects present. These
// two cases are indistinguishable without per-migration effect probes, so the
// conservative choice is to refuse to auto-clear and let an operator confirm
// the schema state before forcing.
func TestMigrations_AutoHealDirty(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	t.Run("by default a dirty DB fails loud with recovery instructions", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Migrate to head, then simulate a dirty state by forcing the dirty
		// flag at the head version.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		headVersion, _, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		require.Greater(t, headVersion, uint(0))
		markDirty(ctx, t, container, headVersion)

		// Sanity: confirm the DB is genuinely dirty before the next run.
		_, dirtyBefore, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		require.True(t, dirtyBefore, "precondition: DB must be dirty before the run")

		// Default-on auto-heal must fail loud (conservative: cannot verify effects).
		// t.Setenv auto-restores and forbids t.Parallel().
		t.Setenv("CUDLY_MIGRATION_AUTOHEAL", "")
		runErr := migrations.RunMigrations(ctx, pool, migrationsPath, "", "")
		require.Error(t, runErr, "default-on auto-heal must fail loud when dirty (cannot verify whether effects were applied)")
		assert.ErrorContains(t, runErr, "is dirty", "error must describe the dirty state")
		assert.ErrorContains(t, runErr, "CUDLY_FORCE_MIGRATION_VERSION", "error must include recovery instructions")

		// The dirty flag must remain -- conservative auto-heal must not silently
		// Force-clear it, which would record the migration as applied without
		// verifying its SQL effects.
		versionAfter, dirtyAfter, verr := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, verr)
		assert.True(t, dirtyAfter, "conservative auto-heal must leave the dirty flag intact")
		assert.Equal(t, headVersion, versionAfter, "conservative auto-heal must not change the recorded version")
	})

	t.Run("with CUDLY_MIGRATION_AUTOHEAL=true a dirty DB fails loud with recovery instructions", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		headVersion, _, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		markDirty(ctx, t, container, headVersion)

		t.Setenv("CUDLY_MIGRATION_AUTOHEAL", "true")
		runErr := migrations.RunMigrations(ctx, pool, migrationsPath, "", "")
		require.Error(t, runErr, "explicit true should also fail loud when dirty")
		assert.ErrorContains(t, runErr, "CUDLY_FORCE_MIGRATION_VERSION", "error must include recovery instructions")

		versionAfter, dirtyAfter, verr := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, verr)
		assert.True(t, dirtyAfter, "conservative auto-heal must leave the dirty flag intact")
		assert.Equal(t, headVersion, versionAfter, "conservative auto-heal must not change the recorded version")
	})

	t.Run("CUDLY_MIGRATION_AUTOHEAL=false bypasses auto-heal check; dirty DB errors via Up()", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		headVersion, _, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		markDirty(ctx, t, container, headVersion)

		// Escape hatch: bypasses the descriptive auto-heal error; the dirty state
		// surfaces via the generic Up() path ("failed to run migrations: migration:
		// N is dirty"). The caller fail-opens on it; the app still starts.
		t.Setenv("CUDLY_MIGRATION_AUTOHEAL", "false")
		err = migrations.RunMigrations(ctx, pool, migrationsPath, "", "")
		require.Error(t, err, "CUDLY_MIGRATION_AUTOHEAL=false must bypass auto-heal so a dirty DB still errors")

		_, dirtyAfter, verr := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, verr)
		assert.True(t, dirtyAfter, "with auto-heal bypassed, the dirty flag must be left intact")
	})

	// TestMigrations_AutoHealDirty_EffectsAbsent is the regression test for the
	// 000074 divergence class: when schema_migrations records dirty=true at
	// version N but N's SQL was rolled back (effects ABSENT), auto-heal must NOT
	// silently Force(N) and mark the never-applied migration as complete.
	//
	// Setup: run all migrations to head, then roll back the last one (so the
	// schema is at head-1 state), then inject dirty=true at head. This exactly
	// reproduces the interrupted-migration scenario: schema_migrations says head
	// is dirty, but head's SQL effects are not in the schema.
	//
	// Expected: RunMigrations returns an error AND leaves schema_migrations
	// dirty at head -- NOT Force-cleared to clean, which would permanently skip
	// the missing DDL.
	t.Run("dirty-at-N with N-effects-absent: auto-heal refuses to silently mark it applied", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Migrate to head so we know the head version number.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		headVersion, _, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		require.Greater(t, headVersion, uint(1), "need at least two migrations for the rollback step")

		// Roll back the last migration so the schema is at head-1 state.
		// The effects of headVersion are now absent from the schema.
		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1),
			"rollback of the last migration must succeed")
		versionAfterRollback, _, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		require.Equal(t, headVersion-1, versionAfterRollback,
			"rollback must land exactly one version below head")

		// Inject dirty=true at headVersion, simulating a migration that started
		// (schema_migrations updated to dirty) but whose SQL was rolled back
		// (effects not in the schema). This is the common interrupted-migration
		// scenario the original defect was about.
		markDirty(ctx, t, container, headVersion)

		// Verify the precondition: schema_migrations reports dirty at headVersion
		// but we have not applied headVersion's effects.
		recordedVersion, dirtyBefore, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, err)
		require.True(t, dirtyBefore, "precondition: schema_migrations must be dirty")
		require.Equal(t, headVersion, recordedVersion, "precondition: dirty version must be head")

		// Run migrations with default auto-heal (conservative: fail loud).
		t.Setenv("CUDLY_MIGRATION_AUTOHEAL", "")
		runErr := migrations.RunMigrations(ctx, pool, migrationsPath, "", "")
		require.Error(t, runErr,
			"auto-heal must NOT silently mark the rolled-back migration as applied "+
				"(that would permanently skip its DDL: the 000074 divergence class)")

		// The dirty flag must remain set and the version must be unchanged:
		// Force(headVersion) was NOT called, so the record was not falsely cleaned.
		// This is the core assertion: a never-applied migration was not recorded
		// as complete simply because it appeared dirty in schema_migrations.
		versionFinal, dirtyFinal, verr := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
		require.NoError(t, verr)
		assert.True(t, dirtyFinal,
			"auto-heal must leave the dirty flag intact (not Force-cleared), "+
				"preventing the missing DDL from being silently skipped")
		assert.Equal(t, headVersion, versionFinal,
			"auto-heal must not change the recorded version to avoid falsely marking the migration applied")
	})
}
