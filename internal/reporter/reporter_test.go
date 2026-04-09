package reporter

import (
	"strings"
	"testing"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/scorer"
	"github.com/stretchr/testify/assert"
)

func makeResult(passed []common.Recommendation, filtered []scorer.FilteredRecommendation) scorer.ScoredResult {
	return scorer.ScoredResult{Passed: passed, Filtered: filtered}
}

func TestRenderTable_ColumnCount(t *testing.T) {
	result := makeResult([]common.Recommendation{
		{Provider: "aws", AccountName: "prod", Region: "us-east-1", Service: "ec2",
			ResourceType: "m5.large", Term: "1yr", Count: 3,
			CommitmentCost: 1200.0, EstimatedSavings: 400.0, SavingsPercentage: 25.0,
			CommitmentType: common.CommitmentReservedInstance},
	}, nil)

	output := RenderTable(result)
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	// Header line
	header := lines[0]
	// Split on 2+ spaces (tabwriter aligns with spaces, not tabs after flush)
	cols := strings.Fields(header)
	assert.Equal(t, 12, len(cols), "expected 12 columns in header, got: %v", cols)
}

func TestRenderTable_Empty(t *testing.T) {
	result := makeResult(nil, nil)
	output := RenderTable(result)
	assert.NotEmpty(t, output)
	assert.Contains(t, output, "No recommendations")
}

func TestRenderTable_ContainsData(t *testing.T) {
	result := makeResult([]common.Recommendation{
		{Provider: "aws", Region: "eu-west-1", Service: "rds", ResourceType: "db.r5.large",
			Count: 2, EstimatedSavings: 300.0, SavingsPercentage: 20.0},
	}, nil)
	output := RenderTable(result)
	assert.Contains(t, output, "eu-west-1")
	assert.Contains(t, output, "db.r5.large")
}

func TestRenderExcluded_Empty(t *testing.T) {
	result := makeResult(nil, nil)
	output := RenderExcluded(result)
	assert.Empty(t, output)
}

func TestRenderExcluded_ContainsReason(t *testing.T) {
	result := makeResult(nil, []scorer.FilteredRecommendation{
		{
			Recommendation: common.Recommendation{Provider: "aws", Service: "ec2", ResourceType: "m5.large"},
			FilterReason:   "savings 3.0% below minimum 5.0%",
		},
	})
	output := RenderExcluded(result)
	assert.Contains(t, output, "savings 3.0% below minimum 5.0%")
	assert.Contains(t, output, "m5.large")
}

func TestRenderSummary_Totals(t *testing.T) {
	result := makeResult([]common.Recommendation{
		{EstimatedSavings: 300.0, CommitmentCost: 900.0},
		{EstimatedSavings: 200.0, CommitmentCost: 600.0},
	}, nil)
	output := RenderSummary(result)
	assert.Contains(t, output, "$500.00")  // total savings
	assert.Contains(t, output, "$1500.00") // total cost
}

func TestRenderSummary_WithFiltered(t *testing.T) {
	result := makeResult(
		[]common.Recommendation{{EstimatedSavings: 100.0, CommitmentCost: 500.0}},
		[]scorer.FilteredRecommendation{
			{FilterReason: "savings 2.0% below minimum 5.0%"},
			{FilterReason: "savings 1.0% below minimum 5.0%"},
		},
	)
	output := RenderSummary(result)
	assert.Contains(t, output, "Filtered: 2")
}
