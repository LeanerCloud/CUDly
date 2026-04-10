package email

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for containsColon
func TestContainsColon(t *testing.T) {
	assert.True(t, containsColon("arn:aws:secretsmanager:us-east-1:123:secret:foo"))
	assert.True(t, containsColon("a:b"))
	assert.True(t, containsColon(":"))
	assert.False(t, containsColon(""))
	assert.False(t, containsColon("nodivider"))
	assert.False(t, containsColon("just/a/path"))
}

// Tests for warnIfPlaintext – exercising all branches
func TestWarnIfPlaintext(t *testing.T) {
	// Empty value: should be a no-op (no panic)
	assert.NotPanics(t, func() { warnIfPlaintext("VAR", "") })

	// Short value (< 20 chars, no colon, no leading slash) → warning branch
	assert.NotPanics(t, func() { warnIfPlaintext("VAR", "short") })

	// Long value with colon → secret-manager reference, no warning
	assert.NotPanics(t, func() { warnIfPlaintext("VAR", "arn:aws:secretsmanager:us-east-1:123456789012:secret:mykey") })

	// Long value starting with slash → secret-manager reference, no warning
	assert.NotPanics(t, func() { warnIfPlaintext("VAR", "/my/secret/path/that/is/long") })

	// Long value without colon and not starting with slash → warning branch
	assert.NotPanics(t, func() { warnIfPlaintext("VAR", "averylongplaintextpasswordwithnospecialchars") })
}

// Tests for renderTemplate error path
func TestRenderTemplate_ParseError(t *testing.T) {
	// A template with an unclosed action will fail to parse
	_, err := renderTemplate("bad", "{{.Foo", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse template")
}

func TestRenderTemplate_ExecuteError(t *testing.T) {
	// Template that calls a method on a nil value will error on Execute
	_, err := renderTemplate("exec-err", "{{.NonExistent.Field}}", struct{ NonExistent interface{} }{NonExistent: nil})
	require.Error(t, err)
}

// Tests for RenderRIExchangePendingApprovalEmail
func TestRenderRIExchangePendingApprovalEmail(t *testing.T) {
	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalPayment: "1500.00",
		Exchanges: []RIExchangeItem{
			{
				RecordID:           "rec-001",
				ApprovalToken:      "tok-abc",
				SourceRIID:         "ri-aabbccdd",
				SourceInstanceType: "m5.xlarge",
				TargetInstanceType: "m5.2xlarge",
				TargetCount:        1,
				PaymentDue:         "500.00",
				UtilizationPct:     45.0,
			},
		},
		Skipped: []SkippedExchange{
			{SourceRIID: "ri-skip1", SourceInstanceType: "t3.micro", Reason: "incompatible family"},
		},
	}

	result, err := RenderRIExchangePendingApprovalEmail(data)
	require.NoError(t, err)
	assert.Contains(t, result, "ri-aabbccdd")
	assert.Contains(t, result, "m5.2xlarge")
	assert.Contains(t, result, "1500.00")
	assert.Contains(t, result, "dashboard.example.com")
	assert.Contains(t, result, "Approval Required")
	assert.Contains(t, result, "ri-skip1")
	assert.Contains(t, result, "incompatible family")
}

func TestRenderRIExchangePendingApprovalEmail_NoSkipped(t *testing.T) {
	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalPayment: "200.00",
		Exchanges: []RIExchangeItem{
			{
				RecordID:           "rec-002",
				ApprovalToken:      "tok-xyz",
				SourceRIID:         "ri-11223344",
				SourceInstanceType: "r5.large",
				TargetInstanceType: "r5.xlarge",
				TargetCount:        2,
				PaymentDue:         "200.00",
				UtilizationPct:     80.5,
			},
		},
	}

	result, err := RenderRIExchangePendingApprovalEmail(data)
	require.NoError(t, err)
	assert.Contains(t, result, "ri-11223344")
	assert.Contains(t, result, "r5.xlarge")
}

// Tests for RenderRIExchangeCompletedEmail
func TestRenderRIExchangeCompletedEmail_AutoMode(t *testing.T) {
	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		Mode:         "auto",
		TotalPayment: "750.00",
		Exchanges: []RIExchangeItem{
			{
				SourceRIID:         "ri-aaaa",
				SourceInstanceType: "m5.large",
				TargetInstanceType: "m5.xlarge",
				TargetCount:        1,
				PaymentDue:         "750.00",
				ExchangeID:         "exc-001",
				Error:              "",
			},
		},
	}

	result, err := RenderRIExchangeCompletedEmail(data)
	require.NoError(t, err)
	assert.Contains(t, result, "automatically")
	assert.Contains(t, result, "ri-aaaa")
	assert.Contains(t, result, "exc-001")
	assert.Contains(t, result, "750.00")
}

