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
	assert.Contains(t, err.Error(), "azure SMTP credentials required")
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

// TestNewSenderFromEnvironment_EmailEnabled covers the EMAIL_ENABLED
// short-circuit added by issue #332. When EMAIL_ENABLED parses as
// false the factory returns a NopSender and skips the provider
// dispatch entirely; when it parses as true / is unset / is unparseable
// the factory falls through to the normal SECRET_PROVIDER-based path.
func TestNewSenderFromEnvironment_EmailEnabled(t *testing.T) {
	// Provider env vars used by the fall-through path (SECRET_PROVIDER=aws).
	// Kept stable across sub-tests; t.Setenv resets after each.
	aws := func(t *testing.T) {
		t.Setenv("SECRET_PROVIDER", "aws")
		t.Setenv("SNS_TOPIC_ARN", "arn:aws:sns:us-east-1:123456789012:topic")
		t.Setenv("FROM_EMAIL", "noreply@example.com")
	}

	// Cases where EMAIL_ENABLED parses as false → NopSender, regardless of
	// what SECRET_PROVIDER says. We deliberately set SECRET_PROVIDER=env
	// (an unsupported email backend) to prove the short-circuit fires
	// BEFORE the dispatch — the test would error out otherwise.
	for _, val := range []string{"false", "False", "FALSE", "0", "f", "F"} {
		t.Run("disabled_"+val, func(t *testing.T) {
			t.Setenv("EMAIL_ENABLED", val)
			t.Setenv("SECRET_PROVIDER", "env")
			ctx := context.Background()
			sender, err := NewSenderFromEnvironment(ctx)
			require.NoError(t, err)
			require.NotNil(t, sender)
			_, ok := sender.(*NopSender)
			assert.True(t, ok, "Expected NopSender for EMAIL_ENABLED=%q, got %T", val, sender)
		})
	}

	// Cases where EMAIL_ENABLED parses as true → fall through to AWS sender.
	for _, val := range []string{"true", "True", "TRUE", "1", "t", "T"} {
		t.Run("enabled_"+val, func(t *testing.T) {
			t.Setenv("EMAIL_ENABLED", val)
			aws(t)
			ctx := context.Background()
			sender, err := NewSenderFromEnvironment(ctx)
			require.NoError(t, err)
			require.NotNil(t, sender)
			_, ok := sender.(*Sender)
			assert.True(t, ok, "Expected AWS Sender for EMAIL_ENABLED=%q, got %T", val, sender)
		})
	}

	// Unset / empty → factory takes the default-enabled path.
	t.Run("unset_falls_through", func(t *testing.T) {
		prev, hadPrev := os.LookupEnv("EMAIL_ENABLED")
		os.Unsetenv("EMAIL_ENABLED")
		t.Cleanup(func() {
			if hadPrev {
				_ = os.Setenv("EMAIL_ENABLED", prev)
				return
			}
			os.Unsetenv("EMAIL_ENABLED")
		})
		aws(t)
		ctx := context.Background()
		sender, err := NewSenderFromEnvironment(ctx)
		require.NoError(t, err)
		_, ok := sender.(*Sender)
		assert.True(t, ok, "Expected AWS Sender when EMAIL_ENABLED is unset")
	})

	t.Run("empty_falls_through", func(t *testing.T) {
		t.Setenv("EMAIL_ENABLED", "")
		aws(t)
		ctx := context.Background()
		sender, err := NewSenderFromEnvironment(ctx)
		require.NoError(t, err)
		_, ok := sender.(*Sender)
		assert.True(t, ok, "Expected AWS Sender when EMAIL_ENABLED is empty")
	})

	// Unparseable value: factory logs a warning and falls through to the
	// default-enabled path rather than crashing. This protects an existing
	// deployment that accidentally sets EMAIL_ENABLED=maybe — the misconfig
	// is visible in logs but doesn't take the app down.
	t.Run("unparseable_warns_and_falls_through", func(t *testing.T) {
		t.Setenv("EMAIL_ENABLED", "maybe")
		aws(t)
		ctx := context.Background()
		sender, err := NewSenderFromEnvironment(ctx)
		require.NoError(t, err)
		_, ok := sender.(*Sender)
		assert.True(t, ok, "Expected AWS Sender for unparseable EMAIL_ENABLED")
	})
}

// Test NewSenderWithConfig.
func TestNewSenderWithConfig_AWS(t *testing.T) {
	cfg := FactoryConfig{
		Provider:     ProviderAWS,
		TopicARN:     "arn:aws:sns:us-east-1:123456789012:topic",
		FromEmail:    "noreply@example.com",
		EmailAddress: "admin@example.com",
	}

	ctx := context.Background()
	sender, err := NewSenderWithConfig(ctx, &cfg)

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
	_, err := NewSenderWithConfig(ctx, &cfg)

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
	sender, err := NewSenderWithConfig(ctx, &cfg)

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
	_, err := NewSenderWithConfig(ctx, &cfg)

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
	sender, err := NewSenderWithConfig(ctx, &cfg)

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
	_, err := NewSenderWithConfig(ctx, &cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported email provider")
}
