// Package email provides email notification functionality using SMTP.
package email

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// SMTPConfig holds configuration for SMTP email sender.
type SMTPConfig struct {
	Host          string
	Username      string
	Password      string //nolint:gosec // G101: field holds a user-supplied runtime password, not a hardcoded credential
	FromEmail     string
	FromName      string
	NotifyEmail   string
	Port          int
	UseTLS        bool
	AllowInsecure bool
}

// SMTPSender handles sending email via SMTP (works for SendGrid, Azure ACS, and others).
type SMTPSender struct {
	host          string
	username      string
	password      string
	fromEmail     string
	fromName      string
	notifyEmail   string
	port          int
	useTLS        bool
	allowInsecure bool
	// muteChecker consults the muted_recipients table before each send.
	// Nil disables mute checking (e.g. when no DB is wired in tests).
	muteChecker MuteChecker
	// unsubscribeBaseURL is the dashboard base URL used to construct the
	// List-Unsubscribe header value. Empty disables the header.
	unsubscribeBaseURL string
}

// WithMuteChecker returns a shallow copy of s with the given MuteChecker wired
// in, mirroring (*Sender).WithMuteChecker so the SMTP transport applies the same
// per-recipient mute suppression as SES.
func (s *SMTPSender) WithMuteChecker(mc MuteChecker) *SMTPSender {
	c := *s
	c.muteChecker = mc
	return &c
}

// WithUnsubscribeBaseURL returns a shallow copy of s with the given base URL
// set, mirroring (*Sender).WithUnsubscribeBaseURL. When non-empty the SMTP
// approval send emits RFC 8058 List-Unsubscribe headers.
func (s *SMTPSender) WithUnsubscribeBaseURL(u string) *SMTPSender {
	c := *s
	c.unsubscribeBaseURL = u
	return &c
}

// NewSMTPSender creates a new SMTP email sender.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("SMTP host is required")
	}
	if cfg.FromEmail == "" {
		return nil, fmt.Errorf("from email is required")
	}

	// Set defaults
	if cfg.Port == 0 {
		cfg.Port = 587 // Default to TLS port
	}
	if !cfg.UseTLS && cfg.Port == 587 {
		cfg.UseTLS = true // Enable TLS for port 587 by default
	}

	notifyEmail := cfg.NotifyEmail
	if notifyEmail == "" {
		notifyEmail = cfg.FromEmail
	}

	return &SMTPSender{
		host:          cfg.Host,
		port:          cfg.Port,
		username:      cfg.Username,
		password:      cfg.Password,
		fromEmail:     cfg.FromEmail,
		fromName:      cfg.FromName,
		notifyEmail:   notifyEmail,
		useTLS:        cfg.UseTLS,
		allowInsecure: cfg.AllowInsecure,
	}, nil
}

// SendNotification is a no-op for SMTP senders (07-N4).
// SMTP has no pub/sub equivalent of SNS: there is no topic to publish to and
// no subscriber list to fan out to. As a result, GCP (SendGrid) and Azure
// (ACS SMTP) deployments do not receive broadcast notifications (new-recs,
// scheduled-purchase reminders without a recipient email, etc.). Callers that
// need broadcast behavior on non-AWS deployments must wire their own fan-out
// or configure an SNS-compatible endpoint. Targeted approval emails
// (SendPurchaseApprovalRequest, SendScheduledPurchaseNotification) are
// unaffected because they use SendToEmailWithCC directly.
func (s *SMTPSender) SendNotification(ctx context.Context, subject, message string) error {
	logging.Debug("SMTP broadcast notification is a no-op (SMTP has no pub/sub mechanism)")
	return nil
}

// sanitizeHeader strips CR and LF characters to prevent SMTP header injection.
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// SendToEmail sends an email directly to a specific email address via SMTP.
func (s *SMTPSender) SendToEmail(ctx context.Context, toEmail, subject, body string) error {
	return s.SendToEmailWithCC(ctx, toEmail, nil, subject, body)
}

