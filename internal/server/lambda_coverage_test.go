package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/mocks"
	"github.com/LeanerCloud/CUDly/internal/testutil"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/mock"
)

func TestIsTextContentType(t *testing.T) {
	tests := []struct {
		ct       string
		expected bool
	}{
		{"text/html", true},
		{"text/plain", true},
		{"text/css", true},
		{"text/javascript", true},
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/javascript", true},
		{"application/xml", true},
		{"image/svg+xml", true},
		{"image/png", false},
		{"image/jpeg", false},
		{"application/octet-stream", false},
		{"font/woff2", false},
	}
	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			testutil.AssertEqual(t, tt.expected, isTextContentType(tt.ct))
		})
	}
}

func TestServeLambdaStatic_Found(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{
		"index.html": "<html>hello</html>",
		"app.js":     "var x=1;",
	})

	app := &Application{staticDir: dir}

	// Text file — body should be plain string
	resp, err := app.serveLambdaStatic("/index.html")
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, 200, resp.StatusCode)
	testutil.AssertEqual(t, false, resp.IsBase64Encoded)
	testutil.AssertContains(t, resp.Body, "<html>")
}

// TestLambdaSecurityHeaders_IncludesCSP locks in that Lambda HTML responses
// carry a Content-Security-Policy header with frame-ancestors 'none'.
// Without it, the meta-tag CSP in index.html can't enforce frame-ancestors
// (browsers ignore that directive in <meta>), leaving the Lambda deploy
// unprotected against clickjacking. See issues/8.
func TestLambdaSecurityHeaders_IncludesCSP(t *testing.T) {
	h := lambdaSecurityHeaders()
	csp, ok := h["Content-Security-Policy"]
	testutil.AssertTrue(t, ok, "lambdaSecurityHeaders must set Content-Security-Policy")
	testutil.AssertContains(t, csp, "frame-ancestors 'none'")
	testutil.AssertContains(t, csp, "default-src 'self'")
}

func TestServeLambdaStatic_BinaryFile(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{
		"index.html": "<html/>",
		"img.png":    "\x89PNG\x0d\x0a",
	})

	app := &Application{staticDir: dir}

	resp, err := app.serveLambdaStatic("/img.png")
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, 200, resp.StatusCode)
	testutil.AssertEqual(t, true, resp.IsBase64Encoded)
}

func TestServeLambdaStatic_NotFound(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})

	app := &Application{staticDir: dir}

	resp, err := app.serveLambdaStatic("/missing.png")
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, 404, resp.StatusCode)
}

func TestHandleLambdaHTTPEvent_StaticPath(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html>spa</html>"})

	app := &Application{
		API:       api.NewHandler(api.HandlerConfig{}),
		staticDir: dir,
	}

	rawEvent := json.RawMessage(`{
		"requestContext": {"http": {"method": "GET"}},
		"rawPath": "/dashboard",
		"headers": {}
	}`)

	ctx := testutil.TestContext(t)
	resp, err := app.handleLambdaHTTPEvent(ctx, rawEvent)
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, 200, resp.StatusCode)
}

// TestHandleLambdaHTTPEvent_DecodesBase64FormBody is a regression test for the
// Lambda Function URL body-decode gap (follow-up to #889): AWS delivers POST
// bodies base64-encoded whenever the Content-Type isn't recognized as plain
// text -- the inbound mirror of the isTextContentType decision this file
// already makes for outbound responses. Before the fix, handleLambdaHTTPEvent
// passed the raw base64 blob straight through to the API router, so
// resolveApprovalToken (internal/api/router.go) could never recover the
// "token=..." pair from the one-click revoke confirmation form's
// x-www-form-urlencoded POST body: every revoke via the email link 401'd in
// Lambda Function URL mode (the primary deploy mode), even though the same
// flow worked under the HTTP/Fargate adapter (which never base64-encodes the
// body it builds).
//
// This drives the real failing scenario end-to-end: a base64-encoded,
// form-urlencoded POST body hitting POST /api/purchases/revoke/{execID}.
// With no session, revokeViaEmailToken (internal/api/handler_purchases.go)
// 401s with "sign in or use the revocation link..." when the token fails to
// resolve, or 401s with the DIFFERENT message "sign in with the account's
// contact email..." from authorizeApprovalAction once the token resolves and
// the (session-only) actor lookup fails. Only the second message is reachable
// once the token has actually been parsed out of the decoded body, so
// asserting it proves the fix; asserting the pre-fix code fails this test
// caught the bug (verified manually before landing the fix).
func TestHandleLambdaHTTPEvent_DecodesBase64FormBody(t *testing.T) {
	execID := "11111111-1111-1111-1111-111111111111"
	exec := &config.PurchaseExecution{
		ExecutionID: execID,
		Status:      "completed",
	}

	mockStore := new(mocks.MockConfigStore)
	mockStore.On("GetExecutionByID", mock.Anything, execID).Return(exec, nil)

	app := &Application{
		API: api.NewHandler(api.HandlerConfig{ConfigStore: mockStore}),
	}

	encodedBody := base64.StdEncoding.EncodeToString([]byte("token=body-token"))
	request := events.LambdaFunctionURLRequest{
		RawPath: "/api/purchases/revoke/" + execID,
		Headers: map[string]string{"content-type": "application/x-www-form-urlencoded"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/purchases/revoke/" + execID,
			},
		},
		Body:            encodedBody,
		IsBase64Encoded: true,
	}
	rawEvent, err := json.Marshal(request)
	testutil.AssertNoError(t, err)

	ctx := testutil.TestContext(t)
	resp, err := app.handleLambdaHTTPEvent(ctx, rawEvent)
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, 401, resp.StatusCode)
	testutil.AssertContains(t, resp.Body, "sign in with the account's contact email")
	testutil.AssertTrue(t, !strings.Contains(resp.Body, "revocation link from the notification email"),
		"token must have been resolved from the decoded body, not left empty")

	mockStore.AssertExpectations(t)
}

func TestHandleLambdaEvent_UnknownEventRouteToScheduled(t *testing.T) {
	// "unknown" event type routes to handleLambdaScheduledEvent, which
	// needs a parseable action. Empty object will fail ParseScheduledEvent.
	app := &Application{
		API: api.NewHandler(api.HandlerConfig{}),
	}

	rawEvent := json.RawMessage(`{"random_key": "random_value"}`)
	ctx := context.Background()
	_, err := app.HandleLambdaEvent(ctx, rawEvent)
	// Unknown action → error from ParseScheduledEvent
	testutil.AssertError(t, err)
}
