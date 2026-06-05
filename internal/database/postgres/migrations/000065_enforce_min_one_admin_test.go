//go:build integration
// +build integration

package migrations_test

import (
	"context"
	"sync"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// adminGroupIDForMinAdminTest mirrors DefaultAdminGroupID / the UUID baked
	// into the 000065 trigger. Copied here so the test is self-contained without
	// depending on declaration order across test files in the same package.
	adminGroupIDForMinAdminTest = "00000000-0000-5000-8000-000000000001"
)

// seedActiveAdmin inserts an active user that is a member of the Administrators
// group and returns its UUID.
func seedActiveAdmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, '', '', true, ARRAY[$2::uuid], NOW(), NOW())
		RETURNING id
	`, email, adminGroupIDForMinAdminTest).Scan(&id)
	require.NoError(t, err, "seeding admin %s must succeed", email)
	return id
}

// countActiveAdmins returns the number of active members of the Administrators
// group, the exact invariant the 000065 trigger protects.
func countActiveAdmins(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM users
		WHERE group_ids @> ARRAY[$1::uuid] AND active = true
	`, adminGroupIDForMinAdminTest).Scan(&n)
	require.NoError(t, err)
	return n
}

// TestMigration_EnforceMinOneAdmin_ConcurrentRace covers issue #919: the real
// TOCTOU race that the deferred constraint triggers + pg_advisory_xact_lock in
// migration 000065 are meant to close. Two concurrent transactions each remove
// admin standing from a DIFFERENT one of the last two active admins. Without
// the advisory lock, each deferred check runs against its own MVCC snapshot,
// both observe count >= 1, and both commit -- leaving zero active admins. The
// lock serializes the count at commit time so exactly one commits and at least
// one active admin always remains.
//
// Unlike the unit tests in service_user_test.go (which hardcode the trigger
// error string and would pass even if the SQL were wrong), this test runs the
// actual migration SQL against a real PostgreSQL instance.
func TestMigration_EnforceMinOneAdmin_ConcurrentRace(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	// runConcurrentRace seeds exactly two active admins, then runs two
	// concurrent transactions that each strip admin standing from a DIFFERENT
	// admin using the supplied mutateSQL (parameterised by the target user ID).
	// A commit barrier holds both transactions at COMMIT until both have done
	// their write, which is what exercises the deferred-trigger + advisory-lock
	// arbitration. It returns how many of the two transactions committed and the
	// number of active admins left afterwards.
	runConcurrentRace := func(t *testing.T, mutateSQL string) (commits, remainingAdmins int) {
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		defer container.Cleanup(ctx)
		pool := container.DB.Pool()

		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

		adminA := seedActiveAdmin(t, ctx, pool, "race-admin-a@test.example")
		adminB := seedActiveAdmin(t, ctx, pool, "race-admin-b@test.example")
		require.Equal(t, 2, countActiveAdmins(t, ctx, pool), "test setup: two active admins expected")

		// Release both goroutines into their COMMIT only after both have
		// completed their mutating statement, maximising the overlap the
		// deferred triggers must arbitrate.
		var writesReady sync.WaitGroup
		writesReady.Add(2)
		commitGate := make(chan struct{})

		results := make([]error, 2)
		targets := []string{adminA, adminB}
		var wg sync.WaitGroup
		wg.Add(2)

		for i := 0; i < 2; i++ {
			go func(idx int) {
				defer wg.Done()
				tx, err := pool.Begin(ctx)
				if err != nil {
					results[idx] = err
					writesReady.Done()
					return
				}
				if _, err := tx.Exec(ctx, mutateSQL, targets[idx]); err != nil {
					_ = tx.Rollback(ctx)
					results[idx] = err
					writesReady.Done()
					return
				}
				// Signal this write is done, then wait for both before committing.
				writesReady.Done()
				<-commitGate
				results[idx] = tx.Commit(ctx)
			}(i)
		}

		writesReady.Wait()
		close(commitGate)
		wg.Wait()

		for _, err := range results {
			if err == nil {
				commits++
			}
		}
		return commits, countActiveAdmins(t, ctx, pool)
	}

	// assertRaceSafe encodes the invariant issue #919 actually demands and that
	// the trigger guarantees: the deferred check must never let BOTH admin-
	// stripping transactions through (commits <= 1), and at least one active
	// admin must always survive (remainingAdmins >= 1). Asserting an exact
	// commits == 1 would be wrong: depending on commit interleaving the advisory
	// lock can legitimately reject BOTH transactions (commits == 0, two admins
	// untouched), which is still safe. The bug this guards against is the
	// pre-trigger behaviour where both committed and zero admins remained.
	assertRaceSafe := func(t *testing.T, commits, remainingAdmins int, op string) {
		t.Helper()
		assert.LessOrEqual(t, commits, 1,
			"the deferred trigger must never let both concurrent %s commit (would leave zero admins)", op)
		assert.GreaterOrEqual(t, remainingAdmins, 1,
			"at least one active admin must remain after the concurrent %s race", op)
	}

	t.Run("concurrent deactivation never removes the last admin", func(t *testing.T) {
		commits, remaining := runConcurrentRace(t, `UPDATE users SET active = false WHERE id = $1`)
		assertRaceSafe(t, commits, remaining, "deactivations")
	})

	t.Run("concurrent deletion never removes the last admin", func(t *testing.T) {
		commits, remaining := runConcurrentRace(t, `DELETE FROM users WHERE id = $1`)
		assertRaceSafe(t, commits, remaining, "deletions")
	})

	t.Run("concurrent group removal never removes the last admin", func(t *testing.T) {
		commits, remaining := runConcurrentRace(t, `UPDATE users SET group_ids = '{}'::uuid[] WHERE id = $1`)
		assertRaceSafe(t, commits, remaining, "group removals")
	})
}

