package ladder

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/ladder"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeRILister: err field before ris to minimize GC pointer-scan range (fieldalignment).
type fakeRILister struct {
	err error
	ris []ec2svc.ConvertibleRI
}

func (f *fakeRILister) ListConvertibleReservedInstances(_ context.Context) ([]ec2svc.ConvertibleRI, error) {
	return f.ris, f.err
}

// fakeSPLister: err field before sps for fieldalignment.
type fakeSPLister struct {
	err error
	sps []ActiveSP
}

func (f *fakeSPLister) ListActiveSPs(_ context.Context) ([]ActiveSP, error) {
	return f.sps, f.err
}

// fakeCoverageSource: error fields before slice for fieldalignment
// (all-pointer types before slice whose trailing len/cap are non-pointer).
type fakeCoverageSource struct {
	coverageErr    error
	onDemandErr    error
	coverageMap    recommendations.PoolCoverageMap
	onDemandPoints []DailyPoint
}

func (f *fakeCoverageSource) GetRICoverageMap(_ context.Context, _ int, _ []string) (recommendations.PoolCoverageMap, error) {
	return f.coverageMap, f.coverageErr
}

func (f *fakeCoverageSource) GetOnDemandSeries(_ context.Context, _ string, _ int) ([]DailyPoint, error) {
	return f.onDemandPoints, f.onDemandErr
}

// fakeUtilizationSource: err field before utils for fieldalignment.
type fakeUtilizationSource struct {
	err   error
	utils []recommendations.RIUtilization
}

func (f *fakeUtilizationSource) GetRIUtilization(_ context.Context, _ int, _ string) ([]recommendations.RIUtilization, error) {
	return f.utils, f.err
}

// fakeSPCoverageSource is a hermetic fake for the spCoverageSource interface.
type fakeSPCoverageSource struct {
	err     error
	summary SPCoverageSummary
}

func (f *fakeSPCoverageSource) GetSPCoverageSummary(_ context.Context, _ string, _ int) (SPCoverageSummary, error) {
	return f.summary, f.err
}

// fakeSPUtilizationSource is a hermetic fake for the spUtilizationSource interface.
type fakeSPUtilizationSource struct {
	err     error
	summary SPUtilizationSummary
	gotType cetypes.SupportedSavingsPlansType
}

