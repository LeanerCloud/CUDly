// Package email provides email notification functionality across multiple cloud providers.
package email

import (
	"context"
	"fmt"
	"os"

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
	AzureConnectionString string
	AzureSenderAddress    string
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
		// GCP uses SendGrid via SMTP
		apiKey := os.Getenv("SENDGRID_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("SENDGRID_API_KEY environment variable required for GCP email")
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

	case ProviderAzure:
		// Azure uses Azure Communication Services via SMTP
		username := os.Getenv("AZURE_SMTP_USERNAME")
		password := os.Getenv("AZURE_SMTP_PASSWORD")
		if username == "" || password == "" {
			return nil, fmt.Errorf("AZURE_SMTP_USERNAME and AZURE_SMTP_PASSWORD environment variables required for Azure email")
		}
		host := os.Getenv("AZURE_SMTP_HOST")
		if host == "" {
			host = "smtp.azurecomm.net" // Default Azure Communication Services SMTP host
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

	default:
		return nil, fmt.Errorf("unsupported email provider: %s", provider)
	}
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
		// For Azure, we expect SMTP credentials in the connection string format
		// Connection string should contain username and password
		if cfg.AzureConnectionString == "" {
			return nil, fmt.Errorf("Azure SMTP credentials required for Azure email")
		}
		// Parse connection string to extract username/password
		// Format: "username=xxx;password=yyy" or just use separate fields
		return NewSMTPSender(SMTPConfig{
			Host:      "smtp.azurecomm.net",
			Port:      587,
			Username:  cfg.AzureConnectionString, // Simplified - in production, parse this
			Password:  cfg.AzureSenderAddress,    // Simplified - in production, parse this
			FromEmail: cfg.FromEmail,
			FromName:  "CUDly",
			UseTLS:    true,
		})

	default:
		return nil, fmt.Errorf("unsupported email provider: %s", cfg.Provider)
	}
}
