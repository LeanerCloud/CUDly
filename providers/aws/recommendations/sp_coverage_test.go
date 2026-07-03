package recommendations

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// floatPtr is a test helper that returns a pointer to f.
func floatPtr(f float64) *float64 { return &f }

// allSPPlanTypes enumerates the SDK's Savings Plans type enum members so the
// filter-construction tests cover every plan type the ladder can reference.
var allSPPlanTypes = []types.SupportedSavingsPlansType{
	types.SupportedSavingsPlansTypeComputeSp,
	types.SupportedSavingsPlansTypeEc2InstanceSp,
	types.SupportedSavingsPlansTypeSagemakerSp,
	types.SupportedSavingsPlansTypeDatabaseSp,
}

// wantSPDimensionValues re-states the SAVINGS_PLANS_TYPE dimension
// vocabulary independently of the production spPlanTypeDimensionValues map,
// so a typo introduced in the production map fails these tests instead of
// being read back and trusted. The CamelCase vocabulary (matching the CUR
// savings_plan_type column) is deliberately NOT the parameter-enum spelling
// ("COMPUTE_SP", ...), which CE would silently match nothing on.
var wantSPDimensionValues = map[types.SupportedSavingsPlansType]string{
	types.SupportedSavingsPlansTypeComputeSp:     "ComputeSavingsPlans",
	types.SupportedSavingsPlansTypeEc2InstanceSp: "EC2InstanceSavingsPlans",
	types.SupportedSavingsPlansTypeSagemakerSp:   "SageMakerSavingsPlans",
	types.SupportedSavingsPlansTypeDatabaseSp:    "DatabaseSavingsPlans",
}

// mockSPCE extends mockCostExplorerAPI with configurable SP coverage and
// utilization responses for hermetic testing without hitting AWS. Incoming
// request inputs are captured so tests can assert Filter construction.
type mockSPCE struct {
	coverageErr error
	utilErr     error
	utilOutput  *costexplorer.GetSavingsPlansUtilizationOutput
	mockCostExplorerAPI
	// coveragePages is consumed in order on each GetSavingsPlansCoverage call.
	// When exhausted, subsequent calls return an empty output.
	coveragePages []*costexplorer.GetSavingsPlansCoverageOutput
	// coverageInputs records each GetSavingsPlansCoverage request.
	coverageInputs []*costexplorer.GetSavingsPlansCoverageInput
	// utilInputs records each GetSavingsPlansUtilization request.
	utilInputs  []*costexplorer.GetSavingsPlansUtilizationInput
	coverageIdx int
	// coverageAlwaysToken makes every GetSavingsPlansCoverage response carry
	// a non-empty NextToken, for exercising the pagination cap.
	coverageAlwaysToken bool
}

func (m *mockSPCE) GetSavingsPlansCoverage(_ context.Context, params *costexplorer.GetSavingsPlansCoverageInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansCoverageOutput, error) {
	m.coverageInputs = append(m.coverageInputs, params)
	if m.coverageErr != nil {
		return nil, m.coverageErr
	}
	if m.coverageAlwaysToken {
		return &costexplorer.GetSavingsPlansCoverageOutput{NextToken: aws.String("again")}, nil
	}
	if m.coverageIdx >= len(m.coveragePages) {
		return &costexplorer.GetSavingsPlansCoverageOutput{}, nil
	}
	out := m.coveragePages[m.coverageIdx]
	m.coverageIdx++
	return out, nil
}

func (m *mockSPCE) GetSavingsPlansUtilization(_ context.Context, params *costexplorer.GetSavingsPlansUtilizationInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansUtilizationOutput, error) {
	m.utilInputs = append(m.utilInputs, params)
	if m.utilErr != nil {
		return nil, m.utilErr
	}
	if m.utilOutput == nil {
		return &costexplorer.GetSavingsPlansUtilizationOutput{}, nil
	}
	return m.utilOutput, nil
}

