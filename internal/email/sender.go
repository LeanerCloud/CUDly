// Package email provides email notification functionality using SNS/SES.
package email

import (
	"context"
	"errors"
	"fmt"
	"os"
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

	// ErrTokenInBroadcast is returned by SendNotification when the message
	// body contains a "token=" query parameter. Broadcasting a body with an
	// approval token leaks it to every SNS subscriber. Use SendToEmailWithCC
	// (targeted SES) for any message that carries an approval URL.
	ErrTokenInBroadcast = errors.New("email: message body contains an approval token; use targeted SES send, not SNS broadcast")
)

// SenderConfig holds configuration for the email sender.
type SenderConfig struct {
	TopicARN     string
	FromEmail    string
	EmailAddress string // Legacy: for SNS notifications
}

// Sender handles sending email notifications.
type Sender struct {
	snsClient    SNSPublisher
	sesClient    SESEmailSender
	topicARN     string
	fromEmail    string
	emailAddress string
	// muteChecker consults the muted_recipients table before each send.
	// Nil disables mute checking (e.g. when no DB is wired in tests).
	muteChecker MuteChecker
	// unsubscribeBaseURL is the dashboard base URL used to construct the
	// List-Unsubscribe header value. Empty disables the header.
	unsubscribeBaseURL string
}

// NewSender creates a new email sender with default context.
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
	return strings.Contains(domain, ".")
}

// NewSenderWithContext creates a new email sender with the provided context.
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

// NewSenderWithClients creates a new email sender with custom clients (for testing).
func NewSenderWithClients(snsClient SNSPublisher, sesClient SESEmailSender, cfg SenderConfig) *Sender {
	return &Sender{
		snsClient:    snsClient,
		sesClient:    sesClient,
		topicARN:     cfg.TopicARN,
		fromEmail:    cfg.FromEmail,
		emailAddress: cfg.EmailAddress,
	}
}

// WithMuteChecker returns a shallow copy of s with the given MuteChecker wired
// in. Callers that have a DB-backed store use this to enable per-recipient mute
// suppression on outbound SES sends.
func (s *Sender) WithMuteChecker(mc MuteChecker) *Sender {
	c := *s
	c.muteChecker = mc
	return &c
}

// WithUnsubscribeBaseURL returns a shallow copy of s with the given base URL
// set. When non-empty the sender appends List-Unsubscribe / List-Unsubscribe-Post
// headers (RFC 8058) to outbound SES messages for applicable scopes.
func (s *Sender) WithUnsubscribeBaseURL(u string) *Sender {
	c := *s
	c.unsubscribeBaseURL = u
	return &c
}

// muteKey reads NOTIFICATION_MUTE_SECRET from env for token derivation. Returns
// nil when unset so DeriveMuteToken uses the dev fallback.
func muteKey() []byte {
	v := os.Getenv("NOTIFICATION_MUTE_SECRET")
	if v == "" {
		return nil
	}
	return []byte(v)
}

// buildUnsubscribeURL constructs the one-click unsubscribe URL for the given
// (email, scope) pair. Returns ("", "") when unsubscribeBaseURL is empty.
func (s *Sender) buildUnsubscribeURL(email, scope string) (unsubURL, mailtoURL string) {
	return unsubscribeURLFor(s.unsubscribeBaseURL, email, scope), ""
}

// listUnsubscribeHeaders returns the List-Unsubscribe and List-Unsubscribe-Post
// header values for the given (email, scope) pair (RFC 8058).
// Returns ("", "") when no base URL is configured.
func (s *Sender) listUnsubscribeHeaders(email, scope string) (headerValue, postValue string) {
	return unsubscribeHeaderValuesFor(s.unsubscribeBaseURL, email, scope)
}

// isMuted returns true when the given address is muted for this scope. When the
// mute checker is nil or returns an error the address is treated as not muted so
// a transient DB outage doesn't silently block approval emails.
func (s *Sender) isMuted(ctx context.Context, email, scope string) bool {
	return isRecipientMuted(ctx, s.muteChecker, email, scope)
}

// filterMutedAddresses returns a copy of addrs with any muted (for scope)
// entries removed. The original slice is not modified. Errors from the mute
// store are treated as "not muted" (fail-open) so a DB hiccup does not
// silently suppress approval emails.
func (s *Sender) filterMutedAddresses(ctx context.Context, addrs []string, scope string) []string {
	return filterMutedRecipients(ctx, s.muteChecker, addrs, scope)
}

// snsMaxSubjectLen is the maximum byte length SNS accepts for a Subject.
// Subjects longer than 100 bytes are rejected with InvalidParameter at runtime.
const snsMaxSubjectLen = 100

