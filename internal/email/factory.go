// Package email provides email notification functionality across multiple cloud providers.
package email

import (
	"context"
	"fmt"
	"os"

	"github.com/LeanerCloud/CUDly/internal/secrets"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// ProviderType represents the cloud provider for email services
type ProviderType string

const (
	ProviderAWS   ProviderType = "aws"
	ProviderGCP   ProviderType = "gcp"
	ProviderAzure ProviderType = "azure"
)

// FactoryConfig holds configuration for creating email senders
type FactoryConfig struct {
	// Common configuration
	FromEmail string
	Provider  ProviderType

	// AWS-specific
	TopicARN     string
	EmailAddress string // Legacy: for SNS notifications

	// GCP-specific (SendGrid)
	SendGridAPIKey string

	// Azure-specific
	AzureSMTPUsername string
	AzureSMTPPassword string
	AzureSMTPHost     string // Defaults to "smtp.azurecomm.net" if empty
}

// NewSenderFromEnvironment creates an email sender based on environment variables
// It auto-detects the cloud provider from SECRET_PROVIDER or CLOUD_PROVIDER env vars
func NewSenderFromEnvironment(ctx context.Context) (SenderInterface, error) {
	// Detect provider from environment
	provider := os.Getenv("SECRET_PROVIDER")
	if provider == "" {
		provider = os.Getenv("CLOUD_PROVIDER")
	}
	if provider == "" {
		provider = "aws" // Default to AWS for backward compatibility
	}

	logging.Infof("Creating email sender for provider: %s", provider)

	switch ProviderType(provider) {
	case ProviderAWS:
		return NewSender(SenderConfig{
			TopicARN:     os.Getenv("SNS_TOPIC_ARN"),
			FromEmail:    os.Getenv("FROM_EMAIL"),
			EmailAddress: os.Getenv("EMAIL_ADDRESS"),
		})
	case ProviderGCP:
		return newGCPSenderFromEnv(ctx)
	case ProviderAzure:
		return newAzureSenderFromEnv(ctx)
	default:
		return nil, fmt.Errorf("unsupported email provider: %s", provider)
	}
}

// warnIfPlaintext logs a warning when a credential value is set as a plaintext
// environment variable rather than a secret manager reference (ARN/resource name).
// TODO: migrate all email credentials to AWS Secrets Manager / GCP Secret Manager /
// Azure Key Vault and remove direct env-var credential support.
func warnIfPlaintext(envVar, value string) {
	if value == "" {
		return
	}
	// Heuristic: secret manager references contain ":" or "/" (ARN, resource path, secret name)
	if len(value) < 20 || (value[0] != '/' && !containsColon(value)) {
		logging.Warnf("security: %s is set as a plaintext env var; consider using a Secrets Manager reference instead", envVar)
	}
}

// containsColon returns true if s contains a colon character.
func containsColon(s string) bool {
	for _, c := range s {
		if c == ':' {
			return true
		}
	}
	return false
}

// newGCPSenderFromEnv creates a SendGrid-based email sender from environment variables
func newGCPSenderFromEnv(ctx context.Context) (SenderInterface, error) {
	apiKey := os.Getenv("SENDGRID_API_KEY")
	warnIfPlaintext("SENDGRID_API_KEY", apiKey)
	if apiKey == "" {
		// Resolve from Secret Manager if secret name is provided
		if secretName := os.Getenv("SENDGRID_API_KEY_SECRET"); secretName != "" {
			resolver, err := secrets.NewResolver(ctx, secrets.LoadConfigFromEnv())
			if err != nil {
				return nil, fmt.Errorf("failed to create secret resolver for SendGrid API key: %w", err)
			}
			defer resolver.Close()
			apiKey, err = resolver.GetSecret(ctx, secretName)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve SendGrid API key from secret %q: %w", secretName, err)
			}
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("SENDGRID_API_KEY or SENDGRID_API_KEY_SECRET environment variable required for GCP email")
	}
	return NewSMTPSender(SMTPConfig{
		Host:      "smtp.sendgrid.net",
		Port:      587,
		Username:  "apikey", // SendGrid uses literal "apikey" as username
		Password:  apiKey,
		FromEmail: os.Getenv("FROM_EMAIL"),
		FromName:  "CUDly",
		UseTLS:    true,
	})
}

// resolveAzureSMTPCredentials returns Azure SMTP username and password from
// environment variables or secret manager.
func resolveAzureSMTPCredentials(ctx context.Context) (username, password string, err error) {
	username = os.Getenv("AZURE_SMTP_USERNAME")
	password = os.Getenv("AZURE_SMTP_PASSWORD")
	if username != "" && password != "" {
		warnIfPlaintext("AZURE_SMTP_USERNAME", username)
		warnIfPlaintext("AZURE_SMTP_PASSWORD", password)
		return username, password, nil
	}

	usernameSecret := os.Getenv("AZURE_SMTP_USERNAME_SECRET")
	passwordSecret := os.Getenv("AZURE_SMTP_PASSWORD_SECRET")
	if usernameSecret == "" || passwordSecret == "" {
		return "", "", fmt.Errorf("Azure SMTP credentials required: set AZURE_SMTP_USERNAME/AZURE_SMTP_PASSWORD or AZURE_SMTP_USERNAME_SECRET/AZURE_SMTP_PASSWORD_SECRET")
	}

	resolver, err := secrets.NewResolver(ctx, secrets.LoadConfigFromEnv())
	if err != nil {
		return "", "", fmt.Errorf("failed to create secret resolver for Azure SMTP credentials: %w", err)
	}
	defer resolver.Close()

	username, err = resolver.GetSecret(ctx, usernameSecret)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve Azure SMTP username from secret %q: %w", usernameSecret, err)
	}
	password, err = resolver.GetSecret(ctx, passwordSecret)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve Azure SMTP password from secret %q: %w", passwordSecret, err)
	}
	return username, password, nil
}

