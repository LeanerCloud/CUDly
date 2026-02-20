package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultSettings(t *testing.T) {
	assert.NotEmpty(t, DefaultSettings, "DefaultSettings should not be empty")

	// Verify all settings have required fields
	for _, setting := range DefaultSettings {
		assert.NotEmpty(t, setting.Key, "Setting key should not be empty")
		assert.NotEmpty(t, setting.Type, "Setting type should not be empty")
		assert.NotEmpty(t, setting.Category, "Setting category should not be empty")
		assert.NotNil(t, setting.Value, "Setting value should not be nil")
	}
}

func TestDefaultSettings_ExpectedKeys(t *testing.T) {
	expectedKeys := []string{
		"purchase_defaults.term",
		"purchase_defaults.payment_option",
		"purchase_defaults.coverage",
		"purchase_defaults.ramp_schedule",
		"notification.days_before",
		"notification.email_enabled",
		"notification.approval_required",
		"providers.aws_enabled",
		"providers.azure_enabled",
		"providers.gcp_enabled",
		"security.session_duration_hours",
		"security.lockout_attempts",
		"security.lockout_duration_minutes",
		"scheduling.auto_collect",
		"scheduling.collect_schedule",
	}

	settingsMap := make(map[string]bool)
	for _, setting := range DefaultSettings {
		settingsMap[setting.Key] = true
	}

	for _, key := range expectedKeys {
		assert.True(t, settingsMap[key], "Expected key %s should exist in DefaultSettings", key)
	}
}

func TestDefaultSettings_PurchaseDefaults(t *testing.T) {
	tests := []struct {
		key           string
		expectedType  string
		expectedValue interface{}
	}{
		{"purchase_defaults.term", "int", 3},
		{"purchase_defaults.payment_option", "string", "no-upfront"},
		{"purchase_defaults.coverage", "float", 80.0},
		{"purchase_defaults.ramp_schedule", "string", "immediate"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setting := GetDefaultSetting(tt.key)
			require.NotNil(t, setting, "Setting should exist for key %s", tt.key)
			assert.Equal(t, tt.expectedType, setting.Type)
			assert.Equal(t, tt.expectedValue, setting.Value)
			assert.Equal(t, "purchase_defaults", setting.Category)
		})
	}
}

func TestDefaultSettings_Notification(t *testing.T) {
	tests := []struct {
		key           string
		expectedType  string
		expectedValue interface{}
	}{
		{"notification.days_before", "int", 3},
		{"notification.email_enabled", "bool", true},
		{"notification.approval_required", "bool", true},
		{"notification.email_from", "string", "noreply@cudly.io"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setting := GetDefaultSetting(tt.key)
			require.NotNil(t, setting, "Setting should exist for key %s", tt.key)
			assert.Equal(t, tt.expectedType, setting.Type)
			assert.Equal(t, tt.expectedValue, setting.Value)
			assert.Equal(t, "notification", setting.Category)
		})
	}
}

func TestDefaultSettings_Providers(t *testing.T) {
	tests := []struct {
		key           string
		expectedValue bool
	}{
		{"providers.aws_enabled", true},
		{"providers.azure_enabled", false},
		{"providers.gcp_enabled", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setting := GetDefaultSetting(tt.key)
			require.NotNil(t, setting, "Setting should exist for key %s", tt.key)
			assert.Equal(t, "bool", setting.Type)
			assert.Equal(t, tt.expectedValue, setting.Value)
			assert.Equal(t, "providers", setting.Category)
		})
	}
}

func TestDefaultSettings_Security(t *testing.T) {
	tests := []struct {
		key           string
		expectedType  string
		expectedValue interface{}
	}{
		{"security.session_duration_hours", "int", 24},
		{"security.lockout_attempts", "int", 5},
		{"security.lockout_duration_minutes", "int", 15},
		{"security.password_min_length", "int", 12},
		{"security.password_require_special", "bool", true},
		{"security.password_require_number", "bool", true},
		{"security.password_require_uppercase", "bool", true},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setting := GetDefaultSetting(tt.key)
			require.NotNil(t, setting, "Setting should exist for key %s", tt.key)
			assert.Equal(t, tt.expectedType, setting.Type)
			assert.Equal(t, tt.expectedValue, setting.Value)
			assert.Equal(t, "security", setting.Category)
		})
	}
}

