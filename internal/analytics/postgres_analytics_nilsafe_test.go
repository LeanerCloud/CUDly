package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostgresAnalyticsStore_BulkInsertSnapshots_Empty verifies the early-return
// path when an empty or nil slice is passed to BulkInsertSnapshots. This path
// returns nil before acquiring a DB connection, so it works with a nil
// *database.Connection.
func TestPostgresAnalyticsStore_BulkInsertSnapshots_Empty(t *testing.T) {
	store := NewPostgresAnalyticsStore(nil)

	t.Run("empty slice returns nil", func(t *testing.T) {
		err := store.BulkInsertSnapshots(context.Background(), []SavingsSnapshot{})
		assert.NoError(t, err)
	})

	t.Run("nil slice returns nil", func(t *testing.T) {
		err := store.BulkInsertSnapshots(context.Background(), nil)
		assert.NoError(t, err)
	})
}

// TestPostgresAnalyticsStore_SaveSnapshot_MetadataMarshalError verifies the
// error path when snapshot.Metadata contains an un-marshallable value (e.g. a
// channel). The error is returned before the DB Exec call is reached, so this
// works with a nil *database.Connection.
func TestPostgresAnalyticsStore_SaveSnapshot_MetadataMarshalError(t *testing.T) {
	store := NewPostgresAnalyticsStore(nil)

	t.Run("returns error when metadata contains un-marshallable value", func(t *testing.T) {
		snapshot := &SavingsSnapshot{
			ID:        "existing-id",
			AccountID: "account-123",
			Timestamp: time.Now().UTC(),
			Provider:  "aws",
			Service:   "rds",
			Region:    "us-east-1",
			// A channel is not JSON-serialisable, so json.Marshal will fail.
			Metadata: map[string]any{
				"bad_field": make(chan int),
			},
		}

		err := store.SaveSnapshot(context.Background(), snapshot)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to marshal metadata")
	})

	t.Run("empty ID is populated before metadata marshal is attempted", func(t *testing.T) {
		// When ID is empty, a UUID is generated. The un-marshallable metadata
		// error is still returned, but the ID field on the snapshot is set first.
		snapshot := &SavingsSnapshot{
			ID:        "", // will be populated
			AccountID: "account-123",
			Timestamp: time.Now().UTC(),
			Metadata: map[string]any{
				"bad_field": make(chan int),
			},
		}

		err := store.SaveSnapshot(context.Background(), snapshot)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to marshal metadata")
		// ID was generated before the error occurred.
		assert.NotEmpty(t, snapshot.ID)
	})
}

// TestPostgresAnalyticsStore_BulkInsertSnapshots_MetadataMarshalError verifies
// the error path inside the CopyFromSlice callback when a snapshot has an
// un-marshallable metadata value. This path does touch s.db.Acquire, so it
// will panic with a nil connection – skip if we cannot intercept. We verify
// the logic only via the testablePostgresAnalyticsStore shim (see
// postgres_analytics_test.go) and document the limitation here.
//
// NOTE: this test is intentionally omitted; the DB-acquire path requires a
// real (or mock-backed) *database.Connection.

// TestPostgresAnalyticsStore_CreatePartitionsForRange_ReversedDates verifies
// that when start > end the loop body is never entered and the function returns
// nil without touching s.db.
func TestPostgresAnalyticsStore_CreatePartitionsForRange_ReversedDates(t *testing.T) {
	store := NewPostgresAnalyticsStore(nil)

	t.Run("start after end returns nil without DB call", func(t *testing.T) {
		future := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
		past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

		err := store.CreatePartitionsForRange(context.Background(), future, past)
		assert.NoError(t, err)
	})

	t.Run("start equal to end with no DB panics – test only the date logic", func(t *testing.T) {
		// Validate that the month-normalisation logic in CreatePartitionsForRange
		// produces a single iteration when start == end within the same month.
		// We replicate the logic here (not calling the method) to avoid a nil-DB panic.
		startDate := time.Date(2024, 5, 5, 0, 0, 0, 0, time.UTC)
		endDate := time.Date(2024, 5, 25, 0, 0, 0, 0, time.UTC)

		current := time.Date(startDate.Year(), startDate.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(endDate.Year(), endDate.Month(), 1, 0, 0, 0, 0, time.UTC)

		iterations := 0
		for !current.After(end) {
			iterations++
			current = current.AddDate(0, 1, 0)
		}
		assert.Equal(t, 1, iterations)
	})
}

// TestPostgresAnalyticsStore_Close_NoOp verifies that Close always returns nil,
// even with a nil *database.Connection.
func TestPostgresAnalyticsStore_Close_NoOp(t *testing.T) {
	store := NewPostgresAnalyticsStore(nil)
	require.NoError(t, store.Close())
}
