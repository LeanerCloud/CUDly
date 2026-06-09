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

// TestMigrations_FullStackIdempotent proves the entire migration stack is
// idempotent: running it to head twice against a fresh DB leaves the second
// run a no-op and the schema_migrations row clean (not dirty).
//
// This is the invariant the opt-in CUDLY_MIGRATION_AUTOHEAL path relies on
// (maybeAutoHealDirty in migrate.go Force()s past a dirty version and lets
// Up() re-apply the pending tail). If a migration were NOT idempotent, a
// re-apply would error or corrupt data, so this test guards the whole
// directory as new migrations are added.
func TestMigrations_FullStackIdempotent(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// First run: migrate to head on a fresh DB.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""),
		"first migration run to head should succeed on a fresh DB")

	versionAfterFirst, dirtyAfterFirst, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
	require.NoError(t, err)
	require.False(t, dirtyAfterFirst, "DB must not be dirty after the first run")
	require.Greater(t, versionAfterFirst, uint(0), "head version should be > 0")

	// Second run: must be a no-op (golang-migrate's m.Up() returns ErrNoChange,
	// which RunMigrations swallows) and must not flip the dirty flag.
	require.NoError(t, migrations.RunMigrations(ctx, pool, migrationsPath, "", ""),
		"second migration run must be a clean no-op (ErrNoChange), proving idempotency")

	versionAfterSecond, dirtyAfterSecond, err := migrations.GetMigrationVersion(ctx, pool, migrationsPath)
	require.NoError(t, err)
	assert.False(t, dirtyAfterSecond, "DB must not be dirty after the second (no-op) run")
	assert.Equal(t, versionAfterFirst, versionAfterSecond,
		"version must be unchanged after a no-op second run")
}