// SendNotification sends a notification email via SNS.
//
// Guard: messages whose body contains "token=" (case-insensitive) are rejected
// with ErrTokenInBroadcast. Approval tokens must never reach the SNS broadcast
// topic because every subscriber would receive a working action link. Use
// SendToEmailWithCC for any message that carries an approval URL.
//
// Subject sanitization (07-M3): the subject is passed through sanitizeHeader
// (strips CR/LF to prevent SNS parameter injection) and truncated to 100 bytes
// (SNS limit). Subjects built from user-controlled fields such as PlanName can
// otherwise cause an InvalidParameter error at publish time.
func (s *Sender) SendNotification(ctx context.Context, subject, message string) error {
	if strings.Contains(strings.ToLower(message), "token=") {
		return ErrTokenInBroadcast
	}

	if s.topicARN == "" {
		logging.Debug("No SNS topic configured, skipping email notification")
		return nil
	}

	if s.snsClient == nil {
		return fmt.Errorf("SNS client not initialized")
	}

	// Sanitize and truncate the SNS Subject (07-M3).
	safeSubject := sanitizeHeader(subject)
	if len(safeSubject) > snsMaxSubjectLen {
		safeSubject = safeSubject[:snsMaxSubjectLen]
	}

	_, err := s.snsClient.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(s.topicARN),
		Subject:  aws.String(safeSubject),
		Message:  aws.String(message),
	})
	if err != nil {
		return fmt.Errorf("failed to publish to SNS: %w", err)
	}

	logging.Debugf("Sent notification: %s", safeSubject)
	return nil
}

// isInSandbox checks if SES is in sandbox mode.
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

// isEmailVerified checks if an email identity is verified in SES.
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

// createVerificationRequest initiates email verification for an email address.
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
// If SES is in sandbox mode, it will automatically verify the recipient email if needed.
func (s *Sender) SendToEmail(ctx context.Context, toEmail, subject, body string) error {
	return s.SendToEmailWithCC(ctx, toEmail, nil, subject, body)
}

// SendToEmailWithCCMultipart is the multipart/alternative variant of
// SendToEmailWithCC: callers pass both a plain-text body and an HTML body,
// SES emits a multipart message, and the recipient's mail client picks
// whichever rendering it supports. Mirrors SendToEmailWithCC for the To/Cc
// dedupe + sandbox-recipient verification — the only delta is the email
// content shape. htmlBody == "" degrades to a single-part text send so
// callers that don't have an HTML body don't need a separate code path.
func (s *Sender) SendToEmailWithCCMultipart(ctx context.Context, toEmail string, ccEmails []string, subject, textBody, htmlBody string) error {
	if htmlBody == "" {
		return s.SendToEmailWithCC(ctx, toEmail, ccEmails, subject, textBody)
	}
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

	input := buildSESSendEmailInputMultipart(s.fromEmail, toEmail, cc, subject, textBody, htmlBody, nil)

	_, err := s.sesClient.SendEmail(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to send email via SES: %w", err)
	}

	if len(cc) > 0 {
		logging.Debugf("Sent multipart email to %s (cc %d): %s", redactEmail(toEmail), len(cc), subject)
	} else {
		logging.Debugf("Sent multipart email to %s: %s", redactEmail(toEmail), subject)
	}
	return nil
}

// SendToEmailWithCC sends an email with a primary To recipient plus optional
// Cc recipients. The To recipient is treated as the authorized actor for the
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

	input := buildSESSendEmailInput(s.fromEmail, toEmail, cc, subject, body, nil)

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
		// Use the real address in the user-facing message so the recipient
		// knows which inbox to check; log only the redacted form (07-M2).
		return fmt.Errorf("recipient email %s is not verified in SES sandbox mode. A verification email has been sent - please check inbox and click the verification link before trying again", toEmail)
	}
	// The user-facing message intentionally includes the full address so the
	// recipient can verify the correct inbox. Wrapped errors/logs use the
	// redacted form per the package's PII stance (07-M2).
	return fmt.Errorf("recipient email %s is not verified in SES sandbox mode. A verification email has been sent - please check inbox and click the verification link, then try again", toEmail)
}

