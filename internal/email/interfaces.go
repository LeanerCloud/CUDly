package email

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// SenderInterface defines the methods required for sending emails
type SenderInterface interface {
	SendNotification(ctx context.Context, subject, message string) error
	SendToEmail(ctx context.Context, toEmail, subject, body string) error
	SendNewRecommendationsNotification(ctx context.Context, data NotificationData) error
	SendScheduledPurchaseNotification(ctx context.Context, data NotificationData) error
	SendPurchaseConfirmation(ctx context.Context, data NotificationData) error
	SendPurchaseFailedNotification(ctx context.Context, data NotificationData) error
	SendPasswordResetEmail(ctx context.Context, email, resetURL string) error
	SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error
	SendRIExchangePendingApproval(ctx context.Context, data RIExchangeNotificationData) error
	SendRIExchangeCompleted(ctx context.Context, data RIExchangeNotificationData) error
	SendPurchaseApprovalRequest(ctx context.Context, data NotificationData) error
}

// Verify that Sender implements SenderInterface
var _ SenderInterface = (*Sender)(nil)

// SNSPublisher defines the interface for SNS publish operations
type SNSPublisher interface {
	Publish(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
}

// SESEmailSender defines the interface for SES send email operations
type SESEmailSender interface {
	SendEmail(ctx context.Context, params *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
	GetAccount(ctx context.Context, params *sesv2.GetAccountInput, optFns ...func(*sesv2.Options)) (*sesv2.GetAccountOutput, error)
	GetEmailIdentity(ctx context.Context, params *sesv2.GetEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error)
	CreateEmailIdentity(ctx context.Context, params *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error)
}

// Ensure concrete types implement interfaces
var _ SNSPublisher = (*sns.Client)(nil)
var _ SESEmailSender = (*sesv2.Client)(nil)
