package email

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSender_SendNewRecommendationsNotification_Success(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalSavings: 500.00,
		Recommendations: []RecommendationSummary{
			{
				Service:        "rds",
				ResourceType:   "db.t3.medium",
				Engine:         "mysql",
				Region:         "us-east-1",
				Count:          1,
				MonthlySavings: 100.00,
			},
		},
	}

	ctx := context.Background()
	err := sender.SendNewRecommendationsNotification(ctx, data)

	require.NoError(t, err)
	mockSNS.AssertExpectations(t)
}

func TestSender_SendScheduledPurchaseNotification_Success(t *testing.T) {
	// Scheduled purchase notifications carry approval tokens and must be
	// delivered via targeted SES, not the SNS broadcast topic.
	mockSES := new(MockSESClient)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("msg-123")}, nil)
	t.Cleanup(func() { mockSES.AssertExpectations(t) })

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "token-123",
		RecipientEmail:    "notify@example.com",
		TotalSavings:      1000.00,
		TotalUpfrontCost:  3000.00,
		PurchaseDate:      "February 1, 2024",
		DaysUntilPurchase: 5,
		PlanName:          "AWS RDS Plan",
	}

	ctx := context.Background()
	err := sender.SendScheduledPurchaseNotification(ctx, data)

	require.NoError(t, err)
}

func TestSender_SendPurchaseConfirmation_Success(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		TotalSavings:     800.00,
		TotalUpfrontCost: 2400.00,
		PlanName:         "EC2 Plan",
	}

	ctx := context.Background()
	err := sender.SendPurchaseConfirmation(ctx, data)

	require.NoError(t, err)
	mockSNS.AssertExpectations(t)
}

func TestSender_SendPurchaseFailedNotification_Success(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
		Recommendations: []RecommendationSummary{
			{Service: "rds", ResourceType: "db.r5.large", Region: "us-east-1", Count: 1},
		},
	}

	ctx := context.Background()
	err := sender.SendPurchaseFailedNotification(ctx, data)

	require.NoError(t, err)
	mockSNS.AssertExpectations(t)
}

func TestSender_SendPasswordResetEmail_Success(t *testing.T) {
	mockSES := new(MockSESClient)
	// GetAccount is called to check sandbox mode - return production mode (not sandbox)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("msg-456")}, nil)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	ctx := context.Background()
	err := sender.SendPasswordResetEmail(ctx, "user@example.com", "https://dashboard.example.com/reset?token=abc")

	require.NoError(t, err)
	mockSES.AssertExpectations(t)
}

func TestSender_SendWelcomeEmail_Success(t *testing.T) {
	mockSES := new(MockSESClient)
	// GetAccount is called to check sandbox mode - return production mode (not sandbox)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("msg-456")}, nil)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	ctx := context.Background()
	err := sender.SendWelcomeEmail(ctx, "newuser@example.com", "https://dashboard.example.com", "user")

	require.NoError(t, err)
	mockSES.AssertExpectations(t)
}

// Test template success paths with no recommendations (edge case).
func TestSender_SendNewRecommendationsNotification_EmptyRecommendations(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	data := NotificationData{
		DashboardURL:    "https://dashboard.example.com",
		TotalSavings:    0,
		Recommendations: []RecommendationSummary{},
	}

	ctx := context.Background()
	err := sender.SendNewRecommendationsNotification(ctx, data)

	require.NoError(t, err)
	mockSNS.AssertExpectations(t)
}

// Test when topic/from email are empty (early return paths).
func TestSender_SendNewRecommendationsNotification_NoTopic(t *testing.T) {
	sender := &Sender{
		topicARN: "",
	}

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
	}

	ctx := context.Background()
	err := sender.SendNewRecommendationsNotification(ctx, data)

	require.NoError(t, err)
}

func TestSender_SendScheduledPurchaseNotification_NoRecipient(t *testing.T) {
	// When RecipientEmail is empty the send must be rejected — the body carries
	// approval tokens that must not be broadcast via SNS.
	sender := &Sender{
		topicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
		fromEmail: "noreply@example.com",
	}

	data := NotificationData{
		DashboardURL:  "https://dashboard.example.com",
		ApprovalToken: "secret-token",
		// RecipientEmail intentionally empty
	}

	ctx := context.Background()
	err := sender.SendScheduledPurchaseNotification(ctx, data)

	require.ErrorIs(t, err, ErrNoRecipient)
}