// oneSPCovPage is a test helper that builds a GetSavingsPlansCoverageOutput
// with a single SavingsPlansCoverage entry from the provided string fields.
// nil token means no more pages (last page).
func oneSPCovPage(covered, onDemand, pct string, nextToken *string) *costexplorer.GetSavingsPlansCoverageOutput {
	return &costexplorer.GetSavingsPlansCoverageOutput{
		SavingsPlansCoverages: []types.SavingsPlansCoverage{
			{
				Coverage: &types.SavingsPlansCoverageData{
					SpendCoveredBySavingsPlans: aws.String(covered),
					OnDemandCost:               aws.String(onDemand),
					CoveragePercentage:         aws.String(pct),
				},
			},
		},
		NextToken: nextToken,
	}
}

// TestGetSPCoverageSummary covers single-page happy path, multi-page
// pagination, empty response, nil Coverage block, zero-coverage vs missing
// data, and unparseable/negative number error paths.
func TestGetSPCoverageSummary(t *testing.T) {
	// lookbackDays=30 => windowHours=720.
	const lookback = 30
	const windowHours = float64(lookback * 24)

	tests := []struct {
		apiErr error
		// expected summary fields; nil means the field must be nil.
		wantPct      *float64
		wantCovered  *float64
		wantOnDemand *float64
		wantEligible *float64
		name         string
		pages        []*costexplorer.GetSavingsPlansCoverageOutput
		wantDays     int
		wantErr      bool
	}{
		{
			name: "single_page_happy_path",
			// $360 covered, $540 billed at on-demand rates (UNCOVERED) over
			// 30 days. Eligible total = 360+540 = 900.
			// covered/hr = 360/720 = 0.5; onDemand/hr = 540/720 = 0.75;
			// eligible/hr = 900/720 = 1.25.
			// pct = 360/900 * 100 = 40.00 (matches CE's own CoveragePercentage).
			pages:        []*costexplorer.GetSavingsPlansCoverageOutput{oneSPCovPage("360", "540", "40.00", nil)},
			wantPct:      floatPtr(360.0 / 900.0 * 100),
			wantCovered:  floatPtr(360.0 / windowHours),
			wantOnDemand: floatPtr(540.0 / windowHours),
			wantEligible: floatPtr(900.0 / windowHours),
			wantDays:     1,
		},
		{
			name: "multi_page_pagination",
			// Page 1: $720 covered, $1440 uncovered; NextToken present.
			// Page 2: $360 covered, $720 uncovered; no NextToken.
			// Totals: covered=1080, onDemand=2160, eligible=3240,
			// pct = 1080/3240 = 33.33%.
			pages: []*costexplorer.GetSavingsPlansCoverageOutput{
				oneSPCovPage("720", "1440", "33.33", aws.String("page2")),
				oneSPCovPage("360", "720", "33.33", nil),
			},
			wantPct:      floatPtr(1080.0 / 3240.0 * 100),
			wantCovered:  floatPtr(1080.0 / windowHours),
			wantOnDemand: floatPtr(2160.0 / windowHours),
			wantEligible: floatPtr(3240.0 / windowHours),
			wantDays:     2,
		},
		{
			name: "empty_response",
			pages: []*costexplorer.GetSavingsPlansCoverageOutput{
				{SavingsPlansCoverages: nil},
			},
			// Days=0 => all fields nil (CE has no data for the window).
			wantDays: 0,
		},
		{
			name: "nil_coverage_block_skipped",
			// CE can omit the Coverage block for periods with no eligible activity.
			// Such entries must not increment Days or contribute to totals.
			pages: []*costexplorer.GetSavingsPlansCoverageOutput{
				{
					SavingsPlansCoverages: []types.SavingsPlansCoverage{
						{Coverage: nil},
					},
				},
			},
			wantDays: 0,
		},
		{
			name: "zero_coverage_with_on_demand_spend",
			// Coverage block present; on-demand spend > 0 but nothing covered
			// by SPs. CoveragePct must be &0.0 (explicit zero), not nil.
			// This is "zero coverage" (distinct from "no data" where Days==0).
			pages: []*costexplorer.GetSavingsPlansCoverageOutput{
				{
					SavingsPlansCoverages: []types.SavingsPlansCoverage{
						{
							Coverage: &types.SavingsPlansCoverageData{
								SpendCoveredBySavingsPlans: aws.String("0"),
								OnDemandCost:               aws.String("720"),
								CoveragePercentage:         aws.String("0.0"),
							},
						},
					},
				},
			},
			wantPct:      floatPtr(0.0),
			wantCovered:  floatPtr(0.0 / windowHours),
			wantOnDemand: floatPtr(720.0 / windowHours),
			wantEligible: floatPtr(720.0 / windowHours),
			wantDays:     1,
		},
		{
			name: "full_coverage_yields_pct_100",
			// Everything covered: OnDemandCost (the UNCOVERED portion) is 0
			// while covered spend is positive. Eligible = covered, so pct
			// must be &100.0 - NOT nil. The old covered/onDemand arithmetic
			// misreported exactly this case as "no eligible activity".
			pages: []*costexplorer.GetSavingsPlansCoverageOutput{
				{
					SavingsPlansCoverages: []types.SavingsPlansCoverage{
						{
							Coverage: &types.SavingsPlansCoverageData{
								SpendCoveredBySavingsPlans: aws.String("720"),
								OnDemandCost:               aws.String("0"),
								CoveragePercentage:         aws.String("100.00"),
							},
						},
					},
				},
			},
			wantPct:      floatPtr(100.0),
			wantCovered:  floatPtr(720.0 / windowHours),
			wantOnDemand: floatPtr(0.0),
			wantEligible: floatPtr(720.0 / windowHours),
			wantDays:     1,
		},
		{
			name: "zero_eligible_leaves_pct_nil",
			// Coverage block present but both covered and uncovered spend
			// are zero: no SP-eligible activity at all, so there is no
			// denominator and CoveragePct must be nil.
			pages: []*costexplorer.GetSavingsPlansCoverageOutput{
				{
					SavingsPlansCoverages: []types.SavingsPlansCoverage{
						{
							Coverage: &types.SavingsPlansCoverageData{
								SpendCoveredBySavingsPlans: aws.String("0"),
								OnDemandCost:               aws.String("0"),
							},
						},
					},
				},
			},
			wantPct:      nil,
			wantCovered:  floatPtr(0.0),
			wantOnDemand: floatPtr(0.0),
			wantEligible: floatPtr(0.0),
			wantDays:     1,
		},
		{
			name:    "unparseable_covered_field",
			pages:   []*costexplorer.GetSavingsPlansCoverageOutput{oneSPCovPage("not-a-number", "540", "0", nil)},
			wantErr: true,
		},
		{
			name:    "unparseable_on_demand_field",
			pages:   []*costexplorer.GetSavingsPlansCoverageOutput{oneSPCovPage("360", "NaN", "0", nil)},
			wantErr: true,
		},
		{
			name:    "negative_covered_value",
			pages:   []*costexplorer.GetSavingsPlansCoverageOutput{oneSPCovPage("-1.0", "540", "0", nil)},
			wantErr: true,
		},
		{
			name:    "negative_on_demand_value",
			pages:   []*costexplorer.GetSavingsPlansCoverageOutput{oneSPCovPage("360", "-5", "0", nil)},
			wantErr: true,
		},
		{
			name:    "api_error_propagated",
			apiErr:  errors.New("CE throttled"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockSPCE{
				coveragePages: tt.pages,
				coverageErr:   tt.apiErr,
			}
			client := NewClientWithAPI(mock, "us-east-1")
			got, err := client.GetSPCoverageSummary(context.Background(), "", lookback)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantDays, got.Days)
			if tt.wantPct == nil {
				assert.Nil(t, got.CoveragePct, "CoveragePct must be nil only when the eligible total is zero or Days==0")
			} else {
				require.NotNil(t, got.CoveragePct)
				assert.InDelta(t, *tt.wantPct, *got.CoveragePct, 0.001)
			}
			if tt.wantCovered == nil {
				assert.Nil(t, got.CoveredUSDPerHour)
			} else {
				require.NotNil(t, got.CoveredUSDPerHour)
				assert.InDelta(t, *tt.wantCovered, *got.CoveredUSDPerHour, 0.0001)
			}
			if tt.wantOnDemand == nil {
				assert.Nil(t, got.OnDemandUSDPerHour)
			} else {
				require.NotNil(t, got.OnDemandUSDPerHour)
				assert.InDelta(t, *tt.wantOnDemand, *got.OnDemandUSDPerHour, 0.0001)
			}
			if tt.wantEligible == nil {
				assert.Nil(t, got.EligibleUSDPerHour)
			} else {
				require.NotNil(t, got.EligibleUSDPerHour)
				assert.InDelta(t, *tt.wantEligible, *got.EligibleUSDPerHour, 0.0001)
			}
		})
	}
}