func TestDefaultSettings_Scheduling(t *testing.T) {
	tests := []struct {
		key           string
		expectedType  string
		expectedValue interface{}
	}{
		{"scheduling.auto_collect", "bool", false},
		{"scheduling.collect_schedule", "string", "rate(1 day)"},
		{"scheduling.auto_purchase", "bool", false},
		{"scheduling.purchase_schedule", "string", "rate(1 day)"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setting := GetDefaultSetting(tt.key)
			require.NotNil(t, setting, "Setting should exist for key %s", tt.key)
			assert.Equal(t, tt.expectedType, setting.Type)
			assert.Equal(t, tt.expectedValue, setting.Value)
			assert.Equal(t, "scheduling", setting.Category)
		})
	}
}

func TestDefaultSettings_AWS(t *testing.T) {
	tests := []struct {
		key           string
		expectedType  string
		expectedValue interface{}
	}{
		{"aws.rds.min_utilization_percent", "float", 50.0},
		{"aws.elasticache.min_utilization_percent", "float", 50.0},
		{"aws.opensearch.min_utilization_percent", "float", 50.0},
		{"aws.ec2.include_convertible", "bool", true},
		{"aws.savings_plans.compute_enabled", "bool", true},
		{"aws.savings_plans.ec2_enabled", "bool", true},
		{"aws.savings_plans.sagemaker_enabled", "bool", true},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setting := GetDefaultSetting(tt.key)
			require.NotNil(t, setting, "Setting should exist for key %s", tt.key)
			assert.Equal(t, tt.expectedType, setting.Type)
			assert.Equal(t, tt.expectedValue, setting.Value)
			assert.Equal(t, "aws", setting.Category)
		})
	}
}

func TestDefaultSettings_Thresholds(t *testing.T) {
	tests := []struct {
		key           string
		expectedType  string
		expectedValue interface{}
	}{
		{"thresholds.min_monthly_savings", "float", 10.0},
		{"thresholds.min_savings_percentage", "float", 5.0},
		{"thresholds.max_upfront_cost", "float", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setting := GetDefaultSetting(tt.key)
			require.NotNil(t, setting, "Setting should exist for key %s", tt.key)
			assert.Equal(t, tt.expectedType, setting.Type)
			assert.Equal(t, tt.expectedValue, setting.Value)
			assert.Equal(t, "thresholds", setting.Category)
		})
	}
}

func TestDefaultSettings_Retention(t *testing.T) {
	tests := []struct {
		key           string
		expectedValue int
	}{
		{"retention.purchase_history_days", 1095},
		{"retention.execution_history_days", 90},
		{"retention.recommendation_cache_hours", 24},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setting := GetDefaultSetting(tt.key)
			require.NotNil(t, setting, "Setting should exist for key %s", tt.key)
			assert.Equal(t, "int", setting.Type)
			assert.Equal(t, tt.expectedValue, setting.Value)
			assert.Equal(t, "retention", setting.Category)
		})
	}
}

func TestDefaultSettings_API(t *testing.T) {
	tests := []struct {
		key           string
		expectedType  string
		expectedValue interface{}
	}{
		{"api.rate_limit_requests_per_minute", "int", 100},
		{"api.rate_limit_enabled", "bool", true},
		{"api.timeout_seconds", "int", 30},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			setting := GetDefaultSetting(tt.key)
			require.NotNil(t, setting, "Setting should exist for key %s", tt.key)
			assert.Equal(t, tt.expectedType, setting.Type)
			assert.Equal(t, tt.expectedValue, setting.Value)
			assert.Equal(t, "api", setting.Category)
		})
	}
}

func TestGetDefaultValue(t *testing.T) {
	tests := []struct {
		key           string
		expectedValue interface{}
	}{
		{"purchase_defaults.term", 3},
		{"purchase_defaults.coverage", 80.0},
		{"notification.email_enabled", true},
		{"nonexistent.key", nil},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			value := GetDefaultValue(tt.key)
			assert.Equal(t, tt.expectedValue, value)
		})
	}
}

