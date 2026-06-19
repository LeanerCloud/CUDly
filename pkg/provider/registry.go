// Package provider provides a registry for cloud providers.
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

// Registry manages registered cloud providers.
type Registry struct {
	providers map[string]ProviderFactory
	mu        sync.RWMutex
}

// ProviderFactory is a function that creates a new provider instance.
// revive/stutter: ProviderFactory is intentional; renaming to Factory would conflict with
// DefaultFactory and FactoryInterface in this same package, and break 4+ external callers.
//
//nolint:revive
type ProviderFactory func(config *ProviderConfig) (Provider, error)

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]ProviderFactory),
	}
}

// GetRegistry returns the global provider registry.
func GetRegistry() *Registry {
	globalRegistryOnce.Do(func() {
		globalRegistry = NewRegistry()
	})
	return globalRegistry
}

// Register registers a provider factory with the registry.
func (r *Registry) Register(name string, factory ProviderFactory) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("provider %s already registered", name)
	}

	r.providers[name] = factory
	return nil
}

// GetProvider creates a provider instance by name with default config.
//
// Returns:
//   - "provider %s not registered" when no factory has been registered for that name
//   - "provider %s factory failed: %w" when the factory itself returned an error
//
// Callers can distinguish the two cases via the returned error message; previously
// both cases returned nil and callers had no way to surface the factory failure.
func (r *Registry) GetProvider(name string) (Provider, error) {
	// Look up the factory under the lock, then release it before invoking the
	// factory. Factories may perform arbitrary work, including network I/O (the
	// GCP factory walks Projects.List() when no project ID is configured); calling
	// them while holding r.mu would block every other registry reader and any
	// writer (e.g. Unregister) for the duration of that I/O.
	r.mu.RLock()
	factory, exists := r.providers[name]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provider %s not registered", name)
	}

	provider, err := factory(&ProviderConfig{Name: name})
	if err != nil {
		return nil, fmt.Errorf("provider %s factory failed: %w", name, err)
	}
	return provider, nil
}

// GetProviderWithConfig creates a provider instance with custom config.
func (r *Registry) GetProviderWithConfig(name string, config *ProviderConfig) (Provider, error) {
	// Snapshot the factory under the lock, call it lock-free (see GetProvider).
	r.mu.RLock()
	factory, exists := r.providers[name]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provider %s not registered", name)
	}

	return factory(config)
}

// GetAllProviders returns instances of all registered providers.
func (r *Registry) GetAllProviders() []Provider {
	// Copy the name->factory map under the lock, then release it and construct
	// the providers lock-free. Factories may do network I/O (see GetProvider);
	// running them under r.mu would serialize every provider's network init and
	// block other registry users for the whole fan-out.
	r.mu.RLock()
	factories := make(map[string]ProviderFactory, len(r.providers))
	for name, factory := range r.providers {
		factories[name] = factory
	}
	r.mu.RUnlock()

	providers := make([]Provider, 0, len(factories))
	for name, factory := range factories {
		provider, err := factory(&ProviderConfig{Name: name})
		if err != nil {
			log.Printf("provider %q factory error: %v", name, err)
			continue
		}
		providers = append(providers, provider)
	}

	return providers
}

// GetProviderNames returns the names of all registered providers.
func (r *Registry) GetProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}

	return names
}

// IsRegistered checks if a provider is registered.
func (r *Registry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.providers[name]
	return exists
}

// Unregister removes a provider from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.providers, name)
}

// RegisterProvider is a convenience function to register with the global registry.
func RegisterProvider(name string, factory ProviderFactory) error {
	return GetRegistry().Register(name, factory)
}
