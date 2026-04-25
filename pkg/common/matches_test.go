package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatches(t *testing.T) {
	t.Parallel()
	base := Recommendation{
		Provider:     ProviderAWS,
		Region:       "us-east-1",
		Service:      ServiceRDS,
		ResourceType: "db.r5.large",
		Details:      &DatabaseDetails{Engine: "mysql"},
	}
	matching := Commitment{
		Provider:     ProviderAWS,
		Region:       "us-east-1",
		Service:      ServiceRDS,
		ResourceType: "db.r5.large",
		Engine:       "mysql",
	}

	assert.True(t, Matches(base, matching), "identical fields should match")

	cases := []struct {
		name      string
		mutate    func(c *Commitment)
		wantMatch bool
	}{
		{"different provider", func(c *Commitment) { c.Provider = ProviderAzure }, false},
		{"different region", func(c *Commitment) { c.Region = "eu-west-1" }, false},
		{"different service", func(c *Commitment) { c.Service = ServiceEC2 }, false},
		{"different resource type", func(c *Commitment) { c.ResourceType = "db.r6g.large" }, false},
		{"different engine", func(c *Commitment) { c.Engine = "postgres" }, false},
		{"engine alias postgres==postgresql", func(c *Commitment) { c.Engine = "postgresql" }, false}, // "mysql" != "postgresql"
		{"state retired still matches (caller filters state)", func(c *Commitment) { c.State = "retired" }, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := matching // copy
			tc.mutate(&c)
			assert.Equal(t, tc.wantMatch, Matches(base, c))
		})
	}
}

func TestMatches_NormalizedEngine(t *testing.T) {
	t.Parallel()
	// "Aurora PostgreSQL" (CE format) on the recommendation should match "aurora-postgresql" on commitment
	rec := Recommendation{
		Provider:     ProviderAWS,
		Region:       "us-east-1",
		Service:      ServiceRDS,
		ResourceType: "db.r5.large",
		Details:      &DatabaseDetails{Engine: "Aurora PostgreSQL"},
	}
	c := Commitment{
		Provider:     ProviderAWS,
		Region:       "us-east-1",
		Service:      ServiceRDS,
		ResourceType: "db.r5.large",
		Engine:       "aurora-postgresql",
	}
	assert.True(t, Matches(rec, c))
}

func TestMatches_NoDetails(t *testing.T) {
	t.Parallel()
	// Compute recommendations have no engine — both sides normalize to ""
	rec := Recommendation{
		Provider:     ProviderAWS,
		Region:       "us-east-1",
		Service:      ServiceEC2,
		ResourceType: "m5.large",
		Details:      nil,
	}
	c := Commitment{
		Provider:     ProviderAWS,
		Region:       "us-east-1",
		Service:      ServiceEC2,
		ResourceType: "m5.large",
		Engine:       "",
	}
	assert.True(t, Matches(rec, c))
}
