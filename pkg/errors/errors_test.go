package errors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNotFoundError(t *testing.T) {
	t.Run("with resource and ID", func(t *testing.T) {
		err := NewNotFoundError("User", "123")
		assert.Equal(t, "User not found: 123", err.Error())
		assert.Equal(t, "User", err.Resource)
		assert.Equal(t, "123", err.ID)
	})

	t.Run("with resource only", func(t *testing.T) {
		err := &NotFoundError{Resource: "Config"}
		assert.Equal(t, "Config not found", err.Error())
	})

	t.Run("with custom message", func(t *testing.T) {
		err := NewNotFoundErrorWithMessage("custom not found message")
		assert.Equal(t, "custom not found message", err.Error())
	})

	t.Run("Is comparison", func(t *testing.T) {
		err := NewNotFoundError("User", "123")
		target := &NotFoundError{}
		assert.True(t, errors.Is(err, target))
	})

	t.Run("IsNotFoundError helper", func(t *testing.T) {
		err := NewNotFoundError("User", "123")
		assert.True(t, IsNotFoundError(err))
		assert.False(t, IsNotFoundError(errors.New("other error")))
	})
}

func TestValidationError(t *testing.T) {
	t.Run("with field and message", func(t *testing.T) {
		err := NewValidationError("email", "invalid format")
		assert.Equal(t, "validation error on field 'email': invalid format", err.Error())
		assert.Equal(t, "email", err.Field)
		assert.Equal(t, "invalid format", err.Message)
	})

	t.Run("without field", func(t *testing.T) {
		err := &ValidationError{Message: "general validation error"}
		assert.Equal(t, "validation error: general validation error", err.Error())
	})

	t.Run("Is comparison", func(t *testing.T) {
		err := NewValidationError("field", "message")
		target := &ValidationError{}
		assert.True(t, errors.Is(err, target))
	})

	t.Run("IsValidationError helper", func(t *testing.T) {
		err := NewValidationError("field", "message")
		assert.True(t, IsValidationError(err))
		assert.False(t, IsValidationError(errors.New("other error")))
	})
}

func TestAuthenticationError(t *testing.T) {
	t.Run("with reason", func(t *testing.T) {
		err := NewAuthenticationError("invalid token")
		assert.Equal(t, "authentication failed: invalid token", err.Error())
		assert.Equal(t, "invalid token", err.Reason)
	})

	t.Run("without reason", func(t *testing.T) {
		err := &AuthenticationError{}
		assert.Equal(t, "authentication failed", err.Error())
	})

	t.Run("Is comparison", func(t *testing.T) {
		err := NewAuthenticationError("reason")
		target := &AuthenticationError{}
		assert.True(t, errors.Is(err, target))
	})

	t.Run("IsAuthenticationError helper", func(t *testing.T) {
		err := NewAuthenticationError("reason")
		assert.True(t, IsAuthenticationError(err))
		assert.False(t, IsAuthenticationError(errors.New("other error")))
	})
}

func TestAuthorizationError(t *testing.T) {
	t.Run("with action and resource", func(t *testing.T) {
		err := NewAuthorizationError("delete", "users")
		assert.Equal(t, "not authorized to delete on users", err.Error())
		assert.Equal(t, "delete", err.Action)
		assert.Equal(t, "users", err.Resource)
	})

	t.Run("without details", func(t *testing.T) {
		err := &AuthorizationError{}
		assert.Equal(t, "not authorized", err.Error())
	})

	t.Run("Is comparison", func(t *testing.T) {
		err := NewAuthorizationError("action", "resource")
		target := &AuthorizationError{}
		assert.True(t, errors.Is(err, target))
	})

	t.Run("IsAuthorizationError helper", func(t *testing.T) {
		err := NewAuthorizationError("action", "resource")
		assert.True(t, IsAuthorizationError(err))
		assert.False(t, IsAuthorizationError(errors.New("other error")))
	})
}

