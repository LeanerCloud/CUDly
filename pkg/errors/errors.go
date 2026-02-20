// Package errors provides custom error types for CUDly.
package errors

import (
	"errors"
	"fmt"
)

// NotFoundError represents a resource not found error
type NotFoundError struct {
	Resource string
	ID       string
	Message  string
}

// Error implements the error interface
func (e *NotFoundError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.ID != "" {
		return fmt.Sprintf("%s not found: %s", e.Resource, e.ID)
	}
	return fmt.Sprintf("%s not found", e.Resource)
}

// Is implements error comparison
func (e *NotFoundError) Is(target error) bool {
	_, ok := target.(*NotFoundError)
	return ok
}

// NewNotFoundError creates a new NotFoundError
func NewNotFoundError(resource, id string) *NotFoundError {
	return &NotFoundError{
		Resource: resource,
		ID:       id,
	}
}

// NewNotFoundErrorWithMessage creates a NotFoundError with a custom message
func NewNotFoundErrorWithMessage(message string) *NotFoundError {
	return &NotFoundError{
		Message: message,
	}
}

// IsNotFoundError checks if an error is a NotFoundError
func IsNotFoundError(err error) bool {
	var notFound *NotFoundError
	return errors.As(err, &notFound)
}

// ValidationError represents a validation error
type ValidationError struct {
	Field   string
	Message string
}

// Error implements the error interface
func (e *ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("validation error on field '%s': %s", e.Field, e.Message)
	}
	return fmt.Sprintf("validation error: %s", e.Message)
}

// Is implements error comparison
func (e *ValidationError) Is(target error) bool {
	_, ok := target.(*ValidationError)
	return ok
}

// NewValidationError creates a new ValidationError
func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Message: message,
	}
}

// IsValidationError checks if an error is a ValidationError
func IsValidationError(err error) bool {
	var validationErr *ValidationError
	return errors.As(err, &validationErr)
}

// AuthenticationError represents an authentication failure
type AuthenticationError struct {
	Reason string
}

// Error implements the error interface
func (e *AuthenticationError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("authentication failed: %s", e.Reason)
	}
	return "authentication failed"
}

// Is implements error comparison
func (e *AuthenticationError) Is(target error) bool {
	_, ok := target.(*AuthenticationError)
	return ok
}

// NewAuthenticationError creates a new AuthenticationError
func NewAuthenticationError(reason string) *AuthenticationError {
	return &AuthenticationError{
		Reason: reason,
	}
}

// IsAuthenticationError checks if an error is an AuthenticationError
func IsAuthenticationError(err error) bool {
	var authErr *AuthenticationError
	return errors.As(err, &authErr)
}

// AuthorizationError represents an authorization failure
type AuthorizationError struct {
	Action   string
	Resource string
}

// Error implements the error interface
func (e *AuthorizationError) Error() string {
	if e.Action != "" && e.Resource != "" {
		return fmt.Sprintf("not authorized to %s on %s", e.Action, e.Resource)
	}
	return "not authorized"
}

// Is implements error comparison
func (e *AuthorizationError) Is(target error) bool {
	_, ok := target.(*AuthorizationError)
	return ok
}

// NewAuthorizationError creates a new AuthorizationError
func NewAuthorizationError(action, resource string) *AuthorizationError {
	return &AuthorizationError{
		Action:   action,
		Resource: resource,
	}
}

// IsAuthorizationError checks if an error is an AuthorizationError
func IsAuthorizationError(err error) bool {
	var authzErr *AuthorizationError
	return errors.As(err, &authzErr)
}

// ConflictError represents a resource conflict (e.g., duplicate)
type ConflictError struct {
	Resource string
	ID       string
	Message  string
}

// Error implements the error interface
func (e *ConflictError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.ID != "" {
		return fmt.Sprintf("%s already exists: %s", e.Resource, e.ID)
	}
	return fmt.Sprintf("%s already exists", e.Resource)
}

// Is implements error comparison
func (e *ConflictError) Is(target error) bool {
	_, ok := target.(*ConflictError)
	return ok
}

// NewConflictError creates a new ConflictError
func NewConflictError(resource, id string) *ConflictError {
	return &ConflictError{
		Resource: resource,
		ID:       id,
	}
}

// IsConflictError checks if an error is a ConflictError
func IsConflictError(err error) bool {
	var conflictErr *ConflictError
	return errors.As(err, &conflictErr)
}

// RateLimitError represents a rate limit exceeded error
type RateLimitError struct {
	RetryAfter int // Seconds until retry is allowed
}

// Error implements the error interface
func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limit exceeded, retry after %d seconds", e.RetryAfter)
	}
	return "rate limit exceeded"
}

// Is implements error comparison
func (e *RateLimitError) Is(target error) bool {
	_, ok := target.(*RateLimitError)
	return ok
}

// NewRateLimitError creates a new RateLimitError
func NewRateLimitError(retryAfter int) *RateLimitError {
	return &RateLimitError{
		RetryAfter: retryAfter,
	}
}

// IsRateLimitError checks if an error is a RateLimitError
func IsRateLimitError(err error) bool {
	var rateErr *RateLimitError
	return errors.As(err, &rateErr)
}

// ServiceError represents an external service error
type ServiceError struct {
	Service string
	Err     error
}

// Error implements the error interface
func (e *ServiceError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s service error: %v", e.Service, e.Err)
	}
	return fmt.Sprintf("%s service error", e.Service)
}

// Unwrap returns the underlying error
func (e *ServiceError) Unwrap() error {
	return e.Err
}

// Is implements error comparison
func (e *ServiceError) Is(target error) bool {
	_, ok := target.(*ServiceError)
	return ok
}

// NewServiceError creates a new ServiceError
func NewServiceError(service string, err error) *ServiceError {
	return &ServiceError{
		Service: service,
		Err:     err,
	}
}

// IsServiceError checks if an error is a ServiceError
func IsServiceError(err error) bool {
	var serviceErr *ServiceError
	return errors.As(err, &serviceErr)
}

// Sentinel errors for common cases
var (
	// ErrNotFound is returned when a resource is not found
	ErrNotFound = errors.New("not found")

	// ErrUnauthorized is returned when authentication fails
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden is returned when authorization fails
	ErrForbidden = errors.New("forbidden")

	// ErrInvalidInput is returned when input validation fails
	ErrInvalidInput = errors.New("invalid input")

	// ErrConflict is returned when a resource conflict occurs
	ErrConflict = errors.New("conflict")

	// ErrInternalError is returned for internal server errors
	ErrInternalError = errors.New("internal error")
)
