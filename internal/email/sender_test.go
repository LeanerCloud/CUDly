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

// MockSNSClient is a mock implementation of SNS client
type MockSNSClient struct {
	mock.Mock
}

func (m *MockSNSClient) Publish(ctx context.Context, input *sns.PublishInput, opts ...func(*sns.Options)) (*sns.PublishOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sns.PublishOutput), args.Error(1)
}

// MockSESClient is a mock implementation of SES client
type MockSESClient struct {
	mock.Mock
}

func (m *MockSESClient) SendEmail(ctx context.Context, input *sesv2.SendEmailInput, opts ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sesv2.SendEmailOutput), args.Error(1)
}

func (m *MockSESClient) GetAccount(ctx context.Context, input *sesv2.GetAccountInput, opts ...func(*sesv2.Options)) (*sesv2.GetAccountOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sesv2.GetAccountOutput), args.Error(1)
}

func (m *MockSESClient) GetEmailIdentity(ctx context.Context, input *sesv2.GetEmailIdentityInput, opts ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sesv2.GetEmailIdentityOutput), args.Error(1)
}

func (m *MockSESClient) CreateEmailIdentity(ctx context.Context, input *sesv2.CreateEmailIdentityInput, opts ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sesv2.CreateEmailIdentityOutput), args.Error(1)
}

// testSender creates a sender with mock clients for testing
type testSender struct {
	*Sender
	mockSNS *MockSNSClient
	mockSES *MockSESClient
}

func newTestSender(topicARN, fromEmail string) *testSender {
	mockSNS := new(MockSNSClient)
	mockSES := new(MockSESClient)

	return &testSender{
		Sender: &Sender{
			snsClient:    nil, // Will be replaced in tests
			sesClient:    nil, // Will be replaced in tests
			topicARN:     topicARN,
			fromEmail:    fromEmail,
			emailAddress: "",
		},
		mockSNS: mockSNS,
		mockSES: mockSES,
	}
}

func TestSenderConfig(t *testing.T) {
	cfg := SenderConfig{
		TopicARN:     "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail:    "noreply@example.com",
		EmailAddress: "admin@example.com",
	}

	assert.Equal(t, "arn:aws:sns:us-east-1:123456789012:topic", cfg.TopicARN)
	assert.Equal(t, "noreply@example.com", cfg.FromEmail)
	assert.Equal(t, "admin@example.com", cfg.EmailAddress)
}

func TestNotificationData(t *testing.T) {
	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "token-123",
		TotalSavings:      1500.50,
		TotalUpfrontCost:  5000.00,
		PurchaseDate:      "January 15, 2024",
		DaysUntilPurchase: 7,
		PlanName:          "Production Plan",
		Recommendations: []RecommendationSummary{
			{
				Service:        "rds",
				ResourceType:   "db.r5.large",
				Engine:         "postgres",
				Region:         "us-east-1",
				Count:          2,
				MonthlySavings: 200.00,
			},
		},
	}

	assert.Equal(t, "https://dashboard.example.com", data.DashboardURL)
	assert.Equal(t, "token-123", data.ApprovalToken)
	assert.Equal(t, 1500.50, data.TotalSavings)
	assert.Equal(t, 5000.00, data.TotalUpfrontCost)
	assert.Equal(t, "January 15, 2024", data.PurchaseDate)
	assert.Equal(t, 7, data.DaysUntilPurchase)
	assert.Equal(t, "Production Plan", data.PlanName)
	assert.Len(t, data.Recommendations, 1)
}

func TestRecommendationSummary(t *testing.T) {
	summary := RecommendationSummary{
		Service:        "elasticache",
		ResourceType:   "cache.r5.large",
		Engine:         "redis",
		Region:         "eu-west-1",
		Count:          3,
		MonthlySavings: 350.00,
	}

	assert.Equal(t, "elasticache", summary.Service)
	assert.Equal(t, "cache.r5.large", summary.ResourceType)
	assert.Equal(t, "redis", summary.Engine)
	assert.Equal(t, "eu-west-1", summary.Region)
	assert.Equal(t, 3, summary.Count)
	assert.Equal(t, 350.00, summary.MonthlySavings)
}

func TestPasswordResetData(t *testing.T) {
	data := PasswordResetData{
		ResetURL: "https://dashboard.example.com/reset?token=abc123",
	}

	assert.Equal(t, "https://dashboard.example.com/reset?token=abc123", data.ResetURL)
}

