package api

import (
	"context"
	"strings"
	"testing"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GET /api/notifications/unsubscribe
// ---------------------------------------------------------------------------

func validUnsubToken(email, scope string) string {
	return common.DeriveMuteToken(nil, email, scope)
}

func TestUnsubscribeHandler_Success(t *testing.T) {
	ctx := context.Background()
	email := "user@example.com"
	scope := string(common.ScopePurchaseApprovals)
	token := validUnsubToken(email, scope)

	mockStore := new(MockConfigStore)
	mockStore.On("UpsertNotificationMute", ctx, email, scope, token).Return(nil)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	h := &Handler{config: mockStore}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"token": token,
			"email": email,
			"scope": scope,
		},
	}
	result, err := r.unsubscribeHandler(ctx, req, nil)
	require.NoError(t, err)
	raw, ok := result.(*rawResponse)
	require.True(t, ok, "expected *rawResponse")
	assert.Equal(t, "text/html; charset=utf-8", raw.contentType)
	assert.Contains(t, raw.body, "Unsubscribed")
	assert.Contains(t, raw.body, "purchase approval request")
}

func TestUnsubscribeHandler_ForgedToken_Returns401(t *testing.T) {
	ctx := context.Background()
	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"token": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"email": "attacker@example.com",
			"scope": string(common.ScopePurchaseApprovals),
		},
	}
	h := &Handler{config: new(MockConfigStore)}
	r := newTestRouter(h)

	_, err := r.unsubscribeHandler(ctx, req, nil)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
}

func TestUnsubscribeHandler_MissingParams_Returns400(t *testing.T) {
	ctx := context.Background()
	h := &Handler{config: new(MockConfigStore)}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"token": "something",
			// email and scope missing
		},
	}
	_, err := r.unsubscribeHandler(ctx, req, nil)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestUnsubscribeHandler_UnknownScope_Returns400(t *testing.T) {
	ctx := context.Background()
	email := "user@example.com"
	scope := "unknown_scope"
	// Use the dev-key token so HMAC passes but scope guard fires first.
	token := common.DeriveMuteToken(nil, email, scope)

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"token": token,
			"email": email,
			"scope": scope,
		},
	}
	h := &Handler{config: new(MockConfigStore)}
	r := newTestRouter(h)

	_, err := r.unsubscribeHandler(ctx, req, nil)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestUnsubscribeHandler_StoreError_Returns500(t *testing.T) {
	ctx := context.Background()
	email := "user@example.com"
	scope := string(common.ScopePurchaseApprovals)
	token := validUnsubToken(email, scope)

	mockStore := new(MockConfigStore)
	mockStore.On("UpsertNotificationMute", mock.Anything, email, scope, token).
		Return(assert.AnError)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	h := &Handler{config: mockStore}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"token": token,
			"email": email,
			"scope": scope,
		},
	}
	_, err := r.unsubscribeHandler(ctx, req, nil)
	require.Error(t, err)
	_, isClient := IsClientError(err)
	assert.False(t, isClient, "store error should be a 500, not a client error")
}

// ---------------------------------------------------------------------------
// scopeLabel helper
// ---------------------------------------------------------------------------

func TestScopeLabel_KnownScopes(t *testing.T) {
	assert.Equal(t, "purchase approval request", scopeLabel(string(common.ScopePurchaseApprovals)))
	assert.Equal(t, "RI exchange approval request", scopeLabel(string(common.ScopeRIExchangeApprovals)))
	assert.Equal(t, "notification", scopeLabel("bogus"))
}

// ---------------------------------------------------------------------------
// redactEmailLocal
// ---------------------------------------------------------------------------

func TestRedactEmailLocal(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"user@example.com", "us***@example.com"},
		{"ab@x.com", "***@x.com"},
		{"a@x.com", "***@x.com"},
		{"noemail", "***"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, redactEmailLocal(c.in), "input: %s", c.in)
	}
}

// ---------------------------------------------------------------------------
// isPublicEndpoint includes /api/notifications/unsubscribe
// ---------------------------------------------------------------------------

func TestIsPublicEndpoint_UnsubscribePath(t *testing.T) {
	h := &Handler{}
	assert.True(t, h.isPublicEndpoint("/api/notifications/unsubscribe"))
	assert.True(t, h.isPublicEndpoint("/api/notifications/unsubscribe?token=x&email=y&scope=z"))
}

// ---------------------------------------------------------------------------
// Route is registered (router smoke test)
// ---------------------------------------------------------------------------

func TestRouter_UnsubscribeRoute_Registered(t *testing.T) {
	// Verify the route is wired: an unsigned token returns 401, which can only
	// happen if the router dispatched to the correct handler.
	ctx := context.Background()
	h := &Handler{config: new(MockConfigStore)}
	r := NewRouter(h)

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/notifications/unsubscribe",
			},
		},
		QueryStringParameters: map[string]string{
			"token": strings.Repeat("a", 64),
			"email": "user@example.com",
			"scope": string(common.ScopePurchaseApprovals),
		},
	}
	_, err := r.Route(ctx, "GET", "/api/notifications/unsubscribe", req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
}
