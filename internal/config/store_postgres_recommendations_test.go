//go:build integration
// +build integration

package config_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRecommendationsStore(ctx context.Context, t *testing.T) (*config.PostgresStore, func()) {
	t.Helper()
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	err = migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", "")
	require.NoError(t, err)
	store := config.NewPostgresStore(container.DB)
	return store, func() { container.Cleanup(ctx) }
}

func awsRec(id, service, region, resourceType string, savings float64) config.RecommendationRecord {
	return config.RecommendationRecord{
		ID:           id,
		Provider:     "aws",
		Service:      service,
		Region:       region,
		ResourceType: resourceType,
		Savings:      savings,
		UpfrontCost:  100.0,
		MonthlyCost:  50.0,
		Count:        1,
		Term:         12,
		Payment:      "no-upfront",
	}
}

func TestPostgresStore_ReplaceRecommendations(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRecommendationsStore(ctx, t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)

	initial := []config.RecommendationRecord{
		awsRec("a1", "ec2", "us-east-1", "m5.large", 10),
		awsRec("a2", "rds", "us-east-1", "db.r5.large", 20),
	}
	require.NoError(t, store.ReplaceRecommendations(ctx, now, initial))

	got, err := store.ListStoredRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// Replace with a new snapshot — original rows must be gone.
	replacement := []config.RecommendationRecord{
		awsRec("b1", "ec2", "eu-west-1", "m5.xlarge", 50),
	}
	require.NoError(t, store.ReplaceRecommendations(ctx, now.Add(time.Minute), replacement))

	got, err = store.ListStoredRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "eu-west-1", got[0].Region)
}

func TestPostgresStore_UpsertRecommendations_PartialCollect(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRecommendationsStore(ctx, t)
	defer cleanup()

	t0 := time.Now().UTC().Truncate(time.Second)

	// Seed full collection covering both aws and azure.
	seed := []config.RecommendationRecord{
		awsRec("a1", "ec2", "us-east-1", "m5.large", 10),
		{ID: "z1", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: "Standard_D2", Savings: 15},
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t0, seed, []string{"aws", "azure"}))

	// Second collect succeeds only for aws — provide new aws rows, list only
	// aws in successfulProviders. Azure rows must be preserved since azure
	// didn't run.
	t1 := t0.Add(time.Hour)
	partial := []config.RecommendationRecord{
		awsRec("a1", "ec2", "us-east-1", "m5.large", 25), // same natural key as before → upsert
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t1, partial, []string{"aws"}))

	got, err := store.ListStoredRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2, "azure row should survive partial-aws collect")

	var awsFound, azureFound bool
	for _, rec := range got {
		if rec.Provider == "aws" {
			awsFound = true
			assert.InDelta(t, 25.0, rec.Savings, 0.001, "aws row should reflect upserted savings")
		}
		if rec.Provider == "azure" {
			azureFound = true
		}
	}
	assert.True(t, awsFound)
	assert.True(t, azureFound)
}

func TestPostgresStore_UpsertRecommendations_EvictsStaleInSuccessfulProvider(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRecommendationsStore(ctx, t)
	defer cleanup()

	t0 := time.Now().UTC().Truncate(time.Second)

	// Seed two aws rows.
	seed := []config.RecommendationRecord{
		awsRec("a1", "ec2", "us-east-1", "m5.large", 10),
		awsRec("a2", "ec2", "us-east-1", "m5.xlarge", 20),
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t0, seed, []string{"aws"}))

	// Second collect: aws only returns one row (the m5.xlarge is gone).
	// With successfulProviders=["aws"], the unseen m5.large must be deleted.
	t1 := t0.Add(time.Hour)
	follow := []config.RecommendationRecord{
		awsRec("a2", "ec2", "us-east-1", "m5.xlarge", 25),
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t1, follow, []string{"aws"}))

	got, err := store.ListStoredRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1, "unseen aws row should be evicted")
	assert.Equal(t, "m5.xlarge", got[0].ResourceType)
}

func TestPostgresStore_ListStoredRecommendations_FilterPushdown(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRecommendationsStore(ctx, t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)

	recs := []config.RecommendationRecord{
		awsRec("a1", "ec2", "us-east-1", "m5.large", 5),
		awsRec("a2", "rds", "us-east-1", "db.r5.large", 30),
		{ID: "z1", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: "Standard_D2", Savings: 20},
	}
	require.NoError(t, store.ReplaceRecommendations(ctx, now, recs))

	// Filter by provider.
	got, err := store.ListStoredRecommendations(ctx, config.RecommendationFilter{Provider: "aws"})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// Filter by service.
	got, err = store.ListStoredRecommendations(ctx, config.RecommendationFilter{Service: "ec2"})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	// Filter by min savings.
	got, err = store.ListStoredRecommendations(ctx, config.RecommendationFilter{MinSavings: 25})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "rds", got[0].Service)
}

func TestPostgresStore_Freshness_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRecommendationsStore(ctx, t)
	defer cleanup()

	// Fresh migration: LastCollectedAt is nil.
	fr, err := store.GetRecommendationsFreshness(ctx)
	require.NoError(t, err)
	assert.Nil(t, fr.LastCollectedAt)
	assert.Nil(t, fr.LastCollectionError)

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, store.ReplaceRecommendations(ctx, now, nil))

	fr, err = store.GetRecommendationsFreshness(ctx)
	require.NoError(t, err)
	require.NotNil(t, fr.LastCollectedAt)
	assert.WithinDuration(t, now, *fr.LastCollectedAt, time.Second)
	assert.Nil(t, fr.LastCollectionError)

	// SetRecommendationsCollectionError leaves last_collected_at untouched.
	require.NoError(t, store.SetRecommendationsCollectionError(ctx, "aws failed"))
	fr, err = store.GetRecommendationsFreshness(ctx)
	require.NoError(t, err)
	require.NotNil(t, fr.LastCollectedAt)
	require.NotNil(t, fr.LastCollectionError)
	assert.Equal(t, "aws failed", *fr.LastCollectionError)
}
