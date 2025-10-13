package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateInstanceType(t *testing.T) {
	tests := []struct {
		name         string
		instanceType string
		service      ServiceType
		expectError  bool
	}{
		{
			name:         "Valid RDS instance type",
			instanceType: "db.t3.small",
			service:      ServiceRDS,
			expectError:  false,
		},
		{
			name:         "Valid ElastiCache instance type",
			instanceType: "cache.r5.large",
			service:      ServiceElastiCache,
			expectError:  false,
		},
		{
			name:         "Valid EC2 instance type",
			instanceType: "m5.xlarge",
			service:      ServiceEC2,
			expectError:  false,
		},
		{
			name:         "Invalid RDS instance type",
			instanceType: "db.invalid.small",
			service:      ServiceRDS,
			expectError:  true,
		},
		{
			name:         "Invalid ElastiCache instance type",
			instanceType: "cache.invalid.large",
			service:      ServiceElastiCache,
			expectError:  true,
		},
		{
			name:         "Instance type with whitespace",
			instanceType: " db.t3.medium ",
			service:      ServiceRDS,
			expectError:  false,
		},
		{
			name:         "Unknown service allows any type",
			instanceType: "any.type",
			service:      "UnknownService",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInstanceType(tt.instanceType, tt.service)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateInstanceTypes(t *testing.T) {
	tests := []struct {
		name          string
		instanceTypes []string
		expectError   bool
	}{
		{
			name:          "Empty list is valid",
			instanceTypes: []string{},
			expectError:   false,
		},
		{
			name:          "All valid instance types",
			instanceTypes: []string{"db.t3.small", "cache.r5.large", "m5.xlarge"},
			expectError:   false,
		},
		{
			name:          "Some invalid instance types",
			instanceTypes: []string{"db.t3.small", "invalid.type", "m5.xlarge"},
			expectError:   true,
		},
		{
			name:          "All invalid instance types",
			instanceTypes: []string{"invalid.type1", "invalid.type2"},
			expectError:   true,
		},
		{
			name:          "Instance types with whitespace",
			instanceTypes: []string{" db.t3.small ", " cache.r5.large "},
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInstanceTypes(tt.instanceTypes)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetInstanceTypesByService(t *testing.T) {
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
			name:          "EC2 service",
			service:       ServiceEC2,
			expectEmpty:   false,
			checkContains: "m5.xlarge",
		},
		{
			name:        "Unknown service",
			service:     "UnknownService",
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			types := GetInstanceTypesByService(tt.service)
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

func TestIsValidInstanceType(t *testing.T) {
	tests := []struct {
		name         string
		instanceType string
		expectValid  bool
	}{
		{
			name:         "Valid RDS type",
			instanceType: "db.t3.small",
			expectValid:  true,
		},
		{
			name:         "Valid ElastiCache type",
			instanceType: "cache.r5.large",
			expectValid:  true,
		},
		{
			name:         "Valid EC2 type",
			instanceType: "m5.xlarge",
			expectValid:  true,
		},
		{
			name:         "Invalid type",
			instanceType: "invalid.type",
			expectValid:  false,
		},
		{
			name:         "Type with whitespace",
			instanceType: " db.t3.medium ",
			expectValid:  true,
		},
		{
			name:         "Empty string",
			instanceType: "",
			expectValid:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := IsValidInstanceType(tt.instanceType)
			assert.Equal(t, tt.expectValid, valid)
		})
	}
}

func TestGetInstanceTypePrefix(t *testing.T) {
	tests := []struct {
		name         string
		instanceType string
		expected     string
	}{
		{
			name:         "RDS instance type",
			instanceType: "db.t3.small",
			expected:     "db.t3",
		},
		{
			name:         "ElastiCache instance type",
			instanceType: "cache.r5.large",
			expected:     "cache.r5",
		},
		{
			name:         "EC2 instance type",
			instanceType: "m5.xlarge",
			expected:     "m5.xlarge", // EC2 only has 2 parts, so prefix is the whole thing
		},
		{
			name:         "Single part",
			instanceType: "single",
			expected:     "single",
		},
		{
			name:         "Empty string",
			instanceType: "",
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix := GetInstanceTypePrefix(tt.instanceType)
			assert.Equal(t, tt.expected, prefix)
		})
	}
}

func TestGetInstanceTypeFamily(t *testing.T) {
	tests := []struct {
		name         string
		instanceType string
		expected     string
	}{
		{
			name:         "RDS instance type",
			instanceType: "db.t3.small",
			expected:     "t3",
		},
		{
			name:         "ElastiCache instance type",
			instanceType: "cache.r5.large",
			expected:     "r5",
		},
		{
			name:         "EC2 instance type",
			instanceType: "m5.xlarge",
			expected:     "m5",
		},
		{
			name:         "EC2 t3 instance",
			instanceType: "t3.medium",
			expected:     "t3",
		},
		{
			name:         "Single part",
			instanceType: "single",
			expected:     "",
		},
		{
			name:         "Empty string",
			instanceType: "",
			expected:     "",
		},
		{
			name:         "DB with only two parts",
			instanceType: "db.t3",
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			family := GetInstanceTypeFamily(tt.instanceType)
			assert.Equal(t, tt.expected, family)
		})
	}
}

func TestValidInstanceTypesMap(t *testing.T) {
	// Test that ValidInstanceTypes map contains expected services
	t.Run("Contains expected services", func(t *testing.T) {
		assert.Contains(t, ValidInstanceTypes, ServiceRDS)
		assert.Contains(t, ValidInstanceTypes, ServiceElastiCache)
		assert.Contains(t, ValidInstanceTypes, ServiceEC2)
		assert.Contains(t, ValidInstanceTypes, ServiceMemoryDB)
		assert.Contains(t, ValidInstanceTypes, ServiceOpenSearch)
		assert.Contains(t, ValidInstanceTypes, ServiceRedshift)
	})

	t.Run("Each service has instance types", func(t *testing.T) {
		for service, types := range ValidInstanceTypes {
			assert.NotEmpty(t, types, "Service %s should have instance types", service)
		}
	})
}