// SendToEmailWithCCMultipart sends a multipart/alternative message (plain-
// text + HTML) via SMTP. htmlBody == "" degrades to a single-part text send
// for backwards compatibility with callers that don't have an HTML body.
func (s *SMTPSender) SendToEmailWithCCMultipart(ctx context.Context, toEmail string, ccEmails []string, subject, textBody, htmlBody string) error {
	if htmlBody == "" {
		return s.SendToEmailWithCC(ctx, toEmail, ccEmails, subject, textBody)
	}
	if s.fromEmail == "" {
		logging.Debug("No from email configured, skipping email")
		return nil
	}

	toEmail = sanitizeHeader(toEmail)
	subject = sanitizeHeader(subject)
	sanitizedCC := sanitizeCCList(toEmail, ccEmails)

	msg := s.buildSMTPMessageMultipart(toEmail, sanitizedCC, subject, textBody, htmlBody)
	rcpts := append([]string{toEmail}, sanitizedCC...)

	if err := s.dispatchSMTP(rcpts, msg); err != nil {
		return err
	}

	if len(sanitizedCC) > 0 {
		logging.Debugf("Sent multipart email via SMTP to %s (cc %d): %s", toEmail, len(sanitizedCC), subject)
	} else {
		logging.Debugf("Sent multipart email via SMTP to %s: %s", toEmail, subject)
	}
	return nil
}

// SendToEmailWithCC sends an email with To + optional Cc recipients via SMTP.
// The Cc header is included in the message envelope so recipients see one
// another, and the SMTP RCPT TO list carries every address so each inbox
// actually receives the message.
func (s *SMTPSender) SendToEmailWithCC(ctx context.Context, toEmail string, ccEmails []string, subject, body string) error {
	if s.fromEmail == "" {
		logging.Debug("No from email configured, skipping email")
		return nil
	}

	// Sanitize header values to prevent SMTP header injection
	toEmail = sanitizeHeader(toEmail)
	subject = sanitizeHeader(subject)

	sanitizedCC := sanitizeCCList(toEmail, ccEmails)

	msg := s.buildSMTPMessage(toEmail, sanitizedCC, subject, body)
	rcpts := append([]string{toEmail}, sanitizedCC...)

	if err := s.dispatchSMTP(rcpts, msg); err != nil {
		return err
	}

	if len(sanitizedCC) > 0 {
		logging.Debugf("Sent email via SMTP to %s (cc %d): %s", toEmail, len(sanitizedCC), subject)
	} else {
		logging.Debugf("Sent email via SMTP to %s: %s", toEmail, subject)
	}
	return nil
}

// sanitizeCCList dedupes cc against toEmail and applies header
// sanitization to each surviving entry.
func sanitizeCCList(toEmail string, ccEmails []string) []string {
	cc := dedupeCCAgainstTo(toEmail, ccEmails)
	if len(cc) == 0 {
		return nil
	}
	out := make([]string, 0, len(cc))
	for _, addr := range cc {
		out = append(out, sanitizeHeader(addr))
	}
	return out
}

// mimeRandBoundary returns a per-message random MIME boundary (07-N2).
// Using a fixed constant boundary risks corruption if a message body
// literally contains that string. crypto/rand gives a collision-free 8-byte
// hex suffix; falls back to a fixed literal on the (exceptional) rand failure
// so messages still deliver in degraded environments.
func mimeRandBoundary() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "cudly-mp-fallback-7e3b1c89af04d2"
	}
	return "cudly-mp-" + hex.EncodeToString(b)
}