// buildSESSendEmailInputMultipart constructs a sesv2.SendEmailInput with
// both a plain-text and an HTML alternative body. SES handles the
// multipart/alternative MIME assembly server-side when both Text and Html
// fields are populated on types.Body. extraHeaders are appended as-is; use
// addListUnsubscribeHeaders to build the RFC 8058 pair.
//
// Header injection note (07-M1): SES v2 SendEmail accepts structured fields
// (Subject.Data, Body.Text.Data, Body.Html.Data) and builds the MIME envelope
// server-side. CR/LF injection via Subject.Data is not possible through the
// structured API. Any future raw-MIME path MUST sanitize the subject with
// sanitizeHeader before composing the header string, matching the SMTP path.
func buildSESSendEmailInputMultipart(fromEmail, toEmail string, cc []string, subject, textBody, htmlBody string, extraHeaders []types.MessageHeader) *sesv2.SendEmailInput {
	destination := &types.Destination{
		ToAddresses: []string{toEmail},
	}
	if len(cc) > 0 {
		destination.CcAddresses = cc
	}
	msg := &types.Message{
		Subject: &types.Content{
			Charset: aws.String("UTF-8"),
			Data:    aws.String(subject),
		},
		Body: &types.Body{
			Text: &types.Content{
				Charset: aws.String("UTF-8"),
				Data:    aws.String(textBody),
			},
			Html: &types.Content{
				Charset: aws.String("UTF-8"),
				Data:    aws.String(htmlBody),
			},
		},
	}
	if len(extraHeaders) > 0 {
		msg.Headers = extraHeaders
	}
	return &sesv2.SendEmailInput{
		Destination:      destination,
		Content:          &types.EmailContent{Simple: msg},
		FromEmailAddress: aws.String(fromEmail),
	}
}

// buildSESSendEmailInput constructs a sesv2.SendEmailInput with the
// destination To + (optional) Cc list and a plain-text body. extraHeaders are
// appended as-is; use addListUnsubscribeHeaders to build the RFC 8058 pair.
// See buildSESSendEmailInputMultipart for the header-injection safety note (07-M1).
func buildSESSendEmailInput(fromEmail, toEmail string, cc []string, subject, body string, extraHeaders []types.MessageHeader) *sesv2.SendEmailInput {
	destination := &types.Destination{
		ToAddresses: []string{toEmail},
	}
	if len(cc) > 0 {
		destination.CcAddresses = cc
	}
	msg := &types.Message{
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
	}
	if len(extraHeaders) > 0 {
		msg.Headers = extraHeaders
	}
	return &sesv2.SendEmailInput{
		Destination:      destination,
		Content:          &types.EmailContent{Simple: msg},
		FromEmailAddress: aws.String(fromEmail),
	}
}

// addListUnsubscribeHeaders returns the RFC 8058 List-Unsubscribe pair as
// sesv2 MessageHeader values. Returns nil when headerValue is empty.
func addListUnsubscribeHeaders(headerValue, postValue string) []types.MessageHeader {
	if headerValue == "" {
		return nil
	}
	hdrs := []types.MessageHeader{
		{Name: aws.String("List-Unsubscribe"), Value: aws.String(headerValue)},
	}
	if postValue != "" {
		hdrs = append(hdrs, types.MessageHeader{
			Name:  aws.String("List-Unsubscribe-Post"),
			Value: aws.String(postValue),
		})
	}
	return hdrs
}

// sendToEmailWithCCMultipartHeaders is the internal variant of
// SendToEmailWithCCMultipart that also accepts custom message headers (e.g.
// List-Unsubscribe). It is used by the mute-aware send path in
// SendPurchaseApprovalRequest so we don't expose a wider public API.
func (s *Sender) sendToEmailWithCCMultipartHeaders(
	ctx context.Context,
	toEmail string,
	ccEmails []string,
	subject, textBody, htmlBody string,
	extraHeaders []types.MessageHeader,
) error {
	if htmlBody == "" {
		// Degrade to plain text; headers still carried via the non-multipart path.
		return s.sendToEmailWithCCHeaders(ctx, toEmail, ccEmails, subject, textBody, extraHeaders)
	}
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
	input := buildSESSendEmailInputMultipart(s.fromEmail, toEmail, cc, subject, textBody, htmlBody, extraHeaders)
	if _, err := s.sesClient.SendEmail(ctx, input); err != nil {
		return fmt.Errorf("failed to send email via SES: %w", err)
	}
	if len(cc) > 0 {
		logging.Debugf("Sent multipart email to %s (cc %d): %s", redactEmail(toEmail), len(cc), subject)
	} else {
		logging.Debugf("Sent multipart email to %s: %s", redactEmail(toEmail), subject)
	}
	return nil
}

