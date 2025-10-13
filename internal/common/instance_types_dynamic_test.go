package common

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestInstanceTypeCache_Get(t *testing.T) {
	ctx := context.Background()

	t.Run("Cache hit returns cached types", func(t *testing.T) {
		cache := &InstanceTypeCache{
			cache:  make(map[ServiceType][]string),
			expiry: make(map[ServiceType]time.Time),
			ttl:    1 * time.Hour,
		}

		expectedTypes := []string{"db.t3.small", "db.t3.medium"}
		cache.cache[ServiceRDS] = expectedTypes
		cache.expiry[ServiceRDS] = time.Now().Add(1 * time.Hour)

		mockClient := &MockPurchaseClient{}

		types, err := cache.Get(ctx, ServiceRDS, mockClient)
		assert.NoError(t, err)
		assert.Equal(t, expectedTypes, types)
		mockClient.AssertNotCalled(t, "GetValidInstanceTypes")
	})

	t.Run("Expired cache fetches new types", func(t *testing.T) {
		cache := &InstanceTypeCache{
			cache:  make(map[ServiceType][]string),
			expiry: make(map[ServiceType]time.Time),
			ttl:    1 * time.Hour,
		}

		// Set expired cache
		cache.cache[ServiceRDS] = []string{"old.type"}
		cache.expiry[ServiceRDS] = time.Now().Add(-1 * time.Hour)

		mockClient := &MockPurchaseClient{}
		newTypes := []string{"db.t3.small", "db.t3.medium"}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return(newTypes, nil)

		types, err := cache.Get(ctx, ServiceRDS, mockClient)
		assert.NoError(t, err)
		assert.Equal(t, newTypes, types)
		mockClient.AssertExpectations(t)
	})

	t.Run("Fetch failure returns static fallback", func(t *testing.T) {
		cache := &InstanceTypeCache{
			cache:  make(map[ServiceType][]string),
			expiry: make(map[ServiceType]time.Time),
			ttl:    1 * time.Hour,
		}

		mockClient := &MockPurchaseClient{}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string(nil), errors.New("API error"))

		types, err := cache.Get(ctx, ServiceRDS, mockClient)
		assert.NoError(t, err)
		assert.NotEmpty(t, types) // Should return static types
		mockClient.AssertExpectations(t)
	})
}

func TestInstanceTypeCache_ClearCache(t *testing.T) {
	cache := &InstanceTypeCache{
		cache:  make(map[ServiceType][]string),
		expiry: make(map[ServiceType]time.Time),
		ttl:    1 * time.Hour,
	}

	cache.cache[ServiceRDS] = []string{"db.t3.small"}
	cache.expiry[ServiceRDS] = time.Now().Add(1 * time.Hour)

	cache.ClearCache()

	assert.Empty(t, cache.cache)
	assert.Empty(t, cache.expiry)
}

func TestGetStaticInstanceTypes(t *testing.T) {
	tests := []struct {
		name          string
		service       ServiceType
		expectEmpty   bool
		checkContains string
	}{
		{
			name:          "RDS service",
			service:       ServiceRDS,
			expectEmpty:   false,
			checkContains: "db.t3.small",
		},
		{
			name:          "ElastiCache service",
			service:       ServiceElastiCache,
			expectEmpty:   false,
			checkContains: "cache.r5.large",
		},
		{
			name:        "Unknown service",
			service:     "UnknownService",
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			types := GetStaticInstanceTypes(tt.service)
			if tt.expectEmpty {
				assert.Empty(t, types)
			} else {
				assert.NotEmpty(t, types)
				if tt.checkContains != "" {
					assert.Contains(t, types, tt.checkContains)
				}
			}
		})
	}
}

