package api

import (
	"context"
	"errors"

	"github.com/aws/aws-lambda-go/events"
)

// routeRequest routes the request to the appropriate handler based on path and method
// This function now delegates to the table-driven router for improved maintainability
func (h *Handler) routeRequest(ctx context.Context, method, path string, req *events.LambdaFunctionURLRequest) (any, error) {
	// Create a new router for each handler to avoid shared state in tests
	r := NewRouter(h)
	return r.Route(ctx, method, path, req)
}

// errNotFound is a sentinel error for 404 responses
var errNotFound = &notFoundError{}

type notFoundError struct{}

func (e *notFoundError) Error() string {
	return "not found"
}

// IsNotFoundError checks if the error is a not found error
func IsNotFoundError(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}

// clientError represents an error that should be returned to the client with a specific HTTP status code.
type clientError struct {
	message string
	code    int
	// details carries optional structured fields (e.g. ops_hint,
	// retry_attempt_n) that the response writer surfaces alongside the
	// human message. Used by retry-soft-block responses (issue #47) so
	// the frontend can render a confirm-with-warning UX without parsing
	// the message string.
	details map[string]any
}

func (e *clientError) Error() string { return e.message }

// Details returns a SHALLOW COPY of the structured detail fields
// attached to this error, or nil when none were set. Callers (the
// response writer) inspect this to enrich the JSON body — `error:
// <message>` plus the detail fields flattened at the top level.
// The copy keeps callers from mutating the error's internal state
// (CR #168 nit hardening — defends against accidental aliasing of
// the constructor's input map).
func (e *clientError) Details() map[string]any { return cloneDetails(e.details) }

// cloneDetails shallow-copies a details map. Returns nil for a nil
// input so the response writer's `len(details) > 0` check still
// short-circuits cleanly.
func cloneDetails(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// NewClientError creates a new client-facing error with the given HTTP status code and message.
func NewClientError(code int, message string) error {
	return &clientError{message: message, code: code}
}

// NewClientErrorWithDetails creates a client-facing error that also
// carries structured detail fields. The response writer flattens those
// into the JSON body so consumers can branch on machine-readable hints
// rather than substring-matching the message. Used by the retry handler
// (issue #47) for ops_hint + retry_attempt_n / threshold callouts.
// The details map is copied at construction so caller mutations after
// the error is created don't leak into the response body.
func NewClientErrorWithDetails(code int, message string, details map[string]any) error {
	return &clientError{message: message, code: code, details: cloneDetails(details)}
}

// IsClientError checks if the error is a client error and returns it.
func IsClientError(err error) (*clientError, bool) {
	var ce *clientError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}
