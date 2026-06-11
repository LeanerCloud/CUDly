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
	SendToEmailWithCCMultipart(ctx context.Context, toEmail string, ccEmails []string, subject, textBody, htmlBody string) error
	SendNewRecommendationsNotification(ctx context.Context, data NotificationData) error
	SendScheduledPurchaseNotification(ctx context.Context, data NotificationData) error
	SendPurchaseConfirmation(ctx context.Context, data NotificationData) error
	SendPurchaseFailedNotification(ctx context.Context, data NotificationData) error
	SendPasswordResetEmail(ctx context.Context, email, resetURL string) error
	SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error
	SendUserInviteEmail(ctx context.Context, email, setupURL string) error
	SendRIExchangePendingApproval(ctx context.Context, data RIExchangeNotificationData) error
	SendRIExchangeCompleted(ctx context.Context, data RIExchangeNotificationData) error
	SendPurchaseApprovalRequest(ctx context.Context, data NotificationData) error
	// SendPurchaseScheduledNotification sends the "approved with delay" email
	// immediately after an approval when Gmail-style pre-fire delay is configured
	// (issue #291 wave-2). Notifies the user that the purchase will execute at
	// RevocationWindowClosesAt and includes a one-click revoke link.
	SendPurchaseScheduledNotification(ctx context.Context, data NotificationData) error
	// SendPurchaseExecutedNotification fires after a purchase executes
	// (regardless of whether it came from the approval-email path or the
	// direct-execute path). Recipients: global notification_email, per-account
	// contact emails, and the requester. The data must carry RevocationToken
	// and RevocationWindowClosesAt so the email embeds a one-click revoke link
	// valid for the AWS cancel window.
	SendPurchaseExecutedNotification(ctx context.Context, data NotificationData) error
	SendRegistrationReceivedNotification(ctx context.Context, data RegistrationNotificationData) error
	SendRegistrationDecisionNotification(ctx context.Context, toEmail string, data RegistrationDecisionData) error
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