// sendToEmailWithCCHeaders is the plain-text variant of
// sendToEmailWithCCMultipartHeaders.
func (s *Sender) sendToEmailWithCCHeaders(
	ctx context.Context,
	toEmail string,
	ccEmails []string,
	subject, body string,
	extraHeaders []types.MessageHeader,
) error {
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
	input := buildSESSendEmailInput(s.fromEmail, toEmail, cc, subject, body, extraHeaders)
	if _, err := s.sesClient.SendEmail(ctx, input); err != nil {
		return fmt.Errorf("failed to send email via SES: %w", err)
	}
	if len(cc) > 0 {
		logging.Debugf("Sent email to %s (cc %d): %s", redactEmail(toEmail), len(cc), subject)
	} else {
		logging.Debugf("Sent email to %s: %s", redactEmail(toEmail), subject)
	}
	return nil
}

// dedupeCCAgainstTo returns cc with the to-address removed (case-insensitive)
// and duplicate entries collapsed, preserving input order. Empty strings are
// dropped so a caller can freely pass optional slots without sanitizing.
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

// NotificationData holds data for rendering email templates.
type NotificationData struct {
	RequestedAt              string
	RequestedByName          string
	ExecutionID              string
	PlanID                   string
	RevokeURL                string
	RevocationWindowClosesAt string
	ArcheraEducationURL      string
	PurchaseDate             string
	PlanName                 string
	ApprovalToken            string
	CancellationWindowNote   string
	DashboardURL             string
	RecipientEmail           string
	RequestedByEmail         string
	CCEmails                 []string
	AuthorizedApprovers      []string
	Recommendations          []RecommendationSummary
	DaysUntilPurchase        int
	TotalUpfrontCost         float64
	TotalSavings             float64
	// RevocationToken is the one-time token embedded in the revocation link
	// of a post-execution notification email. When non-empty, the template
	// renders a "Revoke this purchase" CTA that hits
	// /api/purchases/revoke/{ExecutionID}?token=<RevocationToken>.
	// Empty silently omits the revocation panel so other email flows are
	// unaffected.
	RevocationToken string
	// ExecutedAt is the ISO-8601 / RFC-3339 timestamp the purchase was
	// executed at. Used in the post-execution notification body.
	// Empty omits the timestamp from the body.
	ExecutedAt string
	// ExecutedBy is the email of the user who triggered execution (approved
	// the purchase). Used in the post-execution notification body.
	// Empty omits the field.
	ExecutedBy string
}

// RecommendationSummary is a simplified recommendation for email display.
type RecommendationSummary struct {
	Service        string
	ResourceType   string
	Engine         string
	Region         string
	Payment        string
	AccountLabel   string
	Count          int
	MonthlySavings float64
	Term           int
	UpfrontCost    float64
}

// RIExchangeNotificationData holds data for RI exchange email templates.
type RIExchangeNotificationData struct {
	DashboardURL string
	Mode         string
	Exchanges    []RIExchangeItem
	Skipped      []SkippedExchange
	TotalPayment string
	// RecipientEmail is the primary (To) inbox for the approval-required flow.
	// Must be set when Exchanges contain ApprovalToken values; leave empty
	// only for completion/broadcast notifications that carry no tokens.
	// When non-empty, SendRIExchangePendingApproval routes through targeted
	// SES (not the SNS broadcast topic). Mirrors NotificationData.RecipientEmail.
	RecipientEmail string
	// CCEmails carries additional recipients informed of the pending exchanges
	// but not the authorized approvers. Deduplicated against RecipientEmail.
	CCEmails []string
	// RequestedByName is the human-readable display name of the user who
	// triggered the exchange run. Empty falls back to RequestedByEmail.
	RequestedByName string
	// RequestedByEmail is the requester's email address. Empty omits the
	// requested-by block from the approval email.
	RequestedByEmail string
	// RequestedAt is the ISO-8601 / RFC3339 timestamp the exchange was
	// submitted. Empty omits the timestamp from the summary.
	RequestedAt string
	// CancellationWindowNote is short text rendered below the approve/reject
	// buttons. Empty falls back to a generic 6-hour note.
	CancellationWindowNote string
}

// RIExchangeItem represents a single exchange in an email notification.
type RIExchangeItem struct {
	RecordID           string
	ApprovalToken      string
	SourceRIID         string
	SourceInstanceType string
	TargetInstanceType string
	PaymentDue         string
	ExchangeID         string
	Error              string
	TargetCount        int
	UtilizationPct     float64
}

// SkippedExchange represents an exchange that was skipped.
type SkippedExchange struct {
	SourceRIID         string
	SourceInstanceType string
	Reason             string
}

// redactEmail returns a redacted version of an email address for safe logging.
// e.g. "user@example.com" -> "us***@example.com".
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