// TestGetSPCoverageSummary_NoRegionMeansNilFilter asserts that with an empty
// region the coverage request carries NO Filter at all. Coverage's Filter
// contract supports only LINKED_ACCOUNT, REGION, SERVICE, and INSTANCE_FAMILY
// (SAVINGS_PLANS_TYPE is utilization-only and would draw a
// ValidationException), so with no region there is nothing to filter on.
func TestGetSPCoverageSummary_NoRegionMeansNilFilter(t *testing.T) {
	mock := &mockSPCE{}
	client := NewClientWithAPI(mock, "us-east-1")
	_, err := client.GetSPCoverageSummary(context.Background(), "", 30)
	require.NoError(t, err)
	require.Len(t, mock.coverageInputs, 1, "exactly one CE call expected")
	in := mock.coverageInputs[0]
	assert.Nil(t, in.Filter, "empty region must send no Filter at all")
	assert.Equal(t, []string{spCoverageMetricSpendCovered}, in.Metrics,
		"Metrics is required by the API; its sole valid value must always be sent")
	// Request-shape assertions: daily granularity and a date-only window
	// with start strictly before end (ISO date strings compare correctly
	// as strings).
	assert.Equal(t, types.GranularityDaily, in.Granularity)
	require.NotNil(t, in.TimePeriod)
	start, end := aws.ToString(in.TimePeriod.Start), aws.ToString(in.TimePeriod.End)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, start, "TimePeriod.Start must be date-only")
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, end, "TimePeriod.End must be date-only")
	assert.Less(t, start, end, "TimePeriod.Start must precede End")
}

