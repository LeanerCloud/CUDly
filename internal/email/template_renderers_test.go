package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderPasswordResetEmail(t *testing.T) {
	email := "user@example.com"
	resetURL := "https://dashboard.example.com/reset?token=abc123"

	result, err := RenderPasswordResetEmail(email, resetURL)

	require.NoError(t, err)
	assert.Contains(t, result, email)
	assert.Contains(t, result, resetURL)
	assert.Contains(t, result, "Password Reset")
	assert.Contains(t, result, "CUDly")
}

func TestRenderWelcomeEmail(t *testing.T) {
	email := "user@example.com"
	dashboardURL := "https://dashboard.example.com"
	role := "admin"

	result, err := RenderWelcomeEmail(email, dashboardURL, role)

	require.NoError(t, err)
	assert.Contains(t, result, dashboardURL)
	assert.Contains(t, result, role)
	assert.Contains(t, result, "Welcome")
	assert.Contains(t, result, "CUDly")
}

func TestRenderNewRecommendationsEmail(t *testing.T) {
	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalSavings: 1500.50,
		Recommendations: []RecommendationSummary{
			{
				Service:        "rds",
				ResourceType:   "db.r5.large",
				Engine:         "postgres",
				Region:         "us-east-1",
				Count:          2,
				MonthlySavings: 300.00,
			},
			{
				Service:        "ec2",
				ResourceType:   "m5.xlarge",
				Region:         "us-west-2",
				Count:          5,
				MonthlySavings: 500.00,
			},
		},
	}

	result, err := RenderNewRecommendationsEmail(data)

	require.NoError(t, err)
	assert.Contains(t, result, data.DashboardURL)
	assert.Contains(t, result, "1500.50")
	assert.Contains(t, result, "db.r5.large")
	assert.Contains(t, result, "postgres")
	assert.Contains(t, result, "us-east-1")
	assert.Contains(t, result, "m5.xlarge")
	assert.Contains(t, result, "rds")
	assert.Contains(t, result, "ec2")
}

func TestRenderNewRecommendationsEmail_WithUpfrontCost(t *testing.T) {
	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		TotalSavings:     1000.00,
		TotalUpfrontCost: 5000.00,
		Recommendations:  []RecommendationSummary{},
	}

	result, err := RenderNewRecommendationsEmail(data)

	require.NoError(t, err)
	assert.Contains(t, result, "5000.00")
	assert.Contains(t, result, "Upfront Cost")
}

func TestRenderNewRecommendationsEmail_NoRecommendations(t *testing.T) {
	data := NotificationData{
		DashboardURL:    "https://dashboard.example.com",
		TotalSavings:    0,
		Recommendations: []RecommendationSummary{},
	}

	result, err := RenderNewRecommendationsEmail(data)

	require.NoError(t, err)
	assert.Contains(t, result, data.DashboardURL)
}

func TestRenderScheduledPurchaseEmail(t *testing.T) {
	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "approval-token-xyz",
		ExecutionID:       "exec-render-test-001",
		TotalSavings:      2000.00,
		TotalUpfrontCost:  8000.00,
		PurchaseDate:      "March 15, 2024",
		DaysUntilPurchase: 7,
		PlanName:          "Production AWS Plan",
		Recommendations: []RecommendationSummary{
			{
				Service:        "rds",
				ResourceType:   "db.r5.2xlarge",
				Engine:         "mysql",
				Region:         "eu-west-1",
				Count:          3,
				MonthlySavings: 600.00,
			},
		},
	}

	result, err := RenderScheduledPurchaseEmail(data)

	require.NoError(t, err)
	assert.Contains(t, result, data.DashboardURL)
	assert.Contains(t, result, data.PurchaseDate)
	assert.Contains(t, result, data.PlanName)
	assert.Contains(t, result, "7")
	assert.Contains(t, result, "db.r5.2xlarge")
	assert.Contains(t, result, "mysql")
	// Cancel link must use the direct API path with execution ID and token.
	assert.Contains(t, result, "/purchases/cancel/exec-render-test-001?token=approval-token-xyz")
	// Token must not appear in any other URL (review/edit or pause).
	assert.NotContains(t, result, "action=edit")
	assert.NotContains(t, result, "action=pause")
	assert.NotContains(t, result, "action=cancel")
}

