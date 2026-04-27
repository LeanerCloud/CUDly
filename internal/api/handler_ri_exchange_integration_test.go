//go:build integration
// +build integration

package api

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	"github.com/stretchr/testify/require"
)

// fakeReshapeEC2 is a stub implementation of reshapeEC2Client for the
// integration test. Returns fixed convertible RIs without hitting real
// AWS. Cross-family alternatives now flow through the recommendations
// store (see purchaseRecLookupFromStore), so this fake no longer needs
// to mock FindConvertibleOfferings.
type fakeReshapeEC2 struct {
	instances []ec2svc.ConvertibleRI
}

func (f *fakeReshapeEC2) ListConvertibleReservedInstances(_ context.Context) ([]ec2svc.ConvertibleRI, error) {
	return f.instances, nil
}

// fakeReshapeRecs is a stub for reshapeRecsClient. Counts calls so the
// test can assert cache-hit behaviour on the second request.
type fakeReshapeRecs struct {
	utilization []recommendations.RIUtilization
	calls       atomic.Int32
}

func (f *fakeReshapeRecs) GetRIUtilization(_ context.Context, _ int) ([]recommendations.RIUtilization, error) {
	f.calls.Add(1)
	return f.utilization, nil
}

func setupReshapeHandlerIntegration(ctx context.Context, t *testing.T) (*config.PostgresStore, func()) {
	t.Helper()
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	err = migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", "")
	require.NoError(t, err)
	store := config.NewPostgresStore(container.DB)
	return store, func() { _ = container.Cleanup(ctx) }
}

// buildReshapeHandler constructs a Handler with only the fields the
// reshape-recommendations path touches. AWS config is pre-populated
// so `h.loadAWSConfigWithRegion` returns without trying to hit real
// AWS. Factories are injected so AWS calls go to the fakes.
func buildReshapeHandler(store *config.PostgresStore, ec2Fake *fakeReshapeEC2, recsFake *fakeReshapeRecs) *Handler {
	h := &Handler{
		config:             store,
		auth:               &mockAuthForExchange{},
		apiKey:             "test-api-key",
		reshapeEC2Factory:  func(_ aws.Config) reshapeEC2Client { return ec2Fake },
		reshapeRecsFactory: func(_ aws.Config) reshapeRecsClient { return recsFake },
		// Bypass STS GetCallerIdentity so the test runs without real
		// AWS credentials. Empty cloud-account ID = no AccountIDs
		// filter on the recs lookup, which is the legitimate
		// "no-scope-filter" path; tests that assert scope filtering
		// would set this to a UUID matching a seeded CloudAccount.
		reshapeAccountResolver: func(_ context.Context) (string, error) { return "", nil },
	}
	// Pre-populate the AWS config cache so loadAWSConfigWithRegion
	// returns immediately without LoadDefaultConfig. The Region field
	// isn't read by our fakes but we set it so getBaseAWSConfig's
	// lazy path is sealed.
	h.awsCfgOnce.Do(func() {
		h.awsCfg = aws.Config{Region: "us-east-1"}
	})
	return h
}

func reshapeRequest() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"x-api-key": "test-api-key"},
		QueryStringParameters: map[string]string{
			"region":        "us-east-1",
			"lookback_days": "30",
			"threshold":     "95.0",
		},
	}
}

// seedRecsForRegion writes a snapshot of cached Cost Explorer purchase
// recommendations directly into the store so the reshape lookup has
// something to read. Bypasses the scheduler so the test only exercises
// the read-side mapping logic.
func seedRecsForRegion(ctx context.Context, t *testing.T, store *config.PostgresStore, region string, recs []config.RecommendationRecord) {
	t.Helper()
	require.NoError(t, store.ReplaceRecommendations(ctx, time.Now(), recs),
		"seeding recommendations into the test container failed")
}