// TestGetSPCoverageSummary_PctMatchesCEReportedPct cross-checks our
// covered/(covered+onDemand) arithmetic against the CoveragePercentage that
// CE itself reports for the same figures. Guards the denominator choice:
// with covered=360 and OnDemandCost=540 (the UNCOVERED spend), CE reports
// 40%, and covered/onDemand (the old bug) would yield 66.67%.
func TestGetSPCoverageSummary_PctMatchesCEReportedPct(t *testing.T) {
	const ceReportedPct = "40.00"
	mock := &mockSPCE{
		coveragePages: []*costexplorer.GetSavingsPlansCoverageOutput{
			oneSPCovPage("360", "540", ceReportedPct, nil),
		},
	}
	client := NewClientWithAPI(mock, "us-east-1")
	got, err := client.GetSPCoverageSummary(context.Background(), "", 30)
	require.NoError(t, err)
	require.NotNil(t, got.CoveragePct)
	wantPct, parseErr := strconv.ParseFloat(ceReportedPct, 64)
	require.NoError(t, parseErr)
	assert.InDelta(t, wantPct, *got.CoveragePct, 0.01,
		"computed covered/(covered+onDemand) must agree with CE's own CoveragePercentage")
}

// TestGetSPCoverageSummary_PaginationCap asserts the NextToken loop stops
// with a diagnostic error at maxSPCoveragePages instead of spinning forever
// on an API that keeps returning tokens (mirrors the issue #692 guard).
func TestGetSPCoverageSummary_PaginationCap(t *testing.T) {
	mock := &mockSPCE{coverageAlwaysToken: true}
	client := NewClientWithAPI(mock, "us-east-1")
	_, err := client.GetSPCoverageSummary(context.Background(), "", 30)
	require.Error(t, err, "endless-token pagination must fail loud, not spin")
	assert.Contains(t, err.Error(), "pagination cap")
	assert.Len(t, mock.coverageInputs, maxSPCoveragePages, "loop must stop exactly at the cap")
}