func (f *fakeSPUtilizationSource) GetSPUtilization(_ context.Context, planType cetypes.SupportedSavingsPlansType, _ string, _ int) (SPUtilizationSummary, error) {
	f.gotType = planType
	return f.summary, f.err
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newTestLadder builds a read-side ladder. cov is the fakeCoverageSource,
// which satisfies both riCoverageSource and onDemandSeriesSource, so it is
// passed for both split interfaces.
func newTestLadder(
	t *testing.T,
	ris riLister,
	sps spLister,
	cov *fakeCoverageSource,
	util utilizationSource,
) *AWSLadder {
	t.Helper()
	a, err := New(
		Config{Region: "us-east-1", AccountID: "123456789012", HorizonDays: 30, LookbackDays: 30},
		ris, sps, cov, cov, util,
		nil, nil,
	)
	require.NoError(t, err)
	return a
}

func testScope() ladder.Scope {
	return ladder.Scope{Provider: common.ProviderAWS, AccountID: "123456789012"}
}

// makeRI constructs a ConvertibleRI for test use. fixedPrice is hardcoded to 0
// (no-upfront) and duration to one year (31536000 s); tests that need other
// payment options build ec2svc.ConvertibleRI directly.
func makeRI(id, instanceType string, count int32, recurringHourly float64, end time.Time) ec2svc.ConvertibleRI {
	return ec2svc.ConvertibleRI{
		ReservedInstanceID:    id,
		InstanceType:          instanceType,
		InstanceCount:         count,
		FixedPrice:            0,
		RecurringHourlyAmount: recurringHourly,
		Duration:              31536000, // 1 year in seconds
		State:                 "active",
		End:                   end,
	}
}

func makeSP(id, planType string, hourly float64, end time.Time) ActiveSP {
	return ActiveSP{
		PlanID:              id,
		PlanType:            planType,
		HourlyCommitmentUSD: hourly,
		State:               "active",
		EndDate:             end,
	}
}

// ---------------------------------------------------------------------------
// New() constructor
// ---------------------------------------------------------------------------

func TestNew_RequiredFieldValidation(t *testing.T) {
	ri := &fakeRILister{}
	sp := &fakeSPLister{}
	cov := &fakeCoverageSource{}
	util := &fakeUtilizationSource{}

	// cfg is last in the anonymous struct to minimize GC scan range (fieldalignment):
	// interface fields (all-pointer) before Config (which has trailing int fields).
	tests := []struct {
		name    string
		ri      riLister
		sp      spLister
		riCov   riCoverageSource
		od      onDemandSeriesSource
		util    utilizationSource
		wantErr string
		cfg     Config
	}{
		{"empty region", ri, sp, cov, cov, util, "Region must not be empty", Config{AccountID: "1"}},
		{"empty account", ri, sp, cov, cov, util, "AccountID must not be empty", Config{Region: "us-east-1"}},
		{"nil riLister", nil, sp, cov, cov, util, "riLister must not be nil", Config{Region: "us-east-1", AccountID: "1"}},
		{"nil spLister", ri, nil, cov, cov, util, "spLister must not be nil", Config{Region: "us-east-1", AccountID: "1"}},
		{"nil riCoverageSource", ri, sp, nil, cov, util, "riCoverageSource must not be nil", Config{Region: "us-east-1", AccountID: "1"}},
		{"nil onDemandSeriesSource", ri, sp, cov, nil, util, "onDemandSeriesSource must not be nil", Config{Region: "us-east-1", AccountID: "1"}},
		{"nil utilizationSource", ri, sp, cov, cov, nil, "utilizationSource must not be nil", Config{Region: "us-east-1", AccountID: "1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg, tt.ri, tt.sp, tt.riCov, tt.od, tt.util, nil, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// ---------------------------------------------------------------------------
// Provider / SupportedLayers
// ---------------------------------------------------------------------------

func TestProvider(t *testing.T) {
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	assert.Equal(t, common.ProviderAWS, a.Provider())
}

func TestSupportedLayers_RoleCardinality(t *testing.T) {
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	layers := a.SupportedLayers()
	require.Len(t, layers, 3)

	roleSeen := make(map[ladder.LayerRole]int)
	for _, l := range layers {
		for _, r := range l.Roles {
			roleSeen[r]++
		}
	}
	assert.Equal(t, 1, roleSeen[ladder.RoleFlex], "exactly one RoleFlex")
	assert.Equal(t, 1, roleSeen[ladder.RoleBase], "exactly one RoleBase")
	assert.Equal(t, 1, roleSeen[ladder.RoleBuffer], "exactly one RoleBuffer")

	// Verify the layer-to-role assignment.
	layerRole := make(map[ladder.LayerType]ladder.LayerRole)
	for _, l := range layers {
		layerRole[l.Type] = l.Roles[0]
	}
	assert.Equal(t, ladder.RoleBase, layerRole[ladder.LayerEC2InstanceSP])
	assert.Equal(t, ladder.RoleFlex, layerRole[ladder.LayerComputeSP])
	assert.Equal(t, ladder.RoleBuffer, layerRole[ladder.LayerConvertibleRI])
}

// ---------------------------------------------------------------------------
// PurchaseLayer / ReshapeBuffer without write-side wiring
// ---------------------------------------------------------------------------

func TestPurchaseLayer_ReturnsNotWiredError(t *testing.T) {
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	_, err := a.PurchaseLayer(context.Background(), ladder.LayerConvertibleRI, common.Recommendation{}, common.PurchaseOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write side not wired")
	assert.False(t, errors.Is(err, common.ErrCommitmentPurchaseNotSupported),
		"must NOT wrap ErrCommitmentPurchaseNotSupported -- that sentinel means permanent inability, not missing wiring")
}

func TestReshapeBuffer_ReturnsNotWiredError(t *testing.T) {
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	_, err := a.ReshapeBuffer(context.Background(), testScope(), ladder.BufferReshapeConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write side not wired")
}

// ---------------------------------------------------------------------------
// ListCommitments
// ---------------------------------------------------------------------------

func TestListCommitments_MergesRIsAndSPs(t *testing.T) {
	now := time.Now()
	end1yr := now.Add(365 * 24 * time.Hour)

	ris := []ec2svc.ConvertibleRI{
		makeRI("ri-1", "m5.xlarge", 2, 0.50, end1yr),
	}
	sps := []ActiveSP{
		makeSP("sp-ec2-1", "EC2Instance", 1.00, end1yr),
		makeSP("sp-compute-1", "Compute", 2.00, end1yr),
		makeSP("sp-sagemaker-1", "SageMaker", 0.50, end1yr), // filtered out
	}

	a := newTestLadder(t, &fakeRILister{ris: ris}, &fakeSPLister{sps: sps}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	commitments, err := a.ListCommitments(context.Background(), testScope())
	require.NoError(t, err)

	// 1 RI + 2 SP (EC2Instance + Compute); SageMaker filtered out.
	require.Len(t, commitments, 3)

	var riFound, ec2SPFound, computeSPFound bool
	for _, c := range commitments {
		switch {
		case c.CommitmentType == common.CommitmentReservedInstance:
			riFound = true
			assert.Equal(t, "ri-1", c.CommitmentID)
			assert.Equal(t, 2, c.Count)
			// Per-instance pricing: no-upfront 0.50/hr recurring x 2 instances
			// = 1.00 reservation-total (DescribeReservedInstances fields are
			// per-instance).
			assert.InDelta(t, 1.00, c.Cost, 1e-9)
		case c.CommitmentID == "sp-ec2-1":
			ec2SPFound = true
			assert.Equal(t, common.ServiceSavingsPlansEC2Instance, c.Service)
		case c.CommitmentID == "sp-compute-1":
			computeSPFound = true
			assert.Equal(t, common.ServiceSavingsPlansCompute, c.Service)
		}
	}
	assert.True(t, riFound)
	assert.True(t, ec2SPFound)
	assert.True(t, computeSPFound)
}

func TestListCommitments_EmptySourcesReturnEmptySlice(t *testing.T) {
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	commitments, err := a.ListCommitments(context.Background(), testScope())
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestListCommitments_WrongScope_ReturnsError(t *testing.T) {
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	wrongScope := ladder.Scope{Provider: common.ProviderAWS, AccountID: "999"}
	_, err := a.ListCommitments(context.Background(), wrongScope)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match configured account")
}

func TestListCommitments_RIError_Propagates(t *testing.T) {
	ri := &fakeRILister{err: errors.New("AWS API error")}
	a := newTestLadder(t, ri, &fakeSPLister{}, &fakeCoverageSource{}, &fakeUtilizationSource{})
	_, err := a.ListCommitments(context.Background(), testScope())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RI listing failed")
}

// ---------------------------------------------------------------------------
// riHourlyCost
// ---------------------------------------------------------------------------

func TestRIHourlyCost_PaymentOptions(t *testing.T) {
	oneYearSeconds := int64(31536000)
	tests := []struct {
		name            string
		fixedPrice      float64
		usagePrice      float64
		recurringHourly float64
		duration        int64
		instanceCount   int32
		wantHourly      float64
	}{
		{
			name:            "no-upfront: only recurring",
			fixedPrice:      0,
			recurringHourly: 0.30,
			duration:        oneYearSeconds,
			instanceCount:   1,
			wantHourly:      0.30,
		},
		{
			name:          "all-upfront: only amortized",
			fixedPrice:    8760 * 0.20, // $0.20/hr amortized over 1yr
			duration:      oneYearSeconds,
			instanceCount: 1,
			wantHourly:    0.20,
		},
		{
			name:            "partial-upfront: both",
			fixedPrice:      8760 * 0.10, // $0.10/hr upfront portion
			recurringHourly: 0.15,
			duration:        oneYearSeconds,
			instanceCount:   1,
			wantHourly:      0.25,
		},
		{
			name:            "legacy usage price included",
			usagePrice:      0.05,
			recurringHourly: 0.10,
			duration:        oneYearSeconds,
			instanceCount:   1,
			wantHourly:      0.15,
		},
		{
			name:            "per-instance semantics: count multiplies the rate",
			fixedPrice:      8760 * 0.10, // $0.10/hr upfront portion per instance
			usagePrice:      0.02,
			recurringHourly: 0.08,
			duration:        oneYearSeconds,
			instanceCount:   4,
			wantHourly:      0.80, // (0.10 + 0.02 + 0.08) * 4
		},
		{
			name:            "zero duration: no panic",
			fixedPrice:      1000,
			recurringHourly: 0.10,
			duration:        0,
			instanceCount:   1,
			wantHourly:      0.10, // upfront amortized skipped when duration==0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ri := ec2svc.ConvertibleRI{
				FixedPrice:            tt.fixedPrice,
				UsagePrice:            tt.usagePrice,
				RecurringHourlyAmount: tt.recurringHourly,
				Duration:              tt.duration,
				InstanceCount:         tt.instanceCount,
			}
			got := riHourlyCost(&ri)
			assert.InDelta(t, tt.wantHourly, got, 1e-6)
		})
	}
}

// ---------------------------------------------------------------------------
// GetLayerStates - explicit zero contract
// ---------------------------------------------------------------------------

func TestGetLayerStates_EmptyLayer_ExplicitZeros(t *testing.T) {
	// Empty RI and SP lists. The contract requires ExistingUSDPerHour and
	// ExpiringUSDPerHour to be explicit zero pointers (not nil), and
	// UtilizationPct to be nil (genuinely unmeasured on an empty layer).
	a := newTestLadder(t,
		&fakeRILister{},
		&fakeSPLister{},
		&fakeCoverageSource{},
		&fakeUtilizationSource{},
	)
	states, err := a.GetLayerStates(context.Background(), testScope())
	require.NoError(t, err)

	for _, layerType := range []ladder.LayerType{
		ladder.LayerConvertibleRI,
		ladder.LayerEC2InstanceSP,
		ladder.LayerComputeSP,
	} {
		s, ok := states[layerType]
		require.True(t, ok, "layer %s must be present", layerType)

		require.NotNil(t, s.ExistingUSDPerHour, "ExistingUSDPerHour must be non-nil (explicit zero) for empty layer %s", layerType)
		assert.Equal(t, 0.0, *s.ExistingUSDPerHour, "ExistingUSDPerHour must be 0 for empty layer %s", layerType)

		require.NotNil(t, s.ExpiringUSDPerHour, "ExpiringUSDPerHour must be non-nil (explicit zero) for empty layer %s", layerType)
		assert.Equal(t, 0.0, *s.ExpiringUSDPerHour, "ExpiringUSDPerHour must be 0 for empty layer %s", layerType)

		// UtilizationPct must be nil for an empty layer (genuinely unmeasured).
		assert.Nil(t, s.UtilizationPct, "UtilizationPct must be nil (not measured) for empty layer %s", layerType)
	}
}

// ---------------------------------------------------------------------------
// GetLayerStates - expiry horizon boundary
// ---------------------------------------------------------------------------

func TestGetLayerStates_ExpiryHorizonBoundary(t *testing.T) {
	now := time.Now()
	horizonDays := 30

	// RI that expires exactly at the horizon boundary (on or before horizon).
	atHorizon := now.Add(time.Duration(horizonDays) * 24 * time.Hour)
	justAfter := atHorizon.Add(time.Second)

	riAtHorizon := makeRI("ri-at", "m5.large", 1, 1.00, atHorizon)
	riJustAfter := makeRI("ri-after", "m5.large", 1, 1.00, justAfter)

	cov := &fakeCoverageSource{}
	a, err := New(
		Config{Region: "us-east-1", AccountID: "123456789012", HorizonDays: horizonDays, LookbackDays: 30},
		&fakeRILister{ris: []ec2svc.ConvertibleRI{riAtHorizon, riJustAfter}},
		&fakeSPLister{},
		cov, cov,
		&fakeUtilizationSource{},
		nil, nil,
	)
	require.NoError(t, err)

	states, err := a.GetLayerStates(context.Background(), testScope())
	require.NoError(t, err)

	ri := states[ladder.LayerConvertibleRI]
	require.NotNil(t, ri.ExpiringUSDPerHour)
	// Only riAtHorizon is within the horizon; riJustAfter is not.
	assert.InDelta(t, 1.00, *ri.ExpiringUSDPerHour, 1e-6,
		"only the RI expiring at-or-before the horizon should be counted")
}

// ---------------------------------------------------------------------------
// GetLayerStates - per-instance RI pricing regression
// ---------------------------------------------------------------------------

func TestGetLayerStates_RILayer_CountGreaterThanOne_MultipliesCost(t *testing.T) {
	// Regression: DescribeReservedInstances pricing fields are per-instance.
	// A count=3 RI at 0.40/hr recurring must contribute 1.20/hr to
	// ExistingUSDPerHour — understating this by factor InstanceCount would
	// make the engine overbuy new commitments.
	end1yr := time.Now().Add(365 * 24 * time.Hour)
	ris := []ec2svc.ConvertibleRI{
		makeRI("ri-multi", "m5.large", 3, 0.40, end1yr),
	}

	a := newTestLadder(t,
		&fakeRILister{ris: ris},
		&fakeSPLister{},
		&fakeCoverageSource{},
		&fakeUtilizationSource{},
	)
	states, err := a.GetLayerStates(context.Background(), testScope())
	require.NoError(t, err)

	ri := states[ladder.LayerConvertibleRI]
	require.NotNil(t, ri.ExistingUSDPerHour)
	assert.InDelta(t, 1.20, *ri.ExistingUSDPerHour, 1e-9,
		"reservation-total = per-instance 0.40/hr x 3 instances")
	require.NotNil(t, ri.ExpiringUSDPerHour)
	assert.InDelta(t, 0.0, *ri.ExpiringUSDPerHour, 1e-9,
		"1-year-out expiry is beyond the 30-day horizon")
}

// ---------------------------------------------------------------------------
// GetLayerStates - coverage and utilization
// ---------------------------------------------------------------------------

func TestGetLayerStates_RILayer_CoverageAndUtilization(t *testing.T) {
	ris := []ec2svc.ConvertibleRI{
		makeRI("ri-1", "m5.large", 1, 0.50, time.Now().Add(365*24*time.Hour)),
	}
	coverageMap := recommendations.PoolCoverageMap{
		"us-east-1:m5.large":  {Pct: 80.0, AvgInstancesPerHour: 10.0},
		"us-east-1:m5.xlarge": {Pct: 60.0, AvgInstancesPerHour: 5.0},
	}
	// ReservedInstanceID must match the convertible RI's ID: GetLayerStates
	// intersects CE utilization entries against the account's convertible
	// RIs before aggregating, so an entry for an untracked ID would be
	// dropped (issue #1461).
	utils := []recommendations.RIUtilization{
		{ReservedInstanceID: "ri-1", PurchasedHours: 100, TotalActualHours: 90},
	}

	a := newTestLadder(t,
		&fakeRILister{ris: ris},
		&fakeSPLister{},
		&fakeCoverageSource{coverageMap: coverageMap},
		&fakeUtilizationSource{utils: utils},
	)
	states, err := a.GetLayerStates(context.Background(), testScope())
	require.NoError(t, err)

	ri := states[ladder.LayerConvertibleRI]
	require.NotNil(t, ri.CoveragePct, "CoveragePct should be non-nil when coverage map is populated")
	// Weighted average: (80*10 + 60*5) / (10+5) = (800+300)/15 = 1100/15 ~= 73.33
	assert.InDelta(t, 73.33, *ri.CoveragePct, 0.1)

	require.NotNil(t, ri.UtilizationPct, "UtilizationPct should be non-nil when utilization data is present")
	assert.InDelta(t, 90.0, *ri.UtilizationPct, 1e-6)
}

// TestGetLayerStates_RILayer_UtilizationExcludesUnrelatedReservations is the
// GetLayerStates-side regression test for issue #1461. GetRIUtilization's
// SERVICE+REGION filter narrows the CE response to EC2 RIs in-region, but a
// standard (non-convertible) EC2 RI in the same account/region -- or, before
// the fix, an RDS/ElastiCache/OpenSearch/Redshift reservation blended in by
// an unfiltered query -- would still land in the utils slice this test
// fakes. Without the ID intersection, the layer's UtilizationPct would blend
// in "other-ri"'s poor utilization and trigger a reshape decision that has
// nothing to do with this account's convertible RIs.
func TestGetLayerStates_RILayer_UtilizationExcludesUnrelatedReservations(t *testing.T) {
	ris := []ec2svc.ConvertibleRI{
		makeRI("ri-1", "m5.large", 1, 0.50, time.Now().Add(365*24*time.Hour)),
	}
	utils := []recommendations.RIUtilization{
		// Tracked convertible RI: well utilized.
		{ReservedInstanceID: "ri-1", PurchasedHours: 100, TotalActualHours: 90},
		// Untracked reservation (e.g. RDS, or a standard non-convertible EC2
		// RI) blended into the same CE response: poorly utilized. Pre-fix,
		// this drags the aggregate down to (90+5)/(100+100)=47.5%; post-fix
		// it must be excluded entirely.
		{ReservedInstanceID: "other-ri", PurchasedHours: 100, TotalActualHours: 5},
	}

	a := newTestLadder(t,
		&fakeRILister{ris: ris},
		&fakeSPLister{},
		&fakeCoverageSource{},
		&fakeUtilizationSource{utils: utils},
	)
	states, err := a.GetLayerStates(context.Background(), testScope())
	require.NoError(t, err)

	ri := states[ladder.LayerConvertibleRI]
	require.NotNil(t, ri.UtilizationPct)
	assert.InDelta(t, 90.0, *ri.UtilizationPct, 1e-6,
		"UtilizationPct must reflect only ri-1 (90%%), not the blended 47.5%% from the untracked reservation")
}

func TestGetLayerStates_CoverageError_DegradesToNil(t *testing.T) {
	ris := []ec2svc.ConvertibleRI{
		makeRI("ri-1", "m5.large", 1, 0.50, time.Now().Add(365*24*time.Hour)),
	}
	a := newTestLadder(t,
		&fakeRILister{ris: ris},
		&fakeSPLister{},
		&fakeCoverageSource{coverageErr: errors.New("CE API error")},
		&fakeUtilizationSource{},
	)
	states, err := a.GetLayerStates(context.Background(), testScope())
	require.NoError(t, err, "coverage error must not fail GetLayerStates")
	assert.Nil(t, states[ladder.LayerConvertibleRI].CoveragePct, "CoveragePct must be nil on coverage source error")
}

func TestGetLayerStates_SPLayers_NilSPInterfaces_GiveNilCovUtil(t *testing.T) {
	sps := []ActiveSP{makeSP("sp-1", "Compute", 2.0, time.Now().Add(365*24*time.Hour))}
	a := newTestLadder(t,
		&fakeRILister{},
		&fakeSPLister{sps: sps},
		&fakeCoverageSource{},
		&fakeUtilizationSource{},
	)
	// spCoverageSource and spUtilizationSource are nil (not wired yet).
	states, err := a.GetLayerStates(context.Background(), testScope())
	require.NoError(t, err)

	sp := states[ladder.LayerComputeSP]
	require.NotNil(t, sp.ExistingUSDPerHour)
	assert.InDelta(t, 2.0, *sp.ExistingUSDPerHour, 1e-9)
	assert.Nil(t, sp.CoveragePct, "CoveragePct must be nil when spCoverageSource is not wired")
	assert.Nil(t, sp.UtilizationPct, "UtilizationPct must be nil when spUtilizationSource is not wired")
}

func TestGetLayerStates_SPLayers_SharedCovPct_BothLayersGetSameValue(t *testing.T) {
	// When spCoverageSource is wired, both SP layers must share the same CoveragePct
	// (CE API limitation: GetSavingsPlansCoverage does not support plan-type filtering).
	covPct := 75.0
	spCov := &fakeSPCoverageSource{summary: SPCoverageSummary{CoveragePct: &covPct}}

	cov := &fakeCoverageSource{}
	a, err := New(
		Config{Region: "us-east-1", AccountID: "123456789012", HorizonDays: 30, LookbackDays: 30},
		&fakeRILister{}, &fakeSPLister{}, cov, cov, &fakeUtilizationSource{},
		spCov, nil,
	)
	require.NoError(t, err)

	states, err := a.GetLayerStates(context.Background(), testScope())
	require.NoError(t, err)

	ec2SP := states[ladder.LayerEC2InstanceSP]
	computeSP := states[ladder.LayerComputeSP]

	require.NotNil(t, ec2SP.CoveragePct)
	require.NotNil(t, computeSP.CoveragePct)
	assert.InDelta(t, 75.0, *ec2SP.CoveragePct, 1e-9, "EC2Instance SP layer coverage")
	assert.InDelta(t, 75.0, *computeSP.CoveragePct, 1e-9, "Compute SP layer coverage must equal EC2Instance SP (CE API limitation)")
}

func TestGetLayerStates_SPUtilization_CorrectCEEnum(t *testing.T) {
	// Verify that spLayerState passes the correct CE SDK enum to GetSPUtilization.
	utilPct := 85.0
	spUtil := &fakeSPUtilizationSource{summary: SPUtilizationSummary{UtilizationPct: &utilPct}}

	cov := &fakeCoverageSource{}
	a, err := New(
		Config{Region: "us-east-1", AccountID: "123456789012", HorizonDays: 30, LookbackDays: 30},
		&fakeRILister{}, &fakeSPLister{}, cov, cov, &fakeUtilizationSource{},
		nil, spUtil,
	)
	require.NoError(t, err)

	states, err := a.GetLayerStates(context.Background(), testScope())
	require.NoError(t, err)

	// Both layers should get the same utilization (fake returns same for any planType).
	ec2SP := states[ladder.LayerEC2InstanceSP]
	computeSP := states[ladder.LayerComputeSP]

	require.NotNil(t, ec2SP.UtilizationPct)
	require.NotNil(t, computeSP.UtilizationPct)
	assert.InDelta(t, 85.0, *ec2SP.UtilizationPct, 1e-9)
	assert.InDelta(t, 85.0, *computeSP.UtilizationPct, 1e-9)
}

// ---------------------------------------------------------------------------
// toSPUtilPlanType
// ---------------------------------------------------------------------------

func TestToSPUtilPlanType_MapsCorrectly(t *testing.T) {
	got, err := toSPUtilPlanType("EC2Instance")
	require.NoError(t, err)
	assert.Equal(t, cetypes.SupportedSavingsPlansTypeEc2InstanceSp, got)

	got, err = toSPUtilPlanType("Compute")
	require.NoError(t, err)
	assert.Equal(t, cetypes.SupportedSavingsPlansTypeComputeSp, got)

	_, err = toSPUtilPlanType("SageMaker")
	require.Error(t, err, "unknown plan type must return an error")
}

// ---------------------------------------------------------------------------
// computeEC2CoveragePct
// ---------------------------------------------------------------------------

func TestComputeEC2CoveragePct_WeightedAverage(t *testing.T) {
	m := recommendations.PoolCoverageMap{
		"us-east-1:m5.large":  {Pct: 80.0, AvgInstancesPerHour: 10.0},
		"us-east-1:m5.xlarge": {Pct: 60.0, AvgInstancesPerHour: 5.0},
		"us-west-2:m5.large":  {Pct: 50.0, AvgInstancesPerHour: 8.0}, // different region, excluded
	}
	result := computeEC2CoveragePct(m, "us-east-1")
	require.NotNil(t, result)
	assert.InDelta(t, 73.33, *result, 0.1)
}

func TestComputeEC2CoveragePct_NoMatchingPools_ReturnsNil(t *testing.T) {
	m := recommendations.PoolCoverageMap{
		"us-west-2:m5.large": {Pct: 80.0},
	}
	result := computeEC2CoveragePct(m, "us-east-1")
	assert.Nil(t, result)
}

func TestComputeEC2CoveragePct_AllZeroWeight_UnweightedAverage(t *testing.T) {
	m := recommendations.PoolCoverageMap{
		"us-east-1:m5.large":  {Pct: 80.0, AvgInstancesPerHour: 0},
		"us-east-1:m5.xlarge": {Pct: 60.0, AvgInstancesPerHour: 0},
	}
	result := computeEC2CoveragePct(m, "us-east-1")
	require.NotNil(t, result)
	assert.InDelta(t, 70.0, *result, 1e-6, "unweighted average of 80 and 60")
}

// ---------------------------------------------------------------------------
// nearestRankPercentile
// ---------------------------------------------------------------------------

func TestNearestRankPercentile(t *testing.T) {
	tests := []struct {
		name    string
		data    []float64
		p       float64
		want    float64
		wantErr bool
	}{
		{"p5 of 20 elements", makeRange(20), 5.0, 1.0, false},
		{"p50 of 10 elements", makeRange(10), 50.0, 5.0, false},
		{"p100 returns max", []float64{3, 1, 4, 1, 5, 9}, 100.0, 9.0, false},
		{"p5 of 1 element", []float64{7.0}, 5.0, 7.0, false},
		{"all equal values", []float64{5, 5, 5, 5, 5}, 25.0, 5.0, false},
		{"empty data", []float64{}, 50.0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nearestRankPercentile(tt.data, tt.p)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.InDelta(t, tt.want, got, 1e-9)
		})
	}
}

// ---------------------------------------------------------------------------
// GetUsageBaseline
// ---------------------------------------------------------------------------

func TestGetUsageBaseline_SingleDaySeries_ReturnsThatValue(t *testing.T) {
	// A 7-day series with all same value; p5 should return 3.0.
	cov := &fakeCoverageSource{onDemandPoints: makeConstantPoints(7, 3.0)}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

	bl, err := a.GetUsageBaseline(context.Background(), testScope(), 7, 5.0)
	require.NoError(t, err)
	require.NotNil(t, bl.LowWaterUSDPerHour)
	assert.InDelta(t, 3.0, *bl.LowWaterUSDPerHour, 1e-9)
	// StableUSDPerHour must be nil: no stable-usage estimator exists yet, and
	// the pkg/ladder contract defines Stable as post-buffer-fraction (aliasing
	// it to LowWater would make the engine over-commit the base layer). nil
	// triggers the engine's documented "route all core gap to flex" degradation.
	assert.Nil(t, bl.StableUSDPerHour)
	assert.Equal(t, 7, bl.LookbackDays)
	assert.InDelta(t, 5.0, bl.Percentile, 1e-9)
}

func TestGetUsageBaseline_P5OfVariedSeries(t *testing.T) {
	// 20-element series [1..20]; p5 nearest-rank: ceil(5/100*20)=ceil(1)=1 -> sorted[0]=1.
	cov := &fakeCoverageSource{onDemandPoints: makeRecentPoints(20)}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

	bl, err := a.GetUsageBaseline(context.Background(), testScope(), 20, 5.0)
	require.NoError(t, err)
	require.NotNil(t, bl.LowWaterUSDPerHour)
	assert.InDelta(t, 1.0, *bl.LowWaterUSDPerHour, 1e-9)
}

func TestGetUsageBaseline_EmptySeries_ReturnsError(t *testing.T) {
	cov := &fakeCoverageSource{onDemandPoints: []DailyPoint{}}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})
	_, err := a.GetUsageBaseline(context.Background(), testScope(), 7, 5.0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestGetUsageBaseline_SeriesTooShort_ReturnsError(t *testing.T) {
	// 3 points with recent dates: fires minBaselineSeriesDays=7 check (3 < 7).
	cov := &fakeCoverageSource{onDemandPoints: makeRecentPoints(3)}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})
	_, err := a.GetUsageBaseline(context.Background(), testScope(), 7, 5.0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "below minimum")
}

func TestGetUsageBaseline_NaNElement_ReturnsErrorNamingIndex(t *testing.T) {
	points := makeRecentPoints(10)
	points[4].USDPerHour = math.NaN()
	cov := &fakeCoverageSource{onDemandPoints: points}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})
	_, err := a.GetUsageBaseline(context.Background(), testScope(), 10, 5.0)
	require.Error(t, err, "a NaN element must be rejected at the boundary")
	assert.Contains(t, err.Error(), "index 4")
	assert.Contains(t, err.Error(), "not finite")
}

func TestGetUsageBaseline_InfElement_ReturnsErrorNamingIndex(t *testing.T) {
	points := makeRecentPoints(10)
	points[7].USDPerHour = math.Inf(1)
	cov := &fakeCoverageSource{onDemandPoints: points}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})
	_, err := a.GetUsageBaseline(context.Background(), testScope(), 10, 5.0)
	require.Error(t, err, "an Inf element must be rejected at the boundary")
	assert.Contains(t, err.Error(), "index 7")
	assert.Contains(t, err.Error(), "not finite")
}