// TestMigration_EnforceMinOneAdmin_BlocksLastAdminRemoval verifies the trigger's
// single-transaction guarantee: removing the only remaining active admin must
// fail at COMMIT with the last_admin_constraint_violation exception, while a
// removal that leaves another active admin must succeed. This pins the
// trigger's COUNT/active semantics against the real migration SQL.
func TestMigration_EnforceMinOneAdmin_BlocksLastAdminRemoval(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	// freshPool spins up an isolated migrated database so each subtest starts
	// from a clean admin count.
	freshPool := func(t *testing.T) *pgxpool.Pool {
		t.Helper()
		container, err := testhelpers.SetupPostgresContainer(ctx, t)
		require.NoError(t, err)
		t.Cleanup(func() { _ = container.Cleanup(ctx) })
		pool := container.DB.Pool()
		require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))
		return pool
	}

	t.Run("deactivating the sole admin is rejected", func(t *testing.T) {
		pool := freshPool(t)
		id := seedActiveAdmin(t, ctx, pool, "sole-admin@test.example")
		require.Equal(t, 1, countActiveAdmins(t, ctx, pool))

		_, err := pool.Exec(ctx, `UPDATE users SET active = false WHERE id = $1`, id)
		require.Error(t, err, "deactivating the last active admin must be rejected at commit")
		assert.Contains(t, err.Error(), "last_admin_constraint_violation",
			"the rejection must come from the 000065 trigger")
		assert.Equal(t, 1, countActiveAdmins(t, ctx, pool),
			"the rejected transaction must roll back, leaving the admin active")
	})

	t.Run("deleting the sole admin is rejected", func(t *testing.T) {
		pool := freshPool(t)
		id := seedActiveAdmin(t, ctx, pool, "sole-admin-del@test.example")
		require.Equal(t, 1, countActiveAdmins(t, ctx, pool))

		_, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		require.Error(t, err, "deleting the last active admin must be rejected at commit")
		assert.Contains(t, err.Error(), "last_admin_constraint_violation",
			"the rejection must come from the 000065 trigger")
		assert.Equal(t, 1, countActiveAdmins(t, ctx, pool),
			"the rejected transaction must roll back, leaving the admin active")
	})

	t.Run("removing one of two admins succeeds", func(t *testing.T) {
		pool := freshPool(t)
		a := seedActiveAdmin(t, ctx, pool, "two-admins-a@test.example")
		_ = seedActiveAdmin(t, ctx, pool, "two-admins-b@test.example")
		require.Equal(t, 2, countActiveAdmins(t, ctx, pool))

		_, err := pool.Exec(ctx, `UPDATE users SET active = false WHERE id = $1`, a)
		require.NoError(t, err, "deactivating one of two admins must succeed")
		assert.Equal(t, 1, countActiveAdmins(t, ctx, pool))
	})
}