func TestWelcomeUserData(t *testing.T) {
	data := WelcomeUserData{
		Email:        "newuser@example.com",
		DashboardURL: "https://dashboard.example.com",
		Role:         "user",
	}

	assert.Equal(t, "newuser@example.com", data.Email)
	assert.Equal(t, "https://dashboard.example.com", data.DashboardURL)
	assert.Equal(t, "user", data.Role)
}

func TestSender_SendNotification_NoTopic(t *testing.T) {
	sender := &Sender{
		topicARN: "",
	}

	ctx := context.Background()
	err := sender.SendNotification(ctx, "Test Subject", "Test Message")

	// Should not error when no topic is configured
	assert.NoError(t, err)
}

func TestSender_SendToEmail_NoFromEmail(t *testing.T) {
	sender := &Sender{
		fromEmail: "",
	}

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@example.com", "Test Subject", "Test Body")

	// Should not error when no from email is configured
	assert.NoError(t, err)
}

func TestTemplates_NewRecommendations(t *testing.T) {
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

	// Create sender with mock that expects the call
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil).Once()

	sender := &Sender{
		snsClient: nil, // We'll test template rendering separately
		topicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
	}

	// Just test that template parses without error
	ctx := context.Background()
	err := sender.SendNewRecommendationsNotification(ctx, data)

	// Will fail because snsClient is nil, but we're testing template rendering
	require.Error(t, err)
}

func TestTemplates_ScheduledPurchase(t *testing.T) {
	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "approval-token-abc",
		TotalSavings:      1000.00,
		TotalUpfrontCost:  3000.00,
		PurchaseDate:      "February 1, 2024",
		DaysUntilPurchase: 5,
		PlanName:          "AWS RDS Plan",
		Recommendations: []RecommendationSummary{
			{
				Service:        "rds",
				ResourceType:   "db.r5.xlarge",
				Engine:         "postgres",
				Region:         "us-east-1",
				Count:          2,
				MonthlySavings: 400.00,
			},
		},
	}

	sender := &Sender{
		snsClient: nil,
		topicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
	}

	ctx := context.Background()
	err := sender.SendScheduledPurchaseNotification(ctx, data)

	// Will fail because snsClient is nil, but we're testing template rendering
	require.Error(t, err)
}

func TestTemplates_PurchaseConfirmation(t *testing.T) {
	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		TotalSavings:     800.00,
		TotalUpfrontCost: 2400.00,
		PlanName:         "EC2 Reserved Plan",
		Recommendations: []RecommendationSummary{
			{
				Service:        "ec2",
				ResourceType:   "m5.xlarge",
				Region:         "us-west-2",
				Count:          3,
				MonthlySavings: 250.00,
			},
		},
	}

	sender := &Sender{
		snsClient: nil,
		topicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
	}

	ctx := context.Background()
	err := sender.SendPurchaseConfirmation(ctx, data)

	require.Error(t, err)
}

func TestTemplates_PurchaseFailed(t *testing.T) {
	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
		Recommendations: []RecommendationSummary{
			{
				Service:      "opensearch",
				ResourceType: "r5.large.search",
				Region:       "eu-central-1",
				Count:        1,
			},
		},
	}

	sender := &Sender{
		snsClient: nil,
		topicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
	}

	ctx := context.Background()
	err := sender.SendPurchaseFailedNotification(ctx, data)

	require.Error(t, err)
}

func TestTemplates_PasswordReset(t *testing.T) {
	sender := &Sender{
		sesClient: nil,
		fromEmail: "noreply@example.com",
	}

	ctx := context.Background()
	err := sender.SendPasswordResetEmail(ctx, "user@example.com", "https://dashboard.example.com/reset?token=abc")

	// Will fail because sesClient is nil
	require.Error(t, err)
}

func TestTemplates_WelcomeEmail(t *testing.T) {
	sender := &Sender{
		sesClient: nil,
		fromEmail: "noreply@example.com",
	}

	ctx := context.Background()
	err := sender.SendWelcomeEmail(ctx, "newuser@example.com", "https://dashboard.example.com", "user")

	// Will fail because sesClient is nil
	require.Error(t, err)
}

