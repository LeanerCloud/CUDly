// Package email provides email notification functionality using SMTP.
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// SMTPConfig holds configuration for SMTP email sender
type SMTPConfig struct {
	Host        string // SMTP server host (e.g., "smtp.sendgrid.net" or "smtp.azurecomm.net")
	Port        int    // SMTP server port (usually 587 for TLS, 465 for SSL)
	Username    string // SMTP username (SendGrid API key or Azure connection username)
	Password    string // SMTP password
	FromEmail   string
	FromName    string
	NotifyEmail string // Notification recipient email (defaults to FromEmail if empty)
	UseTLS      bool   // Use STARTTLS (default true)
}

// SMTPSender handles sending email via SMTP (works for SendGrid, Azure ACS, and others)
type SMTPSender struct {
	host        string
	port        int
	username    string
	password    string
	fromEmail   string
	fromName    string
	notifyEmail string
	useTLS      bool
}

// NewSMTPSender creates a new SMTP email sender
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
		host:        cfg.Host,
		port:        cfg.Port,
		username:    cfg.Username,
		password:    cfg.Password,
		fromEmail:   cfg.FromEmail,
		fromName:    cfg.FromName,
		notifyEmail: notifyEmail,
		useTLS:      cfg.UseTLS,
	}, nil
}

// SendNotification sends a notification email via SMTP
// Note: SMTP doesn't have SNS-like pub/sub, so this sends directly
func (s *SMTPSender) SendNotification(ctx context.Context, subject, message string) error {
	logging.Debug("SMTP notification would be sent (not implemented for pub/sub)")
	return nil
}

// sanitizeHeader strips CR and LF characters to prevent SMTP header injection.
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// SendToEmail sends an email directly to a specific email address via SMTP
func (s *SMTPSender) SendToEmail(ctx context.Context, toEmail, subject, body string) error {
	if s.fromEmail == "" {
		logging.Debug("No from email configured, skipping email")
		return nil
	}

	// Sanitize header values to prevent SMTP header injection
	toEmail = sanitizeHeader(toEmail)
	subject = sanitizeHeader(subject)

	// Build email message
	from := s.fromEmail
	if s.fromName != "" {
		from = fmt.Sprintf("%s <%s>", sanitizeHeader(s.fromName), s.fromEmail)
	}

	msg := []byte(fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/plain; charset=UTF-8\r\n"+
		"\r\n"+
		"%s\r\n", from, toEmail, subject, body))

	// Connect to SMTP server
	addr := fmt.Sprintf("%s:%d", s.host, s.port)

	var auth smtp.Auth
	if s.username != "" && s.password != "" {
		auth = smtp.PlainAuth("", s.username, s.password, s.host)
	}

	// Send email
	if s.useTLS {
		// Use STARTTLS
		err := s.sendMailTLS(addr, auth, s.fromEmail, []string{toEmail}, msg)
		if err != nil {
			return fmt.Errorf("failed to send email via SMTP: %w", err)
		}
	} else {
		// Use standard smtp.SendMail
		err := smtp.SendMail(addr, auth, s.fromEmail, []string{toEmail}, msg)
		if err != nil {
			return fmt.Errorf("failed to send email via SMTP: %w", err)
		}
	}

	logging.Debugf("Sent email via SMTP to %s: %s", toEmail, subject)
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

// sendMailTLS sends email using STARTTLS (required for most modern SMTP servers)
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

	if err = c.StartTLS(&tls.Config{ServerName: host}); err != nil {
		return err
	}

	if err = smtpAuthenticate(c, auth); err != nil {
		return err
	}

	if err = smtpSendBody(c, from, to, msg); err != nil {
		return err
	}

	return c.Quit()
}

// SendPasswordResetEmail sends a password reset email
func (s *SMTPSender) SendPasswordResetEmail(ctx context.Context, email, resetURL string) error {
	subject := "Password Reset Request - CUDly"
	body, err := RenderPasswordResetEmail(email, resetURL)
	if err != nil {
		return fmt.Errorf("failed to render password reset email: %w", err)
	}
	return s.SendToEmail(ctx, email, subject, body)
}

// SendWelcomeEmail sends a welcome email to new users
func (s *SMTPSender) SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error {
	subject := "Welcome to CUDly!"
	body, err := RenderWelcomeEmail(email, dashboardURL, role)
	if err != nil {
		return fmt.Errorf("failed to render welcome email: %w", err)
	}
	return s.SendToEmail(ctx, email, subject, body)
}

// SendNewRecommendationsNotification sends a notification about new recommendations
func (s *SMTPSender) SendNewRecommendationsNotification(ctx context.Context, data NotificationData) error {
	subject := "New CUDly Recommendations Available"
	body, err := RenderNewRecommendationsEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render new recommendations email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendScheduledPurchaseNotification sends a notification about scheduled purchase
func (s *SMTPSender) SendScheduledPurchaseNotification(ctx context.Context, data NotificationData) error {
	subject := fmt.Sprintf("CUDly Purchase Scheduled: %s", data.PlanName)
	body, err := RenderScheduledPurchaseEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render scheduled purchase email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendPurchaseConfirmation sends a confirmation email after successful purchase
func (s *SMTPSender) SendPurchaseConfirmation(ctx context.Context, data NotificationData) error {
	subject := "CUDly Purchase Confirmation"
	body, err := RenderPurchaseConfirmationEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render purchase confirmation email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendPurchaseFailedNotification sends a notification when a purchase fails
func (s *SMTPSender) SendPurchaseFailedNotification(ctx context.Context, data NotificationData) error {
	subject := "CUDly Purchase Failed"
	body, err := RenderPurchaseFailedEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render purchase failed email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendRIExchangePendingApproval sends an RI exchange approval email via SMTP
func (s *SMTPSender) SendRIExchangePendingApproval(ctx context.Context, data RIExchangeNotificationData) error {
	subject := fmt.Sprintf("CUDly - RI Exchange Approval Required (%d exchanges)", len(data.Exchanges))
	body, err := RenderRIExchangePendingApprovalEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render ri exchange pending approval email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// SendRIExchangeCompleted sends an RI exchange completion email via SMTP
func (s *SMTPSender) SendRIExchangeCompleted(ctx context.Context, data RIExchangeNotificationData) error {
	subject := fmt.Sprintf("CUDly - RI Exchanges Completed (%d exchanges)", len(data.Exchanges))
	body, err := RenderRIExchangeCompletedEmail(data)
	if err != nil {
		return fmt.Errorf("failed to render ri exchange completed email: %w", err)
	}
	return s.SendToEmail(ctx, s.notifyEmail, subject, body)
}

// Verify that SMTPSender implements SenderInterface
var _ SenderInterface = (*SMTPSender)(nil)
