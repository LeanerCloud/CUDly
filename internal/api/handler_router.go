package api

import (
	"context"

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
