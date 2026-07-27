package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/LeanerCloud/CUDly/internal/testutil"
)

func TestDetectLambdaEventType(t *testing.T) {
	tests := []struct {
		name         string
		rawEvent     string
		expectedType string
	}{
		{
			name: "Lambda Function URL request",
			rawEvent: `{
				"requestContext": {
					"http": {
						"method": "GET"
					}
				}
			}`,
			expectedType: "http",
		},
		{
			name: "API Gateway v2 request",
			rawEvent: `{
				"httpMethod": "POST",
				"path": "/api/test"
			}`,
			expectedType: "http",
		},
		{
			name: "SQS event",
			rawEvent: `{
				"Records": [
					{
						"eventSource": "aws:sqs",
						"body": "{\"test\": \"data\"}"
					}
				]
			}`,
			expectedType: "sqs",
		},
		{
			name: "EventBridge scheduled event",
			rawEvent: `{
				"source": "aws.events",
				"detail-type": "Scheduled Event"
			}`,
			expectedType: "scheduled",
		},
		{
			name: "Custom scheduled event",
			rawEvent: `{
				"action": "collect_recommendations"
			}`,
			expectedType: "scheduled",
		},
		{
			name:         "Unknown event defaults to scheduled",
			rawEvent:     `{"unknown": "event"}`,
			expectedType: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventType := detectLambdaEventType(json.RawMessage(tt.rawEvent))
			testutil.AssertEqual(t, tt.expectedType, eventType)
		})
	}
}

func TestHandleLambdaHTTPEvent(t *testing.T) {
	tests := []struct {
		name           string
		rawEvent       string
		expectError    bool
		expectedStatus int
	}{
		{
			name: "valid HTTP request",
			rawEvent: `{
				"requestContext": {
					"http": {
						"method": "GET",
						"path": "/health"
					},
					"timeEpoch": 1234567890
				},
				"rawPath": "/health",
				"headers": {}
			}`,
			expectError:    false,
			expectedStatus: 200,
		},
		{
			name:           "invalid JSON",
			rawEvent:       `{invalid json}`,
			expectError:    false,
			expectedStatus: 400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testutil.TestContext(t)

			// Create minimal app with mocked API handler
			app := &Application{
				API: api.NewHandler(api.HandlerConfig{}),
			}

			resp, err := app.handleLambdaHTTPEvent(ctx, json.RawMessage(tt.rawEvent))

			if tt.expectError {
				testutil.AssertError(t, err)
			} else {
				testutil.AssertNoError(t, err)
				if resp != nil {
					testutil.AssertEqual(t, tt.expectedStatus, resp.StatusCode)
				}
			}
		})
	}
}

func TestHandleLambdaSQSEvent(t *testing.T) {
	tests := []struct {
		setupMocks  func(*testutil.MockPurchaseManager)
		name        string
		rawEvent    string
		expectError bool
	}{
		{
			name: "valid SQS event with single message",
			rawEvent: `{
				"Records": [
					{
						"messageId": "msg-123",
						"eventSource": "aws:sqs",
						"body": "{\"purchase_id\": \"123\"}"
					}
				]
			}`,
			setupMocks: func(p *testutil.MockPurchaseManager) {
				p.ProcessMessageFunc = func(ctx context.Context, body string) error {
					return nil
				}
			},
			expectError: false,
		},
		{
			name: "valid SQS event with multiple messages",
			rawEvent: `{
				"Records": [
					{
						"messageId": "msg-123",
						"eventSource": "aws:sqs",
						"body": "{\"purchase_id\": \"123\"}"
					},
					{
						"messageId": "msg-456",
						"eventSource": "aws:sqs",
						"body": "{\"purchase_id\": \"456\"}"
					}
				]
			}`,
			setupMocks: func(p *testutil.MockPurchaseManager) {
				callCount := 0
				p.ProcessMessageFunc = func(ctx context.Context, body string) error {
					callCount++
					return nil
				}
			},
			expectError: false,
		},
		{
			name:        "invalid JSON",
			rawEvent:    `{invalid json}`,
			setupMocks:  func(p *testutil.MockPurchaseManager) {},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testutil.TestContext(t)

			mockPurchase := &testutil.MockPurchaseManager{}
			tt.setupMocks(mockPurchase)

			app := &Application{
				Purchase: mockPurchase,
			}

			_, err := app.handleLambdaSQSEvent(ctx, json.RawMessage(tt.rawEvent))

			if tt.expectError {
				testutil.AssertError(t, err)
			} else {
				testutil.AssertNoError(t, err)
			}
		})
	}
}

// testOwnerToken is a fixed, well-formed collection owner token. It has to be
// a real UUID because ParseScheduledEvent rejects malformed non-empty tokens
// at the boundary rather than letting them reach the UUID-typed column.
const testOwnerToken = "6b1f2c34-5d6e-4a7b-8c9d-0e1f2a3b4c5d"

