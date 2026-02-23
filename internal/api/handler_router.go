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
}

func (e *clientError) Error() string { return e.message }

// NewClientError creates a new client-facing error with the given HTTP status code and message.
func NewClientError(code int, message string) error {
	return &clientError{message: message, code: code}
}

// IsClientError checks if the error is a client error and returns it.
func IsClientError(err error) (*clientError, bool) {
	var ce *clientError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}