func TestGetDefaultSetting(t *testing.T) {
	t.Run("existing key", func(t *testing.T) {
		setting := GetDefaultSetting("purchase_defaults.term")
		require.NotNil(t, setting)
		assert.Equal(t, "purchase_defaults.term", setting.Key)
		assert.Equal(t, 3, setting.Value)
		assert.Equal(t, "int", setting.Type)
		assert.Equal(t, "purchase_defaults", setting.Category)
		assert.NotEmpty(t, setting.Description)
	})

	t.Run("nonexistent key", func(t *testing.T) {
		setting := GetDefaultSetting("nonexistent.key")
		assert.Nil(t, setting)
	})

	t.Run("returns copy", func(t *testing.T) {
		setting1 := GetDefaultSetting("purchase_defaults.term")
		setting2 := GetDefaultSetting("purchase_defaults.term")

		require.NotNil(t, setting1)
		require.NotNil(t, setting2)

		// Modify one copy
		setting1.Value = 999

		// Verify the other copy is unchanged
		assert.Equal(t, 3, setting2.Value)
	})
}

func TestGetDefaultsByCategory(t *testing.T) {
	t.Run("purchase_defaults category", func(t *testing.T) {
		settings := GetDefaultsByCategory("purchase_defaults")
		assert.NotEmpty(t, settings)

		for _, setting := range settings {
			assert.Equal(t, "purchase_defaults", setting.Category)
		}

		// Should have at least 4 settings
		assert.GreaterOrEqual(t, len(settings), 4)
	})

	t.Run("notification category", func(t *testing.T) {
		settings := GetDefaultsByCategory("notification")
		assert.NotEmpty(t, settings)

		for _, setting := range settings {
			assert.Equal(t, "notification", setting.Category)
		}
	})

	t.Run("security category", func(t *testing.T) {
		settings := GetDefaultsByCategory("security")
		assert.NotEmpty(t, settings)

		for _, setting := range settings {
			assert.Equal(t, "security", setting.Category)
		}
	})

	t.Run("nonexistent category", func(t *testing.T) {
		settings := GetDefaultsByCategory("nonexistent")
		assert.Empty(t, settings)
	})
}

func TestGetAllCategories(t *testing.T) {
	categories := GetAllCategories()
	assert.NotEmpty(t, categories)

	expectedCategories := []string{
		"purchase_defaults",
		"notification",
		"providers",
		"security",
		"scheduling",
		"aws",
		"thresholds",
		"retention",
		"api",
	}

	categoryMap := make(map[string]bool)
	for _, cat := range categories {
		categoryMap[cat] = true
	}

	for _, expected := range expectedCategories {
		assert.True(t, categoryMap[expected], "Expected category %s to be in result", expected)
	}
}

func TestDefaultSettings_UpdatedAtSet(t *testing.T) {
	for _, setting := range DefaultSettings {
		assert.False(t, setting.UpdatedAt.IsZero(), "UpdatedAt should be set for key %s", setting.Key)
		// UpdatedAt should be reasonably recent (within last 10 years)
		assert.True(t, setting.UpdatedAt.After(time.Now().AddDate(-10, 0, 0)),
			"UpdatedAt should be recent for key %s", setting.Key)
	}
}

func TestDefaultSettings_NoKeyDuplicates(t *testing.T) {
	seen := make(map[string]bool)

	for _, setting := range DefaultSettings {
		assert.False(t, seen[setting.Key], "Duplicate key found: %s", setting.Key)
		seen[setting.Key] = true
	}
}

func TestDefaultSettings_ValidTypes(t *testing.T) {
	validTypes := map[string]bool{
		"int":    true,
		"float":  true,
		"bool":   true,
		"string": true,
		"json":   true,
	}

	for _, setting := range DefaultSettings {
		assert.True(t, validTypes[setting.Type],
			"Invalid type %s for key %s", setting.Type, setting.Key)
	}
}

func TestDefaultSettings_TypeMatchesValue(t *testing.T) {
	for _, setting := range DefaultSettings {
		switch setting.Type {
		case "int":
			_, ok := setting.Value.(int)
			assert.True(t, ok, "Key %s has type int but value is %T", setting.Key, setting.Value)
		case "float":
			_, ok := setting.Value.(float64)
			assert.True(t, ok, "Key %s has type float but value is %T", setting.Key, setting.Value)
		case "bool":
			_, ok := setting.Value.(bool)
			assert.True(t, ok, "Key %s has type bool but value is %T", setting.Key, setting.Value)
		case "string":
			_, ok := setting.Value.(string)
			assert.True(t, ok, "Key %s has type string but value is %T", setting.Key, setting.Value)
		}
	}
}
