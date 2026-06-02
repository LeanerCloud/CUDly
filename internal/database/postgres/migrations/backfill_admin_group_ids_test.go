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

	// Up to head, then roll back to version 55 so the next RunMigrations
	// re-applies 000056. Two steps because 000057 (drop role -> groups) now
	// sits above 000056 at head; rolling back 000057 first also restores the
	// `role` column this test seeds below.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
	require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 2))

	// Simulate a restored/manually-seeded admin whose group_ids drifted to
	// empty (the bug pattern from issue #351).
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, salt, role, active, group_ids, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, '', '', 'admin', false, '{}', NOW(), NOW())
	`, adminEmail)
	require.NoError(t, err)

	drifted := queryAdminGroupIDs(t, ctx, pool, adminEmail)
	require.Empty(t, drifted, "test setup: admin should start with empty group_ids")

	// Re-run migrations with NO admin email. m.Up() re-applies 000056; the
	// Go-level assignAdminGroupAndWarn does NOT run (empty email), so any
	// repair is attributable solely to the SQL migration.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

	got := queryAdminGroupIDs(t, ctx, pool, adminEmail)
	assert.Equal(t, []string{defaultAdminGroupIDTest}, got,
		"migration 000056 must backfill the Administrators group onto a drifted admin row even without an admin email")

	// Idempotent: rolling back the head migration (000057) and re-applying it
	// must not duplicate or drop the Administrators group on the already
	// backfilled admin row.
	require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
	got = queryAdminGroupIDs(t, ctx, pool, adminEmail)
	assert.Equal(t, []string{defaultAdminGroupIDTest}, got,
		"re-applying the head migration must not duplicate the Administrators group entry")
}