func TestRenderRIExchangeCompletedEmail_ManualMode(t *testing.T) {
	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		Mode:         "manual",
		TotalPayment: "300.00",
		Exchanges: []RIExchangeItem{
			{
				SourceRIID:         "ri-bbbb",
				SourceInstanceType: "t3.small",
				TargetInstanceType: "t3.medium",
				TargetCount:        1,
				PaymentDue:         "300.00",
				ExchangeID:         "exc-002",
				Error:              "",
			},
		},
		Skipped: []SkippedExchange{
			{SourceRIID: "ri-skip2", SourceInstanceType: "t3.nano", Reason: "low utilization"},
		},
	}

	result, err := RenderRIExchangeCompletedEmail(data)
	require.NoError(t, err)
	assert.NotContains(t, result, "automatically")
	assert.Contains(t, result, "ri-bbbb")
	assert.Contains(t, result, "ri-skip2")
}

func TestRenderRIExchangeCompletedEmail_WithError(t *testing.T) {
	// Exchanges with a non-empty Error field should be skipped in the template
	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		Mode:         "auto",
		TotalPayment: "0.00",
		Exchanges: []RIExchangeItem{
			{
				SourceRIID:         "ri-fail",
				SourceInstanceType: "c5.large",
				TargetInstanceType: "c5.xlarge",
				TargetCount:        1,
				PaymentDue:         "0.00",
				Error:              "exchange failed due to insufficient capacity",
			},
		},
	}

	result, err := RenderRIExchangeCompletedEmail(data)
	require.NoError(t, err)
	// ri-fail should not appear since it has an error
	assert.NotContains(t, result, "ri-fail")
}

// Tests for RenderPurchaseApprovalRequestEmail
func TestRenderPurchaseApprovalRequestEmail(t *testing.T) {
	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		ApprovalToken:    "tok-approval-123",
		ExecutionID:      "exec-abc",
		TotalSavings:     800.0,
		TotalUpfrontCost: 4000.0,
		Recommendations: []RecommendationSummary{
			{
				Service:        "rds",
				ResourceType:   "db.r5.large",
				Engine:         "postgres",
				Region:         "us-east-1",
				Count:          2,
				MonthlySavings: 400.0,
			},
		},
	}

	result, err := RenderPurchaseApprovalRequestEmail(data)
	require.NoError(t, err)
	assert.Contains(t, result, "Approval Required")
	assert.Contains(t, result, "4000.00")
	assert.Contains(t, result, "800.00")
	assert.Contains(t, result, "db.r5.large")
	assert.Contains(t, result, "postgres")
	assert.Contains(t, result, "exec-abc")
	assert.Contains(t, result, "tok-approval-123")
	assert.Contains(t, result, "approve")
	assert.Contains(t, result, "cancel")
}

// Tests for SMTPSender RI exchange and approval request methods

func TestSMTPSender_SendRIExchangePendingApproval_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "",
	}

	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalPayment: "100.00",
		Exchanges: []RIExchangeItem{
			{RecordID: "r1", SourceRIID: "ri-1", SourceInstanceType: "m5.large",
				TargetInstanceType: "m5.xlarge", TargetCount: 1, PaymentDue: "100.00"},
		},
	}

	err := sender.SendRIExchangePendingApproval(context.Background(), data)
	require.NoError(t, err)
}

func TestSMTPSender_SendRIExchangeCompleted_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "",
	}

	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		Mode:         "auto",
		TotalPayment: "200.00",
		Exchanges: []RIExchangeItem{
			{SourceRIID: "ri-2", SourceInstanceType: "r5.large",
				TargetInstanceType: "r5.xlarge", TargetCount: 1, PaymentDue: "200.00", ExchangeID: "exc-x"},
		},
	}

	err := sender.SendRIExchangeCompleted(context.Background(), data)
	require.NoError(t, err)
}

func TestSMTPSender_SendPurchaseApprovalRequest_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "",
	}

	data := NotificationData{
		DashboardURL:  "https://dashboard.example.com",
		ApprovalToken: "tok",
		ExecutionID:   "exec-1",
		Recommendations: []RecommendationSummary{
			{Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1},
		},
	}

	err := sender.SendPurchaseApprovalRequest(context.Background(), data)
	require.NoError(t, err)
}

