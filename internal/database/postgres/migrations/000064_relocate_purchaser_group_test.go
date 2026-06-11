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
	// purchaserGroupIDTest is the new UUID assigned to the Purchaser group
	// by migration 000064 (relocated from 000005 to fix issue #942).
	purchaserGroupIDTest = "00000000-0000-5000-8000-000000000007"
	// adminGroupIDForPurchaserTest mirrors defaultAdminGroupIDTest; copied
	// here to make the test self-contained without depending on declaration
	// order across test files in the same package.
	adminGroupIDForPurchaserTest = "00000000-0000-5000-8000-000000000001"
)

// queryGroupIDsByName returns the sorted group_ids of a user with the given
// email, as UUID strings.
func queryGroupIDsByEmail(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) []string {
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

// TestMigration_RelocatePurchaserGroup covers issue #942: migration 000064
// must ensure the Purchaser group exists at UUID 00000000-0000-5000-8000-000000000007
// on databases where migration 000059 was silently no-op'd due to the UUID
// collision with Standard Users (000057), and must backfill all Administrators
// group members into Purchaser.
func TestMigration_RelocatePurchaserGroup(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	t.Run("Purchaser seeded at new UUID after full migration run", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		// Purchaser must exist at the new UUID.
		var name string
		err = pool.QueryRow(ctx, `SELECT name FROM groups WHERE id = $1`, purchaserGroupIDTest).Scan(&name)
		require.NoError(t, err, "Purchaser group must exist at UUID %s", purchaserGroupIDTest)
		assert.Equal(t, "Purchaser", name, "group at new UUID must be named 'Purchaser'")

		// Round-trip: lookup by name must return the new UUID.
		var id string
		err = pool.QueryRow(ctx, `SELECT id FROM groups WHERE name = 'Purchaser'`).Scan(&id)
		require.NoError(t, err, "a group named 'Purchaser' must exist")
		assert.Equal(t, purchaserGroupIDTest, id, "Purchaser name must resolve to the new UUID")
	})

	t.Run("admin-backfill: Administrators members land in Purchaser", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin the DB just below 000064 so we can seed an admin and
		// verify the backfill fires when 000064 applies. (Pinning by
		// version stays correct as newer migrations land; the old
		// "head minus one step" approach did not.)
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 63))

		// Seed an admin user in the Administrators group but NOT in Purchaser.
		const adminEmail = "admin-purchaser-test@test.example"
		_, err = pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, '', '', true,
			        ARRAY[$2::uuid], NOW(), NOW())
		`, adminEmail, adminGroupIDForPurchaserTest)
		require.NoError(t, err)

		ids := queryGroupIDsByEmail(t, ctx, pool, adminEmail)
		require.NotContains(t, ids, purchaserGroupIDTest,
			"test setup: admin must not be in Purchaser before migration 000064 runs")

		// Re-apply 000064.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		// Admin must now be in Purchaser.
		ids = queryGroupIDsByEmail(t, ctx, pool, adminEmail)
		assert.Contains(t, ids, purchaserGroupIDTest,
			"migration 000064 admin-backfill must add Administrators members to Purchaser")
	})

	t.Run("idempotent: re-running migrations does not duplicate Purchaser membership", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Seed an admin before first run so 000064 backfill fires.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		const adminEmail = "idempotent-test@test.example"
		_, err = pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, '', '', true,
			        ARRAY[$2::uuid, $3::uuid], NOW(), NOW())
		`, adminEmail, adminGroupIDForPurchaserTest, purchaserGroupIDTest)
		require.NoError(t, err)

		// Run migrations again (no-op at DB level, but 000064's DO block runs again).
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		ids := queryGroupIDsByEmail(t, ctx, pool, adminEmail)
		count := 0
		for _, id := range ids {
			if id == purchaserGroupIDTest {
				count++
			}
		}
		assert.Equal(t, 1, count, "Purchaser UUID must appear exactly once in group_ids after two runs")
	})
}
