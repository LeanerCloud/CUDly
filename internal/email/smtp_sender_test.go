package email

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSMTPSender_Success(t *testing.T) {
	cfg := SMTPConfig{
		Host:      "smtp.example.com",
		Port:      587,
		Username:  "user@example.com",
		Password:  "secret",
		FromEmail: "noreply@example.com",
		FromName:  "CUDly",
		UseTLS:    true,
	}

	sender, err := NewSMTPSender(cfg)

	require.NoError(t, err)
	require.NotNil(t, sender)
	assert.Equal(t, cfg.Host, sender.host)
	assert.Equal(t, cfg.Port, sender.port)
	assert.Equal(t, cfg.Username, sender.username)
	assert.Equal(t, cfg.Password, sender.password)
	assert.Equal(t, cfg.FromEmail, sender.fromEmail)
	assert.Equal(t, cfg.FromName, sender.fromName)
	assert.True(t, sender.useTLS)
}

func TestNewSMTPSender_MissingHost(t *testing.T) {
	cfg := SMTPConfig{
		FromEmail: "noreply@example.com",
		// Host intentionally not set
	}

	_, err := NewSMTPSender(cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SMTP host is required")
}

func TestNewSMTPSender_MissingFromEmail(t *testing.T) {
	cfg := SMTPConfig{
		Host: "smtp.example.com",
		// FromEmail intentionally not set
	}

	_, err := NewSMTPSender(cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "from email is required")
}

func TestNewSMTPSender_DefaultPort(t *testing.T) {
	cfg := SMTPConfig{
		Host:      "smtp.example.com",
		FromEmail: "noreply@example.com",
		// Port intentionally not set - should default to 587
	}

	sender, err := NewSMTPSender(cfg)

	require.NoError(t, err)
	assert.Equal(t, 587, sender.port)
	assert.True(t, sender.useTLS) // TLS should be enabled for port 587
}

func TestNewSMTPSender_CustomPort(t *testing.T) {
	cfg := SMTPConfig{
		Host:      "smtp.example.com",
		Port:      465,
		FromEmail: "noreply@example.com",
		UseTLS:    false,
	}

	sender, err := NewSMTPSender(cfg)

	require.NoError(t, err)
	assert.Equal(t, 465, sender.port)
	assert.False(t, sender.useTLS)
}

func TestNewSMTPSender_Port587AutoTLS(t *testing.T) {
	cfg := SMTPConfig{
		Host:      "smtp.example.com",
		Port:      587,
		FromEmail: "noreply@example.com",
		UseTLS:    false, // Even with UseTLS=false, port 587 should enable TLS
	}

	sender, err := NewSMTPSender(cfg)

	require.NoError(t, err)
	assert.True(t, sender.useTLS) // TLS should be auto-enabled for port 587
}

func TestSMTPConfig_Structure(t *testing.T) {
	cfg := SMTPConfig{
		Host:      "smtp.sendgrid.net",
		Port:      587,
		Username:  "apikey",
		Password:  "SG.xxxxx",
		FromEmail: "noreply@example.com",
		FromName:  "Test App",
		UseTLS:    true,
	}

	assert.Equal(t, "smtp.sendgrid.net", cfg.Host)
	assert.Equal(t, 587, cfg.Port)
	assert.Equal(t, "apikey", cfg.Username)
	assert.Equal(t, "SG.xxxxx", cfg.Password)
	assert.Equal(t, "noreply@example.com", cfg.FromEmail)
	assert.Equal(t, "Test App", cfg.FromName)
	assert.True(t, cfg.UseTLS)
}

func TestSMTPSender_SendNotification(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "noreply@example.com",
	}

	ctx := context.Background()
	err := sender.SendNotification(ctx, "Test Subject", "Test Message")

	// SendNotification for SMTP is a no-op (returns nil)
	require.NoError(t, err)
}

func TestSMTPSender_SendToEmail_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "", // No from email
	}

	ctx := context.Background()
	err := sender.SendToEmail(ctx, "recipient@example.com", "Test Subject", "Test Body")

	// Should return nil when no from email is configured
	require.NoError(t, err)
}

func TestSMTPSender_SendPasswordResetEmail_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "", // No from email - should skip
	}

	ctx := context.Background()
	err := sender.SendPasswordResetEmail(ctx, "user@example.com", "https://example.com/reset")

	// Should return nil when no from email is configured
	require.NoError(t, err)
}

func TestSMTPSender_SendWelcomeEmail_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "", // No from email - should skip
	}

	ctx := context.Background()
	err := sender.SendWelcomeEmail(ctx, "user@example.com", "https://dashboard.example.com", "user")

	require.NoError(t, err)
}

func TestSMTPSender_SendNewRecommendationsNotification_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "", // No from email - should skip
	}

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalSavings: 1000.00,
	}

	ctx := context.Background()
	err := sender.SendNewRecommendationsNotification(ctx, data)

	require.NoError(t, err)
}

func TestSMTPSender_SendScheduledPurchaseNotification_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "", // No from email - should skip
	}

	data := NotificationData{
		DashboardURL:      "https://dashboard.example.com",
		PlanName:          "Test Plan",
		DaysUntilPurchase: 7,
	}

	ctx := context.Background()
	err := sender.SendScheduledPurchaseNotification(ctx, data)

	require.NoError(t, err)
}

func TestSMTPSender_SendPurchaseConfirmation_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "", // No from email - should skip
	}

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
		TotalSavings: 500.00,
	}

	ctx := context.Background()
	err := sender.SendPurchaseConfirmation(ctx, data)

	require.NoError(t, err)
}

func TestSMTPSender_SendPurchaseFailedNotification_NoFromEmail(t *testing.T) {
	sender := &SMTPSender{
		host:      "smtp.example.com",
		port:      587,
		fromEmail: "", // No from email - should skip
	}

	data := NotificationData{
		DashboardURL: "https://dashboard.example.com",
	}

	ctx := context.Background()
	err := sender.SendPurchaseFailedNotification(ctx, data)

	require.NoError(t, err)
}

// Test that SMTPSender implements SenderInterface
func TestSMTPSender_ImplementsInterface(t *testing.T) {
	var sender SenderInterface = &SMTPSender{}
	assert.NotNil(t, sender)
}

func TestNewSMTPSender_NoAuth(t *testing.T) {
	cfg := SMTPConfig{
		Host:      "localhost",
		Port:      25,
		FromEmail: "noreply@localhost",
		UseTLS:    false,
		// Username and Password not set - no auth
	}

	sender, err := NewSMTPSender(cfg)

	require.NoError(t, err)
	require.NotNil(t, sender)
	assert.Empty(t, sender.username)
	assert.Empty(t, sender.password)
}
