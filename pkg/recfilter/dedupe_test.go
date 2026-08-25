package recfilter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServiceClient is a minimal provider.ServiceClient implementing only
// GetExistingCommitments, which is all AdjustRecommendationsForExisting uses.
type fakeServiceClient struct {
	commitments []common.Commitment
	err         error
}

func (f *fakeServiceClient) GetServiceType() common.ServiceType { return "" }
func (f *fakeServiceClient) GetRegion() string                  { return "" }
func (f *fakeServiceClient) GetRecommendations(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	return nil, nil
}
func (f *fakeServiceClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	return f.commitments, f.err
}
func (f *fakeServiceClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	return common.PurchaseResult{}, nil
}
func (f *fakeServiceClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	return nil
}
func (f *fakeServiceClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	return nil, nil
}
func (f *fakeServiceClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	return nil, nil
}

func TestFilterRecentCommitments_StateAndWindow(t *testing.T) {
	t.Parallel()
	now := time.Now()
	commitments := []common.Commitment{
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 1, State: "active", StartDate: now.Add(-25 * time.Hour)},   // too old
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 2, State: "retired", StartDate: now.Add(-1 * time.Hour)},   // wrong state
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 3, State: "cancelled", StartDate: now.Add(-1 * time.Hour)}, // wrong state
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 4, State: "payment-pending", StartDate: now.Add(-1 * time.Hour)},
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 5, State: "active", StartDate: now.Add(-1 * time.Hour)},
	}

	d := NewDuplicateChecker(DefaultDuplicateCheckLookbackHours)
	recent := d.filterRecentCommitments(commitments)

	require.Len(t, recent, 2)
	counts := []int{recent[0].Count, recent[1].Count}
	assert.ElementsMatch(t, []int{4, 5}, counts)
}

func TestAdjustRecommendationsForExisting_EngineNormalizationCollides(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := &fakeServiceClient{commitments: []common.Commitment{
		{ResourceType: "db.r5.large", Region: "us-east-1", Engine: "Aurora PostgreSQL", Count: 5, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
	}}
	rec := common.Recommendation{
		ResourceType: "db.r5.large", Region: "us-east-1", Count: 5,
		Details: &common.DatabaseDetails{Engine: "aurora-postgresql"},
	}

	d := NewDuplicateChecker(0)
	passed, filtered, err := d.AdjustRecommendationsForExisting(ctx, []common.Recommendation{rec}, client)

	require.NoError(t, err)
	assert.Empty(t, passed)
	assert.Len(t, filtered, 1)
}

func TestAdjustRecommendationsForExisting_FullCoverageDrops(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := &fakeServiceClient{commitments: []common.Commitment{
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 5, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
	}}
	rec := common.Recommendation{
		ResourceType: "db.t3.small", Region: "us-east-1", Count: 5,
		Details: &common.DatabaseDetails{Engine: "mysql"},
	}

	d := NewDuplicateChecker(0)
	passed, filtered, err := d.AdjustRecommendationsForExisting(ctx, []common.Recommendation{rec}, client)

	require.NoError(t, err)
	assert.Empty(t, passed)
	require.Len(t, filtered, 1)
	assert.Equal(t, rec, filtered[0])
}

func TestAdjustRecommendationsForExisting_PartialCoverageConsumesBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := &fakeServiceClient{commitments: []common.Commitment{
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 2, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
	}}
	recs := []common.Recommendation{
		{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "mysql"}},
		{ResourceType: "db.t3.small", Region: "us-east-1", Count: 3, Details: &common.DatabaseDetails{Engine: "mysql"}},
	}

	d := NewDuplicateChecker(0)
	passed, filtered, err := d.AdjustRecommendationsForExisting(ctx, recs, client)

	require.NoError(t, err)
	assert.Empty(t, filtered)
	require.Len(t, passed, 2)
	assert.Equal(t, 3, passed[0].Count) // 5 - 2 = 3, budget consumed
	assert.Equal(t, 3, passed[1].Count) // no existing coverage left, unchanged
}

func TestAdjustRecommendationsForExisting_ClientErrorReturnsOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	wantErr := errors.New("boom")
	client := &fakeServiceClient{err: wantErr}
	recs := []common.Recommendation{
		{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "mysql"}},
	}

	d := NewDuplicateChecker(0)
	passed, filtered, err := d.AdjustRecommendationsForExisting(ctx, recs, client)

	assert.Equal(t, recs, passed)
	assert.Nil(t, filtered)
	assert.Equal(t, wantErr, err)
}

func TestAdjustRecommendationsForExisting_NoRecentCommitmentsPassesThrough(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := &fakeServiceClient{}
	recs := []common.Recommendation{
		{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "mysql"}},
	}

	d := NewDuplicateChecker(0)
	passed, filtered, err := d.AdjustRecommendationsForExisting(ctx, recs, client)

	require.NoError(t, err)
	assert.Nil(t, filtered)
	require.Len(t, passed, 1)
	// No reallocation/reordering: passed IS recs, not a copy.
	assert.Same(t, &recs[0], &passed[0])
}

func TestNewDuplicateChecker_DefaultAndCustomLookback(t *testing.T) {
	t.Parallel()
	assert.Equal(t, DefaultDuplicateCheckLookbackHours, NewDuplicateChecker(0).LookbackHours)
	assert.Equal(t, DefaultDuplicateCheckLookbackHours, NewDuplicateChecker(-1).LookbackHours)
	assert.Equal(t, 48, NewDuplicateChecker(48).LookbackHours)
}

func TestAdjustRecommendationsForExisting_NilLogfDoesNotPanic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := &fakeServiceClient{commitments: []common.Commitment{
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 2, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
	}}
	recs := []common.Recommendation{
		{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "mysql"}},
	}

	d := NewDuplicateChecker(0)
	assert.NotPanics(t, func() {
		_, _, err := d.AdjustRecommendationsForExisting(ctx, recs, client)
		require.NoError(t, err)
	})
}

func TestAdjustRecommendationsForExisting_LogfReceivesDecisionTrail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := &fakeServiceClient{commitments: []common.Commitment{
		{ResourceType: "db.t3.small", Region: "us-east-1", Engine: "mysql", Count: 5, State: "active", StartDate: time.Now().Add(-1 * time.Hour)},
	}}
	recs := []common.Recommendation{
		{ResourceType: "db.t3.small", Region: "us-east-1", Count: 5, Details: &common.DatabaseDetails{Engine: "mysql"}},
	}

	var lines []string
	d := NewDuplicateChecker(0)
	d.Logf = func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}

	_, _, err := d.AdjustRecommendationsForExisting(ctx, recs, client)
	require.NoError(t, err)

	require.NotEmpty(t, lines)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "[DuplicateChecker]") {
			found = true
			break
		}
	}
	assert.True(t, found)
}
