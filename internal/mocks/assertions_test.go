package mocks

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// recordingT captures assertion failures without failing the enclosing test, so
// these tests can assert that an assertion helper *fails*.
type recordingT struct {
	failed bool
	// msgs holds the format strings passed to Errorf, so a test can assert
	// which diagnosis fired and not merely that something did.
	msgs []string
}

func (r *recordingT) Logf(string, ...interface{}) {}

func (r *recordingT) Errorf(format string, _ ...interface{}) {
	r.failed = true
	r.msgs = append(r.msgs, format)
}

func (r *recordingT) FailNow() { r.failed = true }

var _ mock.TestingT = (*recordingT)(nil)

// TestAssertNotCalled_SeesDefaultServedCall is the regression test for #1595.
// GetGlobalConfig has no registered expectation, so it serves its hardcoded
// default and never reaches mock.Called. Before the fix, mock.Mock.Calls stayed
// empty and AssertNotCalled reported success even though the call happened.
func TestAssertNotCalled_SeesDefaultServedCall(t *testing.T) {
	m := &MockConfigStore{}
	_, err := m.GetGlobalConfig(context.Background())
	require.NoError(t, err)

	rt := &recordingT{}
	assert.False(t, m.AssertNotCalled(rt, "GetGlobalConfig", mock.Anything),
		"AssertNotCalled must fail: GetGlobalConfig was called")
	assert.True(t, rt.failed, "the failure must be reported to the TestingT")
}

// TestAssertNotCalled_SeesFnOverriddenCall covers the other path that bypasses
// mock.Called: an Fn override.
func TestAssertNotCalled_SeesFnOverriddenCall(t *testing.T) {
	m := &MockConfigStore{
		GetUserEmailByIDFn: func(context.Context, string) (string, error) { return "a@b.c", nil },
	}
	email, err := m.GetUserEmailByID(context.Background(), "user-1")
	require.NoError(t, err)
	require.Equal(t, "a@b.c", email)

	rt := &recordingT{}
	assert.False(t, m.AssertNotCalled(rt, "GetUserEmailByID", mock.Anything, "user-1"),
		"AssertNotCalled must fail: the Fn override served the call")
	assert.True(t, rt.failed)
}

// TestAssertNotCalled_NameOnlyFormMatchesCallWithArguments pins the second way
// these assertions were unfailable: testify diffs an empty expectation against
// the real arguments and counts each as a difference, so the name-only form
// never matched a method that takes parameters.
func TestAssertNotCalled_NameOnlyFormMatchesCallWithArguments(t *testing.T) {
	m := &MockConfigStore{}
	require.NoError(t, m.WithTx(context.Background(), func(pgx.Tx) error { return nil }))

	rt := &recordingT{}
	assert.False(t, m.AssertNotCalled(rt, "WithTx"),
		"the name-only form must fail when the method was called")
	assert.True(t, rt.failed)

	// testify's promoted implementation is the behavior being replaced: it
	// still passes, because WithTx never reached mock.Called and because an
	// empty expectation cannot match a two-argument call.
	//
	// Hoisted to a local rather than written as m.Mock.AssertNotCalled(...):
	// TestNoUnfailableMockAssertions cannot resolve a selector receiver and
	// correctly reports such a site for hand review, which this deliberate
	// call into the broken implementation would otherwise trip forever.
	promoted := &m.Mock
	assert.True(t, promoted.AssertNotCalled(&recordingT{}, "WithTx"))
}

