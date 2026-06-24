package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/aws/aws-lambda-go/events"
)

// CreateHTTPServer builds the HTTP server with routes and timeouts configured,
// but does not start listening. This is useful for testing.
func CreateHTTPServer(app *Application, port int) *http.Server {
	mux := http.NewServeMux()

	// Register health and scheduled task routes (always available).
	// /api/scheduled/* sits behind the scheduledauth middleware — see
	// internal/server/scheduledauth. Mode is selected by
	// SCHEDULED_TASK_AUTH_MODE: oidc on GCP, bearer on Azure, disabled
	// for local dev.
	mux.HandleFunc("/health", app.handleHealthCheck)
	// /version is a root-path (no /api prefix) public endpoint. Like /health
	// it must be registered explicitly: when STATIC_DIR is set the catch-all
	// "/" route serves the SPA, so an unregistered /version would return the
	// frontend index instead of the build metadata. It dispatches through the
	// API router (single source of truth) but skips ensureDB so the deployed
	// commit can still be read when the database is unreachable.
	mux.HandleFunc("/version", app.handleVersion)
	mux.Handle("/api/scheduled/", app.scheduledAuthMiddleware(http.HandlerFunc(app.handleScheduledHTTP)))

	// Intercept OIDC issuer endpoints before both the static-file fallback and
	// the API router. Mirrors the identical intercept in handleLambdaHTTPEvent
	// so the two transports cannot drift (D1 / issue #1024). api.HandleOIDC is
	// auth-less and must sit in front of the SPA handler -- otherwise
	// STATIC_DIR deployments serve index.html for /oidc/... instead of the
	// JWKS/discovery JSON, breaking any federated-credential relying party on
	// Cloud Run or Container Apps.
	mux.HandleFunc(api.OIDCBasePath+"/", app.handleOIDCHTTP)

	// When STATIC_DIR is set, serve static files for non-API paths
	// and route only /api/ to the API handler.
	// When unset, all requests go to the API handler (backward compatible).
	// Read from app.staticDir (single source of truth set by the constructor)
	// rather than re-invoking staticDirFromEnv() -- avoids the double-stat and
	// double-log that M2 identified, and keeps Lambda and HTTP transports
	// reading the same value.
	if app.staticDir != "" {
		mux.HandleFunc("/api/", app.handleHTTPRequest)
		mux.Handle("/", spaFileServer(app.staticDir))
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

// StartHTTPServer starts the HTTP server with graceful shutdown on SIGINT/SIGTERM.
// It blocks until the server exits cleanly. In container orchestrators (Cloud Run,
// Container Apps, Fargate) SIGTERM is the normal stop signal; without this wiring
// the process is killed before deferred app.Close() runs, leaving in-flight
// requests cut and the DB pool/advisory locks undrained (issue #1025).
func StartHTTPServer(app *Application, port int) error {
	server := CreateHTTPServer(app, port)
	log.Printf("Starting HTTP server on %s", server.Addr)

	// Signal context cancels on the first SIGINT or SIGTERM.
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		// ListenAndServe returned before a signal -- hard error (bind failure, etc.).
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sigCtx.Done():
		// Received SIGINT or SIGTERM: drain in-flight requests then close the DB.
		stop() // release signal resources promptly
		log.Printf("Shutdown signal received; draining HTTP server (30s grace)...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server forced shutdown: %v", err)
		}
		return nil
	}
}

// handleOIDCHTTP bridges the standard HTTP path to the Lambda-shaped HandleOIDC
// implementation. Registered at api.OIDCBasePath+"/" so it intercepts all
// /oidc/... requests before the SPA static handler or the API router, exactly
// mirroring the intercept in handleLambdaHTTPEvent.
func (app *Application) handleOIDCHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	lambdaReq := httpToLambdaRequest(r)
	resp, handled := app.API.HandleOIDC(ctx, lambdaReq)
	if !handled {
		// Path matched /oidc/ prefix but is not a recognized OIDC endpoint.
		http.NotFound(w, r)
		return
	}
	lambdaResponseToHTTP(w, resp)
}

