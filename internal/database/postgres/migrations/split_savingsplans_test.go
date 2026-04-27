//go:build integration
// +build integration

package migrations_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getMigrationsPath resolves the migrations directory next to this test file.
func getMigrationsPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filename)
}

// TestMigration_SplitSavingsPlans drives the 000040 migration through
// the three scenarios from the plan §7: umbrella-only, umbrella + PR
// #71's sagemaker row, and fresh install. Round-trips up/down to verify
// no row identity is lost in the lossy down migration.
func TestMigration_SplitSavingsPlans(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	type spRow struct {
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

		// Run migrations up to but not including 000040 by running all,
		// then forcing the migration version back to 000039 and undoing
		// 000040 — equivalent to "stop just before 000040". Simpler: run
		// all migrations, then run down once to undo 000040, then
		// truncate and seed.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))

		// Seed: a single umbrella row with non-default values.
		_, err = pool.Exec(ctx, `
			INSERT INTO service_configs (provider, service, enabled, term, payment, coverage, ramp_schedule)
			VALUES ('aws', 'savings-plans', true, 1, 'no-upfront', 90.50, 'weekly-25pct')`)
		require.NoError(t, err)

		// Up: run 000040.
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

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
	})

	t.Run("scenario 2: umbrella + PR #71 sagemaker row (different values)", func(t *testing.T) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))

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
		// Expect: 4 split rows + sagemaker (kept). Umbrella deleted.
		require.Len(t, got, 5)
		_, hasUmbrella := got["savings-plans"]
		assert.False(t, hasUmbrella, "umbrella should be deleted")
		_, hasSagemaker := got["sagemaker"]
		assert.True(t, hasSagemaker, "PR #71 sagemaker row must be kept for one release")

		// sagemaker slot inherits from PR #71's row (1, partial-upfront).
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

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))

		// Confirm no SP rows present pre-migration.
		require.Empty(t, queryAWSSPRows(t, pool))

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		// Migration is a no-op with no SP rows to seed from.
		assert.Empty(t, queryAWSSPRows(t, pool), "no-op when no umbrella row exists")

		// Down should also be a no-op.
		require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))
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

	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
	require.NoError(t, migrations.RollbackMigrations(ctx, pool, migrationsPath, 1))

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
