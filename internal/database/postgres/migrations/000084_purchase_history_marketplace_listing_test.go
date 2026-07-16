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

// TestMigration084_MarketplaceColumns verifies that migration 000084 creates
// the three new columns (offering_class, listing_id, listing_state) on the
// purchase_history table. The test migrates a fresh DB up through the full
// migration set and asserts that the columns are present and writable.
//
// Fail-before/pass-after: running this test against a DB at version 083 (without
// 000084 applied) would fail because the columns do not exist; after 000084 the
// test passes.
func TestMigration084_MarketplaceColumns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	if err != nil {
		t.Skipf("Skipping test: could not setup postgres container: %v", err)
	}
	defer container.Cleanup(ctx)

	require.NoError(t, migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", ""),
		"migrations must apply cleanly through 000084")

	// Verify all three columns exist by inserting a row that references them.
	// We insert into the minimal required columns plus the three new ones to
	// confirm the DDL was applied; if any column is missing Postgres will error.
	const insertSQL = `
		INSERT INTO purchase_history (
			account_id, purchase_id, timestamp, provider, service, region,
			resource_type, count, term, payment, upfront_cost,
			estimated_savings, plan_id, plan_name, ramp_step, source,
			offering_class, listing_id, listing_state
		) VALUES (
			'test-account', 'ri-084-test', $1, 'aws', 'ec2', 'us-east-1',
			't3.micro', 1, 12, 'All Upfront', 100.0,
			10.0, 'plan-084', 'test plan', 0, 'test',
			'standard', 'ril-084-test', 'active'
		)`

	_, err = container.DB.Pool().Exec(ctx, insertSQL, time.Now())
	require.NoError(t, err, "INSERT referencing offering_class, listing_id, listing_state must succeed after migration 000084")

	// Verify the values round-trip correctly.
	var offeringClass, listingID, listingState string
	row := container.DB.Pool().QueryRow(ctx, `
		SELECT offering_class, listing_id, listing_state
		FROM purchase_history WHERE purchase_id = 'ri-084-test'`)
	require.NoError(t, row.Scan(&offeringClass, &listingID, &listingState))
	assert.Equal(t, "standard", offeringClass, "offering_class must round-trip")
	assert.Equal(t, "ril-084-test", listingID, "listing_id must round-trip")
	assert.Equal(t, "active", listingState, "listing_state must round-trip")
}