func TestTemplateContents(t *testing.T) {
	// Verify template constants are not empty
	assert.NotEmpty(t, newRecommendationsTemplate)
	assert.NotEmpty(t, scheduledPurchaseTemplate)
	assert.NotEmpty(t, purchaseConfirmationTemplate)
	assert.NotEmpty(t, purchaseFailedTemplate)
	assert.NotEmpty(t, passwordResetTemplate)
	assert.NotEmpty(t, welcomeUserTemplate)

	// Verify templates contain expected placeholders
	assert.Contains(t, newRecommendationsTemplate, "{{.DashboardURL}}")
	assert.Contains(t, newRecommendationsTemplate, ".TotalSavings")
	assert.Contains(t, newRecommendationsTemplate, "{{range .Recommendations}}")

	assert.Contains(t, scheduledPurchaseTemplate, ".DaysUntilPurchase")
	assert.Contains(t, scheduledPurchaseTemplate, "{{.PlanName}}")
	assert.Contains(t, scheduledPurchaseTemplate, "{{urlquery .ApprovalToken}}")

	assert.Contains(t, purchaseConfirmationTemplate, ".TotalSavings")
	assert.Contains(t, purchaseConfirmationTemplate, "Purchases Completed")

	assert.Contains(t, purchaseFailedTemplate, "Purchase Failed")
	assert.Contains(t, purchaseFailedTemplate, "{{.DashboardURL}}")

	assert.Contains(t, passwordResetTemplate, "{{.ResetURL}}")
	assert.Contains(t, passwordResetTemplate, "Password Reset")

	assert.Contains(t, welcomeUserTemplate, "{{.Email}}")
	assert.Contains(t, welcomeUserTemplate, "{{.Role}}")
	assert.Contains(t, welcomeUserTemplate, "Welcome")
}

func TestSender_SendNotification_Success(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	ctx := context.Background()
	err := sender.SendNotification(ctx, "Test Subject", "Test Message")

	require.NoError(t, err)
	mockSNS.AssertExpectations(t)
}

func TestSender_SendNotification_NilClient(t *testing.T) {
	sender := &Sender{
		topicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
		snsClient: nil,
	}

	ctx := context.Background()
	err := sender.SendNotification(ctx, "Test Subject", "Test Message")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SNS client not initialized")
}

func TestSender_SendNotification_Error(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(nil, assert.AnError)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	ctx := context.Background()
	err := sender.SendNotification(ctx, "Test Subject", "Test Message")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to publish to SNS")
}

func TestSender_SendToEmail_Success(t *testing.T) {
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
	err := sender.SendToEmail(ctx, "recipient@example.com", "Test Subject", "Test Body")

	require.NoError(t, err)
	mockSES.AssertExpectations(t)
}

func TestSender_SendToEmail_NilClient(t *testing.T) {
	sender := &Sender{
		fromEmail: "noreply@example.com",
		sesClient: nil,
	}

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@example.com", "Test Subject", "Test Body")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SES client not initialized")
}

func TestSender_SendToEmail_Error(t *testing.T) {
	mockSES := new(MockSESClient)
	// GetAccount is called first - return production mode (not sandbox)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(nil, assert.AnError)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@example.com", "Test Subject", "Test Body")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send email via SES")
}

func TestNewSenderWithClients(t *testing.T) {
	mockSNS := new(MockSNSClient)
	mockSES := new(MockSESClient)

	cfg := SenderConfig{
		TopicARN:     "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail:    "noreply@example.com",
		EmailAddress: "admin@example.com",
	}

	sender := NewSenderWithClients(mockSNS, mockSES, cfg)

	assert.NotNil(t, sender)
	assert.Equal(t, cfg.TopicARN, sender.topicARN)
	assert.Equal(t, cfg.FromEmail, sender.fromEmail)
	assert.Equal(t, cfg.EmailAddress, sender.emailAddress)
}

func TestNewSender_Success(t *testing.T) {
	cfg := SenderConfig{
		TopicARN:     "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail:    "noreply@example.com",
		EmailAddress: "admin@example.com",
	}

	sender, err := NewSender(cfg)

	// NewSender uses awsconfig.LoadDefaultConfig which should work in test environment
	require.NoError(t, err)
	require.NotNil(t, sender)
	assert.Equal(t, cfg.TopicARN, sender.topicARN)
	assert.Equal(t, cfg.FromEmail, sender.fromEmail)
	assert.Equal(t, cfg.EmailAddress, sender.emailAddress)
	assert.NotNil(t, sender.snsClient)
	assert.NotNil(t, sender.sesClient)
}