// TestGetSPCoverageSummary_RegionOnlyFilter asserts that a non-empty region
// produces a bare REGION dimension filter - no And wrapper, and critically
// NO SAVINGS_PLANS_TYPE dimension, which the coverage API's Filter contract
// does not support and would reject with a ValidationException.
func TestGetSPCoverageSummary_RegionOnlyFilter(t *testing.T) {
	mock := &mockSPCE{}
	client := NewClientWithAPI(mock, "us-east-1")
	_, err := client.GetSPCoverageSummary(context.Background(), "eu-west-2", 30)
	require.NoError(t, err)
	require.Len(t, mock.coverageInputs, 1)
	filter := mock.coverageInputs[0].Filter
	require.NotNil(t, filter, "non-empty region must set a Filter")
	assert.Nil(t, filter.And, "region-only filter must be a bare dimension, not an And composition")
	require.NotNil(t, filter.Dimensions)
	assert.Equal(t, types.DimensionRegion, filter.Dimensions.Key,
		"coverage filter must be REGION-only; SAVINGS_PLANS_TYPE is unsupported by the coverage API")
	assert.Equal(t, []string{"eu-west-2"}, filter.Dimensions.Values)
	assert.Equal(t, []string{spCoverageMetricSpendCovered}, mock.coverageInputs[0].Metrics,
		"Metrics is required by the API; its sole valid value must always be sent")
}

// TestGetSPCoverageSummary_InvalidLookback verifies that lookbackDays <= 0 is
// an explicit error (fail loud; the caller supplies the default window).
func TestGetSPCoverageSummary_InvalidLookback(t *testing.T) {
	client := NewClientWithAPI(&mockSPCE{}, "us-east-1")
	for _, days := range []int{0, -1, -30} {
		_, err := client.GetSPCoverageSummary(context.Background(), "", days)
		require.Errorf(t, err, "expected error for lookbackDays=%d", days)
	}
}

// TestGetSPCoverageSummary_CtxCancelTerminal verifies that a canceled context
// surfaces as an error rather than a silently partial result
// (feedback_ctx_cancel_terminal). The context is canceled before the call, so
// the ctx.Err() guard fires on the FIRST loop iteration, before any CE call.
func TestGetSPCoverageSummary_CtxCancelTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: ctx.Err() is non-nil on the first loop check

	// Provide a NextToken so the loop would attempt further pages if it
	// ignored cancellation.
	token := "page2"
	mock := &mockSPCE{
		coveragePages: []*costexplorer.GetSavingsPlansCoverageOutput{
			{
				SavingsPlansCoverages: []types.SavingsPlansCoverage{
					{
						Coverage: &types.SavingsPlansCoverageData{
							SpendCoveredBySavingsPlans: aws.String("100"),
							OnDemandCost:               aws.String("200"),
						},
					},
				},
				NextToken: &token,
			},
		},
	}
	client := NewClientWithAPI(mock, "us-east-1")
	_, err := client.GetSPCoverageSummary(ctx, "", 30)
	require.Error(t, err, "canceled context must surface an error, not return partial data")
	assert.Empty(t, mock.coverageInputs, "guard fires before the first CE call on a pre-canceled ctx")
}