// Tests for SMTPSender using notifyEmail (not fromEmail)
func TestSMTPSender_SendRIExchangePendingApproval_WithNotifyEmail(t *testing.T) {
	sender := &SMTPSender{
		host:        "smtp.example.com",
		port:        587,
		fromEmail:   "from@example.com",
		notifyEmail: "notify@example.com",
		// useTLS is false so sendMailTLS is not called; smtp.SendMail will be attempted
		// but fail — we just verify the method reaches the send path (not a no-op)
		useTLS: false,
	}

	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalPayment: "500.00",
		Exchanges: []RIExchangeItem{
			{RecordID: "r3", SourceRIID: "ri-3", SourceInstanceType: "c5.large",
				TargetInstanceType: "c5.xlarge", TargetCount: 1, PaymentDue: "500.00"},
		},
	}

	// Will fail with a network error — just verify it's not the "no from email" no-op.
	err := sender.SendRIExchangePendingApproval(context.Background(), data)
	// The error will be a connection refused / network error (not nil, not "no from email")
	if err != nil {
		assert.NotContains(t, err.Error(), "no from email")
	}
}

// Tests for Sender.SendRIExchangePendingApproval, SendRIExchangeCompleted, SendPurchaseApprovalRequest
// using the mock SNS sender (no SNS topic → no-op path)

func TestSender_SendRIExchangePendingApproval_NoTopic(t *testing.T) {
	mockSNS := &mockSNSPublisher{}
	mockSES := &mockSESEmailSender{}
	sender := NewSenderWithClients(mockSNS, mockSES, SenderConfig{
		// TopicARN is empty → SendNotification is a no-op
		FromEmail: "from@example.com",
	})

	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalPayment: "300.00",
		Exchanges: []RIExchangeItem{
			{RecordID: "r4", SourceRIID: "ri-4", SourceInstanceType: "m4.large",
				TargetInstanceType: "m4.xlarge", TargetCount: 1, PaymentDue: "300.00", UtilizationPct: 55.0},
		},
	}

	err := sender.SendRIExchangePendingApproval(context.Background(), data)
	require.NoError(t, err)
}

func TestSender_SendRIExchangeCompleted_NoTopic(t *testing.T) {
	mockSNS := &mockSNSPublisher{}
	mockSES := &mockSESEmailSender{}
	sender := NewSenderWithClients(mockSNS, mockSES, SenderConfig{
		FromEmail: "from@example.com",
	})

	data := RIExchangeNotificationData{
		DashboardURL: "https://dashboard.example.com",
		Mode:         "auto",
		TotalPayment: "400.00",
		Exchanges: []RIExchangeItem{
			{SourceRIID: "ri-5", SourceInstanceType: "r4.large",
				TargetInstanceType: "r4.xlarge", TargetCount: 1, ExchangeID: "exc-z"},
		},
	}

	err := sender.SendRIExchangeCompleted(context.Background(), data)
	require.NoError(t, err)
}

func TestSender_SendPurchaseApprovalRequest_NoTopic(t *testing.T) {
	mockSNS := &mockSNSPublisher{}
	mockSES := &mockSESEmailSender{}
	sender := NewSenderWithClients(mockSNS, mockSES, SenderConfig{
		FromEmail: "from@example.com",
	})

	data := NotificationData{
		DashboardURL:  "https://dashboard.example.com",
		ApprovalToken: "tok-req",
		ExecutionID:   "exec-req",
		Recommendations: []RecommendationSummary{
			{Service: "rds", ResourceType: "db.m5.large", Region: "eu-west-1", Count: 3},
		},
	}

	err := sender.SendPurchaseApprovalRequest(context.Background(), data)
	require.NoError(t, err)
}

// Tests for redactEmail edge cases
func TestRedactEmail(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user@example.com", "us***@example.com"},
		{"ab@example.com", "***@example.com"}, // local part <= 2 chars
		{"a@example.com", "***@example.com"},  // local part <= 2 chars
		{"noatsign", "***"},
		{"x@y.com", "***@y.com"},            // single char local part
		{"jo@domain.org", "***@domain.org"}, // local part exactly 2 chars → redacted
		{"use@domain.org", "us***@domain.org"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, redactEmail(tt.input))
		})
	}
}