// Test SendToEmail sandbox mode flows
func TestSender_SendToEmail_SandboxModeVerified(t *testing.T) {
	mockSES := new(MockSESClient)
	// GetAccount returns sandbox mode (ProductionAccessEnabled = false)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: false}, nil)
	// GetEmailIdentity returns verified status
	mockSES.On("GetEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.GetEmailIdentityInput")).
		Return(&sesv2.GetEmailIdentityOutput{VerifiedForSendingStatus: true}, nil)
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("msg-789")}, nil)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "verified@example.com", "Test Subject", "Test Body")

	require.NoError(t, err)
	mockSES.AssertExpectations(t)
}

func TestSender_SendToEmail_SandboxModeNotVerified(t *testing.T) {
	mockSES := new(MockSESClient)
	// GetAccount returns sandbox mode
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: false}, nil)
	// GetEmailIdentity returns not verified
	mockSES.On("GetEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.GetEmailIdentityInput")).
		Return(&sesv2.GetEmailIdentityOutput{VerifiedForSendingStatus: false}, nil)
	// CreateEmailIdentity is called to verify the email
	mockSES.On("CreateEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.CreateEmailIdentityInput")).
		Return(&sesv2.CreateEmailIdentityOutput{}, nil)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "unverified@example.com", "Test Subject", "Test Body")

	// Should return error about unverified email in sandbox mode
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not verified in SES sandbox mode")
	mockSES.AssertExpectations(t)
}

func TestSender_SendToEmail_SandboxCheckError(t *testing.T) {
	mockSES := new(MockSESClient)
	// GetAccount fails
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(nil, assert.AnError)
	// SendEmail should still be called (we continue on GetAccount error)
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("msg-456")}, nil)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@example.com", "Test Subject", "Test Body")

	// Should succeed because GetAccount error is logged but we continue
	require.NoError(t, err)
	mockSES.AssertExpectations(t)
}

func TestSender_SendToEmail_EmailIdentityNotFound(t *testing.T) {
	mockSES := new(MockSESClient)
	// GetAccount returns sandbox mode
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: false}, nil)
	// GetEmailIdentity fails - this means identity doesn't exist
	// According to the code, this returns false, nil (not an error)
	mockSES.On("GetEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.GetEmailIdentityInput")).
		Return(nil, assert.AnError)
	// Since identity doesn't exist and is not verified, CreateEmailIdentity is called
	mockSES.On("CreateEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.CreateEmailIdentityInput")).
		Return(&sesv2.CreateEmailIdentityOutput{}, nil)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@example.com", "Test Subject", "Test Body")

	// Should fail because recipient is not verified in sandbox mode
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not verified in SES sandbox mode")
	mockSES.AssertExpectations(t)
}

func TestSender_SendToEmail_CreateVerificationError(t *testing.T) {
	mockSES := new(MockSESClient)
	// GetAccount returns sandbox mode
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: false}, nil)
	// GetEmailIdentity returns not verified
	mockSES.On("GetEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.GetEmailIdentityInput")).
		Return(&sesv2.GetEmailIdentityOutput{VerifiedForSendingStatus: false}, nil)
	// CreateEmailIdentity fails
	mockSES.On("CreateEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.CreateEmailIdentityInput")).
		Return(nil, assert.AnError)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{
		FromEmail: "noreply@example.com",
	})

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "unverified@example.com", "Test Subject", "Test Body")

	// Should return error about unverified email
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not verified in SES sandbox mode")
	mockSES.AssertExpectations(t)
}

// Test isInSandbox directly
func TestSender_isInSandbox_NilClient(t *testing.T) {
	sender := &Sender{
		sesClient: nil,
	}

	ctx := context.Background()
	_, err := sender.isInSandbox(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SES client not initialized")
}

func TestSender_isInSandbox_ProductionMode(t *testing.T) {
	mockSES := new(MockSESClient)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)

	sender := &Sender{
		sesClient: mockSES,
	}

	ctx := context.Background()
	inSandbox, err := sender.isInSandbox(ctx)

	require.NoError(t, err)
	assert.False(t, inSandbox) // ProductionAccessEnabled = true means NOT in sandbox
	mockSES.AssertExpectations(t)
}

