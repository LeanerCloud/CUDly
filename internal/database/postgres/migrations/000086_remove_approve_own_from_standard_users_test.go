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

// standardUsersHasPurchaseVerb reports whether the Standard Users group's
// permissions JSONB array grants the given action on the purchases resource.
func standardUsersHasPurchaseVerb(t *testing.T, ctx context.Context, pool *pgxpool.Pool, action string) bool {
	t.Helper()
	var has bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM groups g, jsonb_array_elements(g.permissions) AS elem
			WHERE g.id = $1
			  AND elem->>'action' = $2
			  AND elem->>'resource' = 'purchases'
		)
	`, standardUsersGroupIDTest, action).Scan(&has)
	require.NoError(t, err, "querying %s:purchases on Standard Users group", action)
	return has
}

// TestMigration_RemoveApproveOwnFromStandardUsers covers issue #1407 (four-eyes
// principle): migration 000086 removes approve-own:purchases from the seeded
// Standard Users group so no user can approve a purchase they created merely by
// virtue of ownership. The removal must be surgical -- the sibling own-scoped
// verbs (cancel-own, retry-own) seeded alongside it in 000057 must be retained.
func TestMigration_RemoveApproveOwnFromStandardUsers(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	t.Run("approve-own removed, sibling own-verbs retained", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin at 000083, the last migration before 000086. approve-own was
		// seeded into Standard Users by 000057 and must still be present here;
		// this is the pre-fix state the migration corrects.
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 83))

		require.True(t, standardUsersHasPurchaseVerb(t, ctx, pool, "approve-own"),
			"precondition: Standard Users must hold approve-own:purchases at v83 (seeded by 000057)")
		require.True(t, standardUsersHasPurchaseVerb(t, ctx, pool, "cancel-own"),
			"precondition: Standard Users must hold cancel-own:purchases at v83")
		require.True(t, standardUsersHasPurchaseVerb(t, ctx, pool, "retry-own"),
			"precondition: Standard Users must hold retry-own:purchases at v83")

		// Apply the remaining chain, which includes 000086.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		// Four-eyes (issue #1407): approve-own must be gone.
		assert.False(t, standardUsersHasPurchaseVerb(t, ctx, pool, "approve-own"),
			"migration 000086 must remove approve-own:purchases from Standard Users")

		// The JSONB filter must be surgical: sibling own-scoped verbs stay.
		assert.True(t, standardUsersHasPurchaseVerb(t, ctx, pool, "cancel-own"),
			"migration 000086 must not remove cancel-own:purchases")
		assert.True(t, standardUsersHasPurchaseVerb(t, ctx, pool, "retry-own"),
			"migration 000086 must not remove retry-own:purchases")
	})

	t.Run("down migration restores approve-own", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Full head has 000086 applied, so approve-own is removed.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		require.False(t, standardUsersHasPurchaseVerb(t, ctx, pool, "approve-own"),
			"head state: approve-own must be absent after 000086")

		// Roll back one step (000086.down) and confirm approve-own is restored.
		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))
		assert.True(t, standardUsersHasPurchaseVerb(t, ctx, pool, "approve-own"),
			"000086 down migration must restore approve-own:purchases to Standard Users")
	})
}