func TestSender_SendPurchaseConfirmation_NoTopic(t *testing.T) {
	sender := &Sender{
		topicARN: "",
	}

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
	}

	ctx := context.Background()
	err := sender.SendPurchaseConfirmation(ctx, data)

	require.NoError(t, err)
}

func TestSender_SendPurchaseFailedNotification_NoTopic(t *testing.T) {
	sender := &Sender{
		topicARN: "",
	}

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
	}

	ctx := context.Background()
	err := sender.SendPurchaseFailedNotification(ctx, data)

	require.NoError(t, err)
}

func TestSender_SendPasswordResetEmail_NoFromEmail(t *testing.T) {
	sender := &Sender{
		fromEmail: "",
	}

	ctx := context.Background()
	err := sender.SendPasswordResetEmail(ctx, "user@example.com", "https://example.com/reset")

	require.NoError(t, err)
}

func TestSender_SendWelcomeEmail_NoFromEmail(t *testing.T) {
	sender := &Sender{
		fromEmail: "",
	}

	ctx := context.Background()
	err := sender.SendWelcomeEmail(ctx, "user@example.com", "https://example.com", "user")

	require.NoError(t, err)
}

// Test error cases for template functions.
func TestSender_SendNewRecommendationsNotification_SNSError(t *testing.T) {
	mockSNS := new(MockSNSClient)
	sender := &Sender{
		snsClient: mockSNS,
		topicARN:  "arn:aws:sns:us-east-1:123456789:topic",
	}

	mockSNS.On("Publish", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ctx := context.Background()
	data := NotificationData{
		DashboardURL: "https://example.com",
		TotalSavings: 1000.0,
		Recommendations: []RecommendationSummary{
			{Service: "EC2", Region: "us-east-1", Count: 5, MonthlySavings: 100.0},
		},
	}

	err := sender.SendNewRecommendationsNotification(ctx, data)
	require.Error(t, err)
}

func TestSender_SendScheduledPurchaseNotification_SESError(t *testing.T) {
	// SES send error must propagate to the caller.
	mockSES := new(MockSESClient)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(nil, assert.AnError)
	t.Cleanup(func() { mockSES.AssertExpectations(t) })

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	ctx := context.Background()
	data := NotificationData{
		DashboardURL:      "https://example.com",
		RecipientEmail:    "notify@example.com",
		ApprovalToken:     "token-abc",
		TotalSavings:      500.0,
		DaysUntilPurchase: 3,
		PlanName:          "Plan A",
	}

	err := sender.SendScheduledPurchaseNotification(ctx, data)
	require.Error(t, err)
}

func TestSender_SendPurchaseConfirmation_SNSError(t *testing.T) {
	mockSNS := new(MockSNSClient)
	sender := &Sender{
		snsClient: mockSNS,
		topicARN:  "arn:aws:sns:us-east-1:123456789:topic",
	}

	mockSNS.On("Publish", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ctx := context.Background()
	data := NotificationData{
		DashboardURL: "https://example.com",
	}

	err := sender.SendPurchaseConfirmation(ctx, data)
	require.Error(t, err)
}

func TestSender_SendPurchaseFailedNotification_SNSError(t *testing.T) {
	mockSNS := new(MockSNSClient)
	sender := &Sender{
		snsClient: mockSNS,
		topicARN:  "arn:aws:sns:us-east-1:123456789:topic",
	}

	mockSNS.On("Publish", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ctx := context.Background()
	data := NotificationData{
		DashboardURL: "https://example.com",
	}

	err := sender.SendPurchaseFailedNotification(ctx, data)
	require.Error(t, err)
}

func TestSender_SendPasswordResetEmail_SESError(t *testing.T) {
	mockSES := new(MockSESClient)
	sender := &Sender{
		sesClient: mockSES,
		fromEmail: "noreply@example.com",
	}

	// GetAccount is called first - return production mode (not sandbox)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	mockSES.On("SendEmail", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ctx := context.Background()
	err := sender.SendPasswordResetEmail(ctx, "user@example.com", "https://example.com/reset")
	require.Error(t, err)
}

func TestSender_SendWelcomeEmail_SESError(t *testing.T) {
	mockSES := new(MockSESClient)
	sender := &Sender{
		sesClient: mockSES,
		fromEmail: "noreply@example.com",
	}

	// GetAccount is called first - return production mode (not sandbox)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	mockSES.On("SendEmail", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ctx := context.Background()
	err := sender.SendWelcomeEmail(ctx, "user@example.com", "https://example.com", "admin")
	require.Error(t, err)
}

// Test multiple recommendations in templates.
func TestSender_SendNewRecommendationsNotification_MultipleRecommendations(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		TotalSavings:     2500.00,
		TotalUpfrontCost: 10000.00,
		Recommendations: []RecommendationSummary{
			{
				Service:        "rds",
				ResourceType:   "db.r5.large",
				Engine:         "postgres",
				Region:         "us-east-1",
				Count:          2,
				MonthlySavings: 400.00,
			},
			{
				Service:        "elasticache",
				ResourceType:   "cache.r5.large",
				Engine:         "redis",
				Region:         "us-west-2",
				Count:          3,
				MonthlySavings: 600.00,
			},
			{
				Service:        "ec2",
				ResourceType:   "m5.xlarge",
				Region:         "eu-west-1",
				Count:          10,
				MonthlySavings: 1500.00,
			},
		},
	}

	ctx := context.Background()
	err := sender.SendNewRecommendationsNotification(ctx, data)

	require.NoError(t, err)
	mockSNS.AssertExpectations(t)
}

func TestSender_SendScheduledPurchaseNotification_WithUpfrontCost(t *testing.T) {
	// Verify that a well-formed data payload with RecipientEmail succeeds via SES.
	mockSES := new(MockSESClient)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("msg-123")}, nil)
	t.Cleanup(func() { mockSES.AssertExpectations(t) })

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "token-456",
		RecipientEmail:    "notify@example.com",
		TotalSavings:      1500.00,
		TotalUpfrontCost:  6000.00,
		PurchaseDate:      "April 1, 2024",
		DaysUntilPurchase: 14,
		PlanName:          "Production Plan",
		Recommendations: []RecommendationSummary{
			{
				Service:        "rds",
				ResourceType:   "db.m5.xlarge",
				Engine:         "mysql",
				Region:         "us-east-1",
				Count:          5,
				MonthlySavings: 750.00,
			},
		},
	}

	ctx := context.Background()
	err := sender.SendScheduledPurchaseNotification(ctx, data)

	require.NoError(t, err)
}