// TestReshapeRecommendations_Integration_EndToEnd exercises the full
// path: auth → cache cold-fetch → AnalyzeReshapingWithRecs (recs-driven
// alternatives) → JSON response. Asserts the response carries
// alternative_targets sorted ascending by effective_monthly_cost,
// sourced from the recommendations table rather than per-rec AWS API
// calls.
func TestReshapeRecommendations_Integration_EndToEnd(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupReshapeHandlerIntegration(ctx, t)
	defer cleanup()

	// Seed cross-family recs in us-east-1: m5 (same family, must be
	// excluded), c5, r5. The handler should surface c5 + r5 as
	// cross-family alternatives ordered by EffectiveMonthlyCost.
	seedRecsForRegion(ctx, t, store, "us-east-1", []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.large", Term: 1, MonthlyCost: 40},
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "c5.large", Term: 1, MonthlyCost: 50},
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "r5.large", Term: 1, MonthlyCost: 60},
	})

	ec2Fake := &fakeReshapeEC2{
		instances: []ec2svc.ConvertibleRI{
			{ReservedInstanceID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1},
		},
	}
	recsFake := &fakeReshapeRecs{
		utilization: []recommendations.RIUtilization{
			{ReservedInstanceID: "ri-1", UtilizationPercent: 50.0},
		},
	}
	h := buildReshapeHandler(store, ec2Fake, recsFake)

	resp, err := h.getReshapeRecommendations(ctx, reshapeRequest())
	require.NoError(t, err)
	body, ok := resp.(*ReshapeRecommendationsResponse)
	require.True(t, ok, "response should be a ReshapeRecommendationsResponse")
	require.Len(t, body.Recommendations, 1)
	rec := body.Recommendations[0]
	require.Equal(t, "ri-1", rec.SourceRIID)
	require.Equal(t, "m5.large", rec.TargetInstanceType)

	// Same-family m5 stripped; c5 + r5 surface ascending by cost.
	require.Len(t, rec.AlternativeTargets, 2,
		"cross-family alternatives must come from the cached recs (m5 same-family excluded)")
	require.Equal(t, "c5.large", rec.AlternativeTargets[0].InstanceType)
	require.InDelta(t, 50.0, rec.AlternativeTargets[0].EffectiveMonthlyCost, 0.001)
	require.Equal(t, "r5.large", rec.AlternativeTargets[1].InstanceType)
	require.InDelta(t, 60.0, rec.AlternativeTargets[1].EffectiveMonthlyCost, 0.001)

	// Verify the RI utilization cache row landed in Postgres.
	entry, err := store.GetRIUtilizationCache(ctx, "us-east-1", 30)
	require.NoError(t, err)
	require.NotNil(t, entry)
	var cached []recommendations.RIUtilization
	require.NoError(t, json.Unmarshal(entry.Payload, &cached))
	require.Len(t, cached, 1)
	require.Equal(t, "ri-1", cached[0].ReservedInstanceID)

	// Utilization fetcher called exactly once on the cold path.
	require.Equal(t, int32(1), recsFake.calls.Load())
}

// TestReshapeRecommendations_Integration_SecondCallHitsCache verifies
// the cache short-circuits the RI utilization fetcher on a second
// request within TTL. The recs lookup is a Postgres read on every
// request (no separate cache layer); only the Cost Explorer
// utilization fetcher is TTL-cached.
func TestReshapeRecommendations_Integration_SecondCallHitsCache(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupReshapeHandlerIntegration(ctx, t)
	defer cleanup()

	seedRecsForRegion(ctx, t, store, "us-east-1", []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "c5.large", Term: 1, MonthlyCost: 40},
	})

	ec2Fake := &fakeReshapeEC2{
		instances: []ec2svc.ConvertibleRI{
			{ReservedInstanceID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1},
		},
	}
	recsFake := &fakeReshapeRecs{
		utilization: []recommendations.RIUtilization{
			{ReservedInstanceID: "ri-1", UtilizationPercent: 50.0},
		},
	}
	h := buildReshapeHandler(store, ec2Fake, recsFake)

	// Cold call.
	_, err := h.getReshapeRecommendations(ctx, reshapeRequest())
	require.NoError(t, err)
	require.Equal(t, int32(1), recsFake.calls.Load())

	// Second call should hit the cache — fetcher call count stays at 1.
	_, err = h.getReshapeRecommendations(ctx, reshapeRequest())
	require.NoError(t, err)
	require.Equal(t, int32(1), recsFake.calls.Load(),
		"fresh read within soft TTL must not re-invoke the utilization fetcher")
}

// TestReshapeRecommendations_Integration_NoCachedRecsReturnsPrimaryOnly
// — when the recommendations table is empty for the region, the rec
// still ships with its primary same-family target but with no
// alternatives. UX matches "AWS hasn't recommended anything for this
// region yet" rather than blanking the page.
func TestReshapeRecommendations_Integration_NoCachedRecsReturnsPrimaryOnly(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupReshapeHandlerIntegration(ctx, t)
	defer cleanup()

	// Note: NO seedRecsForRegion call — table starts empty.

	ec2Fake := &fakeReshapeEC2{
		instances: []ec2svc.ConvertibleRI{
			{ReservedInstanceID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1},
		},
	}
	recsFake := &fakeReshapeRecs{
		utilization: []recommendations.RIUtilization{
			{ReservedInstanceID: "ri-1", UtilizationPercent: 50.0},
		},
	}
	h := buildReshapeHandler(store, ec2Fake, recsFake)

	resp, err := h.getReshapeRecommendations(ctx, reshapeRequest())
	require.NoError(t, err, "handler must not fail when the recommendations cache is empty")
	body := resp.(*ReshapeRecommendationsResponse)
	require.Len(t, body.Recommendations, 1)
	rec := body.Recommendations[0]
	require.Equal(t, "m5.large", rec.TargetInstanceType, "primary target stays intact")
	require.Empty(t, rec.AlternativeTargets,
		"empty cache → no alternatives, primary target still surfaced")
}