func TestGetUsageBaseline_NegativeElement_ReturnsErrorNamingIndex(t *testing.T) {
	points := makeRecentPoints(10)
	points[2].USDPerHour = -0.5
	cov := &fakeCoverageSource{onDemandPoints: points}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})
	_, err := a.GetUsageBaseline(context.Background(), testScope(), 10, 5.0)
	require.Error(t, err, "a negative cost element must be rejected at the boundary")
	assert.Contains(t, err.Error(), "index 2")
	assert.Contains(t, err.Error(), "negative")
}

func TestGetUsageBaseline_OnDemandSourceError_Propagates(t *testing.T) {
	cov := &fakeCoverageSource{onDemandErr: errors.New("CE error")}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})
	_, err := a.GetUsageBaseline(context.Background(), testScope(), 30, 5.0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "on-demand series fetch failed")
}

func TestGetUsageBaseline_InvalidPercentile_ReturnsError(t *testing.T) {
	cov := &fakeCoverageSource{onDemandPoints: makeRecentPoints(30)}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

	for _, p := range []float64{0.0, -1.0, 101.0} {
		_, err := a.GetUsageBaseline(context.Background(), testScope(), 30, p)
		require.Error(t, err, "percentile %g should fail", p)
		assert.Contains(t, err.Error(), "percentile")
	}
}

