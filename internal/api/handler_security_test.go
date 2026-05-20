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

// TestServeDocsUI_RelaxedCSP verifies the /api/docs HTML response carries a
// relaxed Content-Security-Policy that whitelists exactly what Swagger UI
// needs to render. The default restrictive CSP (default-src 'none') blocks
// every script and stylesheet on the page, leaving it blank — issue #329.
//
// Asserts EXACT equality against docsPageCSP so a future broadening of the
// policy (extra source, extra directive, accidental wildcard) triggers a
// test failure rather than silently passing through a Contains-style check.
func TestServeDocsUI_RelaxedCSP(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/docs/",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Exact-match assertion: any drift from the canonical docs policy fails.
	assert.Equal(t, docsPageCSP, resp.Headers["Content-Security-Policy"],
		"docs CSP must match the canonical docsPageCSP exactly")

	// Other security headers stay strict.
	assert.Equal(t, "nosniff", resp.Headers["X-Content-Type-Options"])
	assert.Equal(t, "DENY", resp.Headers["X-Frame-Options"])
	assert.Equal(t, "max-age=31536000; includeSubDomains", resp.Headers["Strict-Transport-Security"])
}

func TestDocsHandler_HEAD_ReturnsHeaders(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	tests := []struct {
		name string
		path string
	}{
		{name: "api docs UI", path: "/api/docs/"},
		{name: "root docs UI", path: "/docs/"},
		{name: "openapi yaml", path: "/api/docs/openapi.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getReq := &events.LambdaFunctionURLRequest{
				RequestContext: events.LambdaFunctionURLRequestContext{
					HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
						Method: "GET",
						Path:   tt.path,
					},
				},
			}
			headReq := &events.LambdaFunctionURLRequest{
				RequestContext: events.LambdaFunctionURLRequestContext{
					HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
						Method: "HEAD",
						Path:   tt.path,
					},
				},
			}

			getResp, err := handler.HandleRequest(ctx, getReq)
			require.NoError(t, err)
			assert.Equal(t, 200, getResp.StatusCode)

			headResp, err := handler.HandleRequest(ctx, headReq)
			require.NoError(t, err)
			assert.Equal(t, 200, headResp.StatusCode)
			assert.Equal(t, getResp.Headers, headResp.Headers)
			assert.Equal(t, getResp.Headers["Content-Security-Policy"], headResp.Headers["Content-Security-Policy"])
			assert.Empty(t, headResp.Body)
		})
	}
}

func TestDocsRoutes_RegisterHEAD(t *testing.T) {
	router := NewRouter(&Handler{})
	expected := map[string]bool{
		"/api/docs": false,
		"/docs":     false,
	}

	for _, route := range router.routes {
		if route.Method == "HEAD" {
			if _, ok := expected[route.PathPrefix]; ok {
				expected[route.PathPrefix] = true
				assert.Equal(t, AuthPublic, route.Auth)
			}
		}
	}

	for path, found := range expected {
		assert.True(t, found, "missing HEAD route for %s", path)
	}
}

// TestServeDocsUI_RelaxedCSP_RootDocs verifies the same relaxed CSP is
// applied to the root /docs/ prefix path (no /api/ prefix). The router
// dispatches both /docs and /api/docs to docsHandler, so both surfaces
// must get the override; otherwise the legacy header link in the
// frontend would render blank.
func TestServeDocsUI_RelaxedCSP_RootDocs(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/docs/",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	assert.Equal(t, docsPageCSP, resp.Headers["Content-Security-Policy"],
		"root /docs/ CSP must match the canonical docsPageCSP exactly")

	// Strict default must not leak through.
	assert.NotEqual(t, "default-src 'none'; frame-ancestors 'none'",
		resp.Headers["Content-Security-Policy"],
		"/docs/ must override the restrictive default CSP")
}

// TestServeOpenAPISpec_KeepsStrictCSP verifies that the openapi.yaml endpoint
// (which serves raw YAML, no scripts) keeps the default restrictive CSP.
// Only the HTML docs page needs the relaxed policy.
func TestServeOpenAPISpec_KeepsStrictCSP(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/docs/openapi.yaml",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Strict CSP unchanged for the YAML endpoint.
	assert.Equal(t, "default-src 'none'; frame-ancestors 'none'",
		resp.Headers["Content-Security-Policy"],
		"openapi.yaml is non-HTML and must keep the strict default CSP")
}

// TestNonDocsPath_KeepsStrictCSP verifies that a non-docs path (e.g. /api/health)
// is unaffected by the docs-path CSP override and still emits the strict CSP.
func TestNonDocsPath_KeepsStrictCSP(t *testing.T) {
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

	assert.Equal(t, "default-src 'none'; frame-ancestors 'none'",
		resp.Headers["Content-Security-Policy"],
		"non-docs paths must retain the strict default CSP")
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
