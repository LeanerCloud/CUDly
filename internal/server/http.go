package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

// CreateHTTPServer builds the HTTP server with routes and timeouts configured,
// but does not start listening. This is useful for testing.
func CreateHTTPServer(app *Application, port int) *http.Server {
	mux := http.NewServeMux()

	// Register health and scheduled task routes (always available)
	mux.HandleFunc("/health", app.handleHealthCheck)
	mux.HandleFunc("/api/scheduled/", app.handleScheduledHTTP)

	// When STATIC_DIR is set, serve static files for non-API paths
	// and route only /api/ to the API handler.
	// When unset, all requests go to the API handler (backward compatible).
	staticDir := staticDirFromEnv()
	if staticDir != "" {
		mux.HandleFunc("/api/", app.handleHTTPRequest)
		mux.Handle("/", spaFileServer(staticDir))
	} else {
		mux.HandleFunc("/", app.handleHTTPRequest)
	}

	addr := fmt.Sprintf(":%d", port)

	return &http.Server{
		Addr:         addr,
		Handler:      securityHeaders(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

// StartHTTPServer starts the standard HTTP server
func StartHTTPServer(app *Application, port int) error {
	server := CreateHTTPServer(app, port)
	log.Printf("Starting HTTP server on %s", server.Addr)
	return server.ListenAndServe()
}

// securityHeaders wraps a handler to add standard security headers to every response.
// These headers were previously set by CDN (CloudFront/Front Door/GLB) but now need
// to come from the server since static files are served directly from the container.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// handleHTTPRequest converts standard HTTP requests to Lambda Function URL format
func (app *Application) handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	// Add request timeout to prevent hanging requests
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Ensure database connection is established (lazy initialization)
	if err := app.ensureDB(ctx); err != nil {
		log.Printf("Failed to establish database connection: %v", err)
		http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}

	// Convert HTTP request to Lambda Function URL request format
	lambdaReq := httpToLambdaRequest(r)

	// Call the API handler
	lambdaResp, err := app.API.HandleRequest(ctx, lambdaReq)
	if err != nil {
		log.Printf("API handler error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Convert Lambda response back to HTTP response
	lambdaResponseToHTTP(w, lambdaResp)
}

// handleScheduledHTTP handles scheduled tasks via HTTP endpoint
// This is used by GCP Cloud Scheduler and Azure Logic Apps
func (app *Application) handleScheduledHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify shared secret for scheduled task authentication
	if secret := app.appConfig.ScheduledTaskSecret; secret != "" {
		provided := r.Header.Get("Authorization")
		expected := "Bearer " + secret
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	ctx := r.Context()

	// Ensure database connection is established (lazy initialization)
	if err := app.ensureDB(ctx); err != nil {
		log.Printf("Failed to establish database connection: %v", err)
		http.Error(w, "Database connection failed", http.StatusServiceUnavailable)
		return
	}

	// Extract task type from URL path
	// Expected format: /api/scheduled/{task_type}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	taskTypeStr := parts[2]
	taskType := ScheduledTaskType(taskTypeStr)

	// Execute scheduled task
	result, err := app.HandleScheduledTask(ctx, taskType)
	if err != nil {
		log.Printf("Scheduled task error: %v", err)
		http.Error(w, fmt.Sprintf("Task failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return result as JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status": "success",
		"task":   taskTypeStr,
		"result": result,
	}); err != nil {
		log.Printf("Failed to encode scheduled task response: %v", err)
	}
}

// httpToLambdaRequest converts a standard HTTP request to Lambda Function URL request format
func httpToLambdaRequest(r *http.Request) *events.LambdaFunctionURLRequest {
	// Read body with size limit to prevent memory exhaustion
	body := ""
	if r.Body != nil {
		defer r.Body.Close()
		const maxBodySize = 10 << 20 // 10 MB
		limited := io.LimitReader(r.Body, maxBodySize)
		bodyBytes, err := io.ReadAll(limited)
		if err == nil && len(bodyBytes) > 0 {
			body = string(bodyBytes)
		}
	}

	// Convert headers - lowercase all keys to match Lambda Function URL behavior.
	// Go's http.Request.Header canonicalizes keys (e.g. "X-CSRF-Token" → "X-Csrf-Token"),
	// but our middleware expects lowercase keys (e.g. "x-csrf-token").
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}

	// Convert query parameters
	queryParams := make(map[string]string)
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			queryParams[key] = values[0]
		}
	}

	// Get client IP from X-Forwarded-For using the rightmost entry, which is
	// set by the nearest trusted proxy (ALB, Cloud Run ingress, etc.). The
	// leftmost entry is client-controlled and trivially spoofable.
	sourceIP := r.RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		sourceIP = strings.TrimSpace(parts[len(parts)-1])
	}

	return &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method:    r.Method,
				Path:      r.URL.Path,
				Protocol:  r.Proto,
				SourceIP:  sourceIP,
				UserAgent: r.Header.Get("User-Agent"),
			},
			TimeEpoch: time.Now().Unix(),
		},
		RawPath:               r.URL.Path,
		RawQueryString:        r.URL.RawQuery,
		Headers:               headers,
		QueryStringParameters: queryParams,
		Body:                  body,
		IsBase64Encoded:       false,
	}
}

