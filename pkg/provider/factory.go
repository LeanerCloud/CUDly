// Package provider provides factory functions for creating providers
package provider

import (
	"context"
	"fmt"
)

// CreateProvider creates a provider instance by name.
// If config.ProviderOverride is set, it is returned directly without consulting
// the registry — this allows callers to inject a pre-built, pre-authenticated provider.
func CreateProvider(name string, config *ProviderConfig) (Provider, error) {
	if config != nil && config.ProviderOverride != nil {
		return config.ProviderOverride, nil
	}
	if config == nil {
		config = &ProviderConfig{Name: name}
	}

	return GetRegistry().GetProviderWithConfig(name, config)
}

// CreateProviders creates multiple provider instances
func CreateProviders(names []string) ([]Provider, error) {
	providers := make([]Provider, 0, len(names))

	for _, name := range names {
		provider, err := CreateProvider(name, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create provider %s: %w", name, err)
		}
		providers = append(providers, provider)
	}

	return providers, nil
}

// CreateAndValidateProvider creates and validates a provider
func CreateAndValidateProvider(ctx context.Context, name string, config *ProviderConfig) (Provider, error) {
	provider, err := CreateProvider(name, config)
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

// GetOrDetectProviders gets specified providers or auto-detects available ones
func GetOrDetectProviders(ctx context.Context, names []string) ([]Provider, error) {
	// If specific providers requested, use those
	if len(names) > 0 {
		return GetProvidersByNames(ctx, names)
	}

	// Otherwise, auto-detect available providers
	return DetectAvailableProviders(ctx)
}
