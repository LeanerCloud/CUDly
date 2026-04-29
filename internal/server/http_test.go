package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/LeanerCloud/CUDly/internal/server/scheduledauth"
	"github.com/LeanerCloud/CUDly/internal/testutil"
	"github.com/aws/aws-lambda-go/events"
)

func TestHttpToLambdaRequest(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		headers        map[string]string
		queryParams    map[string]string
		expectedMethod string
		expectedPath   string
	}{
		{
			name:           "GET request",
			method:         "GET",
			path:           "/api/recommendations",
			expectedMethod: "GET",
			expectedPath:   "/api/recommendations",
		},
		{
			name:           "POST request with body",
			method:         "POST",
			path:           "/api/purchases",
			body:           `{"plan_id": "123"}`,
			expectedMethod: "POST",
			expectedPath:   "/api/purchases",
		},
		{
			name:   "request with headers",
			method: "GET",
			path:   "/api/test",
			headers: map[string]string{
				"Authorization": "Bearer token123",
				"Content-Type":  "application/json",
			},
			expectedMethod: "GET",
			expectedPath:   "/api/test",
		},
		{
			name:   "request with query parameters",
			method: "GET",
			path:   "/api/search?q=test&limit=10",
			queryParams: map[string]string{
				"q":     "test",
				"limit": "10",
			},
			expectedMethod: "GET",
			expectedPath:   "/api/search",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create HTTP request
			var bodyReader *bytes.Reader
			if tt.body != "" {
				bodyReader = bytes.NewReader([]byte(tt.body))
			} else {
				bodyReader = bytes.NewReader([]byte{})
			}

			req := httptest.NewRequest(tt.method, tt.path, bodyReader)

			// Add headers
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			// Convert to Lambda request
			lambdaReq := httpToLambdaRequest(req)

			// Verify conversion
			testutil.AssertEqual(t, tt.expectedMethod, lambdaReq.RequestContext.HTTP.Method)
			testutil.AssertEqual(t, tt.expectedPath, lambdaReq.RequestContext.HTTP.Path)

			if tt.body != "" {
				testutil.AssertEqual(t, tt.body, lambdaReq.Body)
			}

			for key, expectedValue := range tt.headers {
				lowerKey := strings.ToLower(key)
				actualValue, ok := lambdaReq.Headers[lowerKey]
				testutil.AssertTrue(t, ok, "Expected header "+lowerKey+" to be present")
				testutil.AssertEqual(t, expectedValue, actualValue)
			}
		})
	}
}

func TestLambdaResponseToHTTP(t *testing.T) {
	tests := []struct {
		name            string
		lambdaResp      *events.LambdaFunctionURLResponse
		expectedStatus  int
		expectedBody    string
		expectedHeaders map[string]string
		expectedCookies int
	}{
		{
			name: "successful JSON response",
			lambdaResp: &events.LambdaFunctionURLResponse{
				StatusCode: 200,
				Body:       `{"status": "ok"}`,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
			},
			expectedStatus: 200,
			expectedBody:   `{"status": "ok"}`,
			expectedHeaders: map[string]string{
				"Content-Type": "application/json",
			},
		},
		{
			name: "error response",
			lambdaResp: &events.LambdaFunctionURLResponse{
				StatusCode: 500,
				Body:       `{"error": "Internal server error"}`,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
			},
			expectedStatus: 500,
			expectedBody:   `{"error": "Internal server error"}`,
		},
		{
			name: "response with cookies",
			lambdaResp: &events.LambdaFunctionURLResponse{
				StatusCode: 200,
				Body:       "OK",
				Cookies:    []string{"session=abc123; Path=/; HttpOnly"},
			},
			expectedStatus:  200,
			expectedCookies: 1,
		},
		{
			name: "blocks unsafe header with CRLF",
			lambdaResp: &events.LambdaFunctionURLResponse{
				StatusCode: 200,
				Body:       "OK",
				Headers: map[string]string{
					"Content-Type":     "application/json",
					"X-Request-Id":     "safe-value",
					"X-Correlation-Id": "injected\r\nSet-Cookie: evil=value",
				},
			},
			expectedStatus: 200,
			expectedHeaders: map[string]string{
				"Content-Type": "application/json",
				"X-Request-Id": "safe-value",
			},
		},
		{
			name: "blocks cookie with CRLF injection",
			lambdaResp: &events.LambdaFunctionURLResponse{
				StatusCode: 200,
				Body:       "OK",
				Cookies:    []string{"session=abc123; Path=/", "evil=value\r\nX-Injected: header"},
			},
			expectedStatus:  200,
			expectedCookies: 1, // Only the safe cookie should be set
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create response recorder
			w := httptest.NewRecorder()

			// Convert Lambda response to HTTP
			lambdaResponseToHTTP(w, tt.lambdaResp)

			// Verify status code
			testutil.AssertEqual(t, tt.expectedStatus, w.Code)

			// Verify body
			if tt.expectedBody != "" {
				testutil.AssertEqual(t, tt.expectedBody, w.Body.String())
			}

			// Verify headers
			for key, expectedValue := range tt.expectedHeaders {
				actualValue := w.Header().Get(key)
				testutil.AssertEqual(t, expectedValue, actualValue)
			}

			// Verify cookies
			if tt.expectedCookies > 0 {
				cookies := w.Header().Values("Set-Cookie")
				testutil.AssertEqual(t, tt.expectedCookies, len(cookies))
			}

			// Additional check for CRLF test case - verify unsafe header is not present
			if tt.name == "blocks unsafe header with CRLF" {
				testutil.AssertEqual(t, "", w.Header().Get("X-Correlation-Id"))
			}
		})
	}
}

