package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/LeanerCloud/CUDly/internal/testutil"
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