// htmlCSP is the Content-Security-Policy delivered with every HTML
// response from either the container (securityHeaders middleware) or the
// Lambda Function URL (lambdaSecurityHeaders). frame-ancestors 'none' is
// the effective clickjacking gate — browsers ignore that directive in
// <meta>, so it must come from the header. Shared between the two
// delivery paths so they can't drift (issue #8 was partly caused by
// exactly that kind of drift).
const htmlCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// securityHeaders wraps a handler to add standard security headers to every response.
// These headers were previously set by CDN (CloudFront/Front Door/GLB) but now need
// to come from the server since static files are served directly from the container.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", htmlCSP)
		next.ServeHTTP(w, r)
	})
}

// handleHTTPRequest converts standard HTTP requests to Lambda Function URL format.
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

// handleVersion serves the public /version endpoint. It dispatches through the
// API router (the api package owns the response shape and the AuthPublic route)
// but, unlike handleHTTPRequest, deliberately skips ensureDB: the build-version
// metadata is read from process environment variables stamped at build time, so
// it must remain answerable even when the database is unreachable. That is
// precisely the scenario where confirming the deployed commit matters most.
func (app *Application) handleVersion(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	lambdaResp, err := app.API.HandleRequest(ctx, httpToLambdaRequest(r))
	if err != nil {
		log.Printf("Version handler error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	lambdaResponseToHTTP(w, lambdaResp)
}

// scheduledAuthMiddleware returns the configured scheduledauth middleware,
// or a passthrough no-op if app.scheduledAuth is nil. Tests that build
// an Application directly (without NewApplicationFromDeps) leave it
// nil — they exercise the handler in isolation, with the middleware
// covered separately in scheduledauth's own tests.
func (app *Application) scheduledAuthMiddleware(next http.Handler) http.Handler {
	if app.scheduledAuth == nil {
		return next
	}
	return app.scheduledAuth.Middleware(next)
}

// handleScheduledHTTP handles scheduled tasks via HTTP endpoint
// This is used by GCP Cloud Scheduler and Azure Logic Apps. Auth is
// enforced upstream by scheduledAuthMiddleware (see CreateHTTPServer);
// by the time we reach here the request is already authenticated for
// the configured mode.
func (app *Application) handleScheduledHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	// Ensure database connection is established (lazy initialization)
	if err := app.ensureDB(ctx); err != nil {
		log.Printf("Failed to establish database connection: %v", err)
		http.Error(w, "Database connection failed", http.StatusServiceUnavailable)
		return
	}

	// Extract task type from URL path: /api/scheduled/{task_type}.
	// TrimPrefix is cleaner than splitting and indexing parts[2]; it also
	// avoids the length-guard dance while remaining robust to extra slashes
	// (04-L5).
	const scheduledPrefix = "/api/scheduled/"
	taskTypeStr := strings.TrimPrefix(r.URL.Path, scheduledPrefix)
	if taskTypeStr == "" || strings.Contains(taskTypeStr, "/") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	taskType := ScheduledTaskType(taskTypeStr)

	// Execute scheduled task
	result, err := app.HandleScheduledTask(ctx, taskType)
	if err != nil {
		log.Printf("Scheduled task %q error: %v", taskTypeStr, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
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

// httpToLambdaRequest converts a standard HTTP request to Lambda Function URL request format.
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

// isSafeHeaderValue checks that a header value doesn't contain CRLF injection characters.
func isSafeHeaderValue(value string) bool {
	return !strings.ContainsAny(value, "\r\n")
}

// lambdaResponseToHTTP converts a Lambda Function URL response to standard HTTP response.
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

	// Set status code and write body. The body is the already-rendered Lambda
	// response (JSON, or HTML pre-escaped at the handler layer per the
	// escapeHtml convention) and its Content-Type travels through the
	// validated-headers loop above; this adapter only relays it verbatim, so
	// re-escaping here would corrupt legitimate JSON/binary payloads.
	w.WriteHeader(lambdaResp.StatusCode)
	if _, err := w.Write(body); err != nil { //nolint:gosec // G705: body is produced/escaped by the upstream handler; this Lambda->HTTP adapter relays it unchanged
		log.Printf("http: failed to write response body: %v", err)
	}
}
