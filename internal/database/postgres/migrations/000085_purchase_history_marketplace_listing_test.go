//go:build integration
// +build integration

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration085_MarketplaceColumns verifies that migration 000085 creates
// the three new columns (offering_class, listing_id, listing_state) on the
// purchase_history table. The test migrates a fresh DB up through the full
// migration set and asserts that the columns are present and writable.
//
// Fail-before/pass-after: running this test against a DB at version 084 (without
// 000085 applied) would fail because the columns do not exist; after 000085 the
// test passes.
func TestMigration085_MarketplaceColumns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	if err != nil {
		t.Skipf("Skipping test: could not setup postgres container: %v", err)
	}
	defer container.Cleanup(ctx)

	require.NoError(t, migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", ""),
		"migrations must apply cleanly through 000085")

	// Verify all three columns exist by inserting a row that references them.
	// We insert the NOT-NULL-without-default columns plus the three new
	// marketplace columns; if any of the new columns is missing Postgres will
	// error. plan_id is intentionally omitted (nullable UUID FK to
	// purchase_plans) so the test needs no plan fixture.
	const insertSQL = `
		INSERT INTO purchase_history (
			account_id, purchase_id, timestamp, provider, service, region,
			resource_type, term, payment,
			offering_class, listing_id, listing_state
		) VALUES (
			'test-account', 'ri-085-test', $1, 'aws', 'ec2', 'us-east-1',
			't3.micro', 12, 'All Upfront',
			'standard', 'ril-085-test', 'active'
		)`

	_, err = container.DB.Pool().Exec(ctx, insertSQL, time.Now())
	require.NoError(t, err, "INSERT referencing offering_class, listing_id, listing_state must succeed after migration 000085")

	// Verify the values round-trip correctly.
	var offeringClass, listingID, listingState string
	row := container.DB.Pool().QueryRow(ctx, `
		SELECT offering_class, listing_id, listing_state
		FROM purchase_history WHERE purchase_id = 'ri-085-test'`)
	require.NoError(t, row.Scan(&offeringClass, &listingID, &listingState))
	assert.Equal(t, "standard", offeringClass, "offering_class must round-trip")
	assert.Equal(t, "ril-085-test", listingID, "listing_id must round-trip")
	assert.Equal(t, "active", listingState, "listing_state must round-trip")
}