func TestSender_SendPurchaseConfirmation_WithMultipleRecommendations(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		TotalSavings:     3000.00,
		TotalUpfrontCost: 12000.00,
		PlanName:         "Enterprise Plan",
		Recommendations: []RecommendationSummary{
			{
				Service:        "rds",
				ResourceType:   "db.r5.2xlarge",
				Engine:         "postgres",
				Region:         "us-east-1",
				Count:          4,
				MonthlySavings: 1200.00,
			},
			{
				Service:        "ec2",
				ResourceType:   "c5.2xlarge",
				Region:         "us-west-2",
				Count:          8,
				MonthlySavings: 1800.00,
			},
		},
	}

	ctx := context.Background()
	err := sender.SendPurchaseConfirmation(ctx, data)

	require.NoError(t, err)
	mockSNS.AssertExpectations(t)
}

func TestSender_SendPurchaseFailedNotification_MultipleFailures(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
		Recommendations: []RecommendationSummary{
			{
				Service:      "rds",
				ResourceType: "db.r5.large",
				Engine:       "postgres",
				Region:       "us-east-1",
				Count:        2,
			},
			{
				Service:      "opensearch",
				ResourceType: "r5.large.search",
				Region:       "eu-central-1",
				Count:        1,
			},
			{
				Service:      "elasticache",
				ResourceType: "cache.r5.xlarge",
				Engine:       "redis",
				Region:       "ap-southeast-1",
				Count:        3,
			},
		},
	}

	ctx := context.Background()
	err := sender.SendPurchaseFailedNotification(ctx, data)

	require.NoError(t, err)
	mockSNS.AssertExpectations(t)
}

