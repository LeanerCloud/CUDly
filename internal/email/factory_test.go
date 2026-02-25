package email

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderTypeConstants(t *testing.T) {
	assert.Equal(t, ProviderType("aws"), ProviderAWS)
	assert.Equal(t, ProviderType("gcp"), ProviderGCP)
	assert.Equal(t, ProviderType("azure"), ProviderAzure)
}

func TestFactoryConfig(t *testing.T) {
	cfg := FactoryConfig{
		FromEmail:         "test@example.com",
		Provider:          ProviderAWS,
		TopicARN:          "arn:aws:sns:us-east-1:123456789012:topic",
		EmailAddress:      "admin@example.com",
		SendGridAPIKey:    "sg_api_key",
		AzureSMTPUsername: "azure_user",
		AzureSMTPPassword: "azure_pass",
	}

	assert.Equal(t, "test@example.com", cfg.FromEmail)
	assert.Equal(t, ProviderAWS, cfg.Provider)
	assert.Equal(t, "arn:aws:sns:us-east-1:123456789012:topic", cfg.TopicARN)
	assert.Equal(t, "admin@example.com", cfg.EmailAddress)
	assert.Equal(t, "sg_api_key", cfg.SendGridAPIKey)
	assert.Equal(t, "azure_user", cfg.AzureSMTPUsername)
	assert.Equal(t, "azure_pass", cfg.AzureSMTPPassword)
}

func TestNewSenderFromEnvironment_AWS_Default(t *testing.T) {
	// Clear any provider env vars
	os.Unsetenv("SECRET_PROVIDER")
	os.Unsetenv("CLOUD_PROVIDER")

	// Set AWS-specific env vars
	os.Setenv("SNS_TOPIC_ARN", "arn:aws:sns:us-east-1:123456789012:topic")
	os.Setenv("FROM_EMAIL", "noreply@example.com")
	os.Setenv("EMAIL_ADDRESS", "admin@example.com")
	defer func() {
		os.Unsetenv("SNS_TOPIC_ARN")
		os.Unsetenv("FROM_EMAIL")
		os.Unsetenv("EMAIL_ADDRESS")
	}()

	ctx := context.Background()
	sender, err := NewSenderFromEnvironment(ctx)

	require.NoError(t, err)
	require.NotNil(t, sender)

	// Should return an AWS Sender (not SMTP)
	_, ok := sender.(*Sender)
	assert.True(t, ok, "Expected AWS Sender type")
}

func TestNewSenderFromEnvironment_AWS_Explicit(t *testing.T) {
	os.Setenv("SECRET_PROVIDER", "aws")
	os.Setenv("SNS_TOPIC_ARN", "arn:aws:sns:us-east-1:123456789012:topic")
	os.Setenv("FROM_EMAIL", "noreply@example.com")
	defer func() {
		os.Unsetenv("SECRET_PROVIDER")
		os.Unsetenv("SNS_TOPIC_ARN")
		os.Unsetenv("FROM_EMAIL")
	}()

	ctx := context.Background()
	sender, err := NewSenderFromEnvironment(ctx)

	require.NoError(t, err)
	require.NotNil(t, sender)

	_, ok := sender.(*Sender)
	assert.True(t, ok, "Expected AWS Sender type")
}

func TestNewSenderFromEnvironment_AWS_CloudProvider(t *testing.T) {
	os.Unsetenv("SECRET_PROVIDER")
	os.Setenv("CLOUD_PROVIDER", "aws")
	os.Setenv("SNS_TOPIC_ARN", "arn:aws:sns:us-east-1:123456789012:topic")
	os.Setenv("FROM_EMAIL", "noreply@example.com")
	defer func() {
		os.Unsetenv("CLOUD_PROVIDER")
		os.Unsetenv("SNS_TOPIC_ARN")
		os.Unsetenv("FROM_EMAIL")
	}()

	ctx := context.Background()
	sender, err := NewSenderFromEnvironment(ctx)

	require.NoError(t, err)
	require.NotNil(t, sender)
}