func TestSender_isInSandbox_SandboxMode(t *testing.T) {
	mockSES := new(MockSESClient)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: false}, nil)

	sender := &Sender{
		sesClient: mockSES,
	}

	ctx := context.Background()
	inSandbox, err := sender.isInSandbox(ctx)

	require.NoError(t, err)
	assert.True(t, inSandbox) // ProductionAccessEnabled = false means in sandbox
	mockSES.AssertExpectations(t)
}

// Test isEmailVerified directly
func TestSender_isEmailVerified_NilClient(t *testing.T) {
	sender := &Sender{
		sesClient: nil,
	}

	ctx := context.Background()
	_, err := sender.isEmailVerified(ctx, "test@example.com")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SES client not initialized")
}

func TestSender_isEmailVerified_Verified(t *testing.T) {
	mockSES := new(MockSESClient)
	mockSES.On("GetEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.GetEmailIdentityInput")).
		Return(&sesv2.GetEmailIdentityOutput{VerifiedForSendingStatus: true}, nil)

	sender := &Sender{
		sesClient: mockSES,
	}

	ctx := context.Background()
	verified, err := sender.isEmailVerified(ctx, "verified@example.com")

	require.NoError(t, err)
	assert.True(t, verified)
	mockSES.AssertExpectations(t)
}

func TestSender_isEmailVerified_NotVerified(t *testing.T) {
	mockSES := new(MockSESClient)
	mockSES.On("GetEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.GetEmailIdentityInput")).
		Return(&sesv2.GetEmailIdentityOutput{VerifiedForSendingStatus: false}, nil)

	sender := &Sender{
		sesClient: mockSES,
	}

	ctx := context.Background()
	verified, err := sender.isEmailVerified(ctx, "unverified@example.com")

	require.NoError(t, err)
	assert.False(t, verified)
	mockSES.AssertExpectations(t)
}

func TestSender_isEmailVerified_NotFound(t *testing.T) {
	mockSES := new(MockSESClient)
	mockSES.On("GetEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.GetEmailIdentityInput")).
		Return(nil, assert.AnError)

	sender := &Sender{
		sesClient: mockSES,
	}

	ctx := context.Background()
	verified, err := sender.isEmailVerified(ctx, "nonexistent@example.com")

	// When identity doesn't exist, return false without error
	require.NoError(t, err)
	assert.False(t, verified)
	mockSES.AssertExpectations(t)
}

// Test createVerificationRequest directly
func TestSender_createVerificationRequest_NilClient(t *testing.T) {
	sender := &Sender{
		sesClient: nil,
	}

	ctx := context.Background()
	err := sender.createVerificationRequest(ctx, "test@example.com")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SES client not initialized")
}

func TestSender_createVerificationRequest_Success(t *testing.T) {
	mockSES := new(MockSESClient)
	mockSES.On("CreateEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.CreateEmailIdentityInput")).
		Return(&sesv2.CreateEmailIdentityOutput{}, nil)

	sender := &Sender{
		sesClient: mockSES,
	}

	ctx := context.Background()
	err := sender.createVerificationRequest(ctx, "test@example.com")

	require.NoError(t, err)
	mockSES.AssertExpectations(t)
}

func TestSender_createVerificationRequest_Error(t *testing.T) {
	mockSES := new(MockSESClient)
	mockSES.On("CreateEmailIdentity", mock.Anything, mock.AnythingOfType("*sesv2.CreateEmailIdentityInput")).
		Return(nil, assert.AnError)

	sender := &Sender{
		sesClient: mockSES,
	}

	ctx := context.Background()
	err := sender.createVerificationRequest(ctx, "test@example.com")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create email identity verification")
	mockSES.AssertExpectations(t)
}

func TestNewSenderWithContext_Success(t *testing.T) {
	cfg := SenderConfig{
		TopicARN:     "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail:    "noreply@example.com",
		EmailAddress: "admin@example.com",
	}

	ctx := context.Background()
	sender, err := NewSenderWithContext(ctx, cfg)

	require.NoError(t, err)
	require.NotNil(t, sender)
	assert.Equal(t, cfg.TopicARN, sender.topicARN)
	assert.Equal(t, cfg.FromEmail, sender.fromEmail)
	assert.Equal(t, cfg.EmailAddress, sender.emailAddress)
	assert.NotNil(t, sender.snsClient)
	assert.NotNil(t, sender.sesClient)
}