func TestRenderPurchaseConfirmationEmail(t *testing.T) {
	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		TotalSavings:     1200.00,
		TotalUpfrontCost: 4800.00,
		PlanName:         "Savings Plan",
		Recommendations: []RecommendationSummary{
			{
				Service:        "elasticache",
				ResourceType:   "cache.r5.large",
				Engine:         "redis",
				Region:         "ap-northeast-1",
				Count:          2,
				MonthlySavings: 400.00,
			},
		},
	}

	result, err := RenderPurchaseConfirmationEmail(data)

	require.NoError(t, err)
	assert.Contains(t, result, data.DashboardURL)
	assert.Contains(t, result, "Purchases Completed")
	assert.Contains(t, result, "1200.00")
	assert.Contains(t, result, "4800.00")
	assert.Contains(t, result, "cache.r5.large")
	assert.Contains(t, result, "redis")
	assert.Contains(t, result, "history")
}

func TestRenderPurchaseFailedEmail(t *testing.T) {
	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
		Recommendations: []RecommendationSummary{
			{
				Service:      "opensearch",
				ResourceType: "r5.large.search",
				Region:       "us-east-1",
				Count:        1,
			},
			{
				Service:      "rds",
				ResourceType: "db.m5.large",
				Engine:       "postgres",
				Region:       "us-west-2",
				Count:        2,
			},
		},
	}

	result, err := RenderPurchaseFailedEmail(data)

	require.NoError(t, err)
	assert.Contains(t, result, data.DashboardURL)
	assert.Contains(t, result, "Purchase Failed")
	assert.Contains(t, result, "r5.large.search")
	assert.Contains(t, result, "opensearch")
	assert.Contains(t, result, "db.m5.large")
	assert.Contains(t, result, "postgres")
	assert.Contains(t, result, "history")
}

func TestRenderScheduledPurchaseEmail_WithoutEngine(t *testing.T) {
	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "token",
		PurchaseDate:      "April 1, 2024",
		DaysUntilPurchase: 3,
		PlanName:          "Test Plan",
		Recommendations: []RecommendationSummary{
			{
				Service:        "ec2",
				ResourceType:   "m5.large",
				Region:         "us-east-1",
				Count:          10,
				MonthlySavings: 250.00,
			},
		},
	}

	result, err := RenderScheduledPurchaseEmail(data)

	require.NoError(t, err)
	assert.Contains(t, result, "m5.large")
	assert.NotContains(t, result, "()") // Engine should not appear with empty parens
}

func TestRenderPurchaseConfirmationEmail_NoUpfrontCost(t *testing.T) {
	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		TotalSavings:     500.00,
		TotalUpfrontCost: 0, // No upfront cost
		Recommendations:  []RecommendationSummary{},
	}

	result, err := RenderPurchaseConfirmationEmail(data)

	require.NoError(t, err)
	assert.Contains(t, result, "500.00")
	// Should not contain upfront cost line when it's 0
}

func TestWelcomeUserData_Structure(t *testing.T) {
	data := WelcomeUserData{
		Email:        "user@example.com",
		DashboardURL: "https://dashboard.example.com",
		Role:         "admin",
	}

	assert.Equal(t, "user@example.com", data.Email)
	assert.Equal(t, "https://dashboard.example.com", data.DashboardURL)
	assert.Equal(t, "admin", data.Role)
}

