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

// TestMigration_SplitSavingsPlans drives the 000040 migration through
// the three scenarios from the plan §7: umbrella-only, umbrella + PR
// #71's sagemaker row, and fresh install. Round-trips up/down to verify
// no row identity is lost in the lossy down migration.
func TestMigration_SplitSavingsPlans(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	type spRow struct { //nolint:govet // field order matches Scan() call order for clarity; reordering for alignment would obscure the SQL column correspondence
		service      string
		term         int
		payment      string
		enabled      bool
		coverage     float64
		rampSchedule string
	}

	// queryAWSSPRows returns a map keyed by service slug for every
	// `(aws, savings-plans*)` and `(aws, sagemaker)` row in
	// service_configs. Lets the assertions be order-independent.
	queryAWSSPRows := func(t *testing.T, pool *pgxpool.Pool) map[string]spRow {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT service, term, payment, enabled, coverage, ramp_schedule
			FROM service_configs
			WHERE provider = 'aws'
			AND (service LIKE 'savings-plans%' OR service IN ('savingsplans', 'sagemaker'))`)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]spRow{}
		for rows.Next() {
			var r spRow
			require.NoError(t, rows.Scan(&r.service, &r.term, &r.payment, &r.enabled, &r.coverage, &r.rampSchedule))
			out[r.service] = r
		}
		require.NoError(t, rows.Err())
		return out
	}

	t.Run("scenario 1: umbrella only", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin the DB at 000039 (just below the migration under test) and
		// seed before applying 000040. (Pinning by version stays correct
		// as newer migrations land; the old "head minus one step"
		// approach did not.)
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 39))

		// Seed: a single umbrella row with non-default values.
		_, err = pool.Exec(ctx, `
			INSERT INTO service_configs (provider, service, enabled, term, payment, coverage, ramp_schedule)
			VALUES ('aws', 'savings-plans', true, 1, 'no-upfront', 90.50, 'weekly-25pct')`)
		require.NoError(t, err)

		// Up: apply exactly 000040.
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 40))

		got := queryAWSSPRows(t, pool)
		expected := []string{
			"savings-plans-compute",
			"savings-plans-ec2instance",
			"savings-plans-sagemaker",
			"savings-plans-database",
		}
		require.Len(t, got, 4, "expected 4 per-plan-type rows after up; umbrella should be deleted")
		_, hasUmbrella := got["savings-plans"]
		assert.False(t, hasUmbrella, "umbrella row should be deleted")
		for _, slug := range expected {
			r, ok := got[slug]
			require.True(t, ok, "missing row %s", slug)
			assert.Equal(t, 1, r.term)
			assert.Equal(t, "no-upfront", r.payment)
			assert.Equal(t, true, r.enabled)
			assert.InDelta(t, 90.50, r.coverage, 0.001)
			assert.Equal(t, "weekly-25pct", r.rampSchedule)
		}

		// Down: roll back 000040.
		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))
		got = queryAWSSPRows(t, pool)
		require.Len(t, got, 1, "expected umbrella row restored after down")
		r, ok := got["savings-plans"]
		require.True(t, ok)
		assert.Equal(t, 1, r.term)
		assert.Equal(t, "no-upfront", r.payment)

		// Forward again to head: the full chain must re-apply cleanly and
		// land on the four split rows.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		got = queryAWSSPRows(t, pool)
		require.Len(t, got, 4, "expected 4 per-plan-type rows after migrating to head")
		_, hasUmbrella = got["savings-plans"]
		assert.False(t, hasUmbrella, "umbrella row should stay deleted at head")
	})

	t.Run("scenario 2: umbrella + PR #71 sagemaker row (different values)", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin the DB at 000039, just below the migration under test.
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 39))

		// Seed both rows with intentionally divergent term/payment so
		// we can verify which value wins per slot.
		_, err = pool.Exec(ctx, `
			INSERT INTO service_configs (provider, service, enabled, term, payment, coverage)
			VALUES ('aws', 'savings-plans', true, 3, 'all-upfront', 80.00)`)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `
			INSERT INTO service_configs (provider, service, enabled, term, payment, coverage)
			VALUES ('aws', 'sagemaker', true, 1, 'partial-upfront', 75.00)`)
		require.NoError(t, err)

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		got := queryAWSSPRows(t, pool)
		// Expect: 4 split rows. Umbrella deleted by 000040; PR #71's
		// `(aws, sagemaker)` row is also deleted by 000045 (issue #133)
		// after 000040 has copied its term/payment forward into the
		// new `savings-plans-sagemaker` slot.
		require.Len(t, got, 4)
		_, hasUmbrella := got["savings-plans"]
		assert.False(t, hasUmbrella, "umbrella should be deleted")
		_, hasSagemaker := got["sagemaker"]
		assert.False(t, hasSagemaker, "PR #71 sagemaker row should be deleted by 000045 (issue #133)")

		// sagemaker slot inherits from PR #71's row (1, partial-upfront).
		// The inheritance happens during 000040, which runs before
		// 000045 deletes the source row, so the values still flow
		// through into the persisted split row.
		smRow := got["savings-plans-sagemaker"]
		assert.Equal(t, 1, smRow.term, "sagemaker slot should inherit term from (aws, sagemaker)")
		assert.Equal(t, "partial-upfront", smRow.payment, "sagemaker slot should inherit payment from (aws, sagemaker)")

		// Other three slots inherit from the umbrella (3, all-upfront).
		for _, slug := range []string{"savings-plans-compute", "savings-plans-ec2instance", "savings-plans-database"} {
			r := got[slug]
			assert.Equal(t, 3, r.term, "%s should inherit term from umbrella", slug)
			assert.Equal(t, "all-upfront", r.payment, "%s should inherit payment from umbrella", slug)
		}
	})

	t.Run("scenario 3: fresh install (no SP rows)", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		// Pin the DB at 000039, just below the migration under test.
		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 39))

		// Confirm no SP rows present pre-migration.
		require.Empty(t, queryAWSSPRows(t, pool))

		require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 40))

		// Migration is a no-op with no SP rows to seed from.
		assert.Empty(t, queryAWSSPRows(t, pool), "no-op when no umbrella row exists")

		// Down should also be a no-op.
		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))
		assert.Empty(t, queryAWSSPRows(t, pool))

		// Full chain to head stays a no-op for SP rows.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		assert.Empty(t, queryAWSSPRows(t, pool))
	})
}

// TestMigration_SplitSavingsPlans_PurchasePlansJSONB verifies the
// purchase_plans.services JSONB key rewrite half of the migration. A
// purchase plan with `aws:savings-plans` should fan out into four
// `aws:savings-plans-<type>` keys carrying the same value, and the
// source key should be removed. Plans without an SP entry should be
// untouched.
func TestMigration_SplitSavingsPlans_PurchasePlansJSONB(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Pin the DB at 000039, just below the migration under test.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 39))

	// Seed two plans: one with the umbrella SP key, one with only RDS
	// (untouched control). Both have a non-SP key (aws:rds) that must
	// pass through unchanged.
	_, err = pool.Exec(ctx, `
		INSERT INTO purchase_plans (id, name, services) VALUES
		('11111111-1111-1111-1111-111111111111', 'sp-plan',
		 '{"aws:savings-plans": {"term": "1yr", "payment": "all-upfront"}, "aws:rds": {"term": "3yr"}}'::jsonb),
		('22222222-2222-2222-2222-222222222222', 'rds-only-plan',
		 '{"aws:rds": {"term": "1yr"}}'::jsonb)`)
	require.NoError(t, err)

	// Scope both plans to an account: migration 000060 deletes purchase_plans
	// rows that have no plan_accounts entry, and the chain to head runs it.
	_, err = pool.Exec(ctx, `
		INSERT INTO cloud_accounts (id, name, provider, external_id)
		VALUES ('cccccccc-cccc-cccc-cccc-000000000001', 'sp-test-acct', 'aws', '333333333333')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO plan_accounts (plan_id, account_id) VALUES
		('11111111-1111-1111-1111-111111111111', 'cccccccc-cccc-cccc-cccc-000000000001'),
		('22222222-2222-2222-2222-222222222222', 'cccccccc-cccc-cccc-cccc-000000000001')`)
	require.NoError(t, err)

	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

	// SP plan: services should now have four aws:savings-plans-<type>
	// keys plus the untouched aws:rds; the umbrella key should be gone.
	var spServices map[string]any
	err = pool.QueryRow(ctx, `SELECT services FROM purchase_plans WHERE id = '11111111-1111-1111-1111-111111111111'`).Scan(&spServices)
	require.NoError(t, err)
	for _, k := range []string{
		"aws:savings-plans-compute",
		"aws:savings-plans-ec2instance",
		"aws:savings-plans-sagemaker",
		"aws:savings-plans-database",
	} {
		assert.Contains(t, spServices, k, "expected key %s after migration", k)
	}
	assert.NotContains(t, spServices, "aws:savings-plans", "umbrella key should be removed")
	assert.Contains(t, spServices, "aws:rds", "non-SP key should pass through unchanged")

	// Untouched control plan.
	var rdsServices map[string]any
	err = pool.QueryRow(ctx, `SELECT services FROM purchase_plans WHERE id = '22222222-2222-2222-2222-222222222222'`).Scan(&rdsServices)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"aws:rds": map[string]any{"term": "1yr"}}, rdsServices)
}
