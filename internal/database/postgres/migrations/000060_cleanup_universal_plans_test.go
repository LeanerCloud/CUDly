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

// TestMigration_CleanupUniversalPlans asserts that migration 000057 deletes
// every purchase_plans row that has no plan_accounts entry (universal plan)
// while leaving correctly scoped plans intact.
//
// Fixture layout:
//
//	planUniversal1, planUniversal2  -- no plan_accounts rows (should be deleted)
//	planScoped                      -- has one plan_accounts row (must survive)
//
// The test also verifies that:
//   - purchase_executions rows linked to a deleted universal plan have their
//     plan_id NULLed (ON DELETE SET NULL, migration 000033).
//   - purchase_history rows linked to a deleted universal plan have their
//     plan_id NULLed (ON DELETE SET NULL, initial schema).
//   - Re-running the migration (idempotency) is a no-op when no universal
//     plans remain.
//
// The down migration is a no-op by design (deleted rows cannot be recovered
// from SQL alone), so no rollback assertion is made.
func TestMigration_CleanupUniversalPlans(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Run all migrations to head (includes 000057), then roll back one so we
	// sit at 000056, seed fixtures, and re-run to apply 000057 in isolation.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
	require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))

	// Seed: two cloud accounts (needed for plan_accounts FK).
	_, err = pool.Exec(ctx, `
		INSERT INTO cloud_accounts (id, name, provider, external_id)
		VALUES
		  ('aaaaaaaa-aaaa-aaaa-aaaa-000000000001', 'acct-1', 'aws', '111111111111'),
		  ('aaaaaaaa-aaaa-aaaa-aaaa-000000000002', 'acct-2', 'aws', '222222222222')
	`)
	require.NoError(t, err)

	// Two universal plans (no plan_accounts rows).
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_plans (id, name, services)
		VALUES
		  ('11111111-1111-1111-1111-000000000001', 'universal-plan-1', '{"aws:rds": {}}'::jsonb),
		  ('11111111-1111-1111-1111-000000000002', 'universal-plan-2', '{"aws:ec2": {}}'::jsonb)
	`)
	require.NoError(t, err)

	// One correctly scoped plan (has a plan_accounts entry).
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_plans (id, name, services)
		VALUES ('22222222-2222-2222-2222-000000000001', 'scoped-plan', '{"aws:rds": {}}'::jsonb)
	`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO plan_accounts (plan_id, account_id)
		VALUES ('22222222-2222-2222-2222-000000000001', 'aaaaaaaa-aaaa-aaaa-aaaa-000000000001')
	`)
	require.NoError(t, err)

	// Execution linked to universal-plan-1: plan_id must be NULLed after delete.
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_executions (id, plan_id, execution_id, status, step_number, scheduled_date)
		VALUES (
		  'eeeeeeee-eeee-eeee-eeee-000000000001',
		  '11111111-1111-1111-1111-000000000001',
		  'eeeeeeee-eeee-eeee-eeee-000000000002',
		  'completed', 1, NOW()
		)
	`)
	require.NoError(t, err)

	// History row linked to universal-plan-1: plan_id must be NULLed after delete.
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_history
		    (id, account_id, purchase_id, timestamp, provider, service, region,
		     resource_type, term, payment, plan_id, plan_name)
		VALUES (
		  'hhhhhhhh-hhhh-hhhh-hhhh-000000000001',
		  '111111111111',
		  'ri-abc123',
		  NOW(), 'aws', 'rds', 'us-east-1', 'm5.large', 36, 'all-upfront',
		  '11111111-1111-1111-1111-000000000001',
		  'universal-plan-1'
		)
	`)
	require.NoError(t, err)

	// Apply migration 000057.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

	// Universal plans must be gone.
	var universalCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM purchase_plans
		WHERE id IN (
		  '11111111-1111-1111-1111-000000000001',
		  '11111111-1111-1111-1111-000000000002'
		)
	`).Scan(&universalCount)
	require.NoError(t, err)
	assert.Equal(t, 0, universalCount, "both universal plans must be deleted by migration 000057")

	// Scoped plan must survive.
	var scopedCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM purchase_plans
		WHERE id = '22222222-2222-2222-2222-000000000001'
	`).Scan(&scopedCount)
	require.NoError(t, err)
	assert.Equal(t, 1, scopedCount, "scoped plan (with plan_accounts) must survive migration 000057")

	// Execution linked to deleted universal plan must have plan_id NULLed.
	var execPlanID *string
	err = pool.QueryRow(ctx, `
		SELECT plan_id::TEXT FROM purchase_executions
		WHERE id = 'eeeeeeee-eeee-eeee-eeee-000000000001'
	`).Scan(&execPlanID)
	require.NoError(t, err)
	assert.Nil(t, execPlanID, "execution.plan_id must be NULLed when universal plan is deleted (ON DELETE SET NULL)")

	// History row linked to deleted universal plan must have plan_id NULLed.
	var histPlanID *string
	err = pool.QueryRow(ctx, `
		SELECT plan_id::TEXT FROM purchase_history
		WHERE id = 'hhhhhhhh-hhhh-hhhh-hhhh-000000000001'
	`).Scan(&histPlanID)
	require.NoError(t, err)
	assert.Nil(t, histPlanID, "history.plan_id must be NULLed when universal plan is deleted (ON DELETE SET NULL)")

	// Idempotency: re-running the migration path must be a no-op.
	require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))
	// Re-seed only the scoped plan to simulate a clean DB at version 56.
	// The universal plans are intentionally absent (already cleaned up).
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

	var postIdempotentCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM purchase_plans`).Scan(&postIdempotentCount)
	require.NoError(t, err)
	assert.Equal(t, 1, postIdempotentCount, "idempotent re-run must leave the one scoped plan intact")
}