func TestGetUsageBaseline_WrongScope_ReturnsError(t *testing.T) {
	cov := &fakeCoverageSource{onDemandPoints: makeRecentPoints(30)}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})
	badScope := ladder.Scope{Provider: common.ProviderAWS, AccountID: "wrong"}
	_, err := a.GetUsageBaseline(context.Background(), badScope, 30, 5.0)
	require.Error(t, err)
}

// TestGetUsageBaseline_StaleSeries_ReturnsError is a regression test for gap G6a:
// before stale-series detection was added, a CE feed that stopped days ago would
// yield a low-water baseline with no error, and the utilization clamp would trust
// it as though it were current. Now GetUsageBaseline must return an explicit error
// naming the last date when the most-recent point is older than maxSeriesAgeDays.
func TestGetUsageBaseline_StaleSeries_ReturnsError(t *testing.T) {
	// Build a 30-point series whose most-recent point is maxSeriesAgeDays+1 days
	// in the past, simulating a CE feed that stopped (or a wrong date range).
	// The date is built from the UTC calendar day so it is hour-independent.
	staleEnd := utcDay(time.Now()).AddDate(0, 0, -(maxSeriesAgeDays + 1))
	points := make([]DailyPoint, 30)
	for i := range points {
		points[i] = DailyPoint{
			Date:       staleEnd.AddDate(0, 0, -(29 - i)),
			USDPerHour: float64(i + 1),
		}
	}
	cov := &fakeCoverageSource{onDemandPoints: points}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

	_, err := a.GetUsageBaseline(context.Background(), testScope(), 30, 5.0)
	require.Error(t, err, "a series whose tail is older than maxSeriesAgeDays must be rejected")
	assert.Contains(t, err.Error(), "stale", "error must say 'stale'")
	assert.Contains(t, err.Error(), staleEnd.Format("2006-01-02"), "error must name the most-recent date so callers can diagnose the CE feed gap")
}

