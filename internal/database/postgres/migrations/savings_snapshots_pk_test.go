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

// TestMigration_SavingsSnapshotsPK verifies that migration 000027 is
// idempotent on a fresh database.
//
// On a fresh DB, migration 000018 already adds savings_snapshots_pkey. Before
// the fix, 000027's bare ADD CONSTRAINT would fail with "multiple primary
// keys for table savings_snapshots". After the fix, the DROP CONSTRAINT IF
// EXISTS guard makes 000027 safe to apply regardless of whether 000018 has
// already created the constraint.
func TestMigration_SavingsSnapshotsPK(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	t.Run("full migration from scratch succeeds (000018 + 000027 coexist)", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Running all migrations must succeed on a fresh DB — 000027's
		// DROP CONSTRAINT IF EXISTS prevents "multiple primary keys"
		// that the pre-fix ADD CONSTRAINT caused.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""),
			"full migration from scratch should succeed with idempotent 000027")

		// The primary key must exist and cover (id, timestamp).
		var constraintExists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'savings_snapshots_pkey'
				  AND conrelid = 'savings_snapshots'::regclass
				  AND contype = 'p'
			)`).Scan(&constraintExists)
		require.NoError(t, err)
		assert.True(t, constraintExists, "savings_snapshots_pkey should exist after migrations")
	})

	t.Run("rollback to 000026 then re-apply 000027 succeeds", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin the schema at 000027 (which re-created the PK over 000018's
		// version), then roll back exactly 000027. The previous
		// count-rows-in-schema_migrations approach was broken: golang-migrate
		// keeps a single (version, dirty) row, so the count was always 1 and
		// the loop only ever rolled back the newest migration.
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 27))
		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1),
			"rollback of 000027 should succeed")

		// Verify the constraint is gone (000027 was rolled back).
		var constraintExists bool
		err = pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'savings_snapshots_pkey'
				  AND conrelid = 'savings_snapshots'::regclass
				  AND contype = 'p'
			)`).Scan(&constraintExists)
		require.NoError(t, err)
		assert.False(t, constraintExists, "savings_snapshots_pkey should be gone after rollback past 000027")

		// Re-apply all migrations — 000027 must succeed even though
		// 000018's schema_migrations entry is still present (that migration
		// was not rolled back, though its original constraint was superseded
		// when 000027 ran and dropped/re-created savings_snapshots_pkey).
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""),
			"re-applying 000027 over a DB where 000018 already ran must succeed")

		err = pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'savings_snapshots_pkey'
				  AND conrelid = 'savings_snapshots'::regclass
				  AND contype = 'p'
			)`).Scan(&constraintExists)
		require.NoError(t, err)
		assert.True(t, constraintExists, "savings_snapshots_pkey should be restored after re-apply")
	})
}
