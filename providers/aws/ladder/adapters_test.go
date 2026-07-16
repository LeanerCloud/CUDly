package ladder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	sdksp "github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgladder "github.com/LeanerCloud/CUDly/pkg/ladder"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
)

// testRegion is the ladder region used across adapter tests.
const testRegion = "us-east-1"

// ---------------------------------------------------------------------------
// spListerAdapter helpers
// ---------------------------------------------------------------------------

// mockDescribeSP is a hermetic fake for activeSPListAPI.
// pages[0] is served on the first call (no incoming token).
// tokens[i] is the incoming NextToken that selects pages[i+1]. apiErr is
// returned on every call if non-nil.
type mockDescribeSP struct {
	pages  []*sdksp.DescribeSavingsPlansOutput
	tokens []string // tokens[i] = incoming token that selects pages[i+1]
	apiErr error
	calls  int
}

func (m *mockDescribeSP) DescribeSavingsPlans(
	_ context.Context,
	params *sdksp.DescribeSavingsPlansInput,
	_ ...func(*sdksp.Options),
) (*sdksp.DescribeSavingsPlansOutput, error) {
	m.calls++
	if m.apiErr != nil {
		return nil, m.apiErr
	}
	idx := 0
	incoming := aws.ToString(params.NextToken)
	for i, tok := range m.tokens {
		if tok == incoming {
			idx = i + 1
			break
		}
	}
	if idx >= len(m.pages) {
		return nil, errors.New("unexpected page token in mockDescribeSP")
	}
	return m.pages[idx], nil
}

// makeSPEntry builds a minimal DescribeSavingsPlans response entry.
// region is the SP's bound region ("" for global Compute SPs).
func makeSPEntry(id, planType, commitment, region string, state sptypes.SavingsPlanState) sptypes.SavingsPlan {
	return sptypes.SavingsPlan{
		SavingsPlanId:   aws.String(id),
		SavingsPlanType: sptypes.SavingsPlanType(planType),
		Commitment:      aws.String(commitment),
		Region:          aws.String(region),
		State:           state,
		Start:           aws.String("2025-01-01T00:00:00Z"),
		End:             aws.String("2026-01-01T00:00:00Z"),
	}
}

// capturingSPAPI wraps an activeSPListAPI and records the States filter from
// each DescribeSavingsPlans call. Used to assert the states filter contract.
type capturingSPAPI struct {
	inner     activeSPListAPI
	gotStates *[][]sptypes.SavingsPlanState
}

func (c *capturingSPAPI) DescribeSavingsPlans(
	ctx context.Context,
	params *sdksp.DescribeSavingsPlansInput,
	optFns ...func(*sdksp.Options),
) (*sdksp.DescribeSavingsPlansOutput, error) {
	*c.gotStates = append(*c.gotStates, params.States)
	return c.inner.DescribeSavingsPlans(ctx, params, optFns...)
}

// newSPLister builds a region-scoped spListerAdapter over the given mock.
func newSPLister(api activeSPListAPI) *spListerAdapter {
	return &spListerAdapter{api: api, region: testRegion}
}

// ---------------------------------------------------------------------------
// spListerAdapter unit tests
// ---------------------------------------------------------------------------

func TestSPLister_HappyPath(t *testing.T) {
	sp := makeSPEntry("sp-abc", string(sptypes.SavingsPlanTypeCompute), "1.50", "", sptypes.SavingsPlanStateActive)
	mock := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{SavingsPlans: []sptypes.SavingsPlan{sp}}},
	}
	lister := newSPLister(mock)

	got, err := lister.ListActiveSPs(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "sp-abc", got[0].PlanID)
	assert.Equal(t, string(sptypes.SavingsPlanTypeCompute), got[0].PlanType)
	assert.InDelta(t, 1.50, got[0].HourlyCommitmentUSD, 1e-9)
	assert.Equal(t, string(sptypes.SavingsPlanStateActive), got[0].State)
	assert.Equal(t, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), got[0].StartDate)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), got[0].EndDate)
}

// TestSPLister_StatesFilter verifies that DescribeSavingsPlans is called with
// exactly the typed SDK enums for active + payment-pending. payment-pending
// must be included so a just-purchased SP counts as existing commitment
// immediately (otherwise the next run double-purchases); queued stays
// excluded because a future-dated SP does not cover usage until its start.
func TestSPLister_StatesFilter(t *testing.T) {
	var gotStates [][]sptypes.SavingsPlanState
	inner := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{}},
	}
	capturer := &capturingSPAPI{inner: inner, gotStates: &gotStates}
	lister := newSPLister(capturer)

	_, err := lister.ListActiveSPs(context.Background())
	require.NoError(t, err)
	require.Len(t, gotStates, 1, "exactly one API call on an empty account")
	assert.Equal(t, []sptypes.SavingsPlanState{
		sptypes.SavingsPlanStateActive,
		sptypes.SavingsPlanStatePaymentPending,
	}, gotStates[0])
	assert.NotContains(t, gotStates[0], sptypes.SavingsPlanStateQueued,
		"queued (future-dated) SPs must not be requested")
}

