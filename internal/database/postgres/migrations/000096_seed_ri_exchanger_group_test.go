//go:build integration
// +build integration

package migrations_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// riExchangerGroupIDTest is the UUID migration 000096 seeds the RI Exchanger
// group at (issue #1644). Must match DefaultRIExchangerGroupID in
// internal/auth/types.go and RI_EXCHANGER_GROUP_ID in frontend/src/permissions.ts.
const riExchangerGroupIDTest = "00000000-0000-5000-8000-000000000008"

// TestMigration_SeedRIExchangerGroup covers issue #1644.
//
// The carve-out and this seed are only correct together: adding
// execute:ri-exchange to adminCarvedOuts without a group that grants it
// leaves the verb unreachable for everyone, because PR #1737's grant ceiling
// refuses to add a carved-out verb through the API. A seed migration that
// silently no-ops therefore does not merely miss a nice-to-have, it takes
// both routed execute endpoints offline -- which is exactly how 000059 failed
// (#942), by running ON CONFLICT DO NOTHING onto an already-occupied UUID.
// These assertions exist so that failure is loud.
func TestMigration_SeedRIExchangerGroup(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	t.Run("RI Exchanger seeded, system-managed, granting execute:ri-exchange", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		var name string
		var systemManaged bool
		err = pool.QueryRow(ctx,
			`SELECT name, system_managed FROM groups WHERE id = $1`, riExchangerGroupIDTest).
			Scan(&name, &systemManaged)
		require.NoError(t, err, "RI Exchanger group must exist at UUID %s", riExchangerGroupIDTest)
		assert.Equal(t, "RI Exchanger", name)
		assert.True(t, systemManaged,
			"the group must be system-managed so the API cannot edit or delete it")

		// Round-trip by name, so a second row under a different id would fail.
		var id string
		err = pool.QueryRow(ctx, `SELECT id FROM groups WHERE name = 'RI Exchanger'`).Scan(&id)
		require.NoError(t, err, "exactly one group named 'RI Exchanger' must exist")
		assert.Equal(t, riExchangerGroupIDTest, id)

		// The verb itself. Asserting the group exists is not enough: a seed
		// that created the row with the wrong permission list would leave the
		// carve-out ungrantable just as surely as no row at all.
		var grantsExecute bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM groups
				WHERE id = $1
				  AND permissions @> '[{"action":"execute","resource":"ri-exchange"}]'::jsonb
			)`, riExchangerGroupIDTest).Scan(&grantsExecute)
		require.NoError(t, err)
		assert.True(t, grantsExecute,
			"RI Exchanger must grant execute:ri-exchange, or the carved-out verb is unreachable for every principal")
	})

	t.Run("admin-backfill: Administrators members land in RI Exchanger", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin just below 000096 so the backfill is observed firing, rather
		// than inferred from the end state.
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 95))

		const adminEmail = "admin-riexchanger-test@test.example"
		_, err = pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, '', '', true, ARRAY[$2::uuid], NOW(), NOW())
		`, adminEmail, adminGroupIDForPurchaserTest)
		require.NoError(t, err)

		before := queryGroupIDsByEmail(t, ctx, pool, adminEmail)
		require.NotContains(t, before, riExchangerGroupIDTest,
			"precondition: the admin must not already be an RI Exchanger")

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		after := queryGroupIDsByEmail(t, ctx, pool, adminEmail)
		assert.Contains(t, after, riExchangerGroupIDTest,
			"every Administrators member must be backfilled into RI Exchanger, or admins lose RI-exchange execute on upgrade")
		assert.Contains(t, after, adminGroupIDForPurchaserTest,
			"the backfill must add a group, not replace the user's existing membership")
	})

	t.Run("backfill is idempotent and leaves no duplicate entry", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		const adminEmail = "admin-riexchanger-idempotent@test.example"
		_, err = pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, '', '', true, ARRAY[$2::uuid, $3::uuid], NOW(), NOW())
		`, adminEmail, adminGroupIDForPurchaserTest, riExchangerGroupIDTest)
		require.NoError(t, err)

		// Re-running migrations.RunMigrations here would NOT exercise the
		// idempotency guards below: m.Up() returns ErrNoChange once the
		// database is already at the latest version, so the migration body
		// never runs a second time and this subtest would pass unconditionally
		// regardless of whether the DO block's guards work. Reading the up
		// migration file and executing its SQL directly re-applies the DO
		// block for real, the same way 000095's re-run test does.
		upSQL, err := os.ReadFile(filepath.Join(migrationsPath, "000096_seed_ri_exchanger_group.up.sql"))
		require.NoError(t, err, "the up migration file must be readable")
		_, err = pool.Exec(ctx, string(upSQL))
		require.NoError(t, err, "re-running 000096 on an already-seeded database must be a no-op, not an error")

		after := queryGroupIDsByEmail(t, ctx, pool, adminEmail)
		count := 0
		for _, id := range after {
			if id == riExchangerGroupIDTest {
				count++
			}
		}
		assert.Equal(t, 1, count, "RI Exchanger must appear exactly once in group_ids")
	})
}

// TestMigration_SeedRIExchangerGroupDownRollback covers the down migration
// (PR #1758 review, Finding 3). The UPDATE that detaches members and the
// DELETE that drops the row must stay consistent: before this fix the UPDATE
// ran unconditionally while the DELETE alone was guarded on
// name = 'RI Exchanger', so if an operator had renamed the group onto this
// id, the rollback detached every member from a group it then refused to
// delete -- a half-rollback, worse than doing all of it or none of it.
func TestMigration_SeedRIExchangerGroupDownRollback(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	t.Run("ordinary rollback: detaches members and drops the row together", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		const adminEmail = "admin-riexchanger-rollback@test.example"
		_, err = pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, '', '', true, ARRAY[$2::uuid, $3::uuid], NOW(), NOW())
		`, adminEmail, adminGroupIDForPurchaserTest, riExchangerGroupIDTest)
		require.NoError(t, err)

		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, previousMigrationVersion(t, 96)),
			"rolling back 000096 must succeed")

		after := queryGroupIDsByEmail(t, ctx, pool, adminEmail)
		assert.NotContains(t, after, riExchangerGroupIDTest,
			"the down migration must detach the member from the RI Exchanger group")
		assert.Contains(t, after, adminGroupIDForPurchaserTest,
			"the down migration must not touch unrelated group membership")

		var exists bool
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM groups WHERE id = $1)`, riExchangerGroupIDTest).Scan(&exists))
		assert.False(t, exists, "the down migration must drop the seeded group row")
	})

	t.Run("renamed group: rollback touches neither membership nor the row", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		const adminEmail = "admin-riexchanger-renamed@test.example"
		_, err = pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, '', '', true, ARRAY[$2::uuid, $3::uuid], NOW(), NOW())
		`, adminEmail, adminGroupIDForPurchaserTest, riExchangerGroupIDTest)
		require.NoError(t, err)

		// An operator renamed the seeded row onto something else -- direct SQL
		// is the only way this can happen, since the group is system_managed
		// and the API refuses to edit it, but the down migration must not
		// assume that never occurred.
		_, err = pool.Exec(ctx, `UPDATE groups SET name = 'Renamed By Operator' WHERE id = $1`, riExchangerGroupIDTest)
		require.NoError(t, err, "precondition: rename the seeded group")

		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, previousMigrationVersion(t, 96)),
			"rolling back 000096 must succeed even when its guard refuses to act")

		after := queryGroupIDsByEmail(t, ctx, pool, adminEmail)
		assert.Contains(t, after, riExchangerGroupIDTest,
			"the down migration's guard refusing the DELETE must also refuse the UPDATE -- "+
				"detaching members from a group it then declines to drop is a half-rollback")

		var name string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT name FROM groups WHERE id = $1`, riExchangerGroupIDTest).Scan(&name),
			"the renamed group must still exist -- the guard must refuse to drop it")
		assert.Equal(t, "Renamed By Operator", name)
	})
}
