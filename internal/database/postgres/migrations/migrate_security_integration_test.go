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

// TestAdminBootstrap_LogsToStderr_NotStdout is a regression test for
// issue #545 (a follow-up to #440): the admin bootstrap path in
// ensureAdminUser must log to stderr (via log.Printf), never stdout
// (via fmt.Printf).
//
// Note: the pre-057 drift scenario (seeding an admin row with empty
// group_ids to trigger assignAdminGroupAndWarn's backfill path) is no
// longer possible post-migration-000057: the users_min_one_group CHECK
// constraint prevents group_ids from being NULL or empty (issue #945).
// This test therefore exercises the "Admin user created" log path, which
// is emitted on the first cold-start bootstrap and must reach stderr.
func TestAdminBootstrap_LogsToStderr_NotStdout(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()
	const adminEmail = "stderr-bootstrap@test.example"

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	readStdout := captureStdout(t)
	logBuf := captureLogOutput(t)

	// Run migrations + bootstrap. ensureAdminUser fires and emits the
	// "Admin user created" message, which must reach the stderr-bound
	// stdlib logger, not stdout.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, adminEmail, ""))

	stdout := readStdout()
	logged := logBuf.String()

	assert.Contains(t, logged, "Admin user created",
		"the admin bootstrap message must be emitted on the stderr-bound log path")
	assert.NotContains(t, stdout, "Admin user",
		"the admin bootstrap message must not be written to stdout (issue #440/#545)")
	assert.Empty(t, stdout,
		"admin bootstrap must not write anything to stdout; found: %q", stdout)
}
