//go:build integration
// +build integration

package migrations_test

import (
	"context"
	"sort"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	standardUsersGroupIDTest = "00000000-0000-5000-8000-000000000005"
	readOnlyUsersGroupIDTest = "00000000-0000-5000-8000-000000000006"
)

// queryGroupIDs returns the (sorted) group_ids of the user with the given
// email as UUID strings.
func queryGroupIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) []string {
	t.Helper()
	var ids []string
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(ARRAY(SELECT id::text FROM unnest(group_ids) AS id), '{}')
		FROM users WHERE email = $1
	`, email).Scan(&ids)
	require.NoError(t, err, "user row for %s must exist", email)
	sort.Strings(ids)
	return ids
}

// TestMigration_DropUserRoleToGroups covers issue #907: migration 000057 maps
// each legacy role to the equivalent group BEFORE dropping the role column, so
// no user loses access; guarantees no user can be left with zero groups; and
// removes the role column from both users and sessions.
func TestMigration_DropUserRoleToGroups(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Pin the DB at v56, where the `role` column still exists and we can
	// seed role-bearing rows. (Pinning by version stays correct as newer
	// migrations land; the old "head minus one step" approach did not.)
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 56))

	// Seed one user per legacy role, each with empty group_ids (the pre-#907
	// state where authorization came from the role, not groups). Also seed a
	// row with an unknown role to exercise the fail-safe net.
	seed := func(email, role string) {
		_, e := pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, salt, role, active, group_ids, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, '', '', $2, true, '{}', NOW(), NOW())
		`, email, role)
		require.NoError(t, e, "seeding %s", email)
	}
	seed("admin@test.example", "admin")
	seed("user@test.example", "user")
	seed("readonly@test.example", "readonly")

	// Drop the valid_role CHECK so we can seed a row whose role is outside
	// the legacy {admin,user,readonly} set; this is the path the migration's
	// fail-safe net (step 3 in 000057.up.sql) is designed to catch -- without
	// it, that net is exercised by no test and could rot silently.
	_, err = pool.Exec(ctx, `ALTER TABLE users DROP CONSTRAINT IF EXISTS valid_role`)
	require.NoError(t, err)
	seed("unknown@test.example", "legacy-custom")

	// Apply exactly 000057 so the mapping assertions below are not affected
	// by later group migrations (000064 adds admins to Purchaser, etc.).
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 57))

	// Role -> group mapping must preserve access.
	assert.Equal(t, []string{defaultAdminGroupIDTest},
		queryGroupIDs(t, ctx, pool, "admin@test.example"),
		"admin must be mapped to the Administrators group")
	assert.Equal(t, []string{standardUsersGroupIDTest},
		queryGroupIDs(t, ctx, pool, "user@test.example"),
		"user must be mapped to the Standard Users group")
	assert.Equal(t, []string{readOnlyUsersGroupIDTest},
		queryGroupIDs(t, ctx, pool, "readonly@test.example"),
		"readonly must be mapped to the Read-Only Users group")
	// Fail-safe net: a user whose role does not match any known mapping must
	// land in the Read-Only Users group so the >= 1-group CHECK cannot fail.
	assert.Equal(t, []string{readOnlyUsersGroupIDTest},
		queryGroupIDs(t, ctx, pool, "unknown@test.example"),
		"unknown legacy roles must fall back to the Read-Only Users group")

	// The role column must be gone from both tables.
	assertColumnAbsent(t, ctx, pool, "users", "role")
	assertColumnAbsent(t, ctx, pool, "sessions", "role")

	// The DB must refuse a zero-group user (the >= 1-group CHECK).
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
		VALUES (gen_random_uuid(), 'zero@test.example', '', '', true, '{}', NOW(), NOW())
	`)
	require.Error(t, err, "the DB must reject a user with zero groups (users_min_one_group CHECK)")

	// A user WITH a group inserts fine.
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
		VALUES (gen_random_uuid(), 'ok@test.example', '', '', true,
		        ARRAY['`+readOnlyUsersGroupIDTest+`']::uuid[], NOW(), NOW())
	`)
	require.NoError(t, err, "a user with at least one group must insert successfully")

	// The rest of the chain must still apply cleanly over the migrated data.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
}

// assertColumnAbsent fails if the named column still exists on the table.
func assertColumnAbsent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, column string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		)
	`, table, column).Scan(&exists)
	require.NoError(t, err)
	assert.False(t, exists, "%s.%s column must be dropped", table, column)
}

// TestMigration_DropUserRoleToGroups_Down restores the role column and
// reconstructs role from group membership.
func TestMigration_DropUserRoleToGroups_Down(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Pin at v57 so the rollback below exercises exactly 000057's down.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 57))

	// Seed group-only users (post-#907 shape) before rolling back.
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
		VALUES
			(gen_random_uuid(), 'adm@test.example', '', '', true, ARRAY['`+defaultAdminGroupIDTest+`']::uuid[], NOW(), NOW()),
			(gen_random_uuid(), 'ro@test.example',  '', '', true, ARRAY['`+readOnlyUsersGroupIDTest+`']::uuid[], NOW(), NOW()),
			(gen_random_uuid(), 'std@test.example', '', '', true, ARRAY['`+standardUsersGroupIDTest+`']::uuid[], NOW(), NOW())
	`)
	require.NoError(t, err)

	// Roll back 000057.
	require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))

	roleOf := func(email string) string {
		var role string
		require.NoError(t, pool.QueryRow(ctx, `SELECT role FROM users WHERE email = $1`, email).Scan(&role))
		return role
	}
	assert.Equal(t, "admin", roleOf("adm@test.example"))
	assert.Equal(t, "readonly", roleOf("ro@test.example"))
	assert.Equal(t, "user", roleOf("std@test.example"))
}
