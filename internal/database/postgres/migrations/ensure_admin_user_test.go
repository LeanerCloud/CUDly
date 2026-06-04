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

	t.Run("re-run with existing correct admin (post-057 idempotency)", func(t *testing.T) {
		// Migration 000057 added a CHECK constraint (users_min_one_group)
		// that prevents group_ids from being NULL or empty, so the
		// pre-057 drift scenario (empty group_ids on an existing admin row)
		// is structurally impossible post-057. This sub-test verifies that
		// re-running RunMigrations when the admin already exists with the
		// correct group_ids is a no-op that neither fails nor doubles the
		// group entry (issue #945).
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// First pass: create admin via the normal path (migrations + bootstrap).
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))

		initial := queryAdminGroupIDs(t, ctx, pool, adminEmail)
		require.Equal(t, []string{defaultAdminGroupIDTest}, initial,
			"test setup: admin must have the Administrators group after first run")

		// Second pass: re-run RunMigrations with the same admin email.
		// ensureAdminUser fires again; ON CONFLICT DO NOTHING skips the
		// insert; assignAdminGroupAndWarn is a no-op (group_ids is non-empty).
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))

		got := queryAdminGroupIDs(t, ctx, pool, adminEmail)
		assert.Equal(t, []string{defaultAdminGroupIDTest}, got,
			"re-running RunMigrations on an existing correctly-grouped admin must be a no-op")
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
			WHERE email = $2
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
