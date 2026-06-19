package api

import (
	"context"
	"fmt"
	"html/template"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

// mutePageCSP is the Content-Security-Policy for the unsubscribe confirmation
// page. The page is intentionally minimal (no external scripts or styles), so
// we can lock it down tightly.
const mutePageCSP = "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'"

// unsubscribeConfirmTmpl is the HTML confirmation page rendered after a
// successful one-click unsubscribe. No user-supplied data is interpolated via
// {{.}} without html/template escaping, so there is no XSS vector.
var unsubscribeConfirmTmpl = template.Must(template.New("unsub").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Unsubscribed</title>
<style>
body{font-family:sans-serif;max-width:480px;margin:80px auto;padding:0 1rem;color:#222}
h1{font-size:1.4rem}
p{line-height:1.6}
</style>
</head>
<body>
<h1>You have been unsubscribed.</h1>
<p>You will no longer receive <strong>{{.ScopeLabel}}</strong> emails at this address.</p>
<p>This preference is saved. You do not need to click again.</p>
</body>
</html>
`))

// scopeLabel returns a human-readable label for a notification scope.
func scopeLabel(scope string) string {
	switch scope {
	case string(common.ScopePurchaseApprovals):
		return "purchase approval request"
	case string(common.ScopeRIExchangeApprovals):
		return "RI exchange approval request"
	default:
		return "notification"
	}
}

// unsubscribeHandler handles GET /api/notifications/unsubscribe.
// The URL carries a signed token that encodes (email, scope); the handler
// verifies the HMAC, upserts the mute row, and returns a confirmation page.
//
// Auth: AuthPublic (token-based, no login required — mirrors approve/cancel).
func (h *Handler) unsubscribeHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	token := req.QueryStringParameters["token"]
	email := req.QueryStringParameters["email"]
	scope := req.QueryStringParameters["scope"]

	if token == "" || email == "" || scope == "" {
		return nil, NewClientError(400, "token, email and scope are required")
	}

	// Reject unknown scopes early so we never create phantom rows.
	validScope := scope == string(common.ScopePurchaseApprovals) ||
		scope == string(common.ScopeRIExchangeApprovals)
	if !validScope {
		return nil, NewClientError(400, fmt.Sprintf("unknown notification scope: %s", scope))
	}

	// Resolve the HMAC key with the fail-closed production policy: a missing
	// NOTIFICATION_MUTE_SECRET in production is a server misconfiguration, not a
	// client error, and must never silently verify against a well-known key.
	key, err := common.ResolveMuteSecret()
	if err != nil {
		logging.Errorf("notifications/unsubscribe: %v", err)
		return nil, fmt.Errorf("unsubscribe is not configured: %w", err)
	}
	if !common.VerifyMuteToken(key, email, scope, token) {
		logging.Warnf("notifications/unsubscribe: invalid token for scope=%s", scope)
		return nil, NewClientError(401, "invalid or expired unsubscribe token")
	}

	if err := h.config.UpsertNotificationMute(ctx, email, scope, token); err != nil {
		logging.Errorf("notifications/unsubscribe: store error: %v", err)
		return nil, fmt.Errorf("could not save unsubscribe preference: %w", err)
	}

	logging.Infof("notifications/unsubscribe: muted scope=%s for %s", scope, redactEmailLocal(email))

	var buf strings.Builder
	if err := unsubscribeConfirmTmpl.Execute(&buf, struct{ ScopeLabel string }{
		ScopeLabel: scopeLabel(scope),
	}); err != nil {
		return nil, fmt.Errorf("render unsubscribe page: %w", err)
	}

	return &rawResponse{
		contentType: "text/html; charset=utf-8",
		body:        buf.String(),
		csp:         mutePageCSP,
	}, nil
}

// redactEmailLocal returns just the domain part with the local masked, e.g.
// "us***@example.com". Reuses the same masking logic as email/sender.go but
// without importing that package into api (avoids a dependency cycle).
func redactEmailLocal(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at:] // includes '@'
	if len(local) <= 2 {
		return "***" + domain
	}
	return local[:2] + "***" + domain
}
