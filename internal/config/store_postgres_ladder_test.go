//go:build integration
// +build integration

package config

// Real-DB integration tests for the ladder_runs / ladder_tranches store
// methods added in PR-2 (migrations 000080/000081). They run against a
// throwaway PostgreSQL container (testhelpers.SetupPostgresContainer), the
// same harness the other *_db_test.go / *_unassigned_test.go integration
// tests use, and are gated behind the `integration` build tag so the default
// `go test ./...` run stays hermetic.
//
// Coverage:
//   - SaveLadderRun round-trips a run with all NULL monetary columns preserved
//     as nil (never 0-coerced) AND a run with populated monetary columns.
//   - LatestLadderRunStartedAt returns nil with no rows and the MAX(started_at)
//     across multiple runs.
//   - SaveLadderTranches batch-inserts every row in one transaction.
//   - TransitionLadderRunStatus performs a CAS: it updates when the current
//     status is in the from-set (win) and is a no-op returning (nil, nil) when
//     it is not (lose).

import (
	"context"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/LeanerCloud/CUDly/pkg/ladder"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupLadderStore starts a container, runs migrations, and returns a store.
// It skips (not fails) when Docker is unavailable so the suite degrades
// gracefully in environments without a container runtime.
func setupLadderStore(ctx context.Context, t *testing.T) *PostgresStore {
	t.Helper()
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	if err != nil {
		t.Skipf("Skipping integration test: cannot start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() { container.Cleanup(context.Background()) })

	if err := migrations.RunMigrations(ctx, container.DB.Pool(), getTestMigrationsPath(), "", ""); err != nil {
		t.Skipf("Skipping integration test: migrations failed: %v", err)
	}
	return NewPostgresStore(container.DB)
}

// seedLadderConfig inserts a cloud_account and a ladder_config, returning the
// ladder_config id for use as a run/tranche config_id FK.
func seedLadderConfig(ctx context.Context, t *testing.T, store *PostgresStore) string {
	t.Helper()
	acctID := uuid.New().String()
	_, err := store.db.Exec(ctx, `
		INSERT INTO cloud_accounts
			(id, name, enabled, provider, external_id, aws_is_org_root, created_at, updated_at)
		VALUES ($1, 'Ladder Test Account', true, 'aws', '123456789012', false, now(), now())
	`, acctID)
	require.NoError(t, err, "seed cloud_accounts")

	cfg := &LadderConfigDB{
		CloudAccountID:             acctID,
		Provider:                   "aws",
		Enabled:                    true,
		Mode:                       "email_approval",
		Cadence:                    "daily",
		TargetCoverage:             80.0,
		BufferFraction:             0.1,
		BaselinePercentile:         5.0,
		LookbackDays:               30,
		BufferUtilizationThreshold: 50.0,
		MaxActionsPerRun:           5,
		RampSchedule:               []byte(`{"steps":[{"after_days":0,"fraction":1.0}]}`),
	}
	saved, err := store.UpsertLadderConfig(ctx, cfg)
	require.NoError(t, err, "seed ladder_config")
	require.NotEmpty(t, saved.ID)
	return saved.ID
}

func TestPostgresStore_SaveLadderRun_NullMonetaryPreserved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	store := setupLadderStore(ctx, t)

	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("NULL monetary columns round-trip as nil", func(t *testing.T) {
		run := &LadderRunDB{
			StartedAt: now,
			Status:    ladder.RunStatusPlanned,
			// All monetary snapshot fields intentionally nil.
		}
		saved, err := store.SaveLadderRun(ctx, run)
		require.NoError(t, err)
		require.NotEmpty(t, saved.ID, "SaveLadderRun must generate an ID")

		got, err := store.GetLadderRun(ctx, saved.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, ladder.RunStatusPlanned, got.Status)
		// The core assertion: NULL != 0. Absent monetary values must stay nil.
		assert.Nil(t, got.BaselineUSDHr, "baseline_usd_hr NULL must scan as nil, not 0")
		assert.Nil(t, got.TargetUSDHr, "target_usd_hr NULL must scan as nil, not 0")
		assert.Nil(t, got.ExistingUSDHr, "existing_usd_hr NULL must scan as nil, not 0")
		assert.Nil(t, got.GapUSDHr, "gap_usd_hr NULL must scan as nil, not 0")
		assert.Nil(t, got.ConfigID, "config_id NULL must scan as nil")
		// NOT NULL accumulator totals default to 0.
		assert.Equal(t, 0.0, got.TotalHourlyCommit)
		// Plan defaults to '{}' JSONB.
		assert.JSONEq(t, `{}`, string(got.Plan))
	})

	t.Run("populated monetary columns round-trip by value", func(t *testing.T) {
		baseline := 12.5
		target := 10.0
		existing := 3.0
		gap := 7.0
		run := &LadderRunDB{
			StartedAt:         now,
			Status:            ladder.RunStatusPlanned,
			BaselineUSDHr:     &baseline,
			TargetUSDHr:       &target,
			ExistingUSDHr:     &existing,
			GapUSDHr:          &gap,
			TotalHourlyCommit: 7.0,
			Plan:              []byte(`{"actions":[]}`),
		}
		saved, err := store.SaveLadderRun(ctx, run)
		require.NoError(t, err)

		got, err := store.GetLadderRun(ctx, saved.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, got.BaselineUSDHr)
		assert.InDelta(t, baseline, *got.BaselineUSDHr, 1e-6)
		require.NotNil(t, got.GapUSDHr)
		assert.InDelta(t, gap, *got.GapUSDHr, 1e-6)
		assert.InDelta(t, 7.0, got.TotalHourlyCommit, 1e-6)
	})

	t.Run("GetLadderRun returns nil for a missing id", func(t *testing.T) {
		got, err := store.GetLadderRun(ctx, uuid.New().String())
		require.NoError(t, err)
		assert.Nil(t, got, "GetLadderRun must return (nil, nil) for a missing row")
	})
}

func TestPostgresStore_LatestLadderRunStartedAt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	store := setupLadderStore(ctx, t)
	configID := seedLadderConfig(ctx, t, store)

	t.Run("nil when no runs exist for the config", func(t *testing.T) {
		latest, err := store.LatestLadderRunStartedAt(ctx, configID)
		require.NoError(t, err)
		assert.Nil(t, latest, "no runs -> nil (never a zero time)")
	})

	t.Run("returns MAX(started_at) across multiple runs", func(t *testing.T) {
		older := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
		newer := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)

		for _, ts := range []time.Time{older, newer} {
			ts := ts
			cfgID := configID
			_, err := store.SaveLadderRun(ctx, &LadderRunDB{
				ConfigID:  &cfgID,
				StartedAt: ts,
				Status:    ladder.RunStatusPlanned,
			})
			require.NoError(t, err)
		}

		latest, err := store.LatestLadderRunStartedAt(ctx, configID)
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.WithinDuration(t, newer, *latest, time.Second, "must return the most recent started_at")
	})
}

