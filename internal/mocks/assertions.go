package mocks

import (
	"fmt"
	"strings"
	"sync"

	"github.com/stretchr/testify/mock"
)

// recordedCall is one invocation of a MockConfigStore method, captured whether
// or not the method dispatched through mock.Called.
type recordedCall struct {
	method string
	args   mock.Arguments
}

// callLog captures every MockConfigStore invocation so the assertion helpers
// below can see what the code under test actually did.
//
// testify appends to mock.Mock.Calls from inside mock.Called. A MockConfigStore
// method that serves a hardcoded default -- the isExpected short-circuit for an
// unregistered method, or an Fn override -- returns before reaching it, so the
// invocation never lands in mock.Mock.Calls. Any assertion reading that slice is
// then unfailable for such a method: AssertNotCalled walks a slice the call
// could not have reached, and reports success no matter what happened.
type callLog struct {
	mu    sync.Mutex
	calls []recordedCall
}

func (l *callLog) record(method string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, recordedCall{method: method, args: args})
}

// fatalf reports an error in how a test wrote its assertion, then stops that
// test. mock.TestingT has no Fatalf, so this is Errorf plus FailNow. It marks
// itself a helper inline for the same reason the assertion helpers below do.
func fatalf(t mock.TestingT, format string, args ...interface{}) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	t.Errorf(format, args...)
	t.FailNow()
}

// matching counts the recorded invocations of method whose arguments satisfy
// expected. An empty expected matches every invocation of the method.
//
// testify instead diffs an empty expectation against the real arguments and
// counts each one as a difference, so its name-only form never matches a method
// that takes parameters -- a second, independent way for an assertion to become
// unfailable. Naming the method is the only way to express "not called at all",
// so that is what it means here.
//
// A non-empty expected whose length differs from the method's real arity is a
// third way, and it is a bug in the test rather than a legitimate intent:
// Diff pads the shorter side with "(Missing)" and scores every padded position
// as a difference, so diffs is unconditionally non-zero and the assertion can
// never fire. There is no argument list such an expectation could be asking
// about, so it fails loudly instead of silently matching nothing. The check
// needs a recorded call to read the arity from, which is exactly the case
// where the miscount would otherwise produce a false pass; with no call at
// all, any matcher count yields the same correct verdict.
//
// The second result is false when the matcher count was wrong. A real
// *testing.T never returns from the FailNow inside fatalf, but the callers
// still surface it as a failed assertion so that a TestingT whose FailNow
// returns cannot turn a miscounted assertion back into a silent pass.
func (l *callLog) matching(t mock.TestingT, method string, expected []interface{}) (int, bool) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, c := range l.calls {
		if c.method != method {
			continue
		}
		if len(expected) == 0 {
			n++
			continue
		}
		if len(expected) != len(c.args) {
			fatalf(t, "mock: %s takes %d argument(s) but the assertion passed %d matcher(s). "+
				"A matcher count that differs from the real arity can never match, which would "+
				"make this assertion unfailable. Pass %d matcher(s), or none to mean "+
				"\"not called at all, whatever the arguments\".",
				method, len(c.args), len(expected), len(c.args))
			return 0, false
		}
		if _, diffs := mock.Arguments(expected).Diff(c.args); diffs == 0 {
			n++
		}
	}
	return n, true
}

// summary renders the recorded invocations of method for failure messages.
func (l *callLog) summary(method string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	for _, c := range l.calls {
		if c.method == method {
			fmt.Fprintf(&b, "\n\t%s(%v)", c.method, []interface{}(c.args))
		}
	}
	if b.Len() == 0 {
		return "\n\t(no recorded calls to " + method + ")"
	}
	return b.String()
}

// record captures an invocation. Every MockConfigStore method calls it before
// branching, so the assertion helpers below never depend on whether the method
// reached mock.Called.
func (m *MockConfigStore) record(method string, args ...interface{}) {
	m.callLog.record(method, args...)
}

// AssertCalled asserts that method was invoked with arguments satisfying the
// given matchers. It shadows the promoted testify method so it reads the
// complete call log rather than mock.Mock.Calls; pass no arguments to assert
// the method was called at all.
//
// mock.TestingT has no Helper method, so the assertion helpers type-assert for
// it inline. t.Helper() marks its own caller, so delegating that to a shared
// function would report every failure at that function's line instead of the
// assertion's call site.
func (m *MockConfigStore) AssertCalled(t mock.TestingT, method string, arguments ...interface{}) bool {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	n, ok := m.callLog.matching(t, method, arguments)
	if !ok {
		return false
	}
	if n > 0 {
		return true
	}
	t.Errorf("mock: %s was not called with the expected arguments %v. Recorded calls:%s",
		method, arguments, m.callLog.summary(method))
	return false
}

// AssertNotCalled asserts that method was never invoked with arguments
// satisfying the given matchers. It shadows the promoted testify method so it
// reads the complete call log rather than mock.Mock.Calls; pass no arguments to
// assert the method was not called at all.
func (m *MockConfigStore) AssertNotCalled(t mock.TestingT, method string, arguments ...interface{}) bool {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	n, ok := m.callLog.matching(t, method, arguments)
	if !ok {
		return false
	}
	if n > 0 {
		t.Errorf("mock: %s was called %d time(s) matching %v, expected none. Recorded calls:%s",
			method, n, arguments, m.callLog.summary(method))
		return false
	}
	return true
}

// AssertNumberOfCalls asserts how many times method was invoked, with any
// arguments. It shadows the promoted testify method so it reads the complete
// call log rather than mock.Mock.Calls.
func (m *MockConfigStore) AssertNumberOfCalls(t mock.TestingT, method string, expectedCalls int) bool {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	actual, ok := m.callLog.matching(t, method, nil)
	if !ok {
		return false
	}
	if actual != expectedCalls {
		t.Errorf("mock: expected %d call(s) to %s, got %d. Recorded calls:%s",
			expectedCalls, method, actual, m.callLog.summary(method))
		return false
	}
	return true
}
