package api

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
)

func TestHandler_isPublicEndpoint(t *testing.T) {
	handler := &Handler{corsAllowedOrigin: "*"}

	tests := []struct {
		path     string
		expected bool
	}{
		{"/api/health", true},
		{"/api/purchases/approve/12345678-1234-1234-1234-123456789abc", true},
		{"/api/purchases/cancel/45645645-6456-4564-5645-645645645645", true},
		{"/api/config", false},
		{"/api/recommendations", false},
		{"/api/plans", false},
		{"/api/history", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := handler.isPublicEndpoint(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_authenticate(t *testing.T) {
	tests := []struct {
		name     string
		apiKey   string
		headers  map[string]string
		params   map[string]string
		expected bool
	}{
		{
			name:     "no API key configured - deny access",
			apiKey:   "",
			headers:  map[string]string{},
			params:   map[string]string{},
			expected: false,
		},
		{
			name:     "valid API key in X-API-Key header",
			apiKey:   "secret-key",
			headers:  map[string]string{"X-API-Key": "secret-key"},
			params:   map[string]string{},
			expected: true,
		},
		{
			name:     "valid API key in x-api-key header (lowercase)",
			apiKey:   "secret-key",
			headers:  map[string]string{"x-api-key": "secret-key"},
			params:   map[string]string{},
			expected: true,
		},
		{
			name:     "API key in query parameter (not supported for security)",
			apiKey:   "secret-key",
			headers:  map[string]string{},
			params:   map[string]string{"api_key": "secret-key"},
			expected: false, // Query parameters are not supported for security reasons
		},
		{
			name:     "invalid API key",
			apiKey:   "secret-key",
			headers:  map[string]string{"X-API-Key": "wrong-key"},
			params:   map[string]string{},
			expected: false,
		},
		{
			name:     "missing API key when configured",
			apiKey:   "secret-key",
			headers:  map[string]string{},
			params:   map[string]string{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			handler := &Handler{apiKey: tt.apiKey}
			req := &events.LambdaFunctionURLRequest{
				Headers:               tt.headers,
				QueryStringParameters: tt.params,
			}
			result := handler.authenticate(ctx, req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_extractBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected string
	}{
		{
			name:     "valid bearer token with X-Authorization header",
			headers:  map[string]string{"X-Authorization": "Bearer my-token-x"},
			expected: "my-token-x",
		},
		{
			name:     "valid bearer token with lowercase x-authorization header",
			headers:  map[string]string{"x-authorization": "Bearer my-token-x-lower"},
			expected: "my-token-x-lower",
		},
		{
			name:     "X-Authorization takes priority over Authorization",
			headers:  map[string]string{"X-Authorization": "Bearer x-token", "Authorization": "Bearer auth-token"},
			expected: "x-token",
		},
		{
			name:     "valid bearer token with Authorization header",
			headers:  map[string]string{"Authorization": "Bearer my-token-123"},
			expected: "my-token-123",
		},
		{
			name:     "valid bearer token with lowercase authorization header",
			headers:  map[string]string{"authorization": "Bearer my-token-456"},
			expected: "my-token-456",
		},
		{
			name:     "no bearer prefix",
			headers:  map[string]string{"Authorization": "my-token-789"},
			expected: "",
		},
		{
			name:     "empty authorization header",
			headers:  map[string]string{"Authorization": ""},
			expected: "",
		},
		{
			name:     "no authorization header",
			headers:  map[string]string{},
			expected: "",
		},
		{
			name:     "bearer without token",
			headers:  map[string]string{"Authorization": "Bearer "},
			expected: "",
		},
	}

	handler := &Handler{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &events.LambdaFunctionURLRequest{
				Headers: tt.headers,
			}
			result := handler.extractBearerToken(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_authenticate_BearerToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{
		UserID: "11111111-1111-1111-1111-111111111111",
		Email:  "user@example.com",
		Role:   "user",
	}

	mockAuth.On("ValidateSession", ctx, "valid-token").Return(session, nil)
	mockAuth.On("ValidateSession", ctx, "invalid-token").Return(nil, errors.New("invalid session"))

	handler := &Handler{auth: mockAuth, apiKey: ""}

	// Test valid token - valid bearer token allows access
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer valid-token",
		},
	}
	assert.True(t, handler.authenticate(ctx, req))

	// Test invalid token - invalid bearer token denies access
	req = &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer invalid-token",
		},
	}
	// Should be false because token is invalid and no API key provided
	assert.False(t, handler.authenticate(ctx, req))
}

func TestHandler_authenticate_BearerTokenWithAPIKey(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{
		UserID: "11111111-1111-1111-1111-111111111111",
		Email:  "user@example.com",
		Role:   "user",
	}

	mockAuth.On("ValidateSession", ctx, "valid-token").Return(session, nil)
	mockAuth.On("ValidateSession", ctx, "invalid-token").Return(nil, errors.New("invalid session"))

	handler := &Handler{auth: mockAuth, apiKey: "configured-api-key"}

	// Test valid bearer token when API key is configured
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer valid-token",
		},
	}
	assert.True(t, handler.authenticate(ctx, req))

	// Test invalid bearer token when API key is configured
	req = &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer invalid-token",
		},
	}
	// Should be false because API key is configured and bearer token is invalid
	assert.False(t, handler.authenticate(ctx, req))
}