// TestSPLister_PaymentPendingIncluded verifies a payment-pending SP is mapped
// and returned as existing commitment.
func TestSPLister_PaymentPendingIncluded(t *testing.T) {
	sp := makeSPEntry("sp-pending", string(sptypes.SavingsPlanTypeCompute), "3.00", "", sptypes.SavingsPlanStatePaymentPending)
	mock := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{SavingsPlans: []sptypes.SavingsPlan{sp}}},
	}
	lister := newSPLister(mock)

	got, err := lister.ListActiveSPs(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 1, "payment-pending SP must count as existing commitment")
	assert.Equal(t, string(sptypes.SavingsPlanStatePaymentPending), got[0].State)
	assert.InDelta(t, 3.00, got[0].HourlyCommitmentUSD, 1e-9)
}

// TestSPLister_OutOfRegionEC2InstanceExcluded verifies the region-scoping
// contract: an EC2Instance SP bound to another region is excluded (it covers
// that region's usage, and including it would inflate this region's
// ExistingUSDPerHour and under-purchase), while a global Compute SP is
// always included.
func TestSPLister_OutOfRegionEC2InstanceExcluded(t *testing.T) {
	inRegion := makeSPEntry("sp-ec2-local", string(sptypes.SavingsPlanTypeEc2Instance), "1.00", testRegion, sptypes.SavingsPlanStateActive)
	outOfRegion := makeSPEntry("sp-ec2-remote", string(sptypes.SavingsPlanTypeEc2Instance), "2.00", "eu-west-1", sptypes.SavingsPlanStateActive)
	globalCompute := makeSPEntry("sp-compute", string(sptypes.SavingsPlanTypeCompute), "4.00", "", sptypes.SavingsPlanStateActive)
	mock := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{
			SavingsPlans: []sptypes.SavingsPlan{inRegion, outOfRegion, globalCompute},
		}},
	}
	lister := newSPLister(mock)

	got, err := lister.ListActiveSPs(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 2, "out-of-region EC2Instance SP must be excluded")
	ids := []string{got[0].PlanID, got[1].PlanID}
	assert.Contains(t, ids, "sp-ec2-local", "in-region EC2Instance SP kept")
	assert.Contains(t, ids, "sp-compute", "global Compute SP kept in every region")
	assert.NotContains(t, ids, "sp-ec2-remote", "eu-west-1 EC2Instance SP must not leak into us-east-1")
}

// TestSPLister_PaginationExhausted verifies that two pages are fetched and
// merged (regression against issue #692 pattern: truncation at page 1).
func TestSPLister_PaginationExhausted(t *testing.T) {
	sp1 := makeSPEntry("sp-1", string(sptypes.SavingsPlanTypeCompute), "1.00", "", sptypes.SavingsPlanStateActive)
	sp2 := makeSPEntry("sp-2", string(sptypes.SavingsPlanTypeEc2Instance), "2.00", testRegion, sptypes.SavingsPlanStateActive)
	pages := []*sdksp.DescribeSavingsPlansOutput{
		{SavingsPlans: []sptypes.SavingsPlan{sp1}, NextToken: aws.String("tok1")},
		{SavingsPlans: []sptypes.SavingsPlan{sp2}},
	}
	mock := &mockDescribeSP{pages: pages, tokens: []string{"tok1"}}
	lister := newSPLister(mock)

	got, err := lister.ListActiveSPs(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 2, "both pages must be merged")
	assert.Equal(t, "sp-1", got[0].PlanID)
	assert.Equal(t, "sp-2", got[1].PlanID)
	assert.Equal(t, 2, mock.calls, "two API calls expected")
}

// TestSPLister_InvalidCommitmentFails verifies fail-loud on a non-numeric
// Commitment string (feedback_strict_int_parse, feedback_no_silent_fallbacks).
func TestSPLister_InvalidCommitmentFails(t *testing.T) {
	sp := makeSPEntry("sp-bad", string(sptypes.SavingsPlanTypeCompute), "not-a-number", "", sptypes.SavingsPlanStateActive)
	mock := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{SavingsPlans: []sptypes.SavingsPlan{sp}}},
	}
	lister := newSPLister(mock)

	_, err := lister.ListActiveSPs(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse Commitment")
}

