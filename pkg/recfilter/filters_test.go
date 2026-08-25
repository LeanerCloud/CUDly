package recfilter

import (
	"fmt"
	"testing"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
)

func TestIncludesEngine_CEAndRISpellingsBothMatch(t *testing.T) {
	tests := []struct {
		name           string
		recEngine      string
		includeEngines []string
		expected       bool
	}{
		{"CE spelling rec, RI spelling filter", "Aurora PostgreSQL", []string{"aurora-postgresql"}, true},
		{"RI spelling rec, CE-normalized filter", "aurora-postgresql", []string{"aurora-postgresql"}, true},
		{"postgres rec, postgresql filter", "postgres", []string{"postgresql"}, true},
		{"PostgreSQL rec, postgresql filter", "PostgreSQL", []string{"postgresql"}, true},
		// The reciprocal direction: the filter carries the alias and the
		// recommendation the canonical name. Matching only after normalizing
		// the recommendation would miss every one of these.
		{"postgresql rec, postgres filter", "postgresql", []string{"postgres"}, true},
		{"aurora-postgresql rec, CE spelling filter", "aurora-postgresql", []string{"Aurora PostgreSQL"}, true},
		{"sqlserver rec, sqlserver-se filter", "sqlserver", []string{"sqlserver-se"}, true},
		{"oracle rec, oracle-ee filter", "oracle", []string{"oracle-ee"}, true},
		{"unrecognized engine still matches case-insensitively", "Db2", []string{"DB2"}, true},
		{"unrelated engine does not match", "mysql", []string{"postgres"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Filters{IncludeEngines: tt.includeEngines}
			rec := common.Recommendation{Details: &common.DatabaseDetails{Engine: tt.recEngine}}
			assert.Equal(t, tt.expected, f.IncludesEngine(&rec))
		})
	}
}

func TestIncludesRegion_EmptyIncludeListAllowsAll(t *testing.T) {
	f := Filters{}
	assert.True(t, f.IncludesRegion("us-east-1"))
	assert.True(t, f.IncludesRegion("eu-west-1"))
}

func TestIncludesInstanceType_EmptyIncludeListAllowsAll(t *testing.T) {
	f := Filters{}
	assert.True(t, f.IncludesInstanceType("db.t3.micro"))
	assert.True(t, f.IncludesInstanceType("cache.r5.large"))
}

func TestIncludesEngine_EmptyIncludeListAllowsAll(t *testing.T) {
	f := Filters{}
	rec := common.Recommendation{Details: &common.DatabaseDetails{Engine: "mysql"}}
	assert.True(t, f.IncludesEngine(&rec))
}

func TestIncludesRegion_ExcludeBeatsInclude(t *testing.T) {
	f := Filters{IncludeRegions: []string{"us-east-1"}, ExcludeRegions: []string{"us-east-1"}}
	assert.False(t, f.IncludesRegion("us-east-1"))
}

func TestIncludesInstanceType_ExcludeBeatsInclude(t *testing.T) {
	f := Filters{IncludeInstanceTypes: []string{"db.t3.micro"}, ExcludeInstanceTypes: []string{"db.t3.micro"}}
	assert.False(t, f.IncludesInstanceType("db.t3.micro"))
}

// Exclude entries are normalized on the same axis as include entries: an
// operator excluding "postgres" must not still get "postgresql" rows.
func TestIncludesEngine_ExcludeNormalizesAliases(t *testing.T) {
	f := Filters{ExcludeEngines: []string{"postgres"}}
	rec := common.Recommendation{Details: &common.DatabaseDetails{Engine: "PostgreSQL"}}
	assert.False(t, f.IncludesEngine(&rec))

	other := common.Recommendation{Details: &common.DatabaseDetails{Engine: "mysql"}}
	assert.True(t, f.IncludesEngine(&other))
}

func TestIncludesEngine_ExcludeBeatsInclude(t *testing.T) {
	f := Filters{IncludeEngines: []string{"mysql"}, ExcludeEngines: []string{"mysql"}}
	rec := common.Recommendation{Details: &common.DatabaseDetails{Engine: "mysql"}}
	assert.False(t, f.IncludesEngine(&rec))
}

func TestIncludesPoolSize(t *testing.T) {
	tests := []struct {
		name     string
		avg      float64
		minPool  float64
		expected bool
	}{
		{"filter disabled", 0.5, 0, true},
		{"no per-hour signal passes through", 0, 2.0, true},
		{"below threshold", 1.5, 2.0, false},
		{"at threshold", 2.0, 2.0, true},
		{"above threshold", 3.0, 2.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Filters{MinPoolSize: tt.minPool}
			rec := common.Recommendation{AverageInstancesUsedPerHour: tt.avg}
			assert.Equal(t, tt.expected, f.IncludesPoolSize(&rec))
		})
	}
}

func TestApplyMinPoolSize_NilLogfSafeAndRecordsDrop(t *testing.T) {
	f := Filters{MinPoolSize: 2.0}
	recs := []common.Recommendation{
		{Region: "us-east-1", ResourceType: "db.t3.micro", AverageInstancesUsedPerHour: 1.5},
	}
	drops := common.NewDropSummary()

	assert.NotPanics(t, func() {
		result := f.ApplyMinPoolSize(recs, nil, drops)
		assert.Empty(t, result)
	})
	assert.Equal(t, 1, drops.Total())
	assert.Contains(t, drops.FormatOneLine(), common.DropMinPoolSize)
}

func TestApplyMinPoolSize_DisabledReturnsUnchangedNoDrops(t *testing.T) {
	f := Filters{MinPoolSize: 0}
	recs := []common.Recommendation{
		{Region: "us-east-1", ResourceType: "db.t3.micro", AverageInstancesUsedPerHour: 0.1},
	}
	drops := common.NewDropSummary()

	result := f.ApplyMinPoolSize(recs, nil, drops)

	assert.Equal(t, recs, result)
	assert.Equal(t, 0, drops.Total())
}

func TestApplyMinPoolSize_LogfReceivesExpectedLines(t *testing.T) {
	f := Filters{MinPoolSize: 2.0}
	recs := []common.Recommendation{
		{Service: common.ServiceRDS, Region: "us-east-1", ResourceType: "db.t3.micro", AverageInstancesUsedPerHour: 1.5},
		{Service: common.ServiceRDS, Region: "us-east-1", ResourceType: "db.t3.small", AverageInstancesUsedPerHour: 5.0},
	}
	drops := common.NewDropSummary()

	var lines []string
	logf := func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}

	result := f.ApplyMinPoolSize(recs, logf, drops)

	assert.Len(t, result, 1)
	assert.Len(t, lines, 2)
	assert.Contains(t, lines[0], "--min-pool-size=2.0 dropped")
	assert.Contains(t, lines[1], "--min-pool-size dropped 1 recommendation(s)")
}

// The dimension predicates are deliberately independent rather than bundled
// behind one PassesDimensions helper: region resolution is provider-specific
// (a Savings Plan's effective region lives in Details, see #1881) and needs
// providers/aws, which the pkg module cannot import. Callers compose the
// portable checks with their own region handling.
func TestDimensionPredicates_IgnoreAccount(t *testing.T) {
	f := Filters{}
	rec := common.Recommendation{
		Region:       "us-east-1",
		ResourceType: "db.t3.micro",
		AccountName:  "some-restricted-account",
	}
	assert.True(t, f.IncludesRegion(rec.Region))
	assert.True(t, f.IncludesInstanceType(rec.ResourceType))
	assert.True(t, f.IncludesEngine(&rec))
}