// Issue #287: extended approval-request plain-text template carries
// per-rec Term/Payment/Upfront/AccountLabel + the requested-by header
// + the cancellation-window note.
func TestRenderPurchaseApprovalRequestEmail_NewContextFields_Issue287(t *testing.T) {
	data := NotificationData{
		DashboardURL:           "https://dashboard.example.com",
		ApprovalToken:          "tkn-abc",
		ExecutionID:            "exec-123",
		TotalUpfrontCost:       1234.56,
		TotalSavings:           58.0,
		RequestedByName:        "Cristi M",
		RequestedByEmail:       "cristi@acme.com",
		RequestedAt:            "2026-05-04T14:22:00Z",
		CancellationWindowNote: "Test custom window note about AWS refund policy.",
		AuthorizedApprovers:    []string{"approver@acme.com"},
		Recommendations: []RecommendationSummary{{
			Service: "ec2", ResourceType: "m5.large", Region: "us-east-1",
			Count: 8, Term: 3, Payment: "all-upfront", UpfrontCost: 1234.56,
			MonthlySavings: 58.0, AccountLabel: "AWS 540659244915",
		}},
	}

	body, err := RenderPurchaseApprovalRequestEmail(data)
	require.NoError(t, err)

	// Per-rec lines carry the new fields.
	assert.Contains(t, body, "Term: 3yr")
	assert.Contains(t, body, "Payment: all-upfront")
	assert.Contains(t, body, "Upfront: $1234.56")
	assert.Contains(t, body, "Account: AWS 540659244915")

	// Requested-by header.
	assert.Contains(t, body, "Cristi M")
	assert.Contains(t, body, "cristi@acme.com")
	assert.Contains(t, body, "2026-05-04T14:22:00Z")

	// Custom cancellation-window note overrides the generic fallback.
	assert.Contains(t, body, "Test custom window note about AWS refund policy.")
	assert.NotContains(t, body, "Cancellation windows after approval are limited") // generic fallback absent

	// Labeled URLs preserved (the urlquery output renders &amp; via html/template
	// which is a preexisting cosmetic but functional URL form; assert the
	// labels themselves, not the &/&amp; choice).
	assert.Contains(t, body, "Approve: ")
	assert.Contains(t, body, "Cancel:  ")
	assert.Contains(t, body, "/purchases/approve/exec-123")
	assert.Contains(t, body, "/purchases/cancel/exec-123")

	// Authorized-approvers block survives.
	assert.Contains(t, body, "Authorised approver(s)")
	assert.Contains(t, body, "approver@acme.com")
}

// Issue #287: HTML half of the approval email carries inline-styled
// approve/cancel anchors with the correct href, plus the rec summary
// table and the requested-by line.
func TestRenderPurchaseApprovalRequestEmailHTML_Issue287(t *testing.T) {
	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		ApprovalToken:    "tkn-abc",
		ExecutionID:      "exec-123",
		TotalUpfrontCost: 1234.56,
		TotalSavings:     58.0,
		RequestedByEmail: "cristi@acme.com",
		Recommendations: []RecommendationSummary{{
			Service: "ec2", ResourceType: "m5.large", Region: "us-east-1",
			Count: 8, Term: 3, Payment: "all-upfront", UpfrontCost: 1234.56,
			MonthlySavings: 58.0, AccountLabel: "AWS 540659244915",
		}},
	}

	html, err := RenderPurchaseApprovalRequestEmailHTML(data)
	require.NoError(t, err)

	// Inline-styled approve + cancel anchors with the right hrefs.
	assert.Contains(t, html, `href="https://dashboard.example.com/purchases/approve/exec-123?token=tkn-abc"`)
	assert.Contains(t, html, `href="https://dashboard.example.com/purchases/cancel/exec-123?token=tkn-abc"`)
	assert.Contains(t, html, "Approve this purchase")
	assert.Contains(t, html, "Cancel this purchase")
	// Inline style on at least one button (prove CSS classes aren't relied
	// on — email clients often strip <style>; inline-style is the contract).
	assert.Regexp(t, `<a[^>]*style="[^"]*background:#16a34a[^"]*"[^>]*>Approve this purchase</a>`, html)

	// Rec table cells.
	assert.Contains(t, html, "m5.large")
	assert.Contains(t, html, "us-east-1")
	assert.Contains(t, html, "3yr")
	assert.Contains(t, html, "all-upfront")

	// Summary block + requested-by line.
	assert.Contains(t, html, "1234.56")
	assert.Contains(t, html, "58.00")
	assert.Contains(t, html, "cristi@acme.com")
	assert.Contains(t, html, "Purchase Approval Required")

	// Generic cancellation note fallback (no custom note set).
	assert.Contains(t, html, "Cancellation windows after approval are limited")
}

// Issue #314: when ArcheraEducationURL is set, the three purchase-flow
// templates include an Archera mention with the 7-day enrollment window.