// newAzureSenderFromEnv creates an Azure Communication Services email sender from environment variables
func newAzureSenderFromEnv(ctx context.Context) (SenderInterface, error) {
	username, password, err := resolveAzureSMTPCredentials(ctx)
	if err != nil {
		return nil, err
	}

	host := os.Getenv("AZURE_SMTP_HOST")
	if host == "" {
		host = "smtp.azurecomm.net"
	}
	return NewSMTPSender(SMTPConfig{
		Host:      host,
		Port:      587,
		Username:  username,
		Password:  password,
		FromEmail: os.Getenv("FROM_EMAIL"),
		FromName:  "CUDly",
		UseTLS:    true,
	})
}

// NewSenderWithConfig creates an email sender with explicit configuration
func NewSenderWithConfig(ctx context.Context, cfg FactoryConfig) (SenderInterface, error) {
	switch cfg.Provider {
	case ProviderAWS:
		return NewSender(SenderConfig{
			TopicARN:     cfg.TopicARN,
			FromEmail:    cfg.FromEmail,
			EmailAddress: cfg.EmailAddress,
		})

	case ProviderGCP:
		if cfg.SendGridAPIKey == "" {
			return nil, fmt.Errorf("SendGrid API key required for GCP email")
		}
		return NewSMTPSender(SMTPConfig{
			Host:      "smtp.sendgrid.net",
			Port:      587,
			Username:  "apikey",
			Password:  cfg.SendGridAPIKey,
			FromEmail: cfg.FromEmail,
			FromName:  "CUDly",
			UseTLS:    true,
		})

	case ProviderAzure:
		if cfg.AzureSMTPUsername == "" || cfg.AzureSMTPPassword == "" {
			return nil, fmt.Errorf("AzureSMTPUsername and AzureSMTPPassword required for Azure email")
		}
		host := cfg.AzureSMTPHost
		if host == "" {
			host = "smtp.azurecomm.net"
		}
		return NewSMTPSender(SMTPConfig{
			Host:      host,
			Port:      587,
			Username:  cfg.AzureSMTPUsername,
			Password:  cfg.AzureSMTPPassword,
			FromEmail: cfg.FromEmail,
			FromName:  "CUDly",
			UseTLS:    true,
		})

	default:
		return nil, fmt.Errorf("unsupported email provider: %s", cfg.Provider)
	}
}
