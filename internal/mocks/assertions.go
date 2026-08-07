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

// matching counts the recorded invocations of method whose arguments satisfy
// expected. An empty expected matches every invocation of the method.
//
// testify instead diffs an empty expectation against the real arguments and
// counts each one as a difference, so its name-only form never matches a method
// that takes parameters -- a second, independent way for an assertion to become
// unfailable. Naming the method is the only way to express "not called at all",
// so that is what it means here.
func (l *callLog) matching(method string, expected []interface{}) int {
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
		if _, diffs := mock.Arguments(expected).Diff(c.args); diffs == 0 {
			n++
		}
	}
	return n
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

func markHelper(t mock.TestingT) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
}

// AssertCalled asserts that method was invoked with arguments satisfying the
// given matchers. It shadows the promoted testify method so it reads the
// complete call log rather than mock.Mock.Calls; pass no arguments to assert
// the method was called at all.
func (m *MockConfigStore) AssertCalled(t mock.TestingT, method string, arguments ...interface{}) bool {
	markHelper(t)
	if m.callLog.matching(method, arguments) > 0 {
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
	markHelper(t)
	if n := m.callLog.matching(method, arguments); n > 0 {
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
	markHelper(t)
	if actual := m.callLog.matching(method, nil); actual != expectedCalls {
		t.Errorf("mock: expected %d call(s) to %s, got %d. Recorded calls:%s",
			expectedCalls, method, actual, m.callLog.summary(method))
		return false
	}
	return true
}