// TestSPLister_BadDatesFail verifies fail-loud on missing or unparseable
// Start/End dates: a silently zero EndDate would drop the SP from
// sumExpiringSPHourlyCost and understate expiring commitment.
func TestSPLister_BadDatesFail(t *testing.T) {
	base := func() sptypes.SavingsPlan {
		return makeSPEntry("sp-dates", string(sptypes.SavingsPlanTypeCompute), "1.00", "", sptypes.SavingsPlanStateActive)
	}
	tests := []struct {
		name    string
		mutate  func(*sptypes.SavingsPlan)
		wantMsg string
	}{
		{"nil Start", func(sp *sptypes.SavingsPlan) { sp.Start = nil }, "nil Start date"},
		{"nil End", func(sp *sptypes.SavingsPlan) { sp.End = nil }, "nil End date"},
		{"garbage Start", func(sp *sptypes.SavingsPlan) { sp.Start = aws.String("garbage") }, "cannot parse Start date"},
		{"garbage End", func(sp *sptypes.SavingsPlan) { sp.End = aws.String("garbage") }, "cannot parse End date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := base()
			tt.mutate(&sp)
			mock := &mockDescribeSP{
				pages: []*sdksp.DescribeSavingsPlansOutput{{SavingsPlans: []sptypes.SavingsPlan{sp}}},
			}
			lister := newSPLister(mock)

			_, err := lister.ListActiveSPs(context.Background())

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)
		})
	}
}