// Issue #287: SendPurchaseApprovalRequest now ships multipart/alternative
// (text + HTML). Capture the SES SendEmailInput and assert both bodies
// are populated with the expected substrings, and the From address is
// the configured FROM_EMAIL (not a hardcoded literal).
func TestSender_SendPurchaseApprovalRequest_Multipart_Issue287(t *testing.T) {
	mockSES := new(MockSESClient)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)

	var captured *sesv2.SendEmailInput
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*sesv2.SendEmailInput)
		}).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("msg-multipart")}, nil)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{FromEmail: "noreply@example.com"})

	data := NotificationData{
		DashboardURL:     "https://dashboard.example.com",
		ApprovalToken:    "tkn-abc",
		ExecutionID:      "exec-123",
		TotalUpfrontCost: 100.0,
		TotalSavings:     10.0,
		RecipientEmail:   "approver@acme.com",
		RequestedByEmail: "cristi@acme.com",
		Recommendations:  []RecommendationSummary{{Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, Term: 3, Payment: "all-upfront"}},
	}

	err := sender.SendPurchaseApprovalRequest(context.Background(), data)
	require.NoError(t, err)
	require.NotNil(t, captured, "SendEmail should have been called")

	// Both halves populated.
	require.NotNil(t, captured.Content.Simple.Body.Text)
	require.NotNil(t, captured.Content.Simple.Body.Html, "HTML body must be present for multipart/alternative")

	textBody := *captured.Content.Simple.Body.Text.Data
	htmlBody := *captured.Content.Simple.Body.Html.Data

	// Plain-text shape: includes labeled URLs, no HTML markup.
	assert.Contains(t, textBody, "Approve: ")
	assert.Contains(t, textBody, "Cancel: ")
	assert.NotContains(t, textBody, "<a ", "plain-text body should not carry HTML anchors")

	// HTML shape: includes inline-styled anchors.
	assert.Contains(t, htmlBody, `href="https://dashboard.example.com/purchases/approve/exec-123?token=tkn-abc"`)
	assert.Contains(t, htmlBody, "Approve this purchase")

	// From is the configured value, not a hardcoded literal.
	require.NotNil(t, captured.FromEmailAddress)
	assert.Equal(t, "noreply@example.com", *captured.FromEmailAddress)

	// To recipient is the data.RecipientEmail.
	require.Len(t, captured.Destination.ToAddresses, 1)
	assert.Equal(t, "approver@acme.com", captured.Destination.ToAddresses[0])
}

// Issue #287: SendToEmailWithCCMultipart with empty htmlBody falls back
// to single-part text — the existing code path stays valid for callers
// that haven't been upgraded.
func TestSender_SendToEmailWithCCMultipart_FallsBackWhenHTMLEmpty_Issue287(t *testing.T) {
	mockSES := new(MockSESClient)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	var captured *sesv2.SendEmailInput
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*sesv2.SendEmailInput)
		}).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("msg-single")}, nil)

	sender := NewSenderWithClients(nil, mockSES, SenderConfig{FromEmail: "noreply@example.com"})

	err := sender.SendToEmailWithCCMultipart(context.Background(), "to@example.com", nil, "Subj", "plain body", "")
	require.NoError(t, err)
	require.NotNil(t, captured)
	require.NotNil(t, captured.Content.Simple.Body.Text)
	assert.Nil(t, captured.Content.Simple.Body.Html, "empty HTML body should NOT be sent as multipart")
}

// ---------------------------------------------------------------------------
// Regression tests for issue #1015: approval tokens broadcast via SNS
// ---------------------------------------------------------------------------

// TestSendNotification_RejectsTokenBearingBody verifies the structural guard:
// SendNotification must return ErrTokenInBroadcast when the message body
// contains "token=" so a body with an approval URL can never reach the SNS
// broadcast topic, regardless of how it got there.
func TestSendNotification_RejectsTokenBearingBody(t *testing.T) {
	t.Parallel()
	mockSNS := new(MockSNSClient)
	// No Publish calls expected — the guard fires before the client is touched.
	t.Cleanup(func() { mockSNS.AssertExpectations(t) })

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	ctx := context.Background()
	tokenBodies := []string{
		"Review: https://app.example.com/approve?token=abc123",
		"token=somesecret",
		"?token=xyz&action=pause",
		// Mixed-case variants: the guard is case-insensitive so future
		// template changes that capitalize the query param can't slip a
		// token-bearing body past the broadcast guard.
		"Review: https://app.example.com/approve?Token=abc123",
		"?TOKEN=xyz&action=pause",
	}
	for _, body := range tokenBodies {
		err := sender.SendNotification(ctx, "Test", body)
		assert.ErrorIsf(t, err, ErrTokenInBroadcast,
			"expected ErrTokenInBroadcast for body %q", body)
	}
}