func TestConflictError(t *testing.T) {
	t.Run("with resource and ID", func(t *testing.T) {
		err := NewConflictError("User", "john@example.com")
		assert.Equal(t, "User already exists: john@example.com", err.Error())
		assert.Equal(t, "User", err.Resource)
		assert.Equal(t, "john@example.com", err.ID)
	})

	t.Run("with resource only", func(t *testing.T) {
		err := &ConflictError{Resource: "Plan"}
		assert.Equal(t, "Plan already exists", err.Error())
	})

	t.Run("with custom message", func(t *testing.T) {
		err := &ConflictError{Message: "custom conflict message"}
		assert.Equal(t, "custom conflict message", err.Error())
	})

	t.Run("Is comparison", func(t *testing.T) {
		err := NewConflictError("resource", "id")
		target := &ConflictError{}
		assert.True(t, errors.Is(err, target))
	})

	t.Run("IsConflictError helper", func(t *testing.T) {
		err := NewConflictError("resource", "id")
		assert.True(t, IsConflictError(err))
		assert.False(t, IsConflictError(errors.New("other error")))
	})
}

func TestRateLimitError(t *testing.T) {
	t.Run("with retry after", func(t *testing.T) {
		err := NewRateLimitError(60)
		assert.Equal(t, "rate limit exceeded, retry after 60 seconds", err.Error())
		assert.Equal(t, 60, err.RetryAfter)
	})

	t.Run("without retry after", func(t *testing.T) {
		err := &RateLimitError{}
		assert.Equal(t, "rate limit exceeded", err.Error())
	})

	t.Run("Is comparison", func(t *testing.T) {
		err := NewRateLimitError(30)
		target := &RateLimitError{}
		assert.True(t, errors.Is(err, target))
	})

	t.Run("IsRateLimitError helper", func(t *testing.T) {
		err := NewRateLimitError(30)
		assert.True(t, IsRateLimitError(err))
		assert.False(t, IsRateLimitError(errors.New("other error")))
	})
}

func TestServiceError(t *testing.T) {
	t.Run("with service and error", func(t *testing.T) {
		underlying := errors.New("connection refused")
		err := NewServiceError("AWS", underlying)
		assert.Equal(t, "AWS service error: connection refused", err.Error())
		assert.Equal(t, "AWS", err.Service)
		assert.Equal(t, underlying, err.Err)
	})

	t.Run("without underlying error", func(t *testing.T) {
		err := &ServiceError{Service: "Azure"}
		assert.Equal(t, "Azure service error", err.Error())
	})

	t.Run("Unwrap", func(t *testing.T) {
		underlying := errors.New("timeout")
		err := NewServiceError("GCP", underlying)
		assert.Equal(t, underlying, err.Unwrap())
	})

	t.Run("errors.Is with wrapped error", func(t *testing.T) {
		underlying := errors.New("timeout")
		err := NewServiceError("AWS", underlying)
		assert.True(t, errors.Is(err, underlying))
	})

	t.Run("Is comparison", func(t *testing.T) {
		err := NewServiceError("service", nil)
		target := &ServiceError{}
		assert.True(t, errors.Is(err, target))
	})

	t.Run("IsServiceError helper", func(t *testing.T) {
		err := NewServiceError("service", nil)
		assert.True(t, IsServiceError(err))
		assert.False(t, IsServiceError(errors.New("other error")))
	})
}

func TestSentinelErrors(t *testing.T) {
	t.Run("ErrNotFound", func(t *testing.T) {
		assert.Equal(t, "not found", ErrNotFound.Error())
	})

	t.Run("ErrUnauthorized", func(t *testing.T) {
		assert.Equal(t, "unauthorized", ErrUnauthorized.Error())
	})

	t.Run("ErrForbidden", func(t *testing.T) {
		assert.Equal(t, "forbidden", ErrForbidden.Error())
	})

	t.Run("ErrInvalidInput", func(t *testing.T) {
		assert.Equal(t, "invalid input", ErrInvalidInput.Error())
	})

	t.Run("ErrConflict", func(t *testing.T) {
		assert.Equal(t, "conflict", ErrConflict.Error())
	})

	t.Run("ErrInternalError", func(t *testing.T) {
		assert.Equal(t, "internal error", ErrInternalError.Error())
	})
}

func TestWrappedErrors(t *testing.T) {
	t.Run("wrapped NotFoundError", func(t *testing.T) {
		inner := NewNotFoundError("Resource", "id")
		wrapped := errors.Join(errors.New("context"), inner)
		assert.True(t, IsNotFoundError(wrapped))
	})

	t.Run("wrapped ValidationError", func(t *testing.T) {
		inner := NewValidationError("field", "msg")
		wrapped := errors.Join(errors.New("context"), inner)
		assert.True(t, IsValidationError(wrapped))
	})
}