// safeHeaderNames is a whitelist of headers that are safe to pass through from Lambda responses.
// This prevents header injection attacks where malicious headers could be set.
var safeHeaderNames = map[string]bool{
	// Content headers
	"content-type":     true,
	"content-length":   true,
	"content-encoding": true,
	// Caching headers
	"cache-control": true,
	"etag":          true,
	"last-modified": true,
	// Request tracking headers
	"x-request-id":     true,
	"x-correlation-id": true,
	// CORS headers
	"access-control-allow-origin":      true,
	"access-control-allow-methods":     true,
	"access-control-allow-headers":     true,
	"access-control-allow-credentials": true,
	"access-control-max-age":           true,
	// Security headers
	"strict-transport-security": true,
	"x-content-type-options":    true,
	"x-frame-options":           true,
	"x-xss-protection":          true,
	"content-security-policy":   true,
	"referrer-policy":           true,
	"permissions-policy":        true,
}

// isSafeHeaderValue checks that a header value doesn't contain CRLF injection characters
func isSafeHeaderValue(value string) bool {
	return !strings.ContainsAny(value, "\r\n")
}

// lambdaResponseToHTTP converts a Lambda Function URL response to standard HTTP response
func lambdaResponseToHTTP(w http.ResponseWriter, lambdaResp *events.LambdaFunctionURLResponse) {
	// Decode body before writing headers/status to avoid double WriteHeader on error
	var body []byte
	if lambdaResp.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(lambdaResp.Body)
		if err != nil {
			log.Printf("Error decoding base64 response body: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		body = decoded
	} else {
		body = []byte(lambdaResp.Body)
	}

	// Set headers with validation to prevent header injection
	for key, value := range lambdaResp.Headers {
		lowerKey := strings.ToLower(key)
		if !safeHeaderNames[lowerKey] {
			log.Printf("Blocked unsafe header from Lambda response: %s", key)
			continue
		}
		if !isSafeHeaderValue(value) {
			log.Printf("Blocked header with unsafe value (CRLF injection attempt): %s", key)
			continue
		}
		w.Header().Set(key, value)
	}

	// Set cookies with CRLF validation
	for _, cookie := range lambdaResp.Cookies {
		if !isSafeHeaderValue(cookie) {
			log.Printf("Blocked cookie with unsafe value (CRLF injection attempt)")
			continue
		}
		w.Header().Add("Set-Cookie", cookie)
	}

	// Set status code and write body
	w.WriteHeader(lambdaResp.StatusCode)
	w.Write(body)
}