func TestRenderPurchaseApprovalRequestEmail_ArcheraBlock(t *testing.T) {
	data := NotificationData{
		DashboardURL:        "https://dashboard.example.com",
		ApprovalToken:       "tkn-xyz",
		ExecutionID:         "exec-314",
		TotalUpfrontCost:    500.00,
		TotalSavings:        50.00,
		ArcheraEducationURL: "https://dashboard.example.com/archera-insurance",
		Recommendations: []RecommendationSummary{{
			Service: "ec2", ResourceType: "m5.large", Region: "us-east-1",
			Count: 1, MonthlySavings: 50.00,
		}},
	}

	body, err := RenderPurchaseApprovalRequestEmail(data)
	require.NoError(t, err)

	assert.Contains(t, body, "Archera")
	assert.Contains(t, body, "7 day") // template uses "7 days" in approval, "7-day" in confirmation
	assert.Contains(t, body, "https://dashboard.example.com/archera-insurance")
}

func TestRenderPurchaseApprovalRequestEmail_NoArcheraBlock_WhenURLEmpty(t *testing.T) {
	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		ApprovalToken:    "tkn-xyz",
		ExecutionID:      "exec-314b",
		TotalUpfrontCost: 500.00,
		TotalSavings:     50.00,
		// ArcheraEducationURL intentionally empty
		Recommendations: []RecommendationSummary{{
			Service: "ec2", ResourceType: "m5.large", Region: "us-east-1",
			Count: 1, MonthlySavings: 50.00,
		}},
	}

	body, err := RenderPurchaseApprovalRequestEmail(data)
	require.NoError(t, err)

	assert.NotContains(t, body, "Archera Insurance")
	assert.NotContains(t, body, "archera-insurance")
}

func TestRenderPurchaseApprovalRequestEmailHTML_ArcheraBlock(t *testing.T) {
	data := NotificationData{
		DashboardURL:        "https://dashboard.example.com",
		ApprovalToken:       "tkn-xyz",
		ExecutionID:         "exec-314",
		TotalUpfrontCost:    500.00,
		TotalSavings:        50.00,
		ArcheraEducationURL: "https://dashboard.example.com/archera-insurance",
		Recommendations: []RecommendationSummary{{
			Service: "ec2", ResourceType: "m5.large", Region: "us-east-1",
			Count: 1, MonthlySavings: 50.00,
		}},
	}

	html, err := RenderPurchaseApprovalRequestEmailHTML(data)
	require.NoError(t, err)

	assert.Contains(t, html, "Archera")
	assert.Contains(t, html, "7&nbsp;days") // HTML entity used in template for non-breaking space
	assert.Contains(t, html, "https://dashboard.example.com/archera-insurance")
}

func TestRenderPurchaseConfirmationEmail_ArcheraBlock(t *testing.T) {
	data := NotificationData{
		DashboardURL:        "https://dashboard.example.com",
		TotalSavings:        1200.00,
		TotalUpfrontCost:    4800.00,
		ArcheraEducationURL: "https://dashboard.example.com/archera-insurance",
		Recommendations: []RecommendationSummary{{
			Service: "ec2", ResourceType: "m5.large", Region: "us-east-1",
			Count: 2, MonthlySavings: 600.00,
		}},
	}

	body, err := RenderPurchaseConfirmationEmail(data)
	require.NoError(t, err)

	assert.Contains(t, body, "Archera")
	assert.Contains(t, body, "7-day") // confirmation template uses "7-day enrollment window"
	assert.Contains(t, body, "https://archera.ai/signup?mode=cudly")
	assert.Contains(t, body, "https://dashboard.example.com/archera-insurance")
}

// Issue #287: when AuthorizedApprovers is empty the HTML omits the
// approver-warning block (legacy broadcast behaviour preserved).
func TestRenderPurchaseApprovalRequestEmailHTML_NoApprovers(t *testing.T) {
	data := NotificationData{
		DashboardURL:    "https://example.com",
		ApprovalToken:   "tkn",
		ExecutionID:     "exec-1",
		Recommendations: []RecommendationSummary{{Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1}},
	}
	html, err := RenderPurchaseApprovalRequestEmailHTML(data)
	require.NoError(t, err)
	assert.NotContains(t, html, "Authorised approver")
}