// TestSPLister_APIErrorPropagated verifies that a DescribeSavingsPlans error
// is propagated to the caller (no silent swallowing).
func TestSPLister_APIErrorPropagated(t *testing.T) {
	sentinel := errors.New("DescribeSavingsPlans failed")
	mock := &mockDescribeSP{
		pages:  []*sdksp.DescribeSavingsPlansOutput{{}},
		apiErr: sentinel,
	}
	lister := newSPLister(mock)

	_, err := lister.ListActiveSPs(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

// TestSPLister_EmptyResult verifies that an account with no matching SPs
// returns an empty slice without error.
func TestSPLister_EmptyResult(t *testing.T) {
	mock := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{}},
	}
	lister := newSPLister(mock)

	got, err := lister.ListActiveSPs(context.Background())

	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestSPLister_ContextCancelled verifies that a cancelled context terminates
// the listing loop before the first (or any subsequent) API call.
func TestSPLister_ContextCancelled(t *testing.T) {
	pages := []*sdksp.DescribeSavingsPlansOutput{
		{NextToken: aws.String("tok1")},
		{},
	}
	mock := &mockDescribeSP{pages: pages, tokens: []string{"tok1"}}
	lister := newSPLister(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := lister.ListActiveSPs(ctx)
	require.Error(t, err, "cancelled context must produce an error")
}

// TestSPLister_NilIDFails verifies fail-loud on a SP entry with a nil
// SavingsPlanId (un-identifiable entry; feedback_no_silent_fallbacks).
func TestSPLister_NilIDFails(t *testing.T) {
	sp := sptypes.SavingsPlan{
		SavingsPlanId: nil, // nil: cannot identify
		Commitment:    aws.String("1.00"),
	}
	mock := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{SavingsPlans: []sptypes.SavingsPlan{sp}}},
	}
	lister := newSPLister(mock)

	_, err := lister.ListActiveSPs(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil SavingsPlanId")
}

// ---------------------------------------------------------------------------
// Direct mapping tests for the recommendations-backed adapters
// ---------------------------------------------------------------------------

// ladderCEMock implements recommendations.CostExplorerAPI with canned
// responses so the ladder-side adapters can be exercised against a real
// *recommendations.Client without AWS.
type ladderCEMock struct {
	costAndUsage  *costexplorer.GetCostAndUsageOutput
	spCoverage    *costexplorer.GetSavingsPlansCoverageOutput
	spUtilization *costexplorer.GetSavingsPlansUtilizationOutput
}

func (m *ladderCEMock) GetReservationPurchaseRecommendation(
	_ context.Context, _ *costexplorer.GetReservationPurchaseRecommendationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	return &costexplorer.GetReservationPurchaseRecommendationOutput{}, nil
}

func (m *ladderCEMock) GetSavingsPlansPurchaseRecommendation(
	_ context.Context, _ *costexplorer.GetSavingsPlansPurchaseRecommendationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error) {
	return &costexplorer.GetSavingsPlansPurchaseRecommendationOutput{}, nil
}

func (m *ladderCEMock) GetReservationUtilization(
	_ context.Context, _ *costexplorer.GetReservationUtilizationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationUtilizationOutput, error) {
	return &costexplorer.GetReservationUtilizationOutput{}, nil
}

func (m *ladderCEMock) GetReservationCoverage(
	_ context.Context, _ *costexplorer.GetReservationCoverageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetReservationCoverageOutput, error) {
	return &costexplorer.GetReservationCoverageOutput{}, nil
}

func (m *ladderCEMock) GetSavingsPlansCoverage(
	_ context.Context, _ *costexplorer.GetSavingsPlansCoverageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansCoverageOutput, error) {
	if m.spCoverage != nil {
		return m.spCoverage, nil
	}
	return &costexplorer.GetSavingsPlansCoverageOutput{}, nil
}

func (m *ladderCEMock) GetSavingsPlansUtilization(
	_ context.Context, _ *costexplorer.GetSavingsPlansUtilizationInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansUtilizationOutput, error) {
	if m.spUtilization != nil {
		return m.spUtilization, nil
	}
	return &costexplorer.GetSavingsPlansUtilizationOutput{}, nil
}

func (m *ladderCEMock) GetCostAndUsage(
	_ context.Context, _ *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	if m.costAndUsage != nil {
		return m.costAndUsage, nil
	}
	return &costexplorer.GetCostAndUsageOutput{}, nil
}

// ceDailyRow builds one GetCostAndUsage daily row keyed by the CamelCase
// "UnblendedCost" metric name. The literal MUST stay CamelCase: it mirrors
// what real CE echoes back for a GetCostAndUsage request (the enum-derived
// "UNBLENDED_COST" belongs to other CE APIs). If production regressed to the
// SCREAMING_SNAKE lookup, the r.Total lookup would miss this key and the
// mapping test below would fail with a missing-metric error.
func ceDailyRow(dateStr, amount string) cetypes.ResultByTime {
	return cetypes.ResultByTime{
		TimePeriod: &cetypes.DateInterval{Start: aws.String(dateStr), End: aws.String(dateStr)},
		Total: map[string]cetypes.MetricValue{
			"UnblendedCost": {Amount: aws.String(amount)},
		},
	}
}

// TestOnDemandSeriesAdapter_MapsDailyCostToDailyPoint verifies the direct
// []recommendations.DailyCost -> []DailyPoint mapping: dates and values are
// copied index-for-index, oldest-first.
func TestOnDemandSeriesAdapter_MapsDailyCostToDailyPoint(t *testing.T) {
	mock := &ladderCEMock{costAndUsage: &costexplorer.GetCostAndUsageOutput{
		ResultsByTime: []cetypes.ResultByTime{
			ceDailyRow("2026-01-02", "480"), // $20/hr, newer
			ceDailyRow("2026-01-01", "240"), // $10/hr, older
		},
	}}
	adapter := &onDemandSeriesAdapter{client: recommendations.NewClientWithAPI(mock, testRegion)}

	points, err := adapter.GetOnDemandSeries(context.Background(), testRegion, 7)

	require.NoError(t, err)
	require.Len(t, points, 2)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), points[0].Date, "oldest first")
	assert.InDelta(t, 10.0, points[0].USDPerHour, 1e-9)
	assert.Equal(t, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), points[1].Date)
	assert.InDelta(t, 20.0, points[1].USDPerHour, 1e-9)
}

// TestOnDemandSeriesAdapter_PropagatesError verifies errors pass through the
// adapter unchanged (no silent fallback).
func TestOnDemandSeriesAdapter_PropagatesError(t *testing.T) {
	// Empty CE output -> the client errors on the empty series.
	adapter := &onDemandSeriesAdapter{client: recommendations.NewClientWithAPI(&ladderCEMock{}, testRegion)}

	_, err := adapter.GetOnDemandSeries(context.Background(), testRegion, 7)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no on-demand data")
}

// TestSPCoverageAdapter_MapsCoveragePct verifies the direct
// recommendations.SPCoverageSummary -> local SPCoverageSummary mapping with a
// populated CE response: covered $120 + on-demand $120 over 1 day = 50%.
func TestSPCoverageAdapter_MapsCoveragePct(t *testing.T) {
	mock := &ladderCEMock{spCoverage: &costexplorer.GetSavingsPlansCoverageOutput{
		SavingsPlansCoverages: []cetypes.SavingsPlansCoverage{{
			Coverage: &cetypes.SavingsPlansCoverageData{
				SpendCoveredBySavingsPlans: aws.String("120"),
				OnDemandCost:               aws.String("120"),
			},
		}},
	}}
	adapter := &spCoverageAdapter{client: recommendations.NewClientWithAPI(mock, testRegion)}

	got, err := adapter.GetSPCoverageSummary(context.Background(), testRegion, 1)

	require.NoError(t, err)
	require.NotNil(t, got.CoveragePct)
	assert.InDelta(t, 50.0, *got.CoveragePct, 1e-9, "covered/(covered+onDemand) = 120/240 = 50%")
}

