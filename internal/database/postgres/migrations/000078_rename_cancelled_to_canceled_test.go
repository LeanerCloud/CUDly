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

// renameCanceledVersion is the version of the expand-contract rename migration
// (000078). renameCanceledPrevVersion is the version immediately below it,
// used to roll the schema back down across 000078's down.sql.
const (
	renameCanceledVersion     = 78
	renameCanceledPrevVersion = 77
)

// TestMigration_000078_RollbackWithCanceledRows is the regression guard for
// the CRITICAL rollback-ordering bug CodeRabbit flagged on PR #1277.
//
// The expand-contract rename (000078) backfills purchase_executions.status and
// ri_exchange_history.status from 'cancelled' to 'canceled', adds a canceled_by
// column, and widens both status CHECK constraints to accept both spellings.
//
// The DOWN migration must:
//  1. Convert 'canceled' rows back to 'cancelled' BEFORE re-adding the
//     'cancelled'-only CHECK constraint. The original (buggy) ordering re-added
//     the narrowed constraint first, so any row still in the new 'canceled'
//     state violated the constraint mid-rollback and the rollback FAILED.
//  2. Backfill canceled_by into cancelled_by BEFORE dropping canceled_by, so the
//     actor attribution recorded by new code during the deploy window survives.
//
// This test seeds rows in the NEW state (status='canceled', canceled_by set)
// after the up migration, then rolls back and asserts the rollback succeeds,
// the data is converted back, and the actor attribution is preserved.
func TestMigration_000078_RollbackWithCanceledRows(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Apply the full migration set, ending at 000078 (or later head). The
	// dual-accept constraint and canceled_by column are now in place.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

	// Seed a purchase_executions row in the NEW canceled state with an actor
	// recorded in canceled_by (what new code writes during the deploy window).
	const (
		execID    = "78787878-7878-7878-7878-000000000001"
		execActor = "canceler@test.example"
	)
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_executions
		    (id, execution_id, status, step_number, scheduled_date, canceled_by)
		VALUES (
		  '78787878-7878-7878-7878-000000000002',
		  $1, 'canceled', 1, NOW(), $2
		)
	`, execID, execActor)
	require.NoError(t, err, "seeding a status='canceled' execution must satisfy the widened CHECK")

	// Seed an ri_exchange_history row in the NEW canceled state.
	const exchangeID = "78787878-7878-7878-7878-000000000003"
	_, err = pool.Exec(ctx, `
		INSERT INTO ri_exchange_history
		    (id, account_id, region, source_ri_ids, source_instance_type,
		     source_count, target_offering_id, target_instance_type,
		     target_count, status)
		VALUES (
		  $1, '123456789012', 'us-east-1', ARRAY['ri-1'], 'm5.large',
		  1, 'offer-1', 'm5.xlarge', 1, 'canceled'
		)
	`, exchangeID)
	require.NoError(t, err, "seeding a status='canceled' exchange must satisfy the widened CHECK")

	// --- The critical assertion: rolling back with 'canceled' rows present
	//     must SUCCEED. With the buggy ordering this errored out because the
	//     re-added 'cancelled'-only constraint rejected the live 'canceled' rows.
	require.NoError(t,
		migrations.MigrateToVersion(ctx, pool, migrationsPath, renameCanceledPrevVersion),
		"rollback of 000078 must succeed even with rows in the new 'canceled' state")

	// Data must have been converted back to the British spelling.
	var execStatus string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM purchase_executions WHERE execution_id = $1`, execID,
	).Scan(&execStatus))
	assert.Equal(t, "cancelled", execStatus,
		"DOWN must convert purchase_executions 'canceled' back to 'cancelled'")

	var exchangeStatus string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM ri_exchange_history WHERE id = $1`, exchangeID,
	).Scan(&exchangeStatus))
	assert.Equal(t, "cancelled", exchangeStatus,
		"DOWN must convert ri_exchange_history 'canceled' back to 'cancelled'")

	// Actor attribution must survive the column drop: canceled_by was drained
	// into cancelled_by before canceled_by was dropped (finding 2).
	var legacyActor string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT cancelled_by FROM purchase_executions WHERE execution_id = $1`, execID,
	).Scan(&legacyActor))
	assert.Equal(t, execActor, legacyActor,
		"DOWN must backfill canceled_by into cancelled_by before dropping canceled_by")

	// canceled_by must be gone after the rollback.
	var canceledByExists bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'purchase_executions' AND column_name = 'canceled_by'
		)
	`).Scan(&canceledByExists))
	assert.False(t, canceledByExists, "DOWN must drop the canceled_by column")

	// The narrowed constraint must be back: writing 'canceled' must now fail.
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_executions
		    (id, execution_id, status, step_number, scheduled_date)
		VALUES (
		  '78787878-7878-7878-7878-000000000004',
		  '78787878-7878-7878-7878-000000000005', 'canceled', 1, NOW()
		)
	`)
	require.Error(t, err, "after rollback the 'cancelled'-only CHECK must reject 'canceled'")

	// And the legacy spelling must still be accepted post-rollback.
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_executions
		    (id, execution_id, status, step_number, scheduled_date)
		VALUES (
		  '78787878-7878-7878-7878-000000000006',
		  '78787878-7878-7878-7878-000000000007', 'cancelled', 1, NOW()
		)
	`)
	require.NoError(t, err, "after rollback the 'cancelled'-only CHECK must accept 'cancelled'")
}

// TestMigration_000078_UpAcceptsBothSpellings verifies the EXPAND half:
// after the up migration both 'cancelled' (legacy) and 'canceled' (new) satisfy
// the widened CHECK constraints, and pre-existing 'cancelled' rows are
// backfilled to 'canceled'. This is what makes the rolling deploy safe.
func TestMigration_000078_UpAcceptsBothSpellings(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Pin just below 000078 and seed a legacy 'cancelled' row + cancelled_by
	// so we can prove the up migration backfills both the status and the column.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, renameCanceledPrevVersion))

	const (
		execID    = "77777777-7777-7777-7777-000000000001"
		execActor = "legacy-canceler@test.example"
	)
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_executions
		    (id, execution_id, status, step_number, scheduled_date, cancelled_by)
		VALUES (
		  '77777777-7777-7777-7777-000000000002',
		  $1, 'cancelled', 1, NOW(), $2
		)
	`, execID, execActor)
	require.NoError(t, err, "seeding a legacy 'cancelled' row below 000078 must succeed")

	// Apply 000078 (EXPAND + BACKFILL).
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, renameCanceledVersion))

	// Pre-existing 'cancelled' must have been backfilled to 'canceled'.
	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM purchase_executions WHERE execution_id = $1`, execID,
	).Scan(&status))
	assert.Equal(t, "canceled", status, "UP must backfill legacy 'cancelled' to 'canceled'")

	// cancelled_by must have been backfilled into canceled_by.
	var newActor string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT canceled_by FROM purchase_executions WHERE execution_id = $1`, execID,
	).Scan(&newActor))
	assert.Equal(t, execActor, newActor, "UP must backfill cancelled_by into canceled_by")

	// Both spellings must satisfy the widened constraint (dual-write window).
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_executions
		    (id, execution_id, status, step_number, scheduled_date)
		VALUES (
		  '77777777-7777-7777-7777-000000000003',
		  '77777777-7777-7777-7777-000000000004', 'cancelled', 1, NOW()
		)
	`)
	require.NoError(t, err, "widened CHECK must still accept legacy 'cancelled'")

	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_executions
		    (id, execution_id, status, step_number, scheduled_date)
		VALUES (
		  '77777777-7777-7777-7777-000000000005',
		  '77777777-7777-7777-7777-000000000006', 'canceled', 1, NOW()
		)
	`)
	require.NoError(t, err, "widened CHECK must accept new 'canceled'")
}
