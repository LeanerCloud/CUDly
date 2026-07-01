package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSuppressionStore is a minimal in-memory stand-in for the
// scheduler's StoreInterface. We only implement the two methods the
// applySuppressions path touches plus the default no-ops forwarded
// through the embedded MockConfigStore in scheduler_test.go.
type mockSuppressionStore struct {
	MockConfigStore
	recs []config.RecommendationRecord
	sups []config.PurchaseSuppression
}

func (m *mockSuppressionStore) ListStoredRecommendations(_ context.Context, _ config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	return m.recs, nil
}
func (m *mockSuppressionStore) ListActiveSuppressions(_ context.Context) ([]config.PurchaseSuppression, error) {
	return m.sups, nil
}
func (m *mockSuppressionStore) GetRecommendationsFreshness(_ context.Context) (*config.RecommendationsFreshness, error) {
	now := time.Now()
	return &config.RecommendationsFreshness{LastCollectedAt: &now}, nil
}
func (m *mockSuppressionStore) MarkCollectionStarted(_ context.Context) (bool, error) {
	return true, nil
}
func (m *mockSuppressionStore) ClearCollectionStarted(_ context.Context) error {
	return nil
}

// GetServiceConfig / GetAccountServiceOverride: required by the override
// helper that runs inside ListRecommendations after the suppression pass
// (issue #196). Returning nil signals "no global config registered" — the
// resolver then leaves the rec un-filtered, which is what the suppression
// tests want (they assert on the suppression counter, not on overrides).
func (m *mockSuppressionStore) GetServiceConfig(_ context.Context, _, _ string) (*config.ServiceConfig, error) {
	return nil, nil
}
func (m *mockSuppressionStore) GetAccountServiceOverride(_ context.Context, _, _, _ string) (*config.AccountServiceOverride, error) {
	return nil, nil
}