func TestValidateInstanceTypesStatic(t *testing.T) {
	tests := []struct {
		name          string
		instanceTypes []string
		service       ServiceType
		expectError   bool
	}{
		{
			name:          "Empty list is valid",
			instanceTypes: []string{},
			service:       ServiceRDS,
			expectError:   false,
		},
		{
			name:          "All valid instance types",
			instanceTypes: []string{"db.t3.small", "db.t3.medium"},
			service:       ServiceRDS,
			expectError:   false,
		},
		{
			name:          "Mixed case valid types",
			instanceTypes: []string{"DB.T3.SMALL", "db.t3.medium"},
			service:       ServiceRDS,
			expectError:   false,
		},
		{
			name:          "Invalid instance types",
			instanceTypes: []string{"db.invalid.type"},
			service:       ServiceRDS,
			expectError:   true,
		},
		{
			name:          "Mixed valid and invalid",
			instanceTypes: []string{"db.t3.small", "invalid.type"},
			service:       ServiceRDS,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInstanceTypesStatic(tt.instanceTypes, tt.service)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateInstanceTypesWithService(t *testing.T) {
	ctx := context.Background()

	t.Run("Empty list is valid", func(t *testing.T) {
		mockClient := &MockPurchaseClient{}

		err := ValidateInstanceTypesWithService(ctx, []string{}, ServiceRDS, mockClient)
		assert.NoError(t, err)
		mockClient.AssertNotCalled(t, "GetValidInstanceTypes")
	})

	t.Run("Valid instance types", func(t *testing.T) {
		globalInstanceTypeCache.ClearCache()
		mockClient := &MockPurchaseClient{}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string{"db.t3.small", "db.t3.medium"}, nil)

		err := ValidateInstanceTypesWithService(ctx, []string{"db.t3.small"}, ServiceRDS, mockClient)
		assert.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("Invalid instance types", func(t *testing.T) {
		globalInstanceTypeCache.ClearCache()
		mockClient := &MockPurchaseClient{}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string{"db.t3.small", "db.t3.medium"}, nil)

		err := ValidateInstanceTypesWithService(ctx, []string{"db.invalid.type"}, ServiceRDS, mockClient)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "db.invalid.type")
		mockClient.AssertExpectations(t)
	})

	t.Run("API error falls back to static validation", func(t *testing.T) {
		globalInstanceTypeCache.ClearCache()
		mockClient := &MockPurchaseClient{}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string(nil), errors.New("API error"))

		err := ValidateInstanceTypesWithService(ctx, []string{"db.t3.small"}, ServiceRDS, mockClient)
		assert.NoError(t, err)
		mockClient.AssertExpectations(t)
	})
}

func TestGetAllValidInstanceTypesForServices(t *testing.T) {
	ctx := context.Background()

	t.Run("Multiple services", func(t *testing.T) {
		clientFactory := func(service ServiceType) PurchaseClient {
			mockClient := &MockPurchaseClient{}
			if service == ServiceRDS {
				mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string{"db.t3.small"}, nil)
			} else if service == ServiceElastiCache {
				mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string{"cache.r5.large"}, nil)
			}
			return mockClient
		}

		result, err := GetAllValidInstanceTypesForServices(ctx, []ServiceType{ServiceRDS, ServiceElastiCache}, clientFactory)
		assert.NoError(t, err)
		assert.Contains(t, result, ServiceRDS)
		assert.Contains(t, result, ServiceElastiCache)
	})

	t.Run("Nil client uses static types", func(t *testing.T) {
		clientFactory := func(service ServiceType) PurchaseClient {
			return nil
		}

		result, err := GetAllValidInstanceTypesForServices(ctx, []ServiceType{ServiceRDS}, clientFactory)
		assert.NoError(t, err)
		assert.NotEmpty(t, result[ServiceRDS])
	})
}

