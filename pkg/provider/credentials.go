// Package provider provides credential detection and provider discovery
package provider

import (
	"context"
	"fmt"
	"sync"
)

// CredentialDetector detects available cloud credentials
type CredentialDetector struct {
	providers []Provider
	mu        sync.RWMutex
}

// NewCredentialDetector creates a new credential detector
func NewCredentialDetector() *CredentialDetector {
	return &CredentialDetector{
		providers: make([]Provider, 0),
	}
}

// DetectAvailableProviders scans for configured cloud credentials
// It checks all registered providers and returns those with valid credentials
func DetectAvailableProviders(ctx context.Context) ([]Provider, error) {
	// Get all registered providers from the registry
	allProviders := GetRegistry().GetAllProviders()

	var available []Provider
	var errors []error

	// Check each provider for valid credentials
	for _, provider := range allProviders {
		if provider.IsConfigured() {
			// Validate credentials work
			if err := provider.ValidateCredentials(ctx); err == nil {
				available = append(available, provider)
			} else {
				errors = append(errors, fmt.Errorf("%s: %w", provider.Name(), err))
			}
		}
	}

	// If no providers found, return error with details
	if len(available) == 0 {
		if len(errors) > 0 {
			return nil, fmt.Errorf("no valid cloud credentials found. Errors: %v", errors)
		}
		return nil, fmt.Errorf("no cloud credentials found. Please configure AWS, Azure, or GCP credentials")
	}

	return available, nil
}

// DetectProvider detects a specific provider by name
func DetectProvider(ctx context.Context, name string) (Provider, error) {
	provider, err := GetRegistry().GetProvider(name)
	if err != nil {
		return nil, err
	}

	if !provider.IsConfigured() {
		return nil, fmt.Errorf("provider %s is not configured", name)
	}

	if err := provider.ValidateCredentials(ctx); err != nil {
		return nil, fmt.Errorf("provider %s credentials are invalid: %w", name, err)
	}

	return provider, nil
}

// GetProvidersByNames gets providers by their names
func GetProvidersByNames(ctx context.Context, names []string) ([]Provider, error) {
	var providers []Provider
	var errors []error

	for _, name := range names {
		provider, err := DetectProvider(ctx, name)
		if err != nil {
			errors = append(errors, err)
			continue
		}
		providers = append(providers, provider)
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no valid providers found: %v", errors)
	}

	return providers, nil
}

// CredentialSource represents the source of credentials
type CredentialSource string

const (
	CredentialSourceEnvironment CredentialSource = "environment"
	CredentialSourceFile        CredentialSource = "file"
	CredentialSourceIAMRole     CredentialSource = "iam-role"
	CredentialSourceMSI         CredentialSource = "managed-identity"
	CredentialSourceADC         CredentialSource = "application-default"
	CredentialSourceCLI         CredentialSource = "cli"
)

// BaseCredentials provides a base implementation of Credentials interface
type BaseCredentials struct {
	Source CredentialSource
	Valid  bool
}

func (c BaseCredentials) IsValid() bool {
	return c.Valid
}

func (c BaseCredentials) GetType() string {
	return string(c.Source)
}