// buildSMTPMessageMultipart assembles a multipart/alternative RFC-5322
// message with both a plain-text and an HTML part. A per-message random
// boundary is generated via mimeRandBoundary to eliminate the theoretical
// body-collision risk of a fixed literal (07-N2). `cc` is already sanitized
// and deduped by the caller.
func (s *SMTPSender) buildSMTPMessageMultipart(toEmail string, cc []string, subject, textBody, htmlBody string) []byte {
	boundary := mimeRandBoundary()
	from := s.fromEmail
	if s.fromName != "" {
		from = fmt.Sprintf("%s <%s>", sanitizeHeader(s.fromName), s.fromEmail)
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\n", from, toEmail)
	if len(cc) > 0 {
		headers += fmt.Sprintf("Cc: %s\r\n", strings.Join(cc, ", "))
	}
	headers += fmt.Sprintf("Subject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", subject, boundary)

	return []byte(headers + buildMultipartBody(boundary, textBody, htmlBody))
}

// buildSMTPMessageMultipartWithHeaders is buildSMTPMessageMultipart with extra
// pre-sanitized header lines (e.g. List-Unsubscribe) inserted before the
// header-terminating blank line. extraHeaders must already be CRLF-terminated.
// A per-message random boundary is generated via mimeRandBoundary to avoid
// the body-collision risk of a fixed literal (07-N2).
func (s *SMTPSender) buildSMTPMessageMultipartWithHeaders(toEmail string, cc []string, subject, textBody, htmlBody, extraHeaders string) []byte {
	boundary := mimeRandBoundary()
	from := s.fromEmail
	if s.fromName != "" {
		from = fmt.Sprintf("%s <%s>", sanitizeHeader(s.fromName), s.fromEmail)
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\n", from, toEmail)
	if len(cc) > 0 {
		headers += fmt.Sprintf("Cc: %s\r\n", strings.Join(cc, ", "))
	}
	headers += fmt.Sprintf("Subject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"%s\"\r\n", subject, boundary)
	headers += extraHeaders
	headers += "\r\n"

	return []byte(headers + buildMultipartBody(boundary, textBody, htmlBody))
}

// buildMultipartBody assembles the multipart/alternative body (text + HTML
// parts) for the given boundary. Shared by the plain and header-aware builders.
func buildMultipartBody(boundary, textBody, htmlBody string) string {
	var body strings.Builder
	body.WriteString("--")
	body.WriteString(boundary)
	body.WriteString("\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	body.WriteString(textBody)
	body.WriteString("\r\n--")
	body.WriteString(boundary)
	body.WriteString("\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	body.WriteString(htmlBody)
	body.WriteString("\r\n--")
	body.WriteString(boundary)
	body.WriteString("--\r\n")

	return body.String()
}

// buildSMTPMessageWithHeaders is buildSMTPMessage with extra pre-sanitized
// header lines (e.g. List-Unsubscribe) inserted before the header-terminating
// blank line. extraHeaders must already be CRLF-terminated.
func (s *SMTPSender) buildSMTPMessageWithHeaders(toEmail string, cc []string, subject, body, extraHeaders string) []byte {
	from := s.fromEmail
	if s.fromName != "" {
		from = fmt.Sprintf("%s <%s>", sanitizeHeader(s.fromName), s.fromEmail)
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\n", from, toEmail)
	if len(cc) > 0 {
		headers += fmt.Sprintf("Cc: %s\r\n", strings.Join(cc, ", "))
	}
	headers += fmt.Sprintf("Subject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n", subject)
	headers += extraHeaders
	headers += "\r\n"
	return []byte(headers + body + "\r\n")
}

// buildSMTPMessage assembles the RFC-5322 message bytes (headers + blank
// line + body) for sending via SMTP. `cc` is already sanitized and
// deduped by the caller.
func (s *SMTPSender) buildSMTPMessage(toEmail string, cc []string, subject, body string) []byte {
	from := s.fromEmail
	if s.fromName != "" {
		from = fmt.Sprintf("%s <%s>", sanitizeHeader(s.fromName), s.fromEmail)
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\n", from, toEmail)
	if len(cc) > 0 {
		headers += fmt.Sprintf("Cc: %s\r\n", strings.Join(cc, ", "))
	}
	headers += fmt.Sprintf("Subject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n", subject)
	return []byte(headers + body + "\r\n")
}

// dispatchSMTP runs the actual SMTP SendMail call, routing through
// STARTTLS when s.useTLS is true. The rcpts list carries every address
// that should receive the message (To + Cc).
//
// Security (07-H2): if credentials are configured but TLS is disabled the
// call is refused unless AllowInsecure was explicitly set. Sending SMTP AUTH
// over a non-TLS connection exposes credentials (SendGrid API key, Azure ACS
// password) and token-bearing message bodies in cleartext. AllowInsecure must
// never be set in production; it exists solely for integration tests against a
// local plaintext stub server.
func (s *SMTPSender) dispatchSMTP(rcpts []string, msg []byte) error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	var auth smtp.Auth
	if s.username != "" && s.password != "" {
		auth = smtp.PlainAuth("", s.username, s.password, s.host)
	}
	if auth != nil && !s.useTLS && !s.allowInsecure {
		return fmt.Errorf("SMTP auth over non-TLS connection is refused: set UseTLS=true or, for test-only stubs, AllowInsecure=true")
	}
	if s.useTLS {
		if err := s.sendMailTLS(addr, auth, s.fromEmail, rcpts, msg); err != nil {
			return fmt.Errorf("failed to send email via SMTP: %w", err)
		}
		return nil
	}
	if err := smtp.SendMail(addr, auth, s.fromEmail, rcpts, msg); err != nil {
		return fmt.Errorf("failed to send email via SMTP: %w", err)
	}
	return nil
}

// smtpAuthenticate performs SMTP AUTH with user-friendly 535 error translation.
func smtpAuthenticate(c *smtp.Client, auth smtp.Auth) error {
	if auth == nil {
		return nil
	}
	if err := c.Auth(auth); err != nil {
		if strings.Contains(err.Error(), "535") {
			return fmt.Errorf("SMTP authentication failed - check username/password")
		}
		return err
	}
	return nil
}

// smtpSendBody performs the MAIL/RCPT/DATA sequence on an already-authenticated client.
func smtpSendBody(c *smtp.Client, from string, to []string, msg []byte) error {
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return err
		}
	}

	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

// sendMailTLS sends email using STARTTLS (required for most modern SMTP servers).
func (s *SMTPSender) sendMailTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}

	// MinVersion guards against TLS 1.0/1.1 negotiation (issue #410).
	if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
		return err
	}

	if err := smtpAuthenticate(c, auth); err != nil {
		return err
	}

	if err := smtpSendBody(c, from, to, msg); err != nil {
		return err
	}

	return c.Quit()
}

// SendPasswordResetEmail sends a password reset email as multipart/
// alternative (text + styled HTML). Issue #355.
func (s *SMTPSender) SendPasswordResetEmail(ctx context.Context, email, resetURL string) error {
	return sendMultipartVia(
		ctx, s, email, "Password Reset Request - CUDly", "password-reset",
		func() (string, error) { return RenderPasswordResetEmail(email, resetURL) },
		func() (string, error) { return RenderPasswordResetEmailHTML(email, resetURL) },
	)
}

// SendWelcomeEmail sends a welcome email to new users as multipart/
// alternative (text + styled HTML). Issue #355.
func (s *SMTPSender) SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error {
	return sendMultipartVia(
		ctx, s, email, "Welcome to CUDly!", "welcome",
		func() (string, error) { return RenderWelcomeEmail(email, dashboardURL, role) },
		func() (string, error) { return RenderWelcomeEmailHTML(email, dashboardURL, role) },
	)
}

// SendUserInviteEmail sends an invite-with-setup-link email to a user
// created without a password, as multipart/alternative (text + styled HTML).
// Issue #355.
func (s *SMTPSender) SendUserInviteEmail(ctx context.Context, email, setupURL string) error {
	return sendMultipartVia(
		ctx, s, email, "CUDly - Set your password", "user-invite",
		func() (string, error) { return RenderUserInviteEmail(email, setupURL) },
		func() (string, error) { return RenderUserInviteEmailHTML(email, setupURL) },
	)
}

// SendNewRecommendationsNotification sends a notification about new recommendations.
func (s *SMTPSender) SendNewRecommendationsNotification(ctx context.Context, data NotificationData) error {
	subject := "New CUDly Recommendations Available"
	body, err := RenderNewRecommendationsEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render new recommendations email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendScheduledPurchaseNotification sends a notification about scheduled purchase.
func (s *SMTPSender) SendScheduledPurchaseNotification(ctx context.Context, data NotificationData) error {
	subject := fmt.Sprintf("CUDly Purchase Scheduled: %s", data.PlanName)
	body, err := RenderScheduledPurchaseEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render scheduled purchase email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendPurchaseConfirmation sends a confirmation email after successful purchase.
func (s *SMTPSender) SendPurchaseConfirmation(ctx context.Context, data NotificationData) error {
	subject := "CUDly Purchase Confirmation"
	body, err := RenderPurchaseConfirmationEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render purchase confirmation email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendPurchaseFailedNotification sends a notification when a purchase fails.
func (s *SMTPSender) SendPurchaseFailedNotification(ctx context.Context, data NotificationData) error {
	subject := "CUDly Purchase Failed"
	body, err := RenderPurchaseFailedEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render purchase failed email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendRIExchangePendingApproval sends an RI exchange approval email via SMTP
// as multipart/alternative (plain-text + styled HTML). The body carries live
// approval tokens, so it is sent only to the resolved recipient (the
// submitter's notification email, falling back to the static SMTP notify
// address) plus the deduplicated CC list; it never broadcasts. Returns
// ErrNoRecipient when neither address is configured. Issue #296.
func (s *SMTPSender) SendRIExchangePendingApproval(ctx context.Context, data RIExchangeNotificationData) error {
	recipient := data.RecipientEmail
	if recipient == "" {
		recipient = s.notifyEmail
	}
	if recipient == "" {
		return ErrNoRecipient
	}
	subject := fmt.Sprintf("CUDly - RI Exchange Approval Required (%d exchanges)", len(data.Exchanges))
	return sendRIExchangePendingApprovalVia(ctx, s, recipient, data.CCEmails, subject, data)
}

// SendRIExchangeCompleted sends an RI exchange completion email via SMTP.
func (s *SMTPSender) SendRIExchangeCompleted(ctx context.Context, data RIExchangeNotificationData) error {
	subject := fmt.Sprintf("CUDly - RI Exchanges Completed (%d exchanges)", len(data.Exchanges))
	body, err := RenderRIExchangeCompletedEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render ri exchange completed email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendPurchaseApprovalRequest sends a purchase approval request email via SMTP.
// Prefers data.RecipientEmail (the submitter's notification email from app
// settings) over the static SMTP-configured s.notifyEmail so the approval token
// lands in the right inbox per submitter.
//
// Mute check + List-Unsubscribe mirror the SES (*Sender) path: if the recipient
// has opted out of purchase_approvals the email is silently skipped, muted CC
// addresses are dropped, and an RFC 8058 List-Unsubscribe header pair is added
// when an unsubscribe base URL is configured.
func (s *SMTPSender) SendPurchaseApprovalRequest(ctx context.Context, data NotificationData) error {
	recipient := data.RecipientEmail
	if recipient == "" {
		recipient = s.notifyEmail
	}
	if recipient == "" {
		return ErrNoRecipient
	}

	scope := string(common.ScopePurchaseApprovals)

	// Per-recipient mute check: skip silently if the approver has opted out.
	if isRecipientMuted(ctx, s.muteChecker, recipient, scope) {
		logging.Infof("email/smtp: purchase approval skipped for muted recipient (scope=%s)", scope)
		return nil
	}

	// Filter CC list against mutes so no muted address receives a copy.
	filteredCC := filterMutedRecipients(ctx, s.muteChecker, data.CCEmails, scope)

	// Build RFC 8058 List-Unsubscribe headers scoped to the primary recipient.
	unsubHdr, postHdr := unsubscribeHeaderValuesFor(s.unsubscribeBaseURL, recipient, scope)

	subject := fmt.Sprintf("CUDly - Purchase Approval Required (%d commitment(s))", len(data.Recommendations))

	textBody, err := RenderPurchaseApprovalRequestEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render purchase approval request email (text): %w", err)
	}
	htmlBody, htmlErr := RenderPurchaseApprovalRequestEmailHTML(data)
	if htmlErr != nil {
		logging.Warnf("email: HTML approval-request render failed, falling back to text-only: %v", htmlErr)
		htmlBody = ""
	}
	return s.sendMultipartWithUnsubscribe(ctx, recipient, filteredCC, subject, textBody, htmlBody, unsubHdr, postHdr)
}

// sendMultipartWithUnsubscribe sends a multipart/alternative (text + HTML)
// message via SMTP, optionally injecting the RFC 8058 List-Unsubscribe /
// List-Unsubscribe-Post headers. htmlBody == "" degrades to a single-part text
// send. unsubHdr == "" omits the unsubscribe headers entirely.
func (s *SMTPSender) sendMultipartWithUnsubscribe(ctx context.Context, toEmail string, ccEmails []string, subject, textBody, htmlBody, unsubHdr, postHdr string) error {
	if unsubHdr == "" {
		// No List-Unsubscribe headers required: reuse the existing send path.
		return s.SendToEmailWithCCMultipart(ctx, toEmail, ccEmails, subject, textBody, htmlBody)
	}
	if s.fromEmail == "" {
		logging.Debug("No from email configured, skipping email")
		return nil
	}

	toEmail = sanitizeHeader(toEmail)
	subject = sanitizeHeader(subject)
	sanitizedCC := sanitizeCCList(toEmail, ccEmails)

	extra := buildListUnsubscribeHeaderLines(unsubHdr, postHdr)
	var msg []byte
	if htmlBody == "" {
		msg = s.buildSMTPMessageWithHeaders(toEmail, sanitizedCC, subject, textBody, extra)
	} else {
		msg = s.buildSMTPMessageMultipartWithHeaders(toEmail, sanitizedCC, subject, textBody, htmlBody, extra)
	}
	rcpts := append([]string{toEmail}, sanitizedCC...)

	if err := s.dispatchSMTP(rcpts, msg); err != nil {
		return err
	}

	if len(sanitizedCC) > 0 {
		logging.Debugf("Sent approval email via SMTP to %s (cc %d): %s", toEmail, len(sanitizedCC), subject)
	} else {
		logging.Debugf("Sent approval email via SMTP to %s: %s", toEmail, subject)
	}
	return nil
}

// buildListUnsubscribeHeaderLines returns the RFC 8058 List-Unsubscribe header
// lines (each terminated with CRLF), already sanitized against header
// injection. Returns "" when headerValue is empty.
func buildListUnsubscribeHeaderLines(headerValue, postValue string) string {
	if headerValue == "" {
		return ""
	}
	lines := fmt.Sprintf("List-Unsubscribe: %s\r\n", sanitizeHeader(headerValue))
	if postValue != "" {
		lines += fmt.Sprintf("List-Unsubscribe-Post: %s\r\n", sanitizeHeader(postValue))
	}
	return lines
}

// SendPurchaseScheduledNotification sends the Gmail-style pre-fire delay
// notification email via SMTP. Mirrors the Sender implementation's behavior.
func (s *SMTPSender) SendPurchaseScheduledNotification(ctx context.Context, data NotificationData) error {
	body, err := RenderPurchaseScheduledDelayEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render purchase scheduled delay email: %w", err)
	}
	subject := fmt.Sprintf("CUDly - Purchase Scheduled for %s", data.RevocationWindowClosesAt)
	recipient := data.RecipientEmail
	if recipient == "" {
		recipient = s.notifyEmail
	}
	if recipient == "" {
		return ErrNoRecipient
	}
	return s.SendToEmailWithCC(ctx, recipient, data.CCEmails, subject, body)
}

// SendPurchaseExecutedNotification sends the post-execution notification email
// via SMTP. Mirrors SendPurchaseApprovalRequest: prefers data.RecipientEmail
// over the static s.notifyEmail. Issue #291.
func (s *SMTPSender) SendPurchaseExecutedNotification(ctx context.Context, data NotificationData) error {
	recipient := data.RecipientEmail
	if recipient == "" {
		recipient = s.notifyEmail
	}
	if recipient == "" {
		return ErrNoRecipient
	}
	subject := buildExecutedNotificationSubject(data)
	return sendPurchaseExecutedNotificationVia(ctx, s, recipient, subject, data)
}

// SendRegistrationReceivedNotification sends an email to CUDly administrators
// for a new registration via SMTP. Prefers the caller-resolved
// data.RecipientEmail + CCEmails (admin emails + global notify) so the To /
// Cc semantics match the "authorized reviewers" block in the body; falls
// back to the legacy static s.notifyEmail when the caller didn't resolve
// recipients (e.g. no admin users configured yet).
func (s *SMTPSender) SendRegistrationReceivedNotification(ctx context.Context, data RegistrationNotificationData) error {
	// Sanitize user-controlled fields before interpolating into the Subject header
	// to prevent SMTP header injection (issue #401).
	subject := fmt.Sprintf("CUDly - New Account Registration: %s (%s)",
		sanitizeHeader(data.AccountName), sanitizeHeader(data.Provider))
	body, err := RenderRegistrationReceivedEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render registration received email: %w", err)
	}
	recipient := data.RecipientEmail
	if recipient == "" {
		recipient = s.notifyEmail
	}
	return s.SendToEmailWithCC(ctx, recipient, data.CCEmails, subject, body)
}

// SendRegistrationDecisionNotification sends approval/rejection to the registrant via SMTP.
func (s *SMTPSender) SendRegistrationDecisionNotification(ctx context.Context, toEmail string, data RegistrationDecisionData) error {
	subject := fmt.Sprintf("CUDly - Account Registration %s", data.Decision)
	body, err := RenderRegistrationDecisionEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render registration decision email: %w", err)
	}
	return s.SendToEmail(ctx, toEmail, subject, body)
}

// Verify that SMTPSender implements SenderInterface.
var _ SenderInterface = (*SMTPSender)(nil)