// GetGlobalConfig returns a default config so ListRecommendations can resolve
// the effective stale TTL without panicking on the embedded MockConfigStore.
func (m *mockSuppressionStore) GetGlobalConfig(_ context.Context) (*config.GlobalConfig, error) {
	return &config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil
}
func (m *mockSuppressionStore) UpsertNotificationMute(_ context.Context, _, _, _ string) error {
	return nil
}
func (m *mockSuppressionStore) IsNotificationMuted(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func strPtr(s string) *string { return &s }

func TestApplySuppressions_SubtractsCount(t *testing.T) {
	ctx := context.Background()
	future := time.Now().Add(24 * time.Hour)
	store := &mockSuppressionStore{
		recs: []config.RecommendationRecord{
			{ID: "r1", Provider: "aws", Service: "ec2", Region: "us-east-1",
				ResourceType: "t4g.nano", Count: 5,
				CloudAccountID: strPtr("acct-1")},
		},
		sups: []config.PurchaseSuppression{
			{ExecutionID: "e1", AccountID: "acct-1", Provider: "aws",
				Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano",
				SuppressedCount: 3, ExpiresAt: future},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, 2, recs[0].Count, "5 - 3 = 2")
	assert.Equal(t, 3, recs[0].SuppressedCount)
	require.NotNil(t, recs[0].SuppressionExpiresAt)
	assert.WithinDuration(t, future, *recs[0].SuppressionExpiresAt, time.Second)
	require.NotNil(t, recs[0].PrimarySuppressionExecutionID)
	assert.Equal(t, "e1", *recs[0].PrimarySuppressionExecutionID)
}

func TestApplySuppressions_DropsFullyCoveredRecs(t *testing.T) {
	ctx := context.Background()
	future := time.Now().Add(24 * time.Hour)
	store := &mockSuppressionStore{
		recs: []config.RecommendationRecord{
			{ID: "r1", Provider: "aws", Service: "ec2", Region: "us-east-1",
				ResourceType: "t4g.nano", Count: 5,
				CloudAccountID: strPtr("acct-1")},
		},
		sups: []config.PurchaseSuppression{
			{ExecutionID: "e1", AccountID: "acct-1", Provider: "aws",
				Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano",
				SuppressedCount: 5, ExpiresAt: future},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	assert.Empty(t, recs, "rec fully covered by suppression should be dropped")
}

func TestApplySuppressions_CumulativeAcrossExecutions(t *testing.T) {
	ctx := context.Background()
	early := time.Now().Add(12 * time.Hour)
	later := time.Now().Add(48 * time.Hour)
	store := &mockSuppressionStore{
		recs: []config.RecommendationRecord{
			{ID: "r1", Provider: "aws", Service: "ec2", Region: "us-east-1",
				ResourceType: "t4g.nano", Count: 10,
				CloudAccountID: strPtr("acct-1")},
		},
		sups: []config.PurchaseSuppression{
			{ExecutionID: "e1", AccountID: "acct-1", Provider: "aws",
				Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano",
				SuppressedCount: 3, ExpiresAt: later, CreatedAt: time.Now().Add(-time.Hour)},
			{ExecutionID: "e2", AccountID: "acct-1", Provider: "aws",
				Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano",
				SuppressedCount: 4, ExpiresAt: early, CreatedAt: time.Now()},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, 3, recs[0].Count, "10 - 3 - 4 = 3")
	assert.Equal(t, 7, recs[0].SuppressedCount)
	require.NotNil(t, recs[0].SuppressionExpiresAt)
	assert.WithinDuration(t, early, *recs[0].SuppressionExpiresAt, time.Second,
		"earliest expiry across contributors wins")
	// Primary = most-contributing execution (4 > 3 → e2)
	require.NotNil(t, recs[0].PrimarySuppressionExecutionID)
	assert.Equal(t, "e2", *recs[0].PrimarySuppressionExecutionID)
}

func TestApplySuppressions_EngineDifferentiates(t *testing.T) {
	// A suppression for engine='' must NOT match a rec with
	// engine='postgres' (and vice versa). Pins the 6-tuple match rule.
	ctx := context.Background()
	future := time.Now().Add(24 * time.Hour)
	store := &mockSuppressionStore{
		recs: []config.RecommendationRecord{
			{ID: "r1", Provider: "aws", Service: "rds", Region: "us-east-1",
				ResourceType: "db.t4g.micro", Engine: "postgres", Count: 5,
				CloudAccountID: strPtr("acct-1")},
		},
		sups: []config.PurchaseSuppression{
			// Suppression with empty engine — different 6-tuple.
			{ExecutionID: "e1", AccountID: "acct-1", Provider: "aws",
				Service: "rds", Region: "us-east-1", ResourceType: "db.t4g.micro",
				Engine: "", SuppressedCount: 5, ExpiresAt: future},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, 5, recs[0].Count, "engine mismatch → no subtraction")
	assert.Equal(t, 0, recs[0].SuppressedCount)
}

func TestApplySuppressions_NilAccountIDNormalised(t *testing.T) {
	// A rec with CloudAccountID=nil should match a suppression row
	// with account_id='' (and vice versa). Pins the normalisation
	// rule in applySuppressions.
	ctx := context.Background()
	future := time.Now().Add(24 * time.Hour)
	store := &mockSuppressionStore{
		recs: []config.RecommendationRecord{
			{ID: "r1", Provider: "aws", Service: "ec2", Region: "us-east-1",
				ResourceType: "t4g.nano", Count: 5, CloudAccountID: nil},
		},
		sups: []config.PurchaseSuppression{
			{ExecutionID: "e1", AccountID: "", Provider: "aws",
				Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano",
				SuppressedCount: 2, ExpiresAt: future},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, 3, recs[0].Count, "5 - 2 = 3 (nil account matched to empty-string suppression)")
}

// TestApplySuppressionIndex_DoesNotMutateCallerSlice asserts that
// applySuppressionIndex allocates a fresh output slice and never writes back
// into the caller's backing array (05-M1).
//
// Pre-fix: out := recs[:0] shared the caller's backing array, so any append
// inside the function would overwrite elements that the caller still expected
// at their original positions. The fix allocates make([]T, 0, len(recs)).
func TestApplySuppressionIndex_DoesNotMutateCallerSlice(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	// Two recs; only the second is suppressed and therefore dropped.
	recs := []config.RecommendationRecord{
		{ID: "keep", Provider: "aws", Service: "ec2", Region: "us-east-1",
			ResourceType: "t4g.nano", Count: 5, CloudAccountID: strPtr("acct-1")},
		{ID: "drop", Provider: "aws", Service: "ec2", Region: "us-east-2",
			ResourceType: "t4g.nano", Count: 5, CloudAccountID: strPtr("acct-1")},
	}
	// Keep a copy of the original IDs so we can detect mutations.
	originalIDs := []string{recs[0].ID, recs[1].ID}

	sups := []config.PurchaseSuppression{
		{ExecutionID: "e1", AccountID: "acct-1", Provider: "aws",
			Service: "ec2", Region: "us-east-2", ResourceType: "t4g.nano",
			SuppressedCount: 5, ExpiresAt: future},
	}
	index := aggregateSuppressions(sups)
	out := applySuppressionIndex(recs, index)

	// The survivor set must contain only the non-suppressed rec.
	require.Len(t, out, 1)
	assert.Equal(t, "keep", out[0].ID)

	// The original slice must be unchanged: both elements at their
	// original positions with the original IDs (pre-fix: recs[1].ID
	// would be overwritten by "keep" since out alias the same array).
	require.Len(t, recs, 2, "original slice length must not change")
	assert.Equal(t, originalIDs[0], recs[0].ID, "recs[0] must not be mutated")
	assert.Equal(t, originalIDs[1], recs[1].ID, "recs[1] must not be mutated")
}

// Suppress unused-import warning in case this is the only file that imports pgx.
var _ pgx.Tx = (pgx.Tx)(nil)
