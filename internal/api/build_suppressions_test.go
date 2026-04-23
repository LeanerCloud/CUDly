package api

import (
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

func TestBuildSuppressions_CreatesOneRowPerTuple(t *testing.T) {
	now := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	gp := &config.GlobalConfig{GracePeriodDays: map[string]int{"aws": 7, "azure": 7, "gcp": 7}}

	sups := buildSuppressions([]config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano", Count: 3, CloudAccountID: strPtr("acct-1")},
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano", Count: 2, CloudAccountID: strPtr("acct-1")},
		{Provider: "aws", Service: "rds", Region: "us-east-1", ResourceType: "db.t4g.micro", Engine: "postgres", Count: 1, CloudAccountID: strPtr("acct-1")},
	}, "exec-1", gp, now)

	require.Len(t, sups, 2, "duplicate 6-tuple coalesced to one row; distinct service → separate row")
	// First bucket: summed count (3 + 2).
	assert.Equal(t, 5, sups[0].SuppressedCount)
	assert.Equal(t, "t4g.nano", sups[0].ResourceType)
	// Second bucket: the RDS one with engine=postgres.
	assert.Equal(t, 1, sups[1].SuppressedCount)
	assert.Equal(t, "postgres", sups[1].Engine)
	// Expiry = now + 7 days for every row.
	assert.Equal(t, now.Add(7*24*time.Hour), sups[0].ExpiresAt)
}

func TestBuildSuppressions_SkipsProvidersWithGrace0(t *testing.T) {
	now := time.Now()
	gp := &config.GlobalConfig{GracePeriodDays: map[string]int{"aws": 7, "azure": 0, "gcp": 14}}

	sups := buildSuppressions([]config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano", Count: 1, CloudAccountID: strPtr("acct-1")},
		{Provider: "azure", Service: "compute", Region: "eastus", ResourceType: "Standard_D2d_v5", Count: 2, CloudAccountID: strPtr("acct-2")},
		{Provider: "gcp", Service: "compute", Region: "us-central1", ResourceType: "n1-standard-1", Count: 3, CloudAccountID: strPtr("acct-3")},
	}, "exec-1", gp, now)

	require.Len(t, sups, 2, "azure has grace=0 → no row; aws + gcp still produce rows")
	providers := []string{sups[0].Provider, sups[1].Provider}
	assert.Contains(t, providers, "aws")
	assert.Contains(t, providers, "gcp")
	assert.NotContains(t, providers, "azure")
}

func TestBuildSuppressions_NilAccountIDNormalised(t *testing.T) {
	now := time.Now()
	gp := &config.GlobalConfig{}

	sups := buildSuppressions([]config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano", Count: 1, CloudAccountID: nil},
	}, "exec-1", gp, now)

	require.Len(t, sups, 1)
	assert.Equal(t, "", sups[0].AccountID, "nil CloudAccountID → empty-string account_id")
}

func TestBuildSuppressions_SkipsZeroCountRecs(t *testing.T) {
	now := time.Now()
	gp := &config.GlobalConfig{}

	sups := buildSuppressions([]config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano", Count: 0, CloudAccountID: strPtr("acct-1")},
	}, "exec-1", gp, now)

	assert.Empty(t, sups, "zero-count recs don't produce suppression rows")
}
