//go:build integration
// +build integration

package config_test

import (
	"context"
	"fmt"
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

	// Seed full collection covering both aws and azure (both ambient — nil account).
	seed := []config.RecommendationRecord{
		awsRec("a1", "ec2", "us-east-1", "m5.large", 10),
		{ID: "z1", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: "Standard_D2", Savings: 15},
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t0, seed, []config.SuccessfulCollect{
		{Provider: "aws"},
		{Provider: "azure"},
	}))

	// Second collect succeeds only for aws — provide new aws rows, list only
	// aws in successfulCollects. Azure rows must be preserved since azure
	// didn't run.
	t1 := t0.Add(time.Hour)
	partial := []config.RecommendationRecord{
		awsRec("a1", "ec2", "us-east-1", "m5.large", 25), // same natural key as before → upsert
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t1, partial, []config.SuccessfulCollect{
		{Provider: "aws"},
	}))

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

	// Seed two aws rows (ambient — nil account).
	seed := []config.RecommendationRecord{
		awsRec("a1", "ec2", "us-east-1", "m5.large", 10),
		awsRec("a2", "ec2", "us-east-1", "m5.xlarge", 20),
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t0, seed, []config.SuccessfulCollect{
		{Provider: "aws"},
	}))

	// Second collect: aws only returns one row (the m5.xlarge is gone).
	// With successfulCollects=[{aws,nil}], the unseen m5.large must be deleted.
	t1 := t0.Add(time.Hour)
	follow := []config.RecommendationRecord{
		awsRec("a2", "ec2", "us-east-1", "m5.xlarge", 25),
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t1, follow, []config.SuccessfulCollect{
		{Provider: "aws"},
	}))

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

// TestPostgresStore_UpsertRecommendations_StoresAllTermVariants pins
// the broadened-natural-key behaviour from migration 000032: when
// Azure returns multiple `(term, payment)` variants for the same
// (account, provider, service, region, resource_type) SKU, all of
// them must round-trip through the cache as distinct rows. Pre-fix
// they collided on `ON CONFLICT DO UPDATE command cannot affect
// row a second time` (SQLSTATE 21000); the worked-around dedupe
// helper would silently drop all but the highest-savings variant.
//
// Schema dependency: requires migration 000032 applied (term and
// payment_option columns + the 7-column unique index). The
// setupRecommendationsStore helper runs the full migration chain.
func TestPostgresStore_UpsertRecommendations_StoresAllTermVariants(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRecommendationsStore(ctx, t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)

	// Three Azure recs that share (account, provider, service, region,
	// resource_type) but differ in (term, payment) — same SKU, three
	// variants the user might choose between.
	azureSKU := "Standard_D2s_v3"
	variants := []config.RecommendationRecord{
		{ID: "v1", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: azureSKU, Term: 1, Payment: "upfront", Savings: 100, UpfrontCost: 1000, Count: 1},
		{ID: "v2", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: azureSKU, Term: 3, Payment: "upfront", Savings: 500, UpfrontCost: 4000, Count: 1},
		{ID: "v3", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: azureSKU, Term: 3, Payment: "no-upfront", Savings: 350, UpfrontCost: 0, Count: 1},
	}

	require.NoError(t, store.UpsertRecommendations(ctx, now, variants, []config.SuccessfulCollect{
		{Provider: "azure"},
	}))

	got, err := store.ListStoredRecommendations(ctx, config.RecommendationFilter{Provider: "azure"})
	require.NoError(t, err)
	require.Len(t, got, 3, "all 3 (term, payment) variants must round-trip — pre-fix this would have collapsed to 1")

	// Distinct (Term, Payment) tuples — none missing.
	seen := map[string]bool{}
	for _, r := range got {
		key := fmt.Sprintf("%d/%s", r.Term, r.Payment)
		seen[key] = true
	}
	assert.True(t, seen["1/upfront"], "1yr upfront variant must be present")
	assert.True(t, seen["3/upfront"], "3yr upfront variant must be present")
	assert.True(t, seen["3/no-upfront"], "3yr no-upfront variant must be present")
}

