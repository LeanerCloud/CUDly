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

func validUnsubToken(t *testing.T, email, scope string) string {
	t.Helper()
	t.Setenv("NOTIFICATION_MUTE_SECRET", "handler-notifications-test-secret")
	// Resolve the key the same way the handler does so the generated token verifies.
	key, err := common.ResolveMuteSecret()
	require.NoError(t, err)
	return common.DeriveMuteToken(key, email, scope)
}

func TestUnsubscribeHandler_Success(t *testing.T) {
	ctx := context.Background()
	email := "user@example.com"
	scope := string(common.ScopePurchaseApprovals)
	token := validUnsubToken(t, email, scope)

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

func TestUnsubscribeHandler_POSTRejectsInvalidOneClickBody(t *testing.T) {
	ctx := context.Background()
	email := "user@example.com"
	scope := string(common.ScopePurchaseApprovals)
	token := validUnsubToken(t, email, scope)

	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertNotCalled(t, "UpsertNotificationMute") })
	h := &Handler{config: mockStore}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"},
		},
		QueryStringParameters: map[string]string{
			"token": token,
			"email": email,
			"scope": scope,
		},
		Body: "List-Unsubscribe=Not-One-Click",
	}
	_, err := h.unsubscribeHandler(ctx, req, nil)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestUnsubscribeHandler_ForgedToken_Returns401(t *testing.T) {
	t.Setenv("NOTIFICATION_MUTE_SECRET", "handler-forged-token-test-secret")
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

func TestUnsubscribeHandler_MissingSecret_FailsClosed(t *testing.T) {
	// A missing NOTIFICATION_MUTE_SECRET must fail closed in every environment.
	// It returns a server-side error (500), never a 401/200, and never reaches
	// the store.
	t.Setenv("NOTIFICATION_MUTE_SECRET", "")
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	t.Cleanup(func() {
		mockStore.AssertNotCalled(t, "UpsertNotificationMute")
	})
	h := &Handler{config: mockStore}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"token": strings.Repeat("a", 64),
			"email": "user@example.com",
			"scope": string(common.ScopePurchaseApprovals),
		},
	}
	_, err := r.unsubscribeHandler(ctx, req, nil)
	require.Error(t, err)
	_, isClient := IsClientError(err)
	assert.False(t, isClient, "missing secret in production is a 500, not a client error")
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

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			// The scope guard fires before token verification, so any non-empty
			// token reaches it; the value is irrelevant to this test.
			"token": strings.Repeat("a", 64),
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
	token := validUnsubToken(t, email, scope)

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
	t.Setenv("NOTIFICATION_MUTE_SECRET", "router-unsubscribe-test-secret")
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

func TestRouter_UnsubscribePOSTRoute_MutesSignedRecipientAndScope(t *testing.T) {
	ctx := context.Background()
	email := "ri-approver@example.com"
	scope := string(common.ScopeRIExchangeApprovals)
	token := validUnsubToken(t, email, scope)

	mockStore := new(MockConfigStore)
	mockStore.On("UpsertNotificationMute", ctx, email, scope, token).Return(nil).Once()
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	r := NewRouter(&Handler{config: mockStore})

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/notifications/unsubscribe",
			},
		},
		QueryStringParameters: map[string]string{
			"token": token,
			"email": email,
			"scope": scope,
		},
		Body: "List-Unsubscribe=One-Click",
	}
	result, err := r.Route(ctx, "POST", "/api/notifications/unsubscribe", req)
	require.NoError(t, err)
	_, ok := result.(*rawResponse)
	require.True(t, ok)
}