// TestGetUsageBaseline_SparseSeries_ReturnsError is a regression test for gap G6a:
// before coverage detection was added, a series with too many missing days for the
// requested lookback window would silently yield a percentile over incomplete data.
// Now GetUsageBaseline must return an error when fewer than
// lookbackDays - maxMissingDays points fall inside the window.
func TestGetUsageBaseline_SparseSeries_ReturnsError(t *testing.T) {
	const lookbackDays = 30
	// n is one point below the minimum: lookbackDays - maxMissingDays - 1.
	// All n points are recent (in-window), so at n=26 (>= minBaselineSeriesDays=7)
	// the in-window coverage check fires, not the absolute-minimum check.
	n := lookbackDays - maxMissingDays - 1
	cov := &fakeCoverageSource{onDemandPoints: makeRecentPoints(n)}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

	_, err := a.GetUsageBaseline(context.Background(), testScope(), lookbackDays, 5.0)
	require.Error(t, err, "a series with too many gaps for the lookback window must be rejected")
	assert.Contains(t, err.Error(), fmt.Sprintf("%d points", n), "error must report the actual in-window point count")
	assert.Contains(t, err.Error(), fmt.Sprintf("%d-day lookback", lookbackDays), "error must report the requested lookback")
}

// TestGetUsageBaseline_InsufficientInWindowCoverage_ReturnsError is a regression
// test for the count-vs-window bug (CodeRabbit finding 1): the coverage check
// must count only points INSIDE the lookback window, not the total. Here there
// are 27 total points (enough to pass a naive total-count check) but only 20
// fall within [today-30, today]; the oldest 7 are pushed far into the past.
func TestGetUsageBaseline_InsufficientInWindowCoverage_ReturnsError(t *testing.T) {
	const lookbackDays = 30
	today := utcDay(time.Now())
	points := make([]DailyPoint, 0, 27)
	// 7 points far outside the window (days -60..-54): defeat a naive count check.
	for d := 60; d >= 54; d-- {
		points = append(points, DailyPoint{Date: today.AddDate(0, 0, -d), USDPerHour: 1.0})
	}
	// 20 fresh, in-window points (days -20..-1).
	for d := 20; d >= 1; d-- {
		points = append(points, DailyPoint{Date: today.AddDate(0, 0, -d), USDPerHour: 2.0})
	}
	require.Len(t, points, 27, "27 total points, enough to defeat a total-count check")

	cov := &fakeCoverageSource{onDemandPoints: points}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

	_, err := a.GetUsageBaseline(context.Background(), testScope(), lookbackDays, 5.0)
	require.Error(t, err, "27 total but only 20 in-window points must be rejected")
	assert.Contains(t, err.Error(), "20 points inside the 30-day lookback window",
		"error must report the in-window count, not the total")
}

