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

		// Determine how many migrations are above 000027 by counting
		// migrations numbered higher, then rolling back that many plus one.
		// Simpler: roll back until 000027 is gone, i.e. roll back enough
		// steps that version 000027 is undone.
		//
		// We use a raw query to find the current version, roll back
		// (current - 26) steps to land at 000026, then run Up again.
		var currentVersion int
		err = pool.QueryRow(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&currentVersion)
		require.NoError(t, err)

		stepsToRollback := currentVersion - 26
		require.Greater(t, stepsToRollback, 0, "there should be migrations above 000026")

		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, stepsToRollback),
			"rollback to 000026 should succeed")

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
		// 000018 is still in effect (it was not rolled back).
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