func TestPostgresStore_SaveLadderTranches_Batch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	store := setupLadderStore(ctx, t)
	configID := seedLadderConfig(ctx, t, store)

	// A tranche run_id FK requires an existing run row.
	cfgID := configID
	run, err := store.SaveLadderRun(ctx, &LadderRunDB{
		ConfigID:  &cfgID,
		StartedAt: time.Now().UTC(),
		Status:    ladder.RunStatusPlanned,
	})
	require.NoError(t, err)

	runID := run.ID
	fire := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Microsecond)
	tranches := []LadderTrancheDB{
		{
			ID:            uuid.New().String(),
			ConfigID:      &cfgID,
			RunID:         &runID,
			LayerType:     ladder.LayerConvertibleRI,
			Term:          ladder.Term1Year,
			PaymentOption: ladder.PaymentNoUpfront,
			Status:        ladder.TrancheStatusScheduled,
			AmountUSDHr:   1.5,
			ScheduledDate: fire,
		},
		{
			ID:            uuid.New().String(),
			ConfigID:      &cfgID,
			RunID:         &runID,
			LayerType:     ladder.LayerEC2InstanceSP,
			Term:          ladder.Term1Year,
			PaymentOption: ladder.PaymentNoUpfront,
			Status:        ladder.TrancheStatusScheduled,
			AmountUSDHr:   2.5,
			ScheduledDate: fire,
		},
	}

	require.NoError(t, store.SaveLadderTranches(ctx, tranches))

	// Verify both rows landed and reference the run.
	var count int
	require.NoError(t, store.db.QueryRow(ctx,
		`SELECT count(*) FROM ladder_tranches WHERE run_id = $1`, runID).Scan(&count))
	assert.Equal(t, 2, count, "both tranches must be inserted in the batch")

	t.Run("empty slice is a no-op", func(t *testing.T) {
		require.NoError(t, store.SaveLadderTranches(ctx, nil))
	})

	t.Run("empty tranche ID is rejected", func(t *testing.T) {
		err := store.SaveLadderTranches(ctx, []LadderTrancheDB{
			{
				ID:            "", // invalid
				RunID:         &runID,
				LayerType:     ladder.LayerConvertibleRI,
				Term:          ladder.Term1Year,
				PaymentOption: ladder.PaymentNoUpfront,
				Status:        ladder.TrancheStatusScheduled,
				AmountUSDHr:   1.0,
				ScheduledDate: fire,
			},
		})
		require.Error(t, err, "an empty tranche ID must be rejected")
	})
}

func TestPostgresStore_TransitionLadderRunStatus_CAS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	store := setupLadderStore(ctx, t)

	run, err := store.SaveLadderRun(ctx, &LadderRunDB{
		StartedAt: time.Now().UTC(),
		Status:    ladder.RunStatusPlanned,
	})
	require.NoError(t, err)

	t.Run("CAS win: current status is in the from-set", func(t *testing.T) {
		updated, err := store.TransitionLadderRunStatus(ctx, run.ID,
			[]ladder.RunStatus{ladder.RunStatusPlanned}, ladder.RunStatusAwaitingApproval)
		require.NoError(t, err)
		require.NotNil(t, updated, "transition from the correct status must update the row")
		assert.Equal(t, ladder.RunStatusAwaitingApproval, updated.Status)
	})

	t.Run("CAS lose: current status not in the from-set", func(t *testing.T) {
		// The row is now awaiting_approval; a transition expecting planned must
		// affect zero rows and signal the miss with (nil, nil).
		updated, err := store.TransitionLadderRunStatus(ctx, run.ID,
			[]ladder.RunStatus{ladder.RunStatusPlanned}, ladder.RunStatusApproved)
		require.NoError(t, err)
		assert.Nil(t, updated, "CAS miss must return (nil, nil), not an error")

		// The stored status must be unchanged from the winning transition.
		got, err := store.GetLadderRun(ctx, run.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, ladder.RunStatusAwaitingApproval, got.Status,
			"a lost CAS must not mutate the row")
	})
}
