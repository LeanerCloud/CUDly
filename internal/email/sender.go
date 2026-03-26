// Package email provides email notification functionality using SNS/SES.
package email

import (
	"context"
	"fmt"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// SenderConfig holds configuration for the email sender
type SenderConfig struct {
	TopicARN     string
	FromEmail    string
	EmailAddress string // Legacy: for SNS notifications
}

// Sender handles sending email notifications
type Sender struct {
	snsClient    SNSPublisher
	sesClient    SESEmailSender
	topicARN     string
	fromEmail    string
	emailAddress string
}

// NewSender creates a new email sender with default context
func NewSender(cfg SenderConfig) (*Sender, error) {
	return NewSenderWithContext(context.Background(), cfg)
}

// NewSenderWithContext creates a new email sender with the provided context
func NewSenderWithContext(ctx context.Context, cfg SenderConfig) (*Sender, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &Sender{
		snsClient:    sns.NewFromConfig(awsCfg),
		sesClient:    sesv2.NewFromConfig(awsCfg),
		topicARN:     cfg.TopicARN,
		fromEmail:    cfg.FromEmail,
		emailAddress: cfg.EmailAddress,
	}, nil
}

// NewSenderWithClients creates a new email sender with custom clients (for testing)
func NewSenderWithClients(snsClient SNSPublisher, sesClient SESEmailSender, cfg SenderConfig) *Sender {
	return &Sender{
		snsClient:    snsClient,
		sesClient:    sesClient,
		topicARN:     cfg.TopicARN,
		fromEmail:    cfg.FromEmail,
		emailAddress: cfg.EmailAddress,
	}
}

// SendNotification sends a notification email via SNS
func (s *Sender) SendNotification(ctx context.Context, subject, message string) error {
	if s.topicARN == "" {
		logging.Debug("No SNS topic configured, skipping email notification")
		return nil
	}

	if s.snsClient == nil {
		return fmt.Errorf("SNS client not initialized")
	}

	_, err := s.snsClient.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(s.topicARN),
		Subject:  aws.String(subject),
		Message:  aws.String(message),
	})
	if err != nil {
		return fmt.Errorf("failed to publish to SNS: %w", err)
	}

	logging.Debugf("Sent notification: %s", subject)
	return nil
}

// isInSandbox checks if SES is in sandbox mode
func (s *Sender) isInSandbox(ctx context.Context) (bool, error) {
	if s.sesClient == nil {
		return false, fmt.Errorf("SES client not initialized")
	}

	output, err := s.sesClient.GetAccount(ctx, &sesv2.GetAccountInput{})
	if err != nil {
		return false, fmt.Errorf("failed to get SES account info: %w", err)
	}

	// ProductionAccessEnabled is false when in sandbox mode
	return !output.ProductionAccessEnabled, nil
}

// isEmailVerified checks if an email identity is verified in SES
func (s *Sender) isEmailVerified(ctx context.Context, email string) (bool, error) {
	if s.sesClient == nil {
		return false, fmt.Errorf("SES client not initialized")
	}

	output, err := s.sesClient.GetEmailIdentity(ctx, &sesv2.GetEmailIdentityInput{
		EmailIdentity: aws.String(email),
	})
	if err != nil {
		// If identity doesn't exist, it's not verified
		return false, nil
	}

	return output.VerifiedForSendingStatus, nil
}

// createVerificationRequest initiates email verification for an email address
func (s *Sender) createVerificationRequest(ctx context.Context, email string) error {
	if s.sesClient == nil {
		return fmt.Errorf("SES client not initialized")
	}

	_, err := s.sesClient.CreateEmailIdentity(ctx, &sesv2.CreateEmailIdentityInput{
		EmailIdentity: aws.String(email),
	})
	if err != nil {
		return fmt.Errorf("failed to create email identity verification: %w", err)
	}

	logging.Infof("Created email verification request for %s - check inbox for verification email", email)
	return nil
}

