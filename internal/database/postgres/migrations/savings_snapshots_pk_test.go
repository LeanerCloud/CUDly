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

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		// Count how many applied migrations are above version 26.
		// Using COUNT from schema_migrations is correct even when migration
		// numbering has gaps: Steps(-n) steps back through n applied
		// migrations, so we need the actual row count, not an arithmetic
		// difference between version numbers.
		var stepsToRollback int
		err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version > 26`).Scan(&stepsToRollback)
		require.NoError(t, err)
		require.Greater(t, stepsToRollback, 0, "there should be migrations above 000026")

		// RollbackMigrations caps a single call at 10 steps; call in a loop.
		const maxRollbackPerCall = 10
		for remaining := stepsToRollback; remaining > 0; remaining -= maxRollbackPerCall {
			batch := remaining
			if batch > maxRollbackPerCall {
				batch = maxRollbackPerCall
			}
			require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, batch),
				"rollback to 000026 should succeed")
		}

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