func TestNewSenderFromEnvironment_GCP_MissingAPIKey(t *testing.T) {
	os.Setenv("SECRET_PROVIDER", "gcp")
	os.Unsetenv("SENDGRID_API_KEY")
	os.Unsetenv("SENDGRID_API_KEY_SECRET")
	defer func() {
		os.Unsetenv("SECRET_PROVIDER")
	}()

	ctx := context.Background()
	_, err := NewSenderFromEnvironment(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SENDGRID_API_KEY")
}

func TestNewSenderFromEnvironment_GCP_WithAPIKey(t *testing.T) {
	os.Setenv("SECRET_PROVIDER", "gcp")
	os.Setenv("SENDGRID_API_KEY", "test_api_key")
	os.Setenv("FROM_EMAIL", "noreply@example.com")
	defer func() {
		os.Unsetenv("SECRET_PROVIDER")
		os.Unsetenv("SENDGRID_API_KEY")
		os.Unsetenv("FROM_EMAIL")
	}()

	ctx := context.Background()
	sender, err := NewSenderFromEnvironment(ctx)

	require.NoError(t, err)
	require.NotNil(t, sender)

	// Should return SMTP sender for GCP
	_, ok := sender.(*SMTPSender)
	assert.True(t, ok, "Expected SMTPSender type for GCP")
}

func TestNewSenderFromEnvironment_Azure_MissingCredentials(t *testing.T) {
	os.Setenv("SECRET_PROVIDER", "azure")
	os.Unsetenv("AZURE_SMTP_USERNAME")
	os.Unsetenv("AZURE_SMTP_PASSWORD")
	os.Unsetenv("AZURE_SMTP_USERNAME_SECRET")
	os.Unsetenv("AZURE_SMTP_PASSWORD_SECRET")
	defer func() {
		os.Unsetenv("SECRET_PROVIDER")
	}()

	ctx := context.Background()
	_, err := NewSenderFromEnvironment(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure SMTP credentials required")
}

func TestNewSenderFromEnvironment_Azure_WithCredentials(t *testing.T) {
	os.Setenv("SECRET_PROVIDER", "azure")
	os.Setenv("AZURE_SMTP_USERNAME", "azure_user")
	os.Setenv("AZURE_SMTP_PASSWORD", "azure_pass")
	os.Setenv("FROM_EMAIL", "noreply@example.com")
	defer func() {
		os.Unsetenv("SECRET_PROVIDER")
		os.Unsetenv("AZURE_SMTP_USERNAME")
		os.Unsetenv("AZURE_SMTP_PASSWORD")
		os.Unsetenv("FROM_EMAIL")
	}()

	ctx := context.Background()
	sender, err := NewSenderFromEnvironment(ctx)

	require.NoError(t, err)
	require.NotNil(t, sender)

	// Should return SMTP sender for Azure
	_, ok := sender.(*SMTPSender)
	assert.True(t, ok, "Expected SMTPSender type for Azure")
}

func TestNewSenderFromEnvironment_Azure_CustomHost(t *testing.T) {
	os.Setenv("SECRET_PROVIDER", "azure")
	os.Setenv("AZURE_SMTP_USERNAME", "azure_user")
	os.Setenv("AZURE_SMTP_PASSWORD", "azure_pass")
	os.Setenv("AZURE_SMTP_HOST", "custom.smtp.host.com")
	os.Setenv("FROM_EMAIL", "noreply@example.com")
	defer func() {
		os.Unsetenv("SECRET_PROVIDER")
		os.Unsetenv("AZURE_SMTP_USERNAME")
		os.Unsetenv("AZURE_SMTP_PASSWORD")
		os.Unsetenv("AZURE_SMTP_HOST")
		os.Unsetenv("FROM_EMAIL")
	}()

	ctx := context.Background()
	sender, err := NewSenderFromEnvironment(ctx)

	require.NoError(t, err)
	require.NotNil(t, sender)
}

func TestNewSenderFromEnvironment_UnsupportedProvider(t *testing.T) {
	os.Setenv("SECRET_PROVIDER", "unsupported")
	defer func() {
		os.Unsetenv("SECRET_PROVIDER")
	}()

	ctx := context.Background()
	_, err := NewSenderFromEnvironment(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported email provider")
}

// Test NewSenderWithConfig
func TestNewSenderWithConfig_AWS(t *testing.T) {
	cfg := FactoryConfig{
		Provider:     ProviderAWS,
		TopicARN:     "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail:    "noreply@example.com",
		EmailAddress: "admin@example.com",
	}

	ctx := context.Background()
	sender, err := NewSenderWithConfig(ctx, cfg)

	require.NoError(t, err)
	require.NotNil(t, sender)

	_, ok := sender.(*Sender)
	assert.True(t, ok, "Expected AWS Sender type")
}

func TestNewSenderWithConfig_GCP_MissingAPIKey(t *testing.T) {
	cfg := FactoryConfig{
		Provider:  ProviderGCP,
		FromEmail: "noreply@example.com",
		// SendGridAPIKey intentionally not set
	}

	ctx := context.Background()
	_, err := NewSenderWithConfig(ctx, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SendGrid API key required")
}

func TestNewSenderWithConfig_GCP_WithAPIKey(t *testing.T) {
	cfg := FactoryConfig{
		Provider:       ProviderGCP,
		FromEmail:      "noreply@example.com",
		SendGridAPIKey: "test_api_key",
	}

	ctx := context.Background()
	sender, err := NewSenderWithConfig(ctx, cfg)

	require.NoError(t, err)
	require.NotNil(t, sender)

	_, ok := sender.(*SMTPSender)
	assert.True(t, ok, "Expected SMTPSender type for GCP")
}

func TestNewSenderWithConfig_Azure_MissingCredentials(t *testing.T) {
	cfg := FactoryConfig{
		Provider:  ProviderAzure,
		FromEmail: "noreply@example.com",
		// AzureSMTPUsername/Password intentionally not set
	}

	ctx := context.Background()
	_, err := NewSenderWithConfig(ctx, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "AzureSMTPUsername and AzureSMTPPassword required")
}

func TestNewSenderWithConfig_Azure_WithCredentials(t *testing.T) {
	cfg := FactoryConfig{
		Provider:          ProviderAzure,
		FromEmail:         "noreply@example.com",
		AzureSMTPUsername: "azure_username",
		AzureSMTPPassword: "azure_password",
	}

	ctx := context.Background()
	sender, err := NewSenderWithConfig(ctx, cfg)

	require.NoError(t, err)
	require.NotNil(t, sender)

	_, ok := sender.(*SMTPSender)
	assert.True(t, ok, "Expected SMTPSender type for Azure")
}

func TestNewSenderWithConfig_UnsupportedProvider(t *testing.T) {
	cfg := FactoryConfig{
		Provider:  ProviderType("unknown"),
		FromEmail: "noreply@example.com",
	}

	ctx := context.Background()
	_, err := NewSenderWithConfig(ctx, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported email provider")
}
