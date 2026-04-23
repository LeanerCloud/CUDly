// Package email provides email notification functionality using SNS/SES.
package email

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// Sentinel errors returned by send-path methods when preconditions aren't met.
// Callers use errors.Is to branch on these without pattern-matching on strings.
var (
	// ErrNoRecipient — the NotificationData has no RecipientEmail for a send
	// that requires a specific recipient (e.g. purchase approval for the user
	// who submitted the purchase, not a broadcast to SNS subscribers).
	ErrNoRecipient = errors.New("email: no recipient address")

	// ErrNoFromEmail — the sender has no FROM_EMAIL configured; nothing can go
	// out. Distinct from ErrNoRecipient so the caller can report which side
	// of the wire is unconfigured.
	ErrNoFromEmail = errors.New("email: no from address")
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

// isValidFromEmail checks that a FROM_EMAIL value has the minimum "x@y.z"
// shape SES will accept. We don't RFC5322-validate — just guard against the
// two misconfigurations we've actually seen:
//   - empty string (env var unset)
//   - "noreply@" with a trailing empty domain (Terraform template expanded
//     against an unset subdomain_zone_name tfvar)
//
// Anything else is handed to SES which will reject with a clear error.
func isValidFromEmail(addr string) bool {
	if addr == "" {
		return false
	}
	at := strings.IndexByte(addr, '@')
	if at <= 0 || at == len(addr)-1 {
		return false
	}
	domain := addr[at+1:]
	if !strings.Contains(domain, ".") {
		return false
	}
	return true
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
	return s.SendToEmailWithCC(ctx, toEmail, nil, subject, body)
}

// SendToEmailWithCC sends an email with a primary To recipient plus optional
// Cc recipients. The To recipient is treated as the authorised actor for the
// message (verified in sandbox mode) and Cc recipients are informed of the
// action without carrying the "you must do something" burden. Duplicate
// entries across To/Cc are stripped so a single inbox is never addressed
// twice.
func (s *Sender) SendToEmailWithCC(ctx context.Context, toEmail string, ccEmails []string, subject, body string) error {
	if s.fromEmail == "" {
		logging.Debug("No from email configured, skipping direct email")
		return nil
	}
	if s.sesClient == nil {
		return fmt.Errorf("SES client not initialized")
	}
	if err := s.ensureSandboxRecipientVerified(ctx, toEmail); err != nil {
		return err
	}

	cc := dedupeCCAgainstTo(toEmail, ccEmails)

	input := buildSESSendEmailInput(s.fromEmail, toEmail, cc, subject, body)

	_, err := s.sesClient.SendEmail(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to send email via SES: %w", err)
	}

	if len(cc) > 0 {
		logging.Debugf("Sent email to %s (cc %d): %s", redactEmail(toEmail), len(cc), subject)
	} else {
		logging.Debugf("Sent email to %s: %s", redactEmail(toEmail), subject)
	}
	return nil
}

// ensureSandboxRecipientVerified short-circuits the SES send path when
// the account is in sandbox mode and the recipient isn't yet verified —
// returning a user-facing error asking the recipient to click the
// verification link SES will have just sent. Non-sandbox accounts always
// proceed. Errors from the sandbox probe itself are logged and ignored
// so a transient SES control-plane hiccup doesn't take down the data-
// plane send path.
func (s *Sender) ensureSandboxRecipientVerified(ctx context.Context, toEmail string) error {
	inSandbox, err := s.isInSandbox(ctx)
	if err != nil {
		logging.Warnf("Failed to check SES sandbox status: %v", err)
		return nil
	}
	if !inSandbox {
		return nil
	}
	logging.Infof("SES is in sandbox mode - checking if recipient %s is verified", redactEmail(toEmail))

	verified, err := s.isEmailVerified(ctx, toEmail)
	if err != nil {
		logging.Warnf("Failed to check email verification status: %v", err)
		return nil
	}
	if verified {
		logging.Infof("Recipient email %s is verified - proceeding with send", redactEmail(toEmail))
		return nil
	}
	logging.Warnf("Recipient email %s is not verified in sandbox mode - creating verification request", redactEmail(toEmail))
	if err := s.createVerificationRequest(ctx, toEmail); err != nil {
		logging.Warnf("Failed to create verification request: %v", err)
		return fmt.Errorf("recipient email %s is not verified in SES sandbox mode. A verification email has been sent - please check inbox and click the verification link before trying again", toEmail)
	}
	return fmt.Errorf("recipient email %s is not verified in SES sandbox mode. A verification email has been sent to %s - please check inbox and click the verification link, then try the password reset again", toEmail, toEmail)
}

// buildSESSendEmailInput constructs a sesv2.SendEmailInput with the
// destination To + (optional) Cc list and a plain-text body.
func buildSESSendEmailInput(fromEmail, toEmail string, cc []string, subject, body string) *sesv2.SendEmailInput {
	destination := &types.Destination{
		ToAddresses: []string{toEmail},
	}
	if len(cc) > 0 {
		destination.CcAddresses = cc
	}
	return &sesv2.SendEmailInput{
		Destination: destination,
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
		FromEmailAddress: aws.String(fromEmail),
	}
}

// dedupeCCAgainstTo returns cc with the to-address removed (case-insensitive)
// and duplicate entries collapsed, preserving input order. Empty strings are
// dropped so a caller can freely pass optional slots without sanitising.
func dedupeCCAgainstTo(to string, cc []string) []string {
	if len(cc) == 0 {
		return nil
	}
	seen := map[string]bool{strings.ToLower(strings.TrimSpace(to)): true}
	out := make([]string, 0, len(cc))
	for _, addr := range cc {
		norm := strings.ToLower(strings.TrimSpace(addr))
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, addr)
	}
	return out
}

// NotificationData holds data for rendering email templates
type NotificationData struct {
	DashboardURL      string
	ApprovalToken     string
	ExecutionID       string
	TotalSavings      float64
	TotalUpfrontCost  float64
	Recommendations   []RecommendationSummary
	PurchaseDate      string
	DaysUntilPurchase int
	PlanName          string
	// RecipientEmail addresses the individual recipient for flows that target
	// a specific user (e.g. purchase approval). Leave empty for broadcast
	// flows that go to preconfigured subscribers via SNS. Purchase approvals
	// MUST set this — silently broadcasting an approval link to every
	// subscriber of an SNS alerts topic would leak the approval token.
	RecipientEmail string
	// CCEmails carries additional recipients (e.g. the global notification
	// email) for flows where more than one inbox needs visibility into the
	// action but only one party is authorised to approve. Empty for single-
	// recipient flows. Purchase approvals use this to keep the global
	// notification email informed while directing the approver role at the
	// account's contact email.
	CCEmails []string
	// AuthorizedApprovers carries the email(s) of the parties who are
	// allowed to click the approve/cancel links. The template prints these
	// verbatim in the message body so recipients on CC know the action
	// isn't theirs to take. When empty the template omits the authorisation
	// block (legacy broadcast behaviour).
	AuthorizedApprovers []string
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
