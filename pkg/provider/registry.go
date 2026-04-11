// Package provider provides a registry for cloud providers
package provider

import (
	"fmt"
	"log"
	"sync"
)

var (
	globalRegistry     *Registry
	globalRegistryOnce sync.Once
)

// Registry manages registered cloud providers
type Registry struct {
	providers map[string]ProviderFactory
	mu        sync.RWMutex
}

// ProviderFactory is a function that creates a new provider instance
type ProviderFactory func(config *ProviderConfig) (Provider, error)

// NewRegistry creates a new provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]ProviderFactory),
	}
}

// GetRegistry returns the global provider registry
func GetRegistry() *Registry {
	globalRegistryOnce.Do(func() {
		globalRegistry = NewRegistry()
	})
	return globalRegistry
}

// Register registers a provider factory with the registry
func (r *Registry) Register(name string, factory ProviderFactory) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("provider %s already registered", name)
	}

	r.providers[name] = factory
	return nil
}

// GetProvider creates a provider instance by name with default config
func (r *Registry) GetProvider(name string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	factory, exists := r.providers[name]
	if !exists {
		return nil
	}

	// Create provider with default config
	provider, err := factory(&ProviderConfig{Name: name})
	if err != nil {
		log.Printf("provider %q factory error: %v", name, err)
		return nil
	}

	return provider
}

// GetProviderWithConfig creates a provider instance with custom config
func (r *Registry) GetProviderWithConfig(name string, config *ProviderConfig) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	factory, exists := r.providers[name]
	if !exists {
		return nil, fmt.Errorf("provider %s not registered", name)
	}

	return factory(config)
}

// GetAllProviders returns instances of all registered providers
func (r *Registry) GetAllProviders() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make([]Provider, 0, len(r.providers))
	for name, factory := range r.providers {
		provider, err := factory(&ProviderConfig{Name: name})
		if err != nil {
			log.Printf("provider %q factory error: %v", name, err)
			continue
		}
		providers = append(providers, provider)
	}

	return providers
}

// GetProviderNames returns the names of all registered providers
func (r *Registry) GetProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}

	return names
}

// IsRegistered checks if a provider is registered
func (r *Registry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.providers[name]
	return exists
}

// Unregister removes a provider from the registry
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.providers, name)
}

// RegisterProvider is a convenience function to register with the global registry
func RegisterProvider(name string, factory ProviderFactory) error {
	return GetRegistry().Register(name, factory)
}
