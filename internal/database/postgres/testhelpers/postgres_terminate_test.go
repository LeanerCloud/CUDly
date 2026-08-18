package testhelpers

import (
	"context"
	"errors"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

// recordingContainer captures the context Terminate is handed. The embedded
// interface is nil on purpose: only Terminate is exercised, and a call to
// anything else should panic rather than pass quietly.
type recordingContainer struct {
	testcontainers.Container
	called bool
	ctxErr error
}

func (r *recordingContainer) Terminate(ctx context.Context, _ ...testcontainers.TerminateOption) error {
	r.called = true
	r.ctxErr = ctx.Err()
	return nil
}

// failingContainer reports a teardown that itself fails.
type failingContainer struct {
	testcontainers.Container
	err    error
	called bool
}

func (f *failingContainer) Terminate(context.Context, ...testcontainers.TerminateOption) error {
	f.called = true
	return f.err
}

// TestTerminateAfterErrorSurvivesDeadContext pins the reason terminateAfterError
// does not simply forward its caller's context. The failures it cleans up after
// are frequently the context expiring, and Terminate on a canceled context
// returns without stopping anything, so forwarding would leave precisely the
// leaked container the helper exists to prevent.
func TestTerminateAfterErrorSurvivesDeadContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  func() context.Context
	}{
		{"canceled parent", func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}},
		{"expired deadline", func() context.Context {
			ctx, cancel := context.WithTimeout(context.Background(), 0)
			t.Cleanup(cancel)
			return ctx
		}},
		{"live parent", func() context.Context { return context.Background() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &recordingContainer{}
			terminateAfterError(tc.ctx(), c, "unit test")

			if !c.called {
				t.Fatal("Terminate was never called, so the container would leak")
			}
			if c.ctxErr != nil {
				t.Errorf("Terminate got an already-dead context (%v); it returns without "+
					"stopping the container, so the cleanup is a no-op", c.ctxErr)
			}
		})
	}
}

// TestTerminateAfterErrorReportsFailure documents that a failing Terminate is
// logged rather than propagated: the caller is already returning the error that
// caused the cleanup, and losing that to a teardown error would be worse.
func TestTerminateAfterErrorReportsFailure(t *testing.T) {
	c := &failingContainer{err: errors.New("boom")}
	terminateAfterError(context.Background(), c, "unit test")

	if !c.called {
		t.Fatal("Terminate was never called")
	}
}