// SendToEmail sends an email directly to a specific email address via SES
// If SES is in sandbox mode, it will automatically verify the recipient email if needed
func (s *Sender) SendToEmail(ctx context.Context, toEmail, subject, body string) error {
	if s.fromEmail == "" {
		logging.Debug("No from email configured, skipping direct email")
		return nil
	}

	if s.sesClient == nil {
		return fmt.Errorf("SES client not initialized")
	}

	// Check if SES is in sandbox mode
	inSandbox, err := s.isInSandbox(ctx)
	if err != nil {
		logging.Warnf("Failed to check SES sandbox status: %v", err)
		// Continue anyway - if we're not in sandbox, the send will work
	} else if inSandbox {
		logging.Infof("SES is in sandbox mode - checking if recipient %s is verified", redactEmail(toEmail))

		// Check if recipient email is verified
		verified, err := s.isEmailVerified(ctx, toEmail)
		if err != nil {
			logging.Warnf("Failed to check email verification status: %v", err)
		} else if !verified {
			logging.Warnf("Recipient email %s is not verified in sandbox mode - creating verification request", redactEmail(toEmail))
			if err := s.createVerificationRequest(ctx, toEmail); err != nil {
				logging.Warnf("Failed to create verification request: %v", err)
				return fmt.Errorf("recipient email %s is not verified in SES sandbox mode. A verification email has been sent - please check inbox and click the verification link before trying again", toEmail)
			}
			return fmt.Errorf("recipient email %s is not verified in SES sandbox mode. A verification email has been sent to %s - please check inbox and click the verification link, then try the password reset again", toEmail, toEmail)
		}

		logging.Infof("Recipient email %s is verified - proceeding with send", redactEmail(toEmail))
	}

	input := &sesv2.SendEmailInput{
		Destination: &types.Destination{
			ToAddresses: []string{toEmail},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{
					Charset: aws.String("UTF-8"),
					Data:    aws.String(subject),
				},
				Body: &types.Body{
					Text: &types.Content{
						Charset: aws.String("UTF-8"),
						Data:    aws.String(body),
					},
				},
			},
		},
		FromEmailAddress: aws.String(s.fromEmail),
	}

	_, err = s.sesClient.SendEmail(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to send email via SES: %w", err)
	}

	logging.Debugf("Sent email to %s: %s", redactEmail(toEmail), subject)
	return nil
}

// NotificationData holds data for rendering email templates
type NotificationData struct {
	DashboardURL      string
	ApprovalToken     string
	TotalSavings      float64
	TotalUpfrontCost  float64
	Recommendations   []RecommendationSummary
	PurchaseDate      string
	DaysUntilPurchase int
	PlanName          string
}

// RecommendationSummary is a simplified recommendation for email display
type RecommendationSummary struct {
	Service        string
	ResourceType   string
	Engine         string
	Region         string
	Count          int
	MonthlySavings float64
}

// RIExchangeNotificationData holds data for RI exchange email templates
type RIExchangeNotificationData struct {
	DashboardURL string
	Mode         string
	Exchanges    []RIExchangeItem
	Skipped      []SkippedExchange
	TotalPayment string
}

// RIExchangeItem represents a single exchange in an email notification
type RIExchangeItem struct {
	RecordID           string
	ApprovalToken      string
	SourceRIID         string
	SourceInstanceType string
	TargetInstanceType string
	TargetCount        int
	PaymentDue         string
	ExchangeID         string
	UtilizationPct     float64
	Error              string
}

// SkippedExchange represents an exchange that was skipped
type SkippedExchange struct {
	SourceRIID         string
	SourceInstanceType string
	Reason             string
}

// redactEmail returns a redacted version of an email address for safe logging.
// e.g. "user@example.com" -> "us***@example.com"
func redactEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at:] // includes the '@'
	if len(local) <= 2 {
		return "***" + domain
	}
	return local[:2] + "***" + domain
}