func TestHandleScheduledHTTP(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		authHeader     string
		setupApp       func(*Application)
		expectedStatus int
		expectError    bool
	}{
		{
			name:   "valid scheduled task",
			method: "POST",
			path:   "/api/scheduled/collect_recommendations",
			setupApp: func(app *Application) {
				app.Scheduler = &testutil.MockScheduler{
					CollectRecommendationsFunc: func(ctx context.Context) (*scheduler.CollectResult, error) {
						return &scheduler.CollectResult{
							Recommendations: 10,
							TotalSavings:    1000.0,
						}, nil
					},
				}
			},
			expectedStatus: 200,
			expectError:    false,
		},
		{
			name:           "invalid method (GET instead of POST)",
			method:         "GET",
			path:           "/api/scheduled/collect_recommendations",
			setupApp:       func(app *Application) {},
			expectedStatus: 405,
			expectError:    false,
		},
		{
			name:           "invalid path (missing task type)",
			method:         "POST",
			path:           "/api/scheduled/",
			setupApp:       func(app *Application) {},
			expectedStatus: 400,
			expectError:    false,
		},
		{
			name:   "auth required but missing",
			method: "POST",
			path:   "/api/scheduled/collect_recommendations",
			setupApp: func(app *Application) {
				v, err := scheduledauth.New(scheduledauth.Config{
					Mode:   scheduledauth.ModeBearer,
					Bearer: "my-secret",
				})
				if err != nil {
					panic(err)
				}
				app.scheduledAuth = v
			},
			expectedStatus: 401,
		},
		{
			name:       "auth required and correct",
			method:     "POST",
			path:       "/api/scheduled/collect_recommendations",
			authHeader: "Bearer my-secret",
			setupApp: func(app *Application) {
				v, err := scheduledauth.New(scheduledauth.Config{
					Mode:   scheduledauth.ModeBearer,
					Bearer: "my-secret",
				})
				if err != nil {
					panic(err)
				}
				app.scheduledAuth = v
				app.Scheduler = &testutil.MockScheduler{
					CollectRecommendationsFunc: func(ctx context.Context) (*scheduler.CollectResult, error) {
						return &scheduler.CollectResult{}, nil
					},
				}
			},
			expectedStatus: 200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &Application{}
			if tt.setupApp != nil {
				tt.setupApp(app)
			}

			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()

			// Drive the request through the same middleware chain
			// that CreateHTTPServer wires up, so auth is enforced
			// upstream of handleScheduledHTTP.
			app.scheduledAuthMiddleware(http.HandlerFunc(app.handleScheduledHTTP)).ServeHTTP(w, req)

			testutil.AssertEqual(t, tt.expectedStatus, w.Code)

			if w.Code == 200 {
				// Verify JSON response
				var response map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &response)
				testutil.AssertNoError(t, err)

				status, ok := response["status"]
				testutil.AssertTrue(t, ok, "Response should contain status")
				testutil.AssertEqual(t, "success", status)
			}
		})
	}
}

func TestCreateHTTPServer(t *testing.T) {
	app := &Application{
		API: api.NewHandler(api.HandlerConfig{}),
	}

	t.Run("server addr and timeouts", func(t *testing.T) {
		srv := CreateHTTPServer(app, 8080)

		testutil.AssertEqual(t, ":8080", srv.Addr)
		testutil.AssertEqual(t, 30*time.Second, srv.ReadTimeout)
		testutil.AssertEqual(t, 30*time.Second, srv.WriteTimeout)
		testutil.AssertEqual(t, 120*time.Second, srv.IdleTimeout)
		testutil.AssertTrue(t, srv.Handler != nil, "Handler should not be nil")
	})

	t.Run("routes respond", func(t *testing.T) {
		// Give app enough state for health check to pass
		healthyApp := &Application{
			API:     api.NewHandler(api.HandlerConfig{}),
			Config:  &mockConfigStoreForHealth{},
			Auth:    createHealthyAuthService(),
			Version: "test",
		}
		srv := CreateHTTPServer(healthyApp, 9090)

		// Use httptest to exercise the handler
		ts := httptest.NewServer(srv.Handler)
		defer ts.Close()

		// Health endpoint should respond 200
		resp, err := http.Get(ts.URL + "/health")
		testutil.AssertNoError(t, err)
		defer resp.Body.Close()
		testutil.AssertEqual(t, http.StatusOK, resp.StatusCode)

		// Root endpoint should respond (API handler)
		resp2, err := http.Get(ts.URL + "/api/test")
		testutil.AssertNoError(t, err)
		defer resp2.Body.Close()
		testutil.AssertTrue(t, resp2.StatusCode > 0, "Should get a status code from root handler")
	})

	t.Run("different port", func(t *testing.T) {
		srv := CreateHTTPServer(app, 3000)
		testutil.AssertEqual(t, ":3000", srv.Addr)
	})
}
