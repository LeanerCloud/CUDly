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

// TestMigration_RecommendationsStateOwnerID locks down migration 000093:
// recommendations_state gains a nullable last_collection_owner_id UUID
// column (issue #261's compare-and-clear guard), and the down migration
// removes it cleanly.
func TestMigration_RecommendationsStateOwnerID(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Pin at 000092 (pre-migration) so the assertions exercise 000093's
	// direct effect rather than whatever migration happens to be newest.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 92))

	var columnExists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			 WHERE table_name = 'recommendations_state'
			   AND column_name = 'last_collection_owner_id'
		)
	`).Scan(&columnExists)
	require.NoError(t, err)
	assert.False(t, columnExists, "last_collection_owner_id must not exist before 000093")

	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 93))

	var dataType, isNullable string
	err = pool.QueryRow(ctx, `
		SELECT data_type, is_nullable
		  FROM information_schema.columns
		 WHERE table_name = 'recommendations_state'
		   AND column_name = 'last_collection_owner_id'
	`).Scan(&dataType, &isNullable)
	require.NoError(t, err, "last_collection_owner_id must exist after 000093")
	assert.Equal(t, "uuid", dataType)
	assert.Equal(t, "YES", isNullable)

	// Rollback restores the pre-migration schema.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 92))

	err = pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			 WHERE table_name = 'recommendations_state'
			   AND column_name = 'last_collection_owner_id'
		)
	`).Scan(&columnExists)
	require.NoError(t, err)
	assert.False(t, columnExists, "last_collection_owner_id must be dropped after rollback to 92")
}
