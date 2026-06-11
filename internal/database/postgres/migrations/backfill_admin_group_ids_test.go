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

// TestMigration_BackfillAdminGroupIDs covers issue #546 acceptance criterion 2:
// the SQL-level idempotent backfill (migration 000056) must repair a drifted
// admin row even when migrations run WITHOUT an admin email (the restore /
// no-ADMIN_EMAIL deployment path, where the Go-level assignAdminGroupAndWarn
// never fires).
//
// Mechanism mirrors split_savingsplans_test: run all migrations, roll back the
// last one (000056) so the DB sits at version 55, seed a drifted admin, then
// re-run migrations with NO admin email so only the SQL migration can repair
// the row. A pass therefore proves the migration (not the Go path) did it.
func TestMigration_BackfillAdminGroupIDs(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()
	const adminEmail = "restore-path@test.example"

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Pin the DB at version 55 so 000056 (the SQL backfill under test) is
	// not applied yet and the `role` column this test seeds below still
	// exists. (Pinning by version stays correct as newer migrations land;
	// the old fixed-step rollback from head did not.)
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 55))

	// Simulate a restored/manually-seeded admin whose group_ids drifted to
	// empty (the bug pattern from issue #351).
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, salt, role, active, group_ids, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, '', '', 'admin', false, '{}', NOW(), NOW())
	`, adminEmail)
	require.NoError(t, err)

	drifted := queryAdminGroupIDs(t, ctx, pool, adminEmail)
	require.Empty(t, drifted, "test setup: admin should start with empty group_ids")

	// Apply exactly 000056. MigrateToVersion runs no Go-level repair
	// (assignAdminGroupAndWarn), so any fix is attributable solely to the
	// SQL migration.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 56))

	got := queryAdminGroupIDs(t, ctx, pool, adminEmail)
	assert.Equal(t, []string{defaultAdminGroupIDTest}, got,
		"migration 000056 must backfill the Administrators group onto a drifted admin row even without an admin email")

	// Idempotent: cycling 000057 down and back up, then migrating to head,
	// must not duplicate or drop the Administrators group on the already
	// backfilled admin row.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 57))
	require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
	got = queryAdminGroupIDs(t, ctx, pool, adminEmail)
	// Later migrations legitimately add more groups to admins (000064 adds
	// Purchaser), so assert on the Administrators entry rather than the
	// exact set: present exactly once, never duplicated or dropped.
	adminEntries := 0
	for _, id := range got {
		if id == defaultAdminGroupIDTest {
			adminEntries++
		}
	}
	assert.Equal(t, 1, adminEntries,
		"re-applying the migration chain must keep exactly one Administrators group entry, got %v", got)
}