func TestFilterValidInstanceTypes(t *testing.T) {
	ctx := context.Background()

	t.Run("Empty list returns empty", func(t *testing.T) {
		mockClient := &MockPurchaseClient{}

		filtered := FilterValidInstanceTypes(ctx, []string{}, ServiceRDS, mockClient)
		assert.Empty(t, filtered)
		mockClient.AssertNotCalled(t, "GetValidInstanceTypes")
	})

	t.Run("Filters invalid instance types", func(t *testing.T) {
		globalInstanceTypeCache.ClearCache()
		mockClient := &MockPurchaseClient{}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string{"db.t3.small", "db.t3.medium"}, nil)

		input := []string{"db.t3.small", "db.invalid.type", "db.t3.medium"}
		filtered := FilterValidInstanceTypes(ctx, input, ServiceRDS, mockClient)

		assert.Len(t, filtered, 2)
		assert.Contains(t, filtered, "db.t3.small")
		assert.Contains(t, filtered, "db.t3.medium")
		assert.NotContains(t, filtered, "db.invalid.type")
		mockClient.AssertExpectations(t)
	})

	t.Run("All valid types returned", func(t *testing.T) {
		globalInstanceTypeCache.ClearCache()
		mockClient := &MockPurchaseClient{}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string{"db.t3.small", "db.t3.medium"}, nil)

		input := []string{"db.t3.small", "db.t3.medium"}
		filtered := FilterValidInstanceTypes(ctx, input, ServiceRDS, mockClient)

		assert.Len(t, filtered, 2)
		mockClient.AssertExpectations(t)
	})

	t.Run("API error uses static validation", func(t *testing.T) {
		globalInstanceTypeCache.ClearCache()
		mockClient := &MockPurchaseClient{}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string(nil), errors.New("API error"))

		input := []string{"db.t3.small"}
		filtered := FilterValidInstanceTypes(ctx, input, ServiceRDS, mockClient)

		// Should still filter using static types
		assert.NotEmpty(t, filtered)
		mockClient.AssertExpectations(t)
	})
}

func TestMergeInstanceTypes(t *testing.T) {
	tests := []struct {
		name     string
		lists    [][]string
		expected []string
	}{
		{
			name:     "Single list",
			lists:    [][]string{{"db.t3.small", "db.t3.medium"}},
			expected: []string{"db.t3.medium", "db.t3.small"},
		},
		{
			name: "Multiple lists with duplicates",
			lists: [][]string{
				{"db.t3.small", "db.t3.medium"},
				{"db.t3.medium", "db.t3.large"},
			},
			expected: []string{"db.t3.large", "db.t3.medium", "db.t3.small"},
		},
		{
			name: "Case insensitive deduplication",
			lists: [][]string{
				{"db.t3.small", "DB.T3.SMALL"},
			},
			expected: []string{"db.t3.small"},
		},
		{
			name:     "Empty lists",
			lists:    [][]string{{}, {}},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeInstanceTypes(tt.lists...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetInstanceTypesByPrefix(t *testing.T) {
	ctx := context.Background()

	t.Run("API error returns static fallback", func(t *testing.T) {
		// Clear global cache to avoid interference
		globalInstanceTypeCache.ClearCache()

		mockClient := &MockPurchaseClient{}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string(nil), errors.New("API error"))

		// When API fails, it returns static types for the prefix
		matching, err := GetInstanceTypesByPrefix(ctx, "db.t3", ServiceRDS, mockClient)
		assert.NoError(t, err) // No error because it falls back to static types
		assert.NotEmpty(t, matching) // Should have static types matching db.t3
		mockClient.AssertExpectations(t)
	})
}

func TestGetCachedInstanceTypes(t *testing.T) {
	ctx := context.Background()

	// Test that global cache is used
	t.Run("Uses global cache", func(t *testing.T) {
		globalInstanceTypeCache.ClearCache()

		mockClient := &MockPurchaseClient{}
		mockClient.On("GetValidInstanceTypes", mock.Anything).Return([]string{"db.t3.small"}, nil).Once()

		// First call should hit API
		types1, err1 := GetCachedInstanceTypes(ctx, ServiceRDS, mockClient)
		assert.NoError(t, err1)
		assert.Equal(t, []string{"db.t3.small"}, types1)

		// Second call should use cache
		types2, err2 := GetCachedInstanceTypes(ctx, ServiceRDS, mockClient)
		assert.NoError(t, err2)
		assert.Equal(t, []string{"db.t3.small"}, types2)

		// Assert API was only called once
		mockClient.AssertExpectations(t)
	})
}
