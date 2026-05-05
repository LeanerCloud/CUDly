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
	assert.Contains(t, result, data.ApprovalToken)
	assert.Contains(t, result, data.PurchaseDate)
	assert.Contains(t, result, data.PlanName)
	assert.Contains(t, result, "7")
	assert.Contains(t, result, "db.r5.2xlarge")
	assert.Contains(t, result, "mysql")
	assert.Contains(t, result, "action=edit")
	assert.Contains(t, result, "action=pause")
	assert.Contains(t, result, "action=cancel")
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