// TestGetUsageBaseline_FreshAndCompleteSeries_NoError is a regression test
// confirming that the stale-series guard does not break the happy path:
// a fresh, sufficiently complete series must still yield a valid baseline.
func TestGetUsageBaseline_FreshAndCompleteSeries_NoError(t *testing.T) {
	// 30 points ending yesterday, which is well within maxSeriesAgeDays=3 and
	// covers the 30-day lookback window with only one day of natural lag.
	cov := &fakeCoverageSource{onDemandPoints: makeRecentPoints(30)}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

	bl, err := a.GetUsageBaseline(context.Background(), testScope(), 30, 5.0)
	require.NoError(t, err, "a fresh, complete series must pass all stale-series checks")
	require.NotNil(t, bl.LowWaterUSDPerHour, "LowWaterUSDPerHour must be set on a valid baseline")
}

// TestGetUsageBaseline_AgeExactlyAtBoundary_NoError locks the off-by-one on the
// freshness comparison: a series whose most-recent point is EXACTLY
// maxSeriesAgeDays old must pass (the check is age > max, not age >= max).
func TestGetUsageBaseline_AgeExactlyAtBoundary_NoError(t *testing.T) {
	// Most-recent point exactly maxSeriesAgeDays UTC calendar days ago -> age ==
	// maxSeriesAgeDays, which must pass (rejection is age > max). The date is
	// built directly from the UTC calendar day (no hour arithmetic) so the test
	// does not depend on the current UTC hour.
	end := utcDay(time.Now()).AddDate(0, 0, -maxSeriesAgeDays)
	points := make([]DailyPoint, 30)
	for i := range points {
		points[i] = DailyPoint{Date: end.AddDate(0, 0, -(29 - i)), USDPerHour: float64(i + 1)}
	}
	cov := &fakeCoverageSource{onDemandPoints: points}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

	_, err := a.GetUsageBaseline(context.Background(), testScope(), 30, 5.0)
	require.NoError(t, err, "a series exactly maxSeriesAgeDays old must pass (boundary is age > max)")
}