// TestSPCoverageAdapter_NilWhenNoData verifies the nil-when-Days==0 contract:
// an empty CE coverage response maps to CoveragePct == nil ("not measured"),
// never a fabricated 0.
func TestSPCoverageAdapter_NilWhenNoData(t *testing.T) {
	adapter := &spCoverageAdapter{client: recommendations.NewClientWithAPI(&ladderCEMock{}, testRegion)}

	got, err := adapter.GetSPCoverageSummary(context.Background(), testRegion, 30)

	require.NoError(t, err)
	assert.Nil(t, got.CoveragePct, "no CE data must map to nil, not 0%")
}

// TestSPUtilizationAdapter_MapsUtilizationPct verifies the direct
// recommendations.SPUtilizationSummary -> local SPUtilizationSummary mapping.
func TestSPUtilizationAdapter_MapsUtilizationPct(t *testing.T) {
	mock := &ladderCEMock{spUtilization: &costexplorer.GetSavingsPlansUtilizationOutput{
		Total: &cetypes.SavingsPlansUtilizationAggregates{
			Utilization: &cetypes.SavingsPlansUtilization{
				UtilizationPercentage: aws.String("85"),
			},
		},
	}}
	adapter := &spUtilizationAdapter{client: recommendations.NewClientWithAPI(mock, testRegion)}

	got, err := adapter.GetSPUtilization(context.Background(), cetypes.SupportedSavingsPlansTypeComputeSp, "", 30)

	require.NoError(t, err)
	require.NotNil(t, got.UtilizationPct)
	assert.InDelta(t, 85.0, *got.UtilizationPct, 1e-9)
}

// TestSPUtilizationAdapter_NilWhenNoData verifies nil propagation on an empty
// CE utilization response.
func TestSPUtilizationAdapter_NilWhenNoData(t *testing.T) {
	adapter := &spUtilizationAdapter{client: recommendations.NewClientWithAPI(&ladderCEMock{}, testRegion)}

	got, err := adapter.GetSPUtilization(context.Background(), cetypes.SupportedSavingsPlansTypeComputeSp, "", 30)

	require.NoError(t, err)
	assert.Nil(t, got.UtilizationPct, "no CE data must map to nil, not 0%")
}

// ---------------------------------------------------------------------------
// Regression test: real spListerAdapter wired into New() must produce
// non-zero ExistingUSDPerHour for an active SP.
// Pre-fix behaviour: noop stub always returned 0.0.
// ---------------------------------------------------------------------------

// TestGetLayerStates_RealSPLister_NonZeroExisting wires a real spListerAdapter
// backed by a hermetic DescribeSavingsPlans mock that returns one active
// Compute SP at $2/hr and asserts that GetLayerStates returns
// ExistingUSDPerHour == 2.0 for LayerComputeSP.
func TestGetLayerStates_RealSPLister_NonZeroExisting(t *testing.T) {
	sp := makeSPEntry("sp-real", string(sptypes.SavingsPlanTypeCompute), "2.00", "", sptypes.SavingsPlanStateActive)
	mockSPAPI := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{SavingsPlans: []sptypes.SavingsPlan{sp}}},
	}

	cov := &fakeCoverageSource{onDemandPoints: makeRecentPoints(7)}
	a, err := New(
		Config{Region: testRegion, AccountID: "123456789012"},
		&fakeRILister{},
		newSPLister(mockSPAPI), // real adapter, not the noop stub
		cov,
		cov,
		&fakeUtilizationSource{},
		nil, // spCoverageSource: not needed for this assertion
		nil, // spUtilizationSource: not needed for this assertion
	)
	require.NoError(t, err)

	states, err := a.GetLayerStates(context.Background(), testScope())

	require.NoError(t, err)
	computeSP, ok := states[pkgladder.LayerComputeSP]
	require.True(t, ok, "LayerComputeSP must be present")
	require.NotNil(t, computeSP.ExistingUSDPerHour,
		"ExistingUSDPerHour must not be nil for a layer with an active SP")
	assert.InDelta(t, 2.0, *computeSP.ExistingUSDPerHour, 1e-9,
		"ExistingUSDPerHour must equal the SP $2/hr commitment (was 0.0 pre-fix)")
}
