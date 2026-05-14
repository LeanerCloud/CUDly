//go:build integration
// +build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultAdminGroupIDTest mirrors the literal in
// internal/database/postgres/migrations/migrate.go (defaultAdminGroupID)
// and 000024_seed_default_groups.up.sql. Duplicated as a test-local
// constant because the production constant is package-private.
const defaultAdminGroupIDTest = "00000000-0000-5000-8000-000000000001"

// queryAdminGroupIDs returns the group_ids array for the admin user
// with the given email, as a []string of UUID strings. Helper for the
// sub-cases below so the assertions stay readable.
func queryAdminGroupIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) []string {
	t.Helper()
	var ids []string
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(ARRAY(
			SELECT id::text FROM unnest(group_ids) AS id
		), '{}')
		FROM users WHERE email = $1
	`, email).Scan(&ids)
	require.NoError(t, err, "user row for %s must exist", email)
	return ids
}

// TestEnsureAdminUser_GroupAssignment covers the five scenarios from
// issue #351: every code path that inserts an admin row must produce
// group_ids containing the Administrators group, post-migration
// drift must self-heal on the next boot, and operator-customised
// group_ids must be preserved.
func TestEnsureAdminUser_GroupAssignment(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()
	const adminEmail = "admin@test.example"

	t.Run("fresh insert no password", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))

		got := queryAdminGroupIDs(t, ctx, pool, adminEmail)
		assert.Equal(t, []string{defaultAdminGroupIDTest}, got,
			"fresh bootstrap admin (no-password path) must have the Administrators group assigned")
	})

	t.Run("fresh insert with password", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, "TestPass!9aF"))

		got := queryAdminGroupIDs(t, ctx, pool, adminEmail)
		assert.Equal(t, []string{defaultAdminGroupIDTest}, got,
			"fresh bootstrap admin (with-password path) must have the Administrators group assigned")
	})

	t.Run("post-migration drift repair", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// First pass: run migrations only, no admin yet. Migration 000024
		// seeds the Administrators group row but does not insert any
		// admin user.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		// Simulate an out-of-band manual DB seed that inserted an admin
		// without group_ids (the bug pattern from issue #351 - happens
		// when an operator runs INSERT INTO users directly).
		_, err = pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, salt, role, active, group_ids, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, '', '', 'admin', false, '{}', NOW(), NOW())
		`, adminEmail)
		require.NoError(t, err)

		// Verify the drift state before re-running RunMigrations.
		drifted := queryAdminGroupIDs(t, ctx, pool, adminEmail)
		require.Empty(t, drifted, "test setup: admin should start with empty group_ids")

		// Second pass: re-run RunMigrations with the admin email. The
		// m.Up() inside is a no-op (already at head, returns
		// ErrNoChange) but ensureAdminUser still fires and triggers
		// the code-level backfill via assignAdminGroupAndWarn.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))

		got := queryAdminGroupIDs(t, ctx, pool, adminEmail)
		assert.Equal(t, []string{defaultAdminGroupIDTest}, got,
			"post-migration drift must self-heal on the next ensureAdminUser run")
	})

	t.Run("idempotency", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Two consecutive RunMigrations calls must not duplicate the
		// admin group in group_ids - DISTINCT(unnest(...)) dedupes
		// and the WHERE clause skips already-populated rows.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))

		got := queryAdminGroupIDs(t, ctx, pool, adminEmail)
		assert.Equal(t, []string{defaultAdminGroupIDTest}, got,
			"calling RunMigrations twice must not duplicate the admin group ID")
		assert.Len(t, got, 1, "group_ids must contain exactly one entry after two runs")
	})

	t.Run("operator customisation preserved", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// First pass: create the admin via the normal path.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))

		// Simulate an operator who deliberately scoped this admin to a
		// custom group only - removing the default Administrators
		// group. The backfill must NOT undo this customisation on
		// subsequent boots (issue body: "leave existing group_ids
		// alone").
		const customGroupID = "11111111-1111-5111-8111-111111111111"
		_, err = pool.Exec(ctx, `
			INSERT INTO groups (id, name, description, permissions, allowed_accounts)
			VALUES ($1::UUID, 'OperatorCustom', 'test', '[]'::jsonb, ARRAY['*'])
			ON CONFLICT DO NOTHING
		`, customGroupID)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `
			UPDATE users SET group_ids = ARRAY[$1]::UUID[]
			WHERE email = $2 AND role = 'admin'
		`, customGroupID, adminEmail)
		require.NoError(t, err)

		// Second pass: re-run RunMigrations. ensureAdminUser fires
		// again, but the backfill's WHERE cardinality=0 clause skips
		// the customised row.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))

		got := queryAdminGroupIDs(t, ctx, pool, adminEmail)
		assert.Equal(t, []string{customGroupID}, got,
			"operator-customised group_ids (non-empty, no default admin group) must be preserved across boots")
	})
}