// TestRenderPasswordResetEmailHTML covers the HTML half of the password
// reset email added for issue #355. Verifies the CTA button is present,
// the absolute URL appears in both the href and the fallback paragraph,
// and the user's email is in the salutation.
func TestRenderPasswordResetEmailHTML(t *testing.T) {
	html, err := RenderPasswordResetEmailHTML("alice@example.com", "https://dashboard.example/reset-password?token=abc")
	require.NoError(t, err)
	assert.NotEmpty(t, html)

	// CTA button with the right text and href.
	assert.Contains(t, html, `href="https://dashboard.example/reset-password?token=abc"`)
	assert.Contains(t, html, ">Reset your password<")

	// Salutation pulls in the recipient.
	assert.Contains(t, html, "Hello alice@example.com,")

	// Fallback "paste this URL" block carries the full absolute URL so MUAs
	// that strip <a> still let the user complete the flow.
	assert.Contains(t, html, "https://dashboard.example/reset-password?token=abc")
}

// TestRenderUserInviteEmailHTML — same assertions for the invite flow.
func TestRenderUserInviteEmailHTML(t *testing.T) {
	html, err := RenderUserInviteEmailHTML("bob@example.com", "https://dashboard.example/reset-password?token=xyz")
	require.NoError(t, err)
	assert.NotEmpty(t, html)

	assert.Contains(t, html, `href="https://dashboard.example/reset-password?token=xyz"`)
	assert.Contains(t, html, ">Set your password<")
	assert.Contains(t, html, "Hello bob@example.com,")
}

// TestRenderWelcomeEmailHTML — same assertions for the welcome flow.
func TestRenderWelcomeEmailHTML(t *testing.T) {
	html, err := RenderWelcomeEmailHTML("carol@example.com", "https://dashboard.example", "admin")
	require.NoError(t, err)
	assert.NotEmpty(t, html)

	assert.Contains(t, html, `href="https://dashboard.example"`)
	assert.Contains(t, html, ">Open dashboard<")
	assert.Contains(t, html, "Hello carol@example.com,")
	assert.Contains(t, html, "<strong>admin</strong>")
}

// TestPlainTextTemplates_NoHTMLEscaping asserts that special characters in data
// fields (ampersand, apostrophe, angle brackets) survive verbatim in plain-text
// email bodies. Previously all renderers used html/template which emitted HTML
// entities (&amp;, &#39;, &lt;) in plain-text output (07-H1 regression).
func TestPlainTextTemplates_NoHTMLEscaping(t *testing.T) {
	// RequestedByName with apostrophe and email with ampersand -- classic
	// html/template escape targets. They must be preserved verbatim in
	// plain-text bodies so recipients can copy-paste the address.
	const name = "O'Brien"
	const email = "a&b@example.com"
	const reason = "Account name <ACME & Co>"

	t.Run("PasswordReset_email_verbatim", func(t *testing.T) {
		body, err := RenderPasswordResetEmail(email, "https://example.com/reset")
		require.NoError(t, err)
		assert.Contains(t, body, email, "plain-text password reset must preserve & in address")
		assert.NotContains(t, body, "&amp;")
	})

	t.Run("UserInvite_email_verbatim", func(t *testing.T) {
		body, err := RenderUserInviteEmail(email, "https://example.com/setup")
		require.NoError(t, err)
		assert.Contains(t, body, email)
		assert.NotContains(t, body, "&amp;")
	})

	t.Run("RegistrationDecision_rejection_reason_verbatim", func(t *testing.T) {
		data := RegistrationDecisionData{
			AccountName:     "ACME & Co",
			Decision:        "Rejected",
			RejectionReason: reason,
		}
		body, err := RenderRegistrationDecisionEmail(data)
		require.NoError(t, err)
		assert.Contains(t, body, reason, "rejection reason must survive verbatim in plain-text body")
		assert.NotContains(t, body, "&amp;")
		assert.NotContains(t, body, "&lt;")
		assert.NotContains(t, body, "&#39;")
	})

	t.Run("PurchaseApprovalRequest_requestedByName_verbatim", func(t *testing.T) {
		data := NotificationData{
			DashboardURL:     "https://example.com",
			ApprovalToken:    "tok",
			ExecutionID:      "exec-1",
			RequestedByName:  name,
			RequestedByEmail: email,
			Recommendations:  []RecommendationSummary{{Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1}},
		}
		body, err := RenderPurchaseApprovalRequestEmail(data)
		require.NoError(t, err)
		assert.Contains(t, body, name, "RequestedByName with apostrophe must be verbatim in plain-text approval")
		assert.Contains(t, body, email, "RequestedByEmail with & must be verbatim in plain-text approval")
		assert.NotContains(t, body, "&#39;")
		assert.NotContains(t, body, "&amp;")
	})

	t.Run("HTML_renderers_still_escape", func(t *testing.T) {
		// HTML halves MUST still escape data -- that is the XSS defence.
		// A name with a script tag must not survive literally in the HTML body.
		const xssName = `<script>alert(1)</script>`
		data := NotificationData{
			DashboardURL:    "https://example.com",
			ApprovalToken:   "tok",
			ExecutionID:     "exec-2",
			RequestedByName: xssName,
			Recommendations: []RecommendationSummary{{Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1}},
		}
		html, err := RenderPurchaseApprovalRequestEmailHTML(data)
		require.NoError(t, err)
		assert.NotContains(t, html, xssName, "html/template must escape <script> tags in HTML bodies")
	})
}