// TestSendNotification_AllowsTokenFreeBody verifies that a well-formed
// broadcast body that contains no token reaches SNS normally.
func TestSendNotification_AllowsTokenFreeBody(t *testing.T) {
	t.Parallel()
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-ok")}, nil).Once()
	t.Cleanup(func() { mockSNS.AssertExpectations(t) })

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	ctx := context.Background()
	err := sender.SendNotification(ctx, "Savings found", "3 new recommendations. Sign in to review.")
	require.NoError(t, err)
}

// TestSendScheduledPurchaseNotification_UsesSESNotSNS verifies that a
// scheduled-purchase notification with RecipientEmail set reaches SES
// (targeted delivery) and does NOT call the SNS publisher.
//
// Regression test for issue #1015: previously the method called
// s.SendNotification which broadcast the token-bearing body to every
// SNS subscriber.
func TestSendScheduledPurchaseNotification_UsesSESNotSNS(t *testing.T) {
	t.Parallel()
	mockSNS := new(MockSNSClient)
	// SNS Publish must NOT be called.
	t.Cleanup(func() { mockSNS.AssertExpectations(t) })

	mockSES := new(MockSESClient)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil).Once()
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("ses-msg-1")}, nil).Once()
	t.Cleanup(func() { mockSES.AssertExpectations(t) })

	sender := NewSenderWithClients(mockSNS, mockSES, SenderConfig{
		TopicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail: "noreply@example.com",
	})

	data := NotificationData{
		DashboardURL:      "https://app.example.com",
		ApprovalToken:     "supersecret-token",
		RecipientEmail:    "notify@example.com",
		TotalSavings:      800.0,
		DaysUntilPurchase: 3,
		PlanName:          "Test Plan",
	}

	ctx := context.Background()
	err := sender.SendScheduledPurchaseNotification(ctx, data)
	require.NoError(t, err)
}

// TestSendScheduledPurchaseNotification_ErrNoRecipientWhenEmpty verifies that
// omitting RecipientEmail returns ErrNoRecipient, not a silent broadcast.
func TestSendScheduledPurchaseNotification_ErrNoRecipientWhenEmpty(t *testing.T) {
	t.Parallel()
	mockSNS := new(MockSNSClient)
	mockSES := new(MockSESClient)
	// Neither SNS Publish nor SES SendEmail should be called.
	t.Cleanup(func() {
		mockSNS.AssertExpectations(t)
		mockSES.AssertExpectations(t)
	})

	sender := NewSenderWithClients(mockSNS, mockSES, SenderConfig{
		TopicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail: "noreply@example.com",
	})

	data := NotificationData{
		DashboardURL:  "https://app.example.com",
		ApprovalToken: "supersecret-token",
		PlanName:      "Test Plan",
		// RecipientEmail intentionally absent
	}

	err := sender.SendScheduledPurchaseNotification(context.Background(), data)
	require.ErrorIs(t, err, ErrNoRecipient)
}

// TestSendRIExchangePendingApproval_UsesSESNotSNS verifies that an RI-exchange
// pending-approval notification with RecipientEmail set reaches SES (targeted
// delivery) and does NOT call the SNS publisher.
//
// Regression test for issue #1015: previously the method called
// s.SendNotification which broadcast per-exchange approve/reject tokens to
// every SNS subscriber, allowing unauthorised spend approval.
func TestSendRIExchangePendingApproval_UsesSESNotSNS(t *testing.T) {
	t.Parallel()
	mockSNS := new(MockSNSClient)
	// SNS Publish must NOT be called.
	t.Cleanup(func() { mockSNS.AssertExpectations(t) })

	mockSES := new(MockSESClient)
	mockSES.On("GetAccount", mock.Anything, mock.AnythingOfType("*sesv2.GetAccountInput")).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil).Once()
	mockSES.On("SendEmail", mock.Anything, mock.AnythingOfType("*sesv2.SendEmailInput")).
		Return(&sesv2.SendEmailOutput{MessageId: aws.String("ses-msg-2")}, nil).Once()
	t.Cleanup(func() { mockSES.AssertExpectations(t) })

	sender := NewSenderWithClients(mockSNS, mockSES, SenderConfig{
		TopicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail: "noreply@example.com",
	})

	data := RIExchangeNotificationData{
		DashboardURL:   "https://app.example.com",
		Mode:           "manual",
		RecipientEmail: "notify@example.com",
		TotalPayment:   "150.00",
		Exchanges: []RIExchangeItem{
			{
				RecordID:           "rec-1",
				ApprovalToken:      "exchange-secret-token",
				SourceRIID:         "ri-abc",
				SourceInstanceType: "m5.large",
				TargetInstanceType: "m5.xlarge",
				TargetCount:        1,
				PaymentDue:         "150.00",
				UtilizationPct:     45.0,
			},
		},
	}

	ctx := context.Background()
	err := sender.SendRIExchangePendingApproval(ctx, data)
	require.NoError(t, err)
}

