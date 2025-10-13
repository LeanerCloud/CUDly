package common

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// InstanceTypeCache caches instance types to avoid repeated API calls
type InstanceTypeCache struct {
	mu     sync.RWMutex
	cache  map[ServiceType][]string
	expiry map[ServiceType]time.Time
	ttl    time.Duration
}

var (
	globalInstanceTypeCache = &InstanceTypeCache{
		cache:  make(map[ServiceType][]string),
		expiry: make(map[ServiceType]time.Time),
		ttl:    24 * time.Hour, // Cache for 24 hours
	}
)

// GetCachedInstanceTypes returns cached instance types or fetches them
func GetCachedInstanceTypes(ctx context.Context, service ServiceType, client PurchaseClient) ([]string, error) {
	return globalInstanceTypeCache.Get(ctx, service, client)
}

// Get returns cached instance types or fetches them using the client
func (c *InstanceTypeCache) Get(ctx context.Context, service ServiceType, client PurchaseClient) ([]string, error) {
	c.mu.RLock()
	if types, ok := c.cache[service]; ok {
		if time.Now().Before(c.expiry[service]) {
			c.mu.RUnlock()
			return types, nil
		}
	}
	c.mu.RUnlock()

	// Cache miss or expired, fetch from AWS
	types, err := client.GetValidInstanceTypes(ctx)
	if err != nil {
		// If fetch fails, return static fallback
		return GetStaticInstanceTypes(service), nil
	}

	// Update cache
	c.mu.Lock()
	c.cache[service] = types
	c.expiry[service] = time.Now().Add(c.ttl)
	c.mu.Unlock()

	return types, nil
}

// ClearCache clears the instance type cache
func (c *InstanceTypeCache) ClearCache() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[ServiceType][]string)
	c.expiry = make(map[ServiceType]time.Time)
}

// ValidateInstanceTypesWithService validates instance types against a specific service
func ValidateInstanceTypesWithService(ctx context.Context, instanceTypes []string, service ServiceType, client PurchaseClient) error {
	if len(instanceTypes) == 0 {
		return nil
	}

	validTypes, err := GetCachedInstanceTypes(ctx, service, client)
	if err != nil {
		// Fall back to static validation
		return ValidateInstanceTypesStatic(instanceTypes, service)
	}

	validTypeMap := make(map[string]bool)
	for _, t := range validTypes {
		validTypeMap[strings.ToLower(t)] = true
	}

	invalidTypes := make([]string, 0)
	for _, instanceType := range instanceTypes {
		instanceType = strings.TrimSpace(strings.ToLower(instanceType))
		if !validTypeMap[instanceType] {
			invalidTypes = append(invalidTypes, instanceType)
		}
	}

	if len(invalidTypes) > 0 {
		return fmt.Errorf("invalid instance type(s) for %s: %s", service, strings.Join(invalidTypes, ", "))
	}

	return nil
}

// ValidateInstanceTypesStatic validates against static list (fallback)
func ValidateInstanceTypesStatic(instanceTypes []string, service ServiceType) error {
	if len(instanceTypes) == 0 {
		return nil
	}

	staticTypes := GetStaticInstanceTypes(service)
	validTypeMap := make(map[string]bool)
	for _, t := range staticTypes {
		validTypeMap[strings.ToLower(t)] = true
	}

	invalidTypes := make([]string, 0)
	for _, instanceType := range instanceTypes {
		instanceType = strings.TrimSpace(strings.ToLower(instanceType))
		if !validTypeMap[instanceType] {
			invalidTypes = append(invalidTypes, instanceType)
		}
	}

	if len(invalidTypes) > 0 {
		return fmt.Errorf("invalid instance type(s): %s", strings.Join(invalidTypes, ", "))
	}

	return nil
}

// GetStaticInstanceTypes returns the static fallback list
func GetStaticInstanceTypes(service ServiceType) []string {
	if types, ok := ValidInstanceTypes[service]; ok {
		return types
	}
	return []string{}
}

// GetAllValidInstanceTypesForServices fetches valid instance types for multiple services
func GetAllValidInstanceTypesForServices(ctx context.Context, services []ServiceType, clientFactory func(ServiceType) PurchaseClient) (map[ServiceType][]string, error) {
	result := make(map[ServiceType][]string)

	for _, service := range services {
		client := clientFactory(service)
		if client == nil {
			// Use static types as fallback
			result[service] = GetStaticInstanceTypes(service)
			continue
		}

		types, err := GetCachedInstanceTypes(ctx, service, client)
		if err != nil {
			// Use static types as fallback
			result[service] = GetStaticInstanceTypes(service)
		} else {
			result[service] = types
		}
	}

	return result, nil
}

// FilterValidInstanceTypes filters a list to only include valid instance types
func FilterValidInstanceTypes(ctx context.Context, instanceTypes []string, service ServiceType, client PurchaseClient) []string {
	if len(instanceTypes) == 0 {
		return instanceTypes
	}

	validTypes, err := GetCachedInstanceTypes(ctx, service, client)
	if err != nil {
		// If can't fetch, don't filter
		return instanceTypes
	}

	validTypeMap := make(map[string]bool)
	for _, t := range validTypes {
		validTypeMap[strings.ToLower(t)] = true
	}

	filtered := make([]string, 0)
	for _, instanceType := range instanceTypes {
		if validTypeMap[strings.ToLower(strings.TrimSpace(instanceType))] {
			filtered = append(filtered, instanceType)
		}
	}

	return filtered
}

// MergeInstanceTypes merges instance types from multiple sources, removing duplicates
func MergeInstanceTypes(lists ...[]string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)

	for _, list := range lists {
		for _, instanceType := range list {
			key := strings.ToLower(strings.TrimSpace(instanceType))
			if !seen[key] {
				seen[key] = true
				result = append(result, instanceType)
			}
		}
	}

	sort.Strings(result)
	return result
}

// GetInstanceTypesByPrefix returns instance types matching a prefix
func GetInstanceTypesByPrefix(ctx context.Context, prefix string, service ServiceType, client PurchaseClient) ([]string, error) {
	allTypes, err := GetCachedInstanceTypes(ctx, service, client)
	if err != nil {
		return nil, err
	}

	prefix = strings.ToLower(strings.TrimSpace(prefix))
	matching := make([]string, 0)

	for _, instanceType := range allTypes {
		if strings.HasPrefix(strings.ToLower(instanceType), prefix) {
			matching = append(matching, instanceType)
		}
	}

	return matching, nil
}
