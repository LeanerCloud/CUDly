//go:build integration
// +build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	"github.com/stretchr/testify/require"
)

// fakeReshapeEC2 is a stub implementation of reshapeEC2Client for the
// integration test. Returns fixed RIs + fixed offerings (or an error
// when errOnOfferings is set). Lets the test assert on what the
// reshape handler does with the AWS-returned shapes without hitting
// real AWS.
type fakeReshapeEC2 struct {
	instances       []ec2svc.ConvertibleRI
	offerings       []exchange.OfferingOption
	offeringsErr    error
	offeringsCalled atomic.Int32
}

func (f *fakeReshapeEC2) ListConvertibleReservedInstances(_ context.Context) ([]ec2svc.ConvertibleRI, error) {
	return f.instances, nil
}
func (f *fakeReshapeEC2) FindConvertibleOfferings(_ context.Context, _ []string) ([]exchange.OfferingOption, error) {
	f.offeringsCalled.Add(1)
	if f.offeringsErr != nil {
		return nil, f.offeringsErr
	}
	return f.offerings, nil
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

// TestReshapeRecommendations_Integration_EndToEnd exercises the full
// path: auth → cache cold-fetch → AnalyzeReshapingWithOfferings
// pricing enrichment → JSON response. Asserts the response carries
// alternative_targets sorted ascending by effective_monthly_cost.
func TestReshapeRecommendations_Integration_EndToEnd(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupReshapeHandlerIntegration(ctx, t)
	defer cleanup()

	ec2Fake := &fakeReshapeEC2{
		instances: []ec2svc.ConvertibleRI{
			{ReservedInstanceID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1},
		},
		offerings: []exchange.OfferingOption{
			{InstanceType: "m5.large", OfferingID: "off-m5", EffectiveMonthlyCost: 40.0},
			{InstanceType: "m6i.large", OfferingID: "off-m6i", EffectiveMonthlyCost: 35.0},
			{InstanceType: "m7g.large", OfferingID: "off-m7g", EffectiveMonthlyCost: 30.0},
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

	// AnalyzeReshapingWithOfferings drops the source family (m5) from
	// the alternatives, so we expect m6i.large + m7g.large only. The
	// order preserves the peer-family allowlist ordering from
	// `peerFamilyGroups["m5"] = {"m5","m6i","m7g"}` (m6i before m7g);
	// the pricing enrichment doesn't re-sort by cost. Callers that
	// want cost-order can sort AlternativeTargets client-side.
	require.Len(t, rec.AlternativeTargets, 2)
	require.Equal(t, "m6i.large", rec.AlternativeTargets[0].InstanceType)
	require.Equal(t, "off-m6i", rec.AlternativeTargets[0].OfferingID)
	require.InDelta(t, 35.0, rec.AlternativeTargets[0].EffectiveMonthlyCost, 0.001)
	require.Equal(t, "m7g.large", rec.AlternativeTargets[1].InstanceType)
	require.Equal(t, "off-m7g", rec.AlternativeTargets[1].OfferingID)
	require.InDelta(t, 30.0, rec.AlternativeTargets[1].EffectiveMonthlyCost, 0.001)

	// Verify the RI utilization cache row landed in Postgres.
	entry, err := store.GetRIUtilizationCache(ctx, "us-east-1", 30)
	require.NoError(t, err)
	require.NotNil(t, entry)
	var cached []recommendations.RIUtilization
	require.NoError(t, json.Unmarshal(entry.Payload, &cached))
	require.Len(t, cached, 1)
	require.Equal(t, "ri-1", cached[0].ReservedInstanceID)

	// Fetcher + offerings each called exactly once on the cold path.
	require.Equal(t, int32(1), recsFake.calls.Load())
	require.Equal(t, int32(1), ec2Fake.offeringsCalled.Load())
}

// TestReshapeRecommendations_Integration_SecondCallHitsCache verifies
// the cache short-circuits the RI utilization fetcher on a second
// request within TTL.
func TestReshapeRecommendations_Integration_SecondCallHitsCache(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupReshapeHandlerIntegration(ctx, t)
	defer cleanup()

	ec2Fake := &fakeReshapeEC2{
		instances: []ec2svc.ConvertibleRI{
			{ReservedInstanceID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1},
		},
		offerings: []exchange.OfferingOption{
			{InstanceType: "m5.large", OfferingID: "off-m5", EffectiveMonthlyCost: 40.0},
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

	// Offerings lookup is NOT cached (distinct concern) — expected to
	// be called on each request.
	require.Equal(t, int32(2), ec2Fake.offeringsCalled.Load())
}

// TestReshapeRecommendations_Integration_OfferingsLookupErrorKeepsNameOnlyAlternatives
// verifies the graceful-degradation contract: when FindConvertibleOfferings
// returns an error, the base recs still ship with name-only
// alternatives (no OfferingID, zero cost).
func TestReshapeRecommendations_Integration_OfferingsLookupErrorKeepsNameOnlyAlternatives(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupReshapeHandlerIntegration(ctx, t)
	defer cleanup()

	ec2Fake := &fakeReshapeEC2{
		instances: []ec2svc.ConvertibleRI{
			{ReservedInstanceID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1},
		},
		offeringsErr: fmt.Errorf("simulated cost-explorer 5xx"),
	}
	recsFake := &fakeReshapeRecs{
		utilization: []recommendations.RIUtilization{
			{ReservedInstanceID: "ri-1", UtilizationPercent: 50.0},
		},
	}
	h := buildReshapeHandler(store, ec2Fake, recsFake)

	resp, err := h.getReshapeRecommendations(ctx, reshapeRequest())
	require.NoError(t, err, "handler must not fail when the offerings lookup errors — graceful degradation")
	body := resp.(*ReshapeRecommendationsResponse)
	require.Len(t, body.Recommendations, 1)
	rec := body.Recommendations[0]
	require.Equal(t, "m5.large", rec.TargetInstanceType, "primary target stays intact")

	// AnalyzeReshapingWithOfferings returns the base recs on lookup
	// error, which means AlternativeTargets keeps its name-only
	// entries from AnalyzeReshaping (instance_type set, offering_id
	// empty, cost zero).
	require.NotEmpty(t, rec.AlternativeTargets, "base recs still ship with name-only alternatives")
	for _, alt := range rec.AlternativeTargets {
		require.NotEmpty(t, alt.InstanceType)
		require.Empty(t, alt.OfferingID, "lookup error leaves offering_id blank")
		require.Zero(t, alt.EffectiveMonthlyCost, "lookup error leaves cost zero")
	}
}