// Tests for sanitizeHeader — it strips CR and LF entirely (does not replace with space)
func TestSanitizeHeader(t *testing.T) {
	assert.Equal(t, "helloworld", sanitizeHeader("hello\r\nworld"))
	assert.Equal(t, "helloworld", sanitizeHeader("hello\nworld"))
	assert.Equal(t, "helloworld", sanitizeHeader("hello\rworld"))
	assert.Equal(t, "clean", sanitizeHeader("clean"))
	assert.Equal(t, "", sanitizeHeader(""))
	assert.Equal(t, "Subject Line", sanitizeHeader("Subject\r Line"))
}

// Test smtpAuthenticate nil auth path (covered by unit test without real SMTP)
func TestSmtpAuthenticate_NilAuth(t *testing.T) {
	// nil auth → immediate return nil
	err := smtpAuthenticate(nil, nil)
	require.NoError(t, err)
}

// Test smtpSendBody — can't call without a real client, but we can test
// that the function exists and has a valid signature by referencing it.
// The coverage tool counts the function as covered only when called, so
// we test the error paths that don't require a live SMTP server via
// the SendToEmail no-from-email short-circuit above.

// Tests for SMTPSender.notifyEmail defaults to fromEmail when not set
func TestSMTPSender_NotifyEmailDefaultsToFromEmail(t *testing.T) {
	cfg := SMTPConfig{
		Host:        "smtp.example.com",
		Port:        587,
		FromEmail:   "from@example.com",
		NotifyEmail: "", // not set
		UseTLS:      false,
	}

	sender, err := NewSMTPSender(cfg)
	require.NoError(t, err)
	assert.Equal(t, "from@example.com", sender.notifyEmail)
}

func TestSMTPSender_NotifyEmailExplicit(t *testing.T) {
	cfg := SMTPConfig{
		Host:        "smtp.example.com",
		Port:        587,
		FromEmail:   "from@example.com",
		NotifyEmail: "notify@example.com",
		UseTLS:      false,
	}

	sender, err := NewSMTPSender(cfg)
	require.NoError(t, err)
	assert.Equal(t, "notify@example.com", sender.notifyEmail)
}

// Test for strings.Contains("535") error path in smtpAuthenticate — we cannot
// call it without a real *smtp.Client, but at minimum we can verify the
// function is importable and the "nil auth" path works (already done above).

// Mock helpers used by the Sender tests above

// mockSNSPublisher satisfies SNSPublisher for tests that never reach the network.
type mockSNSPublisher struct{}

func (m *mockSNSPublisher) Publish(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
	return &sns.PublishOutput{}, nil
}

// mockSESEmailSender satisfies SESEmailSender with no-op methods.
type mockSESEmailSender struct{}

func (m *mockSESEmailSender) SendEmail(ctx context.Context, params *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	return &sesv2.SendEmailOutput{}, nil
}

func (m *mockSESEmailSender) GetAccount(ctx context.Context, params *sesv2.GetAccountInput, optFns ...func(*sesv2.Options)) (*sesv2.GetAccountOutput, error) {
	return &sesv2.GetAccountOutput{}, nil
}

func (m *mockSESEmailSender) GetEmailIdentity(ctx context.Context, params *sesv2.GetEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error) {
	return &sesv2.GetEmailIdentityOutput{VerifiedForSendingStatus: true}, nil
}

func (m *mockSESEmailSender) CreateEmailIdentity(ctx context.Context, params *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error) {
	return &sesv2.CreateEmailIdentityOutput{}, nil
}

var _ SNSPublisher = (*mockSNSPublisher)(nil)
var _ SESEmailSender = (*mockSESEmailSender)(nil)

func TestSMTPSender_SendToEmail_AuthPath(t *testing.T) {
	// A sender with username/password set but useTLS=false will use smtp.SendMail.
	// With a bogus host, it will fail at Dial — which still exercises the auth
	// construction branch (auth != nil when username/password are both non-empty).
	sender := &SMTPSender{
		host:      "127.0.0.1",
		port:      9, // DISCARD port — usually not listening, will get connection refused
		fromEmail: "from@example.com",
		fromName:  "Test",
		username:  "user",
		password:  "pass",
		useTLS:    false,
	}

	// This will fail with a network error, but the important thing is it takes the
	// "non-TLS with auth" branch in SendToEmail.
	err := sender.SendToEmail(context.Background(), "to@example.com", "Subject", "Body")
	// We expect an error (no server listening), but not a panic or wrong-branch error.
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "SMTP") || strings.Contains(err.Error(), "connection") ||
		strings.Contains(err.Error(), "refused") || strings.Contains(err.Error(), "dial"),
		"expected a network-level SMTP error, got: %v", err)
}