// TestAssertNotCalled_WrongMatcherCountFailsLoudly pins the third way these
// assertions were unfailable, the one that survived the name-only repair.
// TransitionExecutionStatus takes five arguments; an assertion written with
// four matchers reaches Diff, which pads the fifth position with "(Missing)"
// and scores it as a difference. diffs is then non-zero for every recorded
// call, so the assertion matches nothing and can never fire, whatever the code
// under test does. It is a bug in the test, and is now reported as one.
func TestAssertNotCalled_WrongMatcherCountFailsLoudly(t *testing.T) {
	m := &MockConfigStore{}
	m.On("TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&config.PurchaseExecution{}, nil)
	_, err := m.TransitionExecutionStatus(context.Background(), "exec-1", []string{"scheduled"}, "approved", nil)
	require.NoError(t, err)

	rt := &recordingT{}
	assert.False(t, m.AssertNotCalled(rt, "TransitionExecutionStatus",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything),
		"four matchers against a five-argument method must not report success")
	assert.True(t, rt.failed, "the miscount must be reported to the TestingT")
	require.Len(t, rt.msgs, 1)
	assert.Contains(t, rt.msgs[0], "matcher(s)",
		"the failure must diagnose the arity, not the call itself")

	// The two counts that can express an intent both still fire on the call.
	full := &recordingT{}
	assert.False(t, m.AssertNotCalled(full, "TransitionExecutionStatus",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything))
	assert.True(t, full.failed)

	nameOnly := &recordingT{}
	assert.False(t, m.AssertNotCalled(nameOnly, "TransitionExecutionStatus"))
	assert.True(t, nameOnly.failed)
}

// TestAssertCalled_WrongMatcherCountFailsLoudly covers the mirror image. Here
// the miscount would have produced a spurious failure rather than a false pass,
// but the diagnosis is the same and must name the arity rather than dump an
// argument diff the reader cannot act on.
func TestAssertCalled_WrongMatcherCountFailsLoudly(t *testing.T) {
	m := &MockConfigStore{}
	_, err := m.GetPurchasePlan(context.Background(), "plan-a")
	require.NoError(t, err)

	rt := &recordingT{}
	assert.False(t, m.AssertCalled(rt, "GetPurchasePlan", mock.Anything))
	assert.True(t, rt.failed)
	require.Len(t, rt.msgs, 1)
	assert.Contains(t, rt.msgs[0], "matcher(s)",
		"the failure must diagnose the arity, not report the call as missing")
}

// TestMatching_WrongMatcherCountIsInertWithoutACall records the deliberate
// limit of the arity check: it reads the arity off a recorded call, so with no
// call to the method there is nothing to compare against. That is the case
// where the miscount cannot cause a false pass anyway, because every matcher
// count yields the same correct "not called" verdict.
func TestMatching_WrongMatcherCountIsInertWithoutACall(t *testing.T) {
	m := &MockConfigStore{}
	rt := &recordingT{}
	assert.True(t, m.AssertNotCalled(rt, "TransitionExecutionStatus",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything))
	assert.False(t, rt.failed)
}

// TestAssertNotCalled_PassesWhenNotCalled guards against the opposite failure:
// an assertion that always fails is no more useful than one that never does.
func TestAssertNotCalled_PassesWhenNotCalled(t *testing.T) {
	m := &MockConfigStore{}
	rt := &recordingT{}
	assert.True(t, m.AssertNotCalled(rt, "WithTx"))
	assert.True(t, m.AssertNotCalled(rt, "GetGlobalConfig", mock.Anything))
	assert.False(t, rt.failed)
}

// TestAssertNotCalled_DiscriminatesOnArguments confirms argument matching still
// narrows the assertion rather than being ignored.
func TestAssertNotCalled_DiscriminatesOnArguments(t *testing.T) {
	m := &MockConfigStore{}
	_, err := m.GetPurchasePlan(context.Background(), "plan-a")
	require.NoError(t, err)

	rt := &recordingT{}
	assert.True(t, m.AssertNotCalled(rt, "GetPurchasePlan", mock.Anything, "plan-b"),
		"a call with different arguments must not satisfy the matcher")
	assert.False(t, rt.failed)

	assert.False(t, m.AssertNotCalled(rt, "GetPurchasePlan", mock.Anything, "plan-a"))
	assert.True(t, rt.failed)
}

// TestAssertCalled_SeesDefaultServedCall covers the mirror image: a call served
// from a default previously could not satisfy AssertCalled either.
func TestAssertCalled_SeesDefaultServedCall(t *testing.T) {
	m := &MockConfigStore{}
	_, err := m.GetPurchasePlan(context.Background(), "plan-a")
	require.NoError(t, err)

	rt := &recordingT{}
	assert.True(t, m.AssertCalled(rt, "GetPurchasePlan", mock.Anything, "plan-a"))
	assert.False(t, rt.failed)

	assert.False(t, m.AssertCalled(rt, "GetPurchasePlan", mock.Anything, "plan-missing"))
	assert.True(t, rt.failed)
}

// TestAssertNumberOfCalls_CountsDefaultServedCalls checks the count-based form
// reads the same log, and does not double count a call that also reached
// mock.Called via a registered expectation.
func TestAssertNumberOfCalls_CountsDefaultServedCalls(t *testing.T) {
	m := &MockConfigStore{}
	for i := 0; i < 3; i++ {
		_, err := m.GetPurchasePlan(context.Background(), "plan-a")
		require.NoError(t, err)
	}
	rt := &recordingT{}
	assert.True(t, m.AssertNumberOfCalls(rt, "GetPurchasePlan", 3))
	assert.False(t, rt.failed)

	registered := &MockConfigStore{}
	registered.On("GetPurchasePlan", mock.Anything, "plan-a").
		Return(&config.PurchasePlan{ID: "plan-a"}, nil)
	_, err := registered.GetPurchasePlan(context.Background(), "plan-a")
	require.NoError(t, err)
	assert.True(t, registered.AssertNumberOfCalls(&recordingT{}, "GetPurchasePlan", 1),
		"a call that also reached mock.Called must be counted once, not twice")
}

// TestRegisteredExpectationsStillDispatch confirms the recording does not
// disturb the existing default-or-dispatch behavior or AssertExpectations.
func TestRegisteredExpectationsStillDispatch(t *testing.T) {
	m := &MockConfigStore{}
	m.On("GetGlobalConfig", mock.Anything).
		Return(&config.GlobalConfig{}, nil)

	cfg, err := m.GetGlobalConfig(context.Background())
	require.NoError(t, err)
	require.NotNil(t, cfg)
	m.AssertExpectations(t)
	m.AssertCalled(t, "GetGlobalConfig", mock.Anything)
}
