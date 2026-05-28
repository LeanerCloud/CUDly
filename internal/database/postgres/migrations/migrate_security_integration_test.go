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

// TestAssignAdminGroup_BackfillLogsToStderr_NotStdout is a regression test for
// issue #545 (a follow-up to #440): the admin group_ids backfill in
// assignAdminGroupAndWarn must log to stderr (via log.Printf), never stdout
// (via fmt.Printf). The earlier #440 fix routed the per-user admin messages to
// the stdlib logger but left the "Backfilled ..." line on fmt.Printf, which
// the unit test could not catch because it uses an unreachable pool so the
// backfill branch never runs. This exercises the branch against a real DB.
func TestAssignAdminGroup_BackfillLogsToStderr_NotStdout(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()
	const adminEmail = "stderr-backfill@test.example"

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Migrate to head with no admin email. Migration 000024 seeds the
	// Administrators group; no admin user is inserted yet.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""))

	// Seed a drifted admin with empty group_ids so the next ensureAdminUser
	// run triggers the backfill (and thus the log line under test).
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, salt, role, active, group_ids, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, '', '', 'admin', false, '{}', NOW(), NOW())
	`, adminEmail)
	require.NoError(t, err)

	readStdout := captureStdout(t)
	logBuf := captureLogOutput(t)

	// Re-run with the admin email so ensureAdminUser -> assignAdminGroupAndWarn
	// fires and backfills the drifted row, emitting the "Backfilled" message.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))

	stdout := readStdout()
	logged := logBuf.String()

	assert.Contains(t, logged, "Backfilled",
		"the backfill message must be emitted on the stderr-bound log path")
	assert.NotContains(t, stdout, "Backfilled",
		"the backfill message must not be written to stdout (issue #440/#545)")
	assert.Empty(t, stdout,
		"admin group backfill must not write anything to stdout; found: %q", stdout)
}
