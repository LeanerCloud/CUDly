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

	assert.True(t, Matches(&base, &matching), "identical fields should match")

	cases := []struct {
		mutate    func(c *Commitment)
		name      string
		wantMatch bool
	}{
		{name: "different provider", mutate: func(c *Commitment) { c.Provider = ProviderAzure }, wantMatch: false},
		{name: "different region", mutate: func(c *Commitment) { c.Region = "eu-west-1" }, wantMatch: false},
		{name: "different service", mutate: func(c *Commitment) { c.Service = ServiceEC2 }, wantMatch: false},
		{name: "different resource type", mutate: func(c *Commitment) { c.ResourceType = "db.r6g.large" }, wantMatch: false},
		{name: "different engine", mutate: func(c *Commitment) { c.Engine = "postgres" }, wantMatch: false},
		{name: "engine alias postgres==postgresql", mutate: func(c *Commitment) { c.Engine = "postgresql" }, wantMatch: false}, // "mysql" != "postgresql"
		{name: "state retired still matches (caller filters state)", mutate: func(c *Commitment) { c.State = "retired" }, wantMatch: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := matching // copy
			tc.mutate(&c)
			assert.Equal(t, tc.wantMatch, Matches(&base, &c))
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
	assert.True(t, Matches(&rec, &c))
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
	assert.True(t, Matches(&rec, &c))
}