// TestRenderScheduledPurchaseEmail_TokenNotInReviewEditLinks is a regression
// test for #406: the scheduled purchase notification must not embed the
// approval token in the Review/Edit or Pause Plan URLs. These actions
// require an authenticated dashboard session; only the Cancel link carries
// the token, and it must use the direct API path (not the SPA root) to
// avoid loading third-party scripts with the token in the Referer header.
//
// It also covers the #581-followup deeplink shape: Review & Edit and
// Pause Plan must include the execution / plan ID so the SPA can scroll
// the user to the relevant row instead of dumping them at the dashboard
// root.
func TestRenderScheduledPurchaseEmail_TokenNotInReviewEditLinks(t *testing.T) {
	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "super-secret-token",
		ExecutionID:       "exec-abc-123",
		PlanID:            "plan-xyz-789",
		TotalSavings:      500.00,
		PurchaseDate:      "March 1, 2025",
		DaysUntilPurchase: 7,
		PlanName:          "Test Plan",
	}

	body, err := RenderScheduledPurchaseEmail(data)
	require.NoError(t, err)

	// The cancel link must use the direct API path with the execution ID and token.
	assert.Contains(t, body, "/purchases/cancel/exec-abc-123?token=super-secret-token")

	// Review & Edit must deeplink to the Purchase History row matching
	// ExecutionID (non-sensitive UUID; auth still required via session cookie).
	assert.Contains(t, body, "/purchases#history?execution=exec-abc-123",
		"Review & Edit link must deeplink to the matching execution row")

	// Pause Plan must deeplink to the Plans tab with the matching plan ID.
	assert.Contains(t, body, "/plans?plan=plan-xyz-789",
		"Pause Plan link must deeplink to the matching plan")

	// The token must not appear in the review/edit or pause lines.
	for _, line := range splitTestLines(body) {
		if containsAnyStr(line, "Review", "Pause") {
			assert.NotContains(t, line, "super-secret-token",
				"line %q must not embed the approval token", line)
		}
	}
}

// splitTestLines splits s on newlines, returning non-empty lines.
func splitTestLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if line := s[start:i]; line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// containsAnyStr reports whether s contains any of the provided substrings.
func containsAnyStr(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

// TestSender_SendRegistrationReceivedNotification_SubjectHeaderInjection is the
// SES-path regression test for #544 / #401: a CR+LF in the attacker-controlled
// AccountName / Provider (sourced from the unauthenticated POST /api/register
// endpoint) must be stripped before the subject reaches the SES SendEmail API,
// so it cannot inject additional email headers. Mirrors the SMTP-path test.
func TestSender_SendRegistrationReceivedNotification_SubjectHeaderInjection(t *testing.T) {
	mockSES := new(MockSESClient)
	// Production mode so the send proceeds straight to SendEmail.
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)

	var capturedSubject string
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(*sesv2.SendEmailInput)
			capturedSubject = aws.ToString(input.Content.Simple.Subject.Data)
		}).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("msg-injection")}, nil)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	injectedName := "Acme\r\nBcc: attacker@evil.example.com"
	injectedProvider := "aws\r\nX-Injected: yes"
	data := RegistrationNotificationData{
		AccountName:    injectedName,
		Provider:       injectedProvider,
		ExternalID:     "ext-123",
		ContactEmail:   "registrant@example.com",
		RecipientEmail: "admin@example.com",
	}

	ctx := context.Background()
	err := sender.SendRegistrationReceivedNotification(ctx, data)
	require.NoError(t, err)
	mockSES.AssertExpectations(t)

	// The subject that reached SES must contain no CR/LF characters.
	assert.NotContains(t, capturedSubject, "\r", "SES subject must not contain CR: %q", capturedSubject)
	assert.NotContains(t, capturedSubject, "\n", "SES subject must not contain LF: %q", capturedSubject)
	// The injected header names must not survive into the subject as injectable headers.
	assert.NotContains(t, capturedSubject, "\nBcc:")
	assert.NotContains(t, capturedSubject, "\nX-Injected:")
}
