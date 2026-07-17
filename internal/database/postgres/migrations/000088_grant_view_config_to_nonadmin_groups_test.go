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

// groupHasPermission reports whether the given group's permissions JSONB array
// grants the given (action, resource) pair.
func groupHasPermission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID, action, resource string) bool {
	t.Helper()
	var has bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM groups g, jsonb_array_elements(g.permissions) AS elem
			WHERE g.id = $1
			  AND elem->>'action' = $2
			  AND elem->>'resource' = $3
		)
	`, groupID, action, resource).Scan(&has)
	require.NoError(t, err, "querying %s:%s on group %s", action, resource, groupID)
	return has
}

// TestMigration_GrantViewConfigToNonAdminGroups covers issues #1401, #1410, and
// #1413: migration 000088 grants view:config to Standard Users and Read-Only
// Users so GET /api/config and GET /api/ri-exchange/config return 200 instead of
// 403 for those groups. The grant must be surgical: no other permissions are
// touched on either group, and the write-gating verb (update:config) is not added.
func TestMigration_GrantViewConfigToNonAdminGroups(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	t.Run("view:config granted to Standard Users and Read-Only Users", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin at 000086, the last migration on main before this change. view:config
		// must be absent from both non-admin groups at this point (000088 not applied).
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 86))

		require.False(t, groupHasPermission(t, ctx, pool, standardUsersGroupIDTest, "view", "config"),
			"precondition: Standard Users must NOT hold view:config at v86")
		require.False(t, groupHasPermission(t, ctx, pool, readOnlyUsersGroupIDTest, "view", "config"),
			"precondition: Read-Only Users must NOT hold view:config at v86")

		// Apply 000088. Pin to this migration's own version so a later
		// migration (e.g. 000089) landing on main cannot change what this
		// test exercises (feedback_migration_test_pin_version, incident #1436).
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 88))

		assert.True(t, groupHasPermission(t, ctx, pool, standardUsersGroupIDTest, "view", "config"),
			"migration 000088 must add view:config to Standard Users (issue #1401)")
		assert.True(t, groupHasPermission(t, ctx, pool, readOnlyUsersGroupIDTest, "view", "config"),
			"migration 000088 must add view:config to Read-Only Users (issue #1401)")
	})

	t.Run("update:config is NOT granted (write gate is unaffected)", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin to 000088 (feedback_migration_test_pin_version, #1436).
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 88))

		assert.False(t, groupHasPermission(t, ctx, pool, standardUsersGroupIDTest, "update", "config"),
			"migration 000088 must NOT grant update:config to Standard Users")
		assert.False(t, groupHasPermission(t, ctx, pool, readOnlyUsersGroupIDTest, "update", "config"),
			"migration 000088 must NOT grant update:config to Read-Only Users")
	})

	t.Run("sibling permissions on Standard Users are preserved", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin to 000088 (feedback_migration_test_pin_version, #1436).
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 88))

		for _, perm := range []struct{ action, resource string }{
			{"view", "recommendations"},
			{"view", "plans"},
			{"view", "purchases"},
			{"view", "history"},
			{"create", "plans"},
			{"update", "plans"},
			{"cancel-own", "purchases"},
			{"retry-own", "purchases"},
		} {
			assert.True(t,
				groupHasPermission(t, ctx, pool, standardUsersGroupIDTest, perm.action, perm.resource),
				"migration 000088 must preserve %s:%s on Standard Users", perm.action, perm.resource)
		}
	})

	t.Run("down migration removes view:config from both groups", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin to 000088 so a later migration on main cannot shift the head
		// this rollback targets (feedback_migration_test_pin_version, #1436).
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 88))

		// Confirm 000088 is applied.
		require.True(t, groupHasPermission(t, ctx, pool, standardUsersGroupIDTest, "view", "config"),
			"head state: Standard Users must hold view:config after 000088")
		require.True(t, groupHasPermission(t, ctx, pool, readOnlyUsersGroupIDTest, "view", "config"),
			"head state: Read-Only Users must hold view:config after 000088")

		// Roll back 000088 specifically (migrate down to 000087), not a
		// relative "rollback 1 from HEAD" which would undo a later migration.
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 87))

		assert.False(t, groupHasPermission(t, ctx, pool, standardUsersGroupIDTest, "view", "config"),
			"000088 down migration must remove view:config from Standard Users")
		assert.False(t, groupHasPermission(t, ctx, pool, readOnlyUsersGroupIDTest, "view", "config"),
			"000088 down migration must remove view:config from Read-Only Users")
	})

	t.Run("idempotent: applying to a group already holding view:config is safe", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin to 000088 (feedback_migration_test_pin_version, #1436).
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 88))

		// Count entries for view:config before the re-apply.
		var count int
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM groups g, jsonb_array_elements(g.permissions) AS elem
			WHERE g.id = $1
			  AND elem->>'action' = 'view'
			  AND elem->>'resource' = 'config'
		`, standardUsersGroupIDTest).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count, "exactly one view:config entry before re-apply")

		// Run the up SQL again directly.
		_, err = pool.Exec(ctx, `
			UPDATE groups
			SET permissions = permissions || '[{"action":"view","resource":"config"}]'::jsonb,
			    updated_at  = NOW()
			WHERE id IN ($1, $2)
			  AND NOT (permissions @> '[{"action":"view","resource":"config"}]')
		`, standardUsersGroupIDTest, readOnlyUsersGroupIDTest)
		require.NoError(t, err)

		// Count must still be 1 (the NOT EXISTS guard prevents duplication).
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM groups g, jsonb_array_elements(g.permissions) AS elem
			WHERE g.id = $1
			  AND elem->>'action' = 'view'
			  AND elem->>'resource' = 'config'
		`, standardUsersGroupIDTest).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "re-applying must not duplicate the view:config entry")
	})
}
