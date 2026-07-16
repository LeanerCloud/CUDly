package ladder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdksp "github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgladder "github.com/LeanerCloud/CUDly/pkg/ladder"
)

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
func makeSPEntry(id, planType, commitment string, state sptypes.SavingsPlanState) sptypes.SavingsPlan {
	return sptypes.SavingsPlan{
		SavingsPlanId:   aws.String(id),
		SavingsPlanType: sptypes.SavingsPlanType(planType),
		Commitment:      aws.String(commitment),
		State:           state,
		Start:           aws.String("2025-01-01T00:00:00Z"),
		End:             aws.String("2026-01-01T00:00:00Z"),
	}
}

// capturingSPAPI wraps an activeSPListAPI and records the States filter from
// each DescribeSavingsPlans call. Used to assert active-only filtering.
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

// ---------------------------------------------------------------------------
// spListerAdapter unit tests
// ---------------------------------------------------------------------------

func TestSPLister_HappyPath(t *testing.T) {
	sp := makeSPEntry("sp-abc", string(sptypes.SavingsPlanTypeCompute), "1.50", sptypes.SavingsPlanStateActive)
	mock := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{SavingsPlans: []sptypes.SavingsPlan{sp}}},
	}
	lister := &spListerAdapter{api: mock}

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

// TestSPLister_ActiveOnlyFilter verifies that DescribeSavingsPlans is called
// with the typed SDK enum sptypes.SavingsPlanStateActive, not a string literal.
func TestSPLister_ActiveOnlyFilter(t *testing.T) {
	var gotStates [][]sptypes.SavingsPlanState
	inner := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{}},
	}
	capturer := &capturingSPAPI{inner: inner, gotStates: &gotStates}
	lister := &spListerAdapter{api: capturer}

	_, err := lister.ListActiveSPs(context.Background())
	require.NoError(t, err)
	require.Len(t, gotStates, 1, "exactly one API call on an empty account")
	assert.Equal(t, []sptypes.SavingsPlanState{sptypes.SavingsPlanStateActive}, gotStates[0])
}

// TestSPLister_PaginationExhausted verifies that two pages are fetched and
// merged (regression against issue #692 pattern: truncation at page 1).
func TestSPLister_PaginationExhausted(t *testing.T) {
	sp1 := makeSPEntry("sp-1", string(sptypes.SavingsPlanTypeCompute), "1.00", sptypes.SavingsPlanStateActive)
	sp2 := makeSPEntry("sp-2", string(sptypes.SavingsPlanTypeEc2Instance), "2.00", sptypes.SavingsPlanStateActive)
	pages := []*sdksp.DescribeSavingsPlansOutput{
		{SavingsPlans: []sptypes.SavingsPlan{sp1}, NextToken: aws.String("tok1")},
		{SavingsPlans: []sptypes.SavingsPlan{sp2}},
	}
	mock := &mockDescribeSP{pages: pages, tokens: []string{"tok1"}}
	lister := &spListerAdapter{api: mock}

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
	sp := makeSPEntry("sp-bad", string(sptypes.SavingsPlanTypeCompute), "not-a-number", sptypes.SavingsPlanStateActive)
	mock := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{SavingsPlans: []sptypes.SavingsPlan{sp}}},
	}
	lister := &spListerAdapter{api: mock}

	_, err := lister.ListActiveSPs(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse Commitment")
}

// TestSPLister_APIErrorPropagated verifies that a DescribeSavingsPlans error
// is propagated to the caller (no silent swallowing).
func TestSPLister_APIErrorPropagated(t *testing.T) {
	sentinel := errors.New("DescribeSavingsPlans failed")
	mock := &mockDescribeSP{
		pages:  []*sdksp.DescribeSavingsPlansOutput{{}},
		apiErr: sentinel,
	}
	lister := &spListerAdapter{api: mock}

	_, err := lister.ListActiveSPs(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

// TestSPLister_EmptyResult verifies that an account with no active SPs returns
// an empty (non-nil) slice without error.
func TestSPLister_EmptyResult(t *testing.T) {
	mock := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{}},
	}
	lister := &spListerAdapter{api: mock}

	got, err := lister.ListActiveSPs(context.Background())

	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestSPLister_ContextCancelled verifies that a cancelled context terminates
// the listing loop before the first (or any subsequent) API call.
func TestSPLister_ContextCancelled(t *testing.T) {
	// First page has a NextToken so a second call would occur without context
	// cancellation; cancelling before the first call proves the loop exits.
	pages := []*sdksp.DescribeSavingsPlansOutput{
		{NextToken: aws.String("tok1")},
		{},
	}
	mock := &mockDescribeSP{pages: pages, tokens: []string{"tok1"}}
	lister := &spListerAdapter{api: mock}

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
	lister := &spListerAdapter{api: mock}

	_, err := lister.ListActiveSPs(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil SavingsPlanId")
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
	sp := makeSPEntry("sp-real", string(sptypes.SavingsPlanTypeCompute), "2.00", sptypes.SavingsPlanStateActive)
	mockSPAPI := &mockDescribeSP{
		pages: []*sdksp.DescribeSavingsPlansOutput{{SavingsPlans: []sptypes.SavingsPlan{sp}}},
	}

	cov := &fakeCoverageSource{onDemandPoints: makeRecentPoints(7)}
	a, err := New(
		Config{Region: "us-east-1", AccountID: "123456789012"},
		&fakeRILister{},
		&spListerAdapter{api: mockSPAPI}, // real adapter, not the noop stub
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