// TestPostgresStore_UpsertRecommendations_AccountScopedEviction pins the
// per-account eviction contract: when only one of two accounts under the
// same provider succeeds, the other account's previous-cycle rows survive.
// This is the core bug-fix this commit lands — pre-fix, eviction was
// scoped to (provider) only and would wipe the failed account's rows.
func TestPostgresStore_UpsertRecommendations_AccountScopedEviction(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRecommendationsStore(ctx, t)
	defer cleanup()

	// Two registered Azure accounts; valid UUIDs because the
	// account_key generated column is UUID-typed.
	acct1 := "11111111-1111-1111-1111-111111111111"
	acct2 := "22222222-2222-2222-2222-222222222222"

	t0 := time.Now().UTC().Truncate(time.Second)

	seed := []config.RecommendationRecord{
		{ID: "az1-a", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: "Standard_D2", Savings: 10, CloudAccountID: &acct1},
		{ID: "az1-b", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: "Standard_D4", Savings: 20, CloudAccountID: &acct1},
		{ID: "az2-a", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: "Standard_D2", Savings: 30, CloudAccountID: &acct2},
		{ID: "az2-b", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: "Standard_D4", Savings: 40, CloudAccountID: &acct2},
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t0, seed, []config.SuccessfulCollect{
		{Provider: "azure", CloudAccountID: &acct1},
		{Provider: "azure", CloudAccountID: &acct2},
	}))

	// Partial collect at t1: only acct-1 succeeded with one new row.
	t1 := t0.Add(time.Hour)
	partial := []config.RecommendationRecord{
		{ID: "az1-c", Provider: "azure", Service: "vm", Region: "eastus", ResourceType: "Standard_D8", Savings: 50, CloudAccountID: &acct1},
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t1, partial, []config.SuccessfulCollect{
		{Provider: "azure", CloudAccountID: &acct1},
	}))

	// Assert: acct-1's stale rows (D2 + D4 from t0) are evicted; acct-1
	// keeps the new D8 row; acct-2's two rows survive.
	got, err := store.ListStoredRecommendations(ctx, config.RecommendationFilter{Provider: "azure"})
	require.NoError(t, err)

	byAccountAndType := map[string]bool{}
	for _, rec := range got {
		var acctTag string
		if rec.CloudAccountID != nil {
			acctTag = *rec.CloudAccountID
		}
		byAccountAndType[acctTag+"|"+rec.ResourceType] = true
	}
	assert.True(t, byAccountAndType[acct1+"|Standard_D8"], "acct-1 new row should be present")
	assert.False(t, byAccountAndType[acct1+"|Standard_D2"], "acct-1 stale D2 should be evicted")
	assert.False(t, byAccountAndType[acct1+"|Standard_D4"], "acct-1 stale D4 should be evicted")
	assert.True(t, byAccountAndType[acct2+"|Standard_D2"], "acct-2 row must be preserved (not in successfulCollects this run)")
	assert.True(t, byAccountAndType[acct2+"|Standard_D4"], "acct-2 row must be preserved (not in successfulCollects this run)")
}

// TestPostgresStore_UpsertRecommendations_AmbientAndRegisteredCoexist pins
// that ambient (cloud_account_id NULL → account_key=uuid.Nil) and
// registered identities are evicted independently within the same
// provider. A partial collect that only registers the registered account
// must not touch the ambient row.
func TestPostgresStore_UpsertRecommendations_AmbientAndRegisteredCoexist(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupRecommendationsStore(ctx, t)
	defer cleanup()

	registeredAcctID := "33333333-3333-3333-3333-333333333333"

	t0 := time.Now().UTC().Truncate(time.Second)

	// Seed one ambient row (CloudAccountID nil) + one registered row.
	seed := []config.RecommendationRecord{
		{ID: "amb1", Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.large", Savings: 10, CloudAccountID: nil},
		{ID: "reg1", Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.xlarge", Savings: 20, CloudAccountID: &registeredAcctID},
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t0, seed, []config.SuccessfulCollect{
		{Provider: "aws"}, // ambient (nil)
		{Provider: "aws", CloudAccountID: &registeredAcctID},
	}))

	// Partial collect at t1: only the registered account succeeded.
	t1 := t0.Add(time.Hour)
	partial := []config.RecommendationRecord{
		{ID: "reg1-v2", Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.xlarge", Savings: 25, CloudAccountID: &registeredAcctID},
	}
	require.NoError(t, store.UpsertRecommendations(ctx, t1, partial, []config.SuccessfulCollect{
		{Provider: "aws", CloudAccountID: &registeredAcctID},
	}))

	got, err := store.ListStoredRecommendations(ctx, config.RecommendationFilter{Provider: "aws"})
	require.NoError(t, err)
	require.Len(t, got, 2, "ambient row must survive; registered row must be upserted")

	var ambientFound, registeredFound bool
	for _, rec := range got {
		if rec.CloudAccountID == nil {
			ambientFound = true
			assert.InDelta(t, 10.0, rec.Savings, 0.001, "ambient row should keep its t0 savings")
		} else {
			registeredFound = true
			assert.Equal(t, registeredAcctID, *rec.CloudAccountID)
			assert.InDelta(t, 25.0, rec.Savings, 0.001, "registered row should reflect upserted savings")
		}
	}
	assert.True(t, ambientFound, "ambient row must remain in DB after partial registered-only collect")
	assert.True(t, registeredFound, "registered row must be upserted")
}