func TestHandleLambdaScheduledEvent(t *testing.T) {
	tests := []struct {
		setupMocks    func(*testutil.MockScheduler)
		name          string
		rawEvent      string
		expectedToken string
		expectError   bool
	}{
		{
			name:     "collect_recommendations event",
			rawEvent: `{"action": "collect_recommendations"}`,
			setupMocks: func(s *testutil.MockScheduler) {
				s.CollectRecommendationsFunc = func(ctx context.Context, ownerToken string) (*scheduler.CollectResult, error) {
					return &scheduler.CollectResult{
						Recommendations: 10,
						TotalSavings:    500.0,
					}, nil
				}
			},
			expectError: false,
		},
		{
			name:     "EventBridge format",
			rawEvent: `{"source": "aws.events", "action": "collect_recommendations"}`,
			setupMocks: func(s *testutil.MockScheduler) {
				s.CollectRecommendationsFunc = func(ctx context.Context, ownerToken string) (*scheduler.CollectResult, error) {
					return &scheduler.CollectResult{
						Recommendations: 5,
						TotalSavings:    250.0,
					}, nil
				}
			},
			expectError: false,
		},
		{
			// Issue #261: the async self-invoke payload's owner_token must
			// survive parsing and reach CollectRecommendations, which is what
			// scopes the deferred clear to this run. Asserting the token the
			// scheduler actually received (rather than only that the event
			// parses) means dropping it anywhere between ParseScheduledEvent
			// and the scheduler fails the test instead of silently stranding
			// the marker for the full 5-minute recovery window.
			name:          "async self-invoke carries owner_token through to the scheduler",
			rawEvent:      `{"source": "aws.events", "action": "collect_recommendations", "owner_token": "` + testOwnerToken + `"}`,
			expectedToken: testOwnerToken,
			expectError:   false,
		},
		{
			// A non-empty owner_token that is not a UUID can only come from a
			// corrupt payload (asyncInvokeSelf always sends a uuid.New()), and
			// could never match a marker owner, so it is rejected at the
			// boundary rather than reaching the UUID-typed persistence layer.
			name:        "malformed owner_token is rejected at the boundary",
			rawEvent:    `{"source": "aws.events", "action": "collect_recommendations", "owner_token": "not-a-uuid"}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testutil.TestContext(t)

			mockScheduler := &testutil.MockScheduler{}
			// Capture the token the scheduler was handed and assert on it from
			// the subtest goroutine rather than inside the mock callback, so a
			// failed assertion never calls FailNow off the owning goroutine.
			gotToken := ""
			collected := false
			mockScheduler.CollectRecommendationsFunc = func(ctx context.Context, ownerToken string) (*scheduler.CollectResult, error) {
				gotToken = ownerToken
				collected = true
				return &scheduler.CollectResult{}, nil
			}
			if tt.setupMocks != nil {
				tt.setupMocks(mockScheduler)
			}

			app := &Application{
				Scheduler: mockScheduler,
			}

			_, err := app.handleLambdaScheduledEvent(ctx, json.RawMessage(tt.rawEvent))

			if tt.expectError {
				testutil.AssertError(t, err)
				return
			}
			testutil.AssertNoError(t, err)
			if tt.expectedToken != "" {
				if !collected {
					t.Fatal("expected CollectRecommendations to be called")
				}
				testutil.AssertEqual(t, tt.expectedToken, gotToken)
			}
		})
	}
}

// TestHandleLambdaEvent_UnknownEventReturnsError is a regression test for
// 04-N4: before the fix, unrecognized payloads were silently routed to
// handleLambdaScheduledEvent, which then failed with "unknown scheduled task
// action" -- masking the real root cause. The fix returns a distinct error
// so callers (and logs) see "unrecognized Lambda event shape" instead.
func TestHandleLambdaEvent_UnknownEventReturnsError(t *testing.T) {
	ctx := testutil.TestContext(t)

	app := &Application{
		API: api.NewHandler(api.HandlerConfig{}),
	}

	_, err := app.HandleLambdaEvent(ctx, json.RawMessage(`{"unknown": "event"}`))
	testutil.AssertError(t, err)
	testutil.AssertTrue(t, strings.Contains(err.Error(), "unrecognized"),
		"expected 'unrecognised' in error, got: "+err.Error())
}

func TestHandleLambdaEvent(t *testing.T) {
	tests := []struct {
		setupApp    func(*Application)
		name        string
		rawEvent    string
		expectError bool
	}{
		{
			name: "HTTP event routing",
			rawEvent: `{
				"requestContext": {
					"http": {"method": "GET"}
				}
			}`,
			setupApp: func(app *Application) {
				// API handler will be nil, causing handled response
			},
			expectError: false,
		},
		{
			name: "SQS event routing",
			rawEvent: `{
				"Records": [{
					"eventSource": "aws:sqs",
					"body": "{}"
				}]
			}`,
			setupApp: func(app *Application) {
				app.Purchase = &testutil.MockPurchaseManager{
					ProcessMessageFunc: func(ctx context.Context, body string) error {
						return nil
					},
				}
			},
			expectError: false,
		},
		{
			name:     "Scheduled event routing",
			rawEvent: `{"action": "collect_recommendations"}`,
			setupApp: func(app *Application) {
				app.Scheduler = &testutil.MockScheduler{
					CollectRecommendationsFunc: func(ctx context.Context, ownerToken string) (*scheduler.CollectResult, error) {
						return &scheduler.CollectResult{}, nil
					},
				}
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testutil.TestContext(t)

			app := &Application{
				API: api.NewHandler(api.HandlerConfig{}),
			}
			if tt.setupApp != nil {
				tt.setupApp(app)
			}

			_, err := app.HandleLambdaEvent(ctx, json.RawMessage(tt.rawEvent))

			if tt.expectError {
				testutil.AssertError(t, err)
			} else {
				testutil.AssertNoError(t, err)
			}
		})
	}
}