// TestGetSPUtilization covers the happy path, nil Total/Utilization, unparseable
// fields, negative values, API errors, and invalid lookback.
func TestGetSPUtilization(t *testing.T) {
	// lookbackDays=30 => windowHours=720.
	const lookback = 30
	const windowHours = float64(lookback * 24)

	// CE reports total amounts for the full window; we divide by windowHours.
	// $720 total commitment / 720h = $1.00/hr; $576 used / 720h = $0.80/hr.
	validTotal := &costexplorer.GetSavingsPlansUtilizationOutput{
		Total: &types.SavingsPlansUtilizationAggregates{
			Utilization: &types.SavingsPlansUtilization{
				UtilizationPercentage: aws.String("80.0"),
				UsedCommitment:        aws.String("576"),
				TotalCommitment:       aws.String("720"),
			},
		},
	}

	tests := []struct {
		utilErr     error
		utilOutput  *costexplorer.GetSavingsPlansUtilizationOutput
		wantUtilPct *float64
		wantUsed    *float64
		wantTotal   *float64
		name        string
		wantErr     bool
	}{
		{
			name:        "happy_path",
			utilOutput:  validTotal,
			wantUtilPct: floatPtr(80.0),
			wantUsed:    floatPtr(576.0 / windowHours),
			wantTotal:   floatPtr(720.0 / windowHours),
		},
		{
			name:       "nil_total_returns_empty_summary",
			utilOutput: &costexplorer.GetSavingsPlansUtilizationOutput{Total: nil},
		},
		{
			name: "nil_utilization_returns_empty_summary",
			utilOutput: &costexplorer.GetSavingsPlansUtilizationOutput{
				Total: &types.SavingsPlansUtilizationAggregates{Utilization: nil},
			},
		},
		{
			name: "unparseable_utilization_pct",
			utilOutput: &costexplorer.GetSavingsPlansUtilizationOutput{
				Total: &types.SavingsPlansUtilizationAggregates{
					Utilization: &types.SavingsPlansUtilization{
						UtilizationPercentage: aws.String("not-a-number"),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "negative_used_commitment",
			utilOutput: &costexplorer.GetSavingsPlansUtilizationOutput{
				Total: &types.SavingsPlansUtilizationAggregates{
					Utilization: &types.SavingsPlansUtilization{
						UtilizationPercentage: aws.String("80.0"),
						UsedCommitment:        aws.String("-576"),
					},
				},
			},
			wantErr: true,
		},
		{
			name:    "api_error_propagated",
			utilErr: errors.New("CE throttled"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockSPCE{
				utilOutput: tt.utilOutput,
				utilErr:    tt.utilErr,
			}
			client := NewClientWithAPI(mock, "us-east-1")
			got, err := client.GetSPUtilization(context.Background(), types.SupportedSavingsPlansTypeComputeSp, "", lookback)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantUtilPct == nil {
				assert.Nil(t, got.UtilizationPct)
			} else {
				require.NotNil(t, got.UtilizationPct)
				assert.InDelta(t, *tt.wantUtilPct, *got.UtilizationPct, 0.001)
			}
			if tt.wantUsed == nil {
				assert.Nil(t, got.UsedCommitmentUSDPerHour)
			} else {
				require.NotNil(t, got.UsedCommitmentUSDPerHour)
				assert.InDelta(t, *tt.wantUsed, *got.UsedCommitmentUSDPerHour, 0.0001)
			}
			if tt.wantTotal == nil {
				assert.Nil(t, got.TotalCommitmentUSDPerHour)
			} else {
				require.NotNil(t, got.TotalCommitmentUSDPerHour)
				assert.InDelta(t, *tt.wantTotal, *got.TotalCommitmentUSDPerHour, 0.0001)
			}
		})
	}
}

// TestGetSPUtilization_FilterPerPlanType asserts the CE Filter is a bare
// SAVINGS_PLANS_TYPE dimension carrying the CamelCase DIMENSION-vocabulary
// value (e.g. "ComputeSavingsPlans"), NOT the parameter-enum spelling
// ("COMPUTE_SP") which CE silently matches nothing on, for every plan type
// the SDK defines.
func TestGetSPUtilization_FilterPerPlanType(t *testing.T) {
	for _, planType := range allSPPlanTypes {
		t.Run(string(planType), func(t *testing.T) {
			mock := &mockSPCE{}
			client := NewClientWithAPI(mock, "us-east-1")
			_, err := client.GetSPUtilization(context.Background(), planType, "", 30)
			require.NoError(t, err)
			require.Len(t, mock.utilInputs, 1, "exactly one CE call expected")
			filter := mock.utilInputs[0].Filter
			require.NotNil(t, filter, "Filter must be set")
			assert.Nil(t, filter.And, "no region => bare plan-type dimension, no And wrapper")
			require.NotNil(t, filter.Dimensions)
			assert.Equal(t, types.DimensionSavingsPlansType, filter.Dimensions.Key)
			assert.Equal(t, []string{wantSPDimensionValues[planType]}, filter.Dimensions.Values,
				"dimension value must be the CamelCase vocabulary, not the parameter enum")
			assert.NotEqual(t, []string{string(planType)}, filter.Dimensions.Values,
				"parameter-enum spelling must never reach the dimension filter")
		})
	}
}

// TestGetSPUtilization_RegionFilterANDed asserts that a non-empty region
// produces an And expression combining the SAVINGS_PLANS_TYPE and REGION
// dimensions.
func TestGetSPUtilization_RegionFilterANDed(t *testing.T) {
	mock := &mockSPCE{}
	client := NewClientWithAPI(mock, "us-east-1")
	_, err := client.GetSPUtilization(context.Background(), types.SupportedSavingsPlansTypeComputeSp, "us-west-2", 30)
	require.NoError(t, err)
	require.Len(t, mock.utilInputs, 1)
	in := mock.utilInputs[0]
	filter := in.Filter
	require.NotNil(t, filter)
	assert.Nil(t, filter.Dimensions, "region set => And composition, not a bare dimension")
	require.Len(t, filter.And, 2, "And must combine plan-type and region dimensions")
	require.NotNil(t, filter.And[0].Dimensions)
	assert.Equal(t, types.DimensionSavingsPlansType, filter.And[0].Dimensions.Key)
	assert.Equal(t, []string{"ComputeSavingsPlans"}, filter.And[0].Dimensions.Values,
		"dimension value must be the CamelCase vocabulary, not the COMPUTE_SP parameter enum")
	require.NotNil(t, filter.And[1].Dimensions)
	assert.Equal(t, types.DimensionRegion, filter.And[1].Dimensions.Key)
	assert.Equal(t, []string{"us-west-2"}, filter.And[1].Dimensions.Values)
	// Request-shape assertions: daily granularity and a date-only window
	// with start strictly before end.
	assert.Equal(t, types.GranularityDaily, in.Granularity)
	require.NotNil(t, in.TimePeriod)
	start, end := aws.ToString(in.TimePeriod.Start), aws.ToString(in.TimePeriod.End)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, start, "TimePeriod.Start must be date-only")
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, end, "TimePeriod.End must be date-only")
	assert.Less(t, start, end, "TimePeriod.Start must precede End")
}

// TestGetSPUtilization_InvalidPlanType verifies that empty or unknown
// plan-type values are rejected at the boundary with an explicit error and
// no CE call is made.
func TestGetSPUtilization_InvalidPlanType(t *testing.T) {
	for _, planType := range []types.SupportedSavingsPlansType{"", "NOT_A_PLAN_TYPE"} {
		t.Run(string(planType), func(t *testing.T) {
			mock := &mockSPCE{}
			client := NewClientWithAPI(mock, "us-east-1")
			_, err := client.GetSPUtilization(context.Background(), planType, "", 30)
			require.Errorf(t, err, "expected error for planType %q", planType)
			assert.Empty(t, mock.utilInputs, "invalid plan type must not reach CE")
		})
	}
}

// TestGetSPUtilization_InvalidLookback verifies that lookbackDays <= 0 is an
// explicit error (fail loud; the caller supplies the default window).
func TestGetSPUtilization_InvalidLookback(t *testing.T) {
	client := NewClientWithAPI(&mockSPCE{}, "us-east-1")
	for _, days := range []int{0, -1} {
		_, err := client.GetSPUtilization(context.Background(), types.SupportedSavingsPlansTypeComputeSp, "", days)
		require.Errorf(t, err, "expected error for lookbackDays=%d", days)
	}
}

// TestParseSPFloat covers the strict float parsing contract: valid values pass,
// unparseable strings error, NaN/Inf error, and negative values error.
func TestParseSPFloat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    float64
		wantErr bool
	}{
		{name: "zero", input: "0", want: 0},
		{name: "positive_decimal", input: "66.667", want: 66.667},
		{name: "large_value", input: "1000000.50", want: 1000000.50},
		{name: "empty_string", input: "", wantErr: true},
		{name: "not_a_number", input: "foo", wantErr: true},
		{name: "nan_string", input: "NaN", wantErr: true},
		{name: "inf_string", input: "Inf", wantErr: true},
		{name: "negative_inf_string", input: "-Inf", wantErr: true},
		{name: "negative_value", input: "-1.0", wantErr: true},
		{name: "negative_zero_is_fine", input: "-0", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSPFloat(tt.input, "testField")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.InDelta(t, tt.want, got, 0.0001)
		})
	}
}
