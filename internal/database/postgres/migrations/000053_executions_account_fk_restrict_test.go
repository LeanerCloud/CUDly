//go:build integration
// +build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration_ExecutionsAccountFKRestrict locks down migration 000053: the
// purchase_executions.cloud_account_id FK must be ON DELETE RESTRICT after
// the migration runs. Issue #606 — silent-orphan regression guard.
func TestMigration_ExecutionsAccountFKRestrict(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Pin the schema at 000053 so the assertions exercise this migration's
	// direct effects; later migrations change unrelated parts of the same
	// tables (e.g. purchase_executions.execution_id becomes a UUID).
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 53))

	// Verify the FK is RESTRICT after the migration.
	var deleteAction string
	err = pool.QueryRow(ctx, `
		SELECT confdeltype::text
		FROM pg_constraint
		WHERE conname = 'purchase_executions_cloud_account_id_fkey'
	`).Scan(&deleteAction)
	require.NoError(t, err)
	assert.Equal(t, "r", deleteAction,
		"expected confdeltype 'r' (RESTRICT) after 000053, got %q", deleteAction)

	// Negative-space guard: migration 000053 must only tighten the executions
	// FK. The recommendations FK has been ON DELETE CASCADE since its creation
	// in 000030 and must be left untouched.
	var recsDeleteAction string
	err = pool.QueryRow(ctx, `
		SELECT confdeltype::text
		FROM pg_constraint
		WHERE conname = 'recommendations_cloud_account_id_fkey'
	`).Scan(&recsDeleteAction)
	require.NoError(t, err)
	assert.Equal(t, "c", recsDeleteAction,
		"recommendations FK must stay CASCADE after 000053, got %q", recsDeleteAction)

	// Behavioural test: insert an account + a pending execution that
	// references it, then attempt to delete the account. Postgres must
	// raise a foreign-key-violation (SQLSTATE 23503).
	_, err = pool.Exec(ctx, `
		INSERT INTO cloud_accounts (id, name, provider, external_id)
		VALUES ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa', 'fk-test', 'aws', '999999999999')
	`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_executions
		    (execution_id, status, step_number, scheduled_date, cloud_account_id)
		VALUES
		    ('eeeeeeee-eeee-eeee-eeee-eeeeeeee0001', 'pending', 1, NOW(), 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa')
	`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `DELETE FROM cloud_accounts WHERE id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'`)
	require.Error(t, err, "DELETE must fail with FK violation")
	// pgx surfaces the SQLSTATE in the error string ("SQLSTATE 23503").
	assert.True(t,
		strings.Contains(err.Error(), "23503") || strings.Contains(err.Error(), "foreign key"),
		"expected FK violation, got %v", err)

	// RESTRICT applies to every referencing row regardless of status, so the
	// account delete only succeeds once the execution row itself is removed
	// (the API layer's Cancel-All-And-Delete flow does exactly that).
	_, err = pool.Exec(ctx, `
		DELETE FROM purchase_executions
		 WHERE execution_id = 'eeeeeeee-eeee-eeee-eeee-eeeeeeee0001'
	`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `DELETE FROM cloud_accounts WHERE id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'`)
	require.NoError(t, err, "DELETE should succeed once referencing executions are removed")
}

// TestMigration_ExecutionsAccountFKRestrict_Rollback asserts that the
// 000053 down migration restores the original SET NULL behaviour, so an
// emergency rollback re-introduces the (documented) silent-orphan
// behaviour rather than leaving the database in an indeterminate state.
func TestMigration_ExecutionsAccountFKRestrict_Rollback(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Pin the schema at 000053, then roll back exactly that migration. A
	// fixed step count from head would exercise whichever migration happens
	// to be newest instead of 000053's down.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 53))
	require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))

	var deleteAction string
	err = pool.QueryRow(ctx, `
		SELECT confdeltype::text
		FROM pg_constraint
		WHERE conname = 'purchase_executions_cloud_account_id_fkey'
	`).Scan(&deleteAction)
	require.NoError(t, err)
	assert.Equal(t, "n", deleteAction,
		"expected confdeltype 'n' (SET NULL) after rollback, got %q", deleteAction)

	// Negative-space guard: rolling back 000053 must not touch the
	// recommendations FK — it has been CASCADE since 000030 and should stay so.
	var recsDeleteAction string
	err = pool.QueryRow(ctx, `
		SELECT confdeltype::text
		FROM pg_constraint
		WHERE conname = 'recommendations_cloud_account_id_fkey'
	`).Scan(&recsDeleteAction)
	require.NoError(t, err)
	assert.Equal(t, "c", recsDeleteAction,
		"recommendations FK must stay CASCADE after rollback, got %q", recsDeleteAction)
}