// Issue #296: plain-text RI exchange pending approval email carries the
// enriched summary fields (requested-by, cancellation-window note) and uses
// labelled Approve/Reject URLs instead of bracket-wrapped actions.
func TestRenderRIExchangePendingApprovalEmail_Issue296(t *testing.T) {
	data := RIExchangeNotificationData{
		DashboardURL:           "https://dashboard.example.com",
		TotalPayment:           "125.50",
		RequestedByName:        "Cristi M",
		RequestedByEmail:       "cristi@acme.com",
		RequestedAt:            "2026-05-22T10:00:00Z",
		CancellationWindowNote: "RI exchanges are irreversible once accepted by AWS.",
		Exchanges: []RIExchangeItem{
			{
				RecordID:           "rec-001",
				ApprovalToken:      "tok-abc",
				SourceRIID:         "ri-0123456789abcdef0",
				SourceInstanceType: "m5.large",
				TargetInstanceType: "m6i.large",
				TargetCount:        1,
				PaymentDue:         "125.50",
				UtilizationPct:     42.5,
			},
		},
	}

	body, err := RenderRIExchangePendingApprovalEmail(data)
	require.NoError(t, err)

	// Exchange details.
	assert.Contains(t, body, "ri-0123456789abcdef0")
	assert.Contains(t, body, "m5.large")
	assert.Contains(t, body, "m6i.large")
	assert.Contains(t, body, "42.5")
	assert.Contains(t, body, "125.50")

	// Requested-by block.
	assert.Contains(t, body, "Cristi M")
	assert.Contains(t, body, "cristi@acme.com")
	assert.Contains(t, body, "2026-05-22T10:00:00Z")

	// Labelled action URLs (not bracket notation).
	assert.Contains(t, body, "Approve: ")
	assert.Contains(t, body, "Reject:  ")
	assert.Contains(t, body, "/api/ri-exchange/approve/rec-001")
	assert.Contains(t, body, "/api/ri-exchange/reject/rec-001")

	// Custom cancellation-window note overrides the generic fallback.
	assert.Contains(t, body, "RI exchanges are irreversible once accepted by AWS.")
	assert.NotContains(t, body, "Please approve within 6 hours")
}

// Issue #296: plain-text template omits requested-by block when email is empty.
func TestRenderRIExchangePendingApprovalEmail_NoRequestedBy(t *testing.T) {
	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalPayment: "50.00",
		Exchanges: []RIExchangeItem{{
			RecordID: "rec-002", ApprovalToken: "tok-xyz",
			SourceRIID: "ri-aaa", SourceInstanceType: "c5.large",
			TargetInstanceType: "c6i.large", TargetCount: 1,
			PaymentDue: "50.00", UtilizationPct: 80.0,
		}},
	}

	body, err := RenderRIExchangePendingApprovalEmail(data)
	require.NoError(t, err)

	assert.NotContains(t, body, "Requested by:")
	// Generic note rendered when CancellationWindowNote is empty.
	assert.Contains(t, body, "Please approve within 6 hours")
}

