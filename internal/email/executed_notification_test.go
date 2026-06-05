package email

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// executedNotificationData returns a representative NotificationData for the
// post-execution notification render tests (issue #291). The revocation token
// deliberately contains characters that MUST be percent-encoded by the
// template's {{urlquery}} pipeline so the tests can assert escaping.
func executedNotificationData() NotificationData {
	return NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		ExecutionID:      "exec-12345",
		TotalSavings:     420.50,
		TotalUpfrontCost: 1200.00,
		RecipientEmail:   "contact@example.com",
		CCEmails:         []string{"notify@example.com", "requester@example.com"},
		RevocationToken:  "tok en/with+special=chars",
		RequestedByEmail: "requester@example.com",
		RequestedByName:  "Requester R",
		RequestedAt:      "2026-06-01T10:00:00Z",
		ExecutedBy:       "admin@example.com",
		ExecutedAt:       "2026-06-01T10:05:00Z",
		Recommendations: []RecommendationSummary{
			{
				Service:        "ec2",
				ResourceType:   "m5.large",
				Region:         "us-east-1",
				Count:          4,
				Term:           3,
				Payment:        "all-upfront",
				UpfrontCost:    1200.00,
				MonthlySavings: 420.50,
				AccountLabel:   "AWS 540659244915",
			},
		},
	}
}

// escapedToken is the exact rendered form of the test token after
// url.QueryEscape (the {{urlquery}} func) followed by html/template's
// contextual auto-escaping: space -> '+' -> '&#43;', '/' -> %2F, '+' -> %2B,
// '=' -> %3D. Both the text and HTML halves use html/template, so both render
// the token identically.
const escapedToken = "tok&#43;en%2Fwith%2Bspecial%3Dchars"

// rawTokenFragment is a substring of the un-escaped token that MUST NOT appear
// verbatim in a correctly-escaped render (it contains a raw '/').
const rawTokenFragment = "tok en/with+special=chars"

func TestRenderPurchaseExecutedNotificationEmail_RevokeURLPresent(t *testing.T) {
	data := executedNotificationData()

	body, err := RenderPurchaseExecutedNotificationEmail(data)
	require.NoError(t, err)

	// The one-click revoke route is rendered with the execution ID and the
	// url-escaped token.
	assert.Contains(t, body, "/api/purchases/revoke/exec-12345?token=")
	// Token is url-escaped: percent-encoded fragments present...
	assert.Contains(t, body, "%2F", "'/' must be percent-encoded")
	assert.Contains(t, body, "%2B", "'+' must be percent-encoded")
	assert.Contains(t, body, "%3D", "'=' must be percent-encoded")
	assert.Contains(t, body, escapedToken)
	// ...and the raw token never leaks verbatim into the URL.
	assert.NotContains(t, body, rawTokenFragment)

	// Recipient summary (To + Cc) is surfaced for the executed-by / requested-by
	// context block.
	assert.Contains(t, body, "admin@example.com")     // executed by
	assert.Contains(t, body, "requester@example.com") // requested by
	assert.Contains(t, body, "Requester R")
}

func TestRenderPurchaseExecutedNotificationEmailHTML_RevokeURLPresent(t *testing.T) {
	data := executedNotificationData()

	body, err := RenderPurchaseExecutedNotificationEmailHTML(data)
	require.NoError(t, err)

	// HTML half renders the revoke CTA anchor with the escaped token.
	assert.Contains(t, body, "/api/purchases/revoke/exec-12345?token="+escapedToken)
	assert.Contains(t, body, "Revoke this purchase")
	assert.Contains(t, body, "%2F")
	assert.Contains(t, body, "%2B")
	assert.Contains(t, body, "%3D")
	assert.NotContains(t, body, rawTokenFragment)
}

func TestRenderPurchaseExecutedNotificationEmail_NoRevokeWhenTokenAbsent(t *testing.T) {
	data := executedNotificationData()
	data.RevocationToken = ""

	body, err := RenderPurchaseExecutedNotificationEmail(data)
	require.NoError(t, err)
	htmlBody, err := RenderPurchaseExecutedNotificationEmailHTML(data)
	require.NoError(t, err)

	// With no token the revocation panel is omitted entirely in both halves.
	assert.NotContains(t, body, "/api/purchases/revoke/")
	assert.NotContains(t, body, "REVOCATION WINDOW")
	assert.NotContains(t, htmlBody, "/api/purchases/revoke/")
	assert.NotContains(t, htmlBody, "Revoke this purchase")
}

func TestRenderPurchaseExecutedNotificationEmail_ExpiryNoteDeferred(t *testing.T) {
	data := executedNotificationData()
	// RevocationWindowClosesAt is deferred to the sibling AWS-revert work; when
	// empty (the present default) the templates omit the expiry sentence.
	data.RevocationWindowClosesAt = ""

	body, err := RenderPurchaseExecutedNotificationEmail(data)
	require.NoError(t, err)
	htmlBody, err := RenderPurchaseExecutedNotificationEmailHTML(data)
	require.NoError(t, err)

	assert.NotContains(t, body, "You can revoke this purchase until")
	assert.NotContains(t, htmlBody, "You can revoke this purchase until")
	// The revoke link itself is still present even without the expiry note.
	assert.Contains(t, body, "/api/purchases/revoke/exec-12345")
}

func TestRenderPurchaseExecutedNotificationEmail_ExpiryNoteWhenPopulated(t *testing.T) {
	data := executedNotificationData()
	// If/when #804 populates the window, the expiry sentence renders.
	data.RevocationWindowClosesAt = "2026-06-08 10:05 UTC"

	body, err := RenderPurchaseExecutedNotificationEmail(data)
	require.NoError(t, err)
	htmlBody, err := RenderPurchaseExecutedNotificationEmailHTML(data)
	require.NoError(t, err)

	assert.Contains(t, body, "You can revoke this purchase until 2026-06-08 10:05 UTC")
	assert.Contains(t, htmlBody, "2026-06-08 10:05 UTC")
}

func TestBuildExecutedNotificationSubject_SingleAndMulti(t *testing.T) {
	single := executedNotificationData()
	subject := buildExecutedNotificationSubject(single)
	assert.Equal(t, "[CUDly] Purchase executed: ec2 m5.large in us-east-1", subject)
	assert.True(t, strings.HasPrefix(subject, "[CUDly] Purchase executed:"))

	multi := executedNotificationData()
	multi.Recommendations = append(multi.Recommendations, RecommendationSummary{
		Service: "rds", ResourceType: "db.r5.large", Region: "eu-west-1",
	})
	multiSubject := buildExecutedNotificationSubject(multi)
	assert.Equal(t, "[CUDly] Purchase executed (2 commitment(s))", multiSubject)
}