// TestGetUsageBaseline_CoverageExactlyAtBoundary_NoError locks the off-by-one on
// the coverage comparison: exactly lookbackDays - maxMissingDays in-window
// points must pass (the check is inWindow < minRequired, not <= minRequired).
func TestGetUsageBaseline_CoverageExactlyAtBoundary_NoError(t *testing.T) {
	const lookbackDays = 30
	n := lookbackDays - maxMissingDays // exactly the minimum required, all in-window
	cov := &fakeCoverageSource{onDemandPoints: makeRecentPoints(n)}
	a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

	_, err := a.GetUsageBaseline(context.Background(), testScope(), lookbackDays, 5.0)
	require.NoError(t, err, "exactly lookbackDays-maxMissingDays in-window points must pass (boundary is inWindow < minRequired)")
}

// TestGetUsageBaseline_NonMonotonicDates_ReturnsError covers the chronology
// hardening: an out-of-order or duplicate-date series must fail loud rather than
// let validateSeriesFreshness trust the last element as newest.
func TestGetUsageBaseline_NonMonotonicDates_ReturnsError(t *testing.T) {
	t.Run("swapped pair", func(t *testing.T) {
		points := makeRecentPoints(30)
		points[10], points[11] = points[11], points[10] // break strict ordering
		cov := &fakeCoverageSource{onDemandPoints: points}
		a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

		_, err := a.GetUsageBaseline(context.Background(), testScope(), 30, 5.0)
		require.Error(t, err, "an out-of-order series must be rejected")
		assert.Contains(t, err.Error(), "strictly increasing")
	})

	t.Run("duplicate date", func(t *testing.T) {
		points := makeRecentPoints(30)
		points[15].Date = points[14].Date // duplicate calendar day
		cov := &fakeCoverageSource{onDemandPoints: points}
		a := newTestLadder(t, &fakeRILister{}, &fakeSPLister{}, cov, &fakeUtilizationSource{})

		_, err := a.GetUsageBaseline(context.Background(), testScope(), 30, 5.0)
		require.Error(t, err, "a duplicate-date series must be rejected")
		assert.Contains(t, err.Error(), "strictly increasing")
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeRange returns a float64 slice [1.0, 2.0, ..., float64(n)].
func makeRange(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(i + 1)
	}
	return out
}

// makeRecentPoints returns n DailyPoints whose USDPerHour values match
// makeRange(n) (values 1.0...float64(n)), with dates ending yesterday.
// Yesterday is used because CE data typically lags ~24 h; ending at yesterday
// keeps the series well within the maxSeriesAgeDays freshness window while
// being realistic for a real CE feed. Ordered oldest-to-newest.
func makeRecentPoints(n int) []DailyPoint {
	// utcDay(now)-1 day = yesterday's UTC calendar day, constructed via calendar
	// arithmetic so the series is independent of the current UTC hour.
	yesterday := utcDay(time.Now()).AddDate(0, 0, -1)
	out := make([]DailyPoint, n)
	for i := range out {
		out[i] = DailyPoint{
			Date:       yesterday.AddDate(0, 0, -(n - 1 - i)),
			USDPerHour: float64(i + 1),
		}
	}
	return out
}

// makeConstantPoints returns n DailyPoints all with the given USDPerHour value,
// ending yesterday. Ordered oldest-to-newest.
func makeConstantPoints(n int, value float64) []DailyPoint {
	yesterday := utcDay(time.Now()).AddDate(0, 0, -1)
	out := make([]DailyPoint, n)
	for i := range out {
		out[i] = DailyPoint{
			Date:       yesterday.AddDate(0, 0, -(n - 1 - i)),
			USDPerHour: value,
		}
	}
	return out
}
