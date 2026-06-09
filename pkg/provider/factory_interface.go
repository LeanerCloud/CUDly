// Package provider provides cloud provider abstractions and factory functions.
package provider

import (
	"context"
)

// FactoryInterface allows creating cloud providers (enables testing)
type FactoryInterface interface {
	CreateAndValidateProvider(ctx context.Context, name string, cfg *ProviderConfig) (Provider, error)
}

// DefaultFactory uses the real provider factory
type DefaultFactory struct{}

// CreateAndValidateProvider creates and validates a provider using the real factory
func (f *DefaultFactory) CreateAndValidateProvider(ctx context.Context, name string, cfg *ProviderConfig) (Provider, error) {
	return CreateAndValidateProvider(ctx, name, cfg)
}
