package api

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetSecurityHeaders verifies that all required security headers are set
func TestSetSecurityHeaders(t *testing.T) {
	headers := make(map[string]string)
	headers = setSecurityHeaders(headers)

	// Verify all required security headers are present
	assert.Equal(t, "nosniff", headers["X-Content-Type-Options"], "X-Content-Type-Options should be nosniff")
	assert.Equal(t, "DENY", headers["X-Frame-Options"], "X-Frame-Options should be DENY")
	// X-XSS-Protection has been removed: it is deprecated and a no-op in modern browsers
	assert.Empty(t, headers["X-XSS-Protection"], "X-XSS-Protection should not be set")
	assert.Equal(t, "max-age=31536000; includeSubDomains", headers["Strict-Transport-Security"], "HSTS should be set with 1 year max-age")
	assert.Equal(t, "strict-origin-when-cross-origin", headers["Referrer-Policy"], "Referrer-Policy should be strict-origin-when-cross-origin")
	assert.Equal(t, "default-src 'none'; frame-ancestors 'none'", headers["Content-Security-Policy"], "CSP should be restrictive")
	assert.Equal(t, "geolocation=(), microphone=(), camera=()", headers["Permissions-Policy"], "Permissions-Policy should disable browser features")
	assert.Equal(t, "no-store, no-cache, must-revalidate", headers["Cache-Control"], "Cache-Control should prevent caching")
}

// TestSetSecurityHeaders_DoesNotOverwrite verifies headers are set correctly
func TestSetSecurityHeaders_DoesNotOverwrite(t *testing.T) {
	headers := map[string]string{
		"Content-Type": "application/json",
	}

	headers = setSecurityHeaders(headers)

	// Original header should still be present
	assert.Equal(t, "application/json", headers["Content-Type"])
	// Security headers should be added
	assert.NotEmpty(t, headers["X-Content-Type-Options"])
}

// TestHandleRequest_SecurityHeaders verifies all responses include security headers
func TestHandleRequest_SecurityHeaders(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{corsAllowedOrigin: "https://example.com"}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/health",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)

	// Verify all security headers are present in response
	assert.Equal(t, "nosniff", resp.Headers["X-Content-Type-Options"])
	assert.Equal(t, "DENY", resp.Headers["X-Frame-Options"])
	// X-XSS-Protection has been removed: deprecated and no-op in modern browsers
	assert.Empty(t, resp.Headers["X-XSS-Protection"])
	assert.Equal(t, "max-age=31536000; includeSubDomains", resp.Headers["Strict-Transport-Security"])
	assert.Equal(t, "strict-origin-when-cross-origin", resp.Headers["Referrer-Policy"])
	assert.Equal(t, "default-src 'none'; frame-ancestors 'none'", resp.Headers["Content-Security-Policy"])
	assert.Equal(t, "geolocation=(), microphone=(), camera=()", resp.Headers["Permissions-Policy"])
	assert.Equal(t, "no-store, no-cache, must-revalidate", resp.Headers["Cache-Control"])
}

// TestHandleRequest_SecurityHeaders_OPTIONS verifies OPTIONS requests include security headers
func TestHandleRequest_SecurityHeaders_OPTIONS(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{corsAllowedOrigin: "https://example.com"}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "OPTIONS",
				Path:   "/api/config",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Verify security headers are present in OPTIONS response
	assert.Equal(t, "nosniff", resp.Headers["X-Content-Type-Options"])
	assert.Equal(t, "DENY", resp.Headers["X-Frame-Options"])
	assert.Equal(t, "max-age=31536000; includeSubDomains", resp.Headers["Strict-Transport-Security"])
}

// TestHandleRequest_SecurityHeaders_ErrorResponse verifies error responses include security headers
func TestHandleRequest_SecurityHeaders_ErrorResponse(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			// No API key provided - should return 401
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/config",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)

	// Verify security headers are present even in error responses
	assert.Equal(t, "nosniff", resp.Headers["X-Content-Type-Options"])
	assert.Equal(t, "DENY", resp.Headers["X-Frame-Options"])
	assert.Equal(t, "max-age=31536000; includeSubDomains", resp.Headers["Strict-Transport-Security"])
	assert.Equal(t, "default-src 'none'; frame-ancestors 'none'", resp.Headers["Content-Security-Policy"])
	assert.Equal(t, "geolocation=(), microphone=(), camera=()", resp.Headers["Permissions-Policy"])
}

// TestHandleRequest_SecurityHeaders_RequestTooLarge verifies 413 responses include security headers
func TestHandleRequest_SecurityHeaders_RequestTooLarge(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	// Create a request body larger than max size (1MB)
	largeBody := make([]byte, 1024*1024+1)
	for i := range largeBody {
		largeBody[i] = 'a'
	}

	req := &events.LambdaFunctionURLRequest{
		Body: string(largeBody),
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/config",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 413, resp.StatusCode)

	// Verify security headers are present in 413 response
	assert.Equal(t, "nosniff", resp.Headers["X-Content-Type-Options"])
	assert.Equal(t, "max-age=31536000; includeSubDomains", resp.Headers["Strict-Transport-Security"])
}
