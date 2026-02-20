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
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "token-123",
		TotalSavings:      1000.00,
		TotalUpfrontCost:  3000.00,
		PurchaseDate:      "February 1, 2024",
		DaysUntilPurchase: 5,
		PlanName:          "AWS RDS Plan",
	}

	ctx := context.Background()
	err := sender.SendScheduledPurchaseNotification(ctx, data)

	require.NoError(t, err)
	mockSNS.AssertExpectations(t)
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

// Test template success paths with no recommendations (edge case)
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

// Test when topic/from email are empty (early return paths)
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

func TestSender_SendScheduledPurchaseNotification_NoTopic(t *testing.T) {
	sender := &Sender{
		topicARN: "",
	}

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
	}

	ctx := context.Background()
	err := sender.SendScheduledPurchaseNotification(ctx, data)

	require.NoError(t, err)
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

// Test error cases for template functions
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

func TestSender_SendScheduledPurchaseNotification_SNSError(t *testing.T) {
	mockSNS := new(MockSNSClient)
	sender := &Sender{
		snsClient: mockSNS,
		topicARN:  "arn:aws:sns:us-east-1:123456789:topic",
	}

	mockSNS.On("Publish", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ctx := context.Background()
	data := NotificationData{
		DashboardURL: "https://example.com",
		TotalSavings: 500.0,
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

// Test multiple recommendations in templates
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
	mockSNS := new(MockSNSClient)
	mockSNS.On("Publish", mock.Anything, mock.AnythingOfType("*sns.PublishInput")).
		Return(&sns.PublishOutput{MessageId: aws.String("msg-123")}, nil)

	sender := NewSenderWithClients(mockSNS, nil, SenderConfig{
		TopicARN: "arn:aws:sns:us-east-1:123456789012:topic",
	})

	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		ApprovalToken:     "token-456",
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
	mockSNS.AssertExpectations(t)
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