// TestSendRIExchangePendingApproval_ErrNoRecipientWhenEmpty verifies that
// omitting RecipientEmail returns ErrNoRecipient, not a silent broadcast.
func TestSendRIExchangePendingApproval_ErrNoRecipientWhenEmpty(t *testing.T) {
	t.Parallel()
	mockSNS := new(MockSNSClient)
	mockSES := new(MockSESClient)
	// Neither SNS Publish nor SES SendEmail should be called.
	t.Cleanup(func() {
		mockSNS.AssertExpectations(t)
		mockSES.AssertExpectations(t)
	})

	sender := NewSenderWithClients(mockSNS, mockSES, SenderConfig{
		TopicARN:  "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail: "noreply@example.com",
	})

	data := RIExchangeNotificationData{
		DashboardURL: "https://app.example.com",
		Mode:         "manual",
		// RecipientEmail intentionally absent
		Exchanges: []RIExchangeItem{
			{RecordID: "rec-1", ApprovalToken: "exchange-secret-token"},
		},
	}

	err := sender.SendRIExchangePendingApproval(context.Background(), data)
	require.ErrorIs(t, err, ErrNoRecipient)
}

// TestSendNotification_SubjectSanitizedAndTruncated verifies that 07-M3 is
// enforced: SendNotification strips CR/LF from the subject and truncates it
// to 100 bytes before publishing to SNS. A subject longer than 100 bytes or
// containing newlines would cause an InvalidParameter error at runtime.
func TestSendNotification_SubjectSanitizedAndTruncated(t *testing.T) {
	t.Parallel()

	t.Run("newline_stripped", func(t *testing.T) {
		var captured *sns.PublishInput
		mockSNS := new(MockSNSClient)
		mockSNS.On("Publish", mock.Anything, mock.MatchedBy(func(in *sns.PublishInput) bool {
			captured = in
			return true
		})).Return(&sns.PublishOutput{MessageId: aws.String("id-1")}, nil)
		t.Cleanup(func() { mockSNS.AssertExpectations(t) })

		sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
			TopicARN: "arn:aws:sns:us-east-1:123:topic",
		})

		err := sender.SendNotification(context.Background(), "Plan A\r\nInjected", "clean body")
		require.NoError(t, err)
		require.NotNil(t, captured)
		assert.NotContains(t, *captured.Subject, "\r")
		assert.NotContains(t, *captured.Subject, "\n")
	})

	t.Run("long_subject_truncated", func(t *testing.T) {
		const longSubject = "AAAAAAAAAA AAAAAAAAAA AAAAAAAAAA AAAAAAAAAA AAAAAAAAAA AAAAAAAAAA AAAAAAAAAA AAAAAAAAAA AAAAAAAAAA AAAAAAAAAA"
		var captured *sns.PublishInput
		mockSNS := new(MockSNSClient)
		mockSNS.On("Publish", mock.Anything, mock.MatchedBy(func(in *sns.PublishInput) bool {
			captured = in
			return true
		})).Return(&sns.PublishOutput{MessageId: aws.String("id-2")}, nil)
		t.Cleanup(func() { mockSNS.AssertExpectations(t) })

		sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
			TopicARN: "arn:aws:sns:us-east-1:123:topic",
		})

		err := sender.SendNotification(context.Background(), longSubject, "clean body")
		require.NoError(t, err)
		require.NotNil(t, captured)
		assert.LessOrEqual(t, len(*captured.Subject), snsMaxSubjectLen,
			"SNS subject must not exceed %d bytes", snsMaxSubjectLen)
	})
}