// Issue #296: HTML half of the RI exchange pending approval email carries
// inline-styled approve/reject anchors with correct hrefs, the exchange
// summary table, and the requested-by line.
func TestRenderRIExchangePendingApprovalEmailHTML_Issue296(t *testing.T) {
	data := RIExchangeNotificationData{
		DashboardURL:           "https://dashboard.example.com",
		TotalPayment:           "125.50",
		RequestedByEmail:       "cristi@acme.com",
		CancellationWindowNote: "RI exchanges are irreversible once accepted by AWS.",
		Exchanges: []RIExchangeItem{
			{
				RecordID:           "rec-001",
				ApprovalToken:      "tok-abc",
				SourceRIID:         "ri-0123456789abcdef0",
				SourceInstanceType: "m5.large",
				TargetInstanceType: "m6i.large",
				TargetCount:        1,
				PaymentDue:         "125.50",
				UtilizationPct:     42.5,
			},
		},
	}

	html, err := RenderRIExchangePendingApprovalEmailHTML(data)
	require.NoError(t, err)
	assert.NotEmpty(t, html)

	// Inline-styled approve anchor with correct href.
	assert.Contains(t, html, `href="https://dashboard.example.com/api/ri-exchange/approve/rec-001?token=tok-abc"`)
	assert.Contains(t, html, `href="https://dashboard.example.com/api/ri-exchange/reject/rec-001?token=tok-abc"`)
	assert.Contains(t, html, ">Approve<")
	assert.Contains(t, html, ">Reject<")

	// Approve button has inline green background (prove CSS classes aren't relied on).
	assert.Regexp(t, `<a[^>]*style="[^"]*background:#16a34a[^"]*"[^>]*>Approve</a>`, html)

	// Exchange table cells.
	assert.Contains(t, html, "m5.large")
	assert.Contains(t, html, "m6i.large")
	assert.Contains(t, html, "42.5%")
	assert.Contains(t, html, "125.50")

	// Summary block + requested-by line.
	assert.Contains(t, html, "cristi@acme.com")
	assert.Contains(t, html, "RI Exchange Approval Required")

	// Custom cancellation note.
	assert.Contains(t, html, "RI exchanges are irreversible once accepted by AWS.")
	assert.NotContains(t, html, "Please approve within 6 hours")
}

// Issue #296: HTML template falls back to generic 6-hour note when
// CancellationWindowNote is empty.
func TestRenderRIExchangePendingApprovalEmailHTML_DefaultCancellationNote(t *testing.T) {
	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalPayment: "10.00",
		Exchanges: []RIExchangeItem{{
			RecordID: "rec-003", ApprovalToken: "tok-def",
			SourceRIID: "ri-bbb", SourceInstanceType: "t3.medium",
			TargetInstanceType: "t4g.medium", TargetCount: 1,
			PaymentDue: "10.00", UtilizationPct: 60.0,
		}},
	}

	html, err := RenderRIExchangePendingApprovalEmailHTML(data)
	require.NoError(t, err)

	assert.Contains(t, html, "Please approve within 6 hours")
}

// Issue #296: skipped exchanges block appears in HTML when Skipped is non-empty.
func TestRenderRIExchangePendingApprovalEmailHTML_SkippedBlock(t *testing.T) {
	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalPayment: "0.00",
		Exchanges:    []RIExchangeItem{},
		Skipped: []SkippedExchange{{
			SourceRIID:         "ri-skip-01",
			SourceInstanceType: "r5.large",
			Reason:             "no matching target available",
		}},
	}

	html, err := RenderRIExchangePendingApprovalEmailHTML(data)
	require.NoError(t, err)

	assert.Contains(t, html, "ri-skip-01")
	assert.Contains(t, html, "no matching target available")
	assert.Contains(t, html, "Skipped")
}
