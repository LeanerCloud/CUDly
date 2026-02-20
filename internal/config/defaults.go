package config

import "time"

// DefaultSettings defines the default configuration values for CUDly
var DefaultSettings = []ConfigSetting{
	// Purchase Defaults
	{
		Key:         "purchase_defaults.term",
		Value:       3,
		Type:        "int",
		Category:    "purchase_defaults",
		Description: "Default commitment term in years (1 or 3)",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "purchase_defaults.payment_option",
		Value:       "no-upfront",
		Type:        "string",
		Category:    "purchase_defaults",
		Description: "Default payment option: no-upfront, partial-upfront, all-upfront",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "purchase_defaults.coverage",
		Value:       80.0,
		Type:        "float",
		Category:    "purchase_defaults",
		Description: "Default coverage percentage (0-100)",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "purchase_defaults.ramp_schedule",
		Value:       "immediate",
		Type:        "string",
		Category:    "purchase_defaults",
		Description: "Default ramp schedule: immediate, weekly-25pct, monthly-10pct",
		UpdatedAt:   time.Now(),
	},

	// Notification Settings
	{
		Key:         "notification.days_before",
		Value:       3,
		Type:        "int",
		Category:    "notification",
		Description: "Days before purchase to send notification",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "notification.email_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "notification",
		Description: "Enable email notifications for purchases",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "notification.approval_required",
		Value:       true,
		Type:        "bool",
		Category:    "notification",
		Description: "Require approval before executing purchases",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "notification.email_from",
		Value:       "noreply@cudly.io",
		Type:        "string",
		Category:    "notification",
		Description: "Email sender address for notifications",
		UpdatedAt:   time.Now(),
	},

	// Provider Settings
	{
		Key:         "providers.aws_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "providers",
		Description: "Enable AWS provider for recommendations and purchases",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "providers.azure_enabled",
		Value:       false,
		Type:        "bool",
		Category:    "providers",
		Description: "Enable Azure provider for recommendations and purchases",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "providers.gcp_enabled",
		Value:       false,
		Type:        "bool",
		Category:    "providers",
		Description: "Enable GCP provider for recommendations and purchases",
		UpdatedAt:   time.Now(),
	},

	// Security Settings
	{
		Key:         "security.session_duration_hours",
		Value:       24,
		Type:        "int",
		Category:    "security",
		Description: "Session duration in hours before re-authentication required",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "security.lockout_attempts",
		Value:       5,
		Type:        "int",
		Category:    "security",
		Description: "Failed login attempts before account lockout",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "security.lockout_duration_minutes",
		Value:       15,
		Type:        "int",
		Category:    "security",
		Description: "Account lockout duration in minutes",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "security.password_min_length",
		Value:       12,
		Type:        "int",
		Category:    "security",
		Description: "Minimum password length requirement",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "security.password_require_special",
		Value:       true,
		Type:        "bool",
		Category:    "security",
		Description: "Require special characters in passwords",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "security.password_require_number",
		Value:       true,
		Type:        "bool",
		Category:    "security",
		Description: "Require numbers in passwords",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "security.password_require_uppercase",
		Value:       true,
		Type:        "bool",
		Category:    "security",
		Description: "Require uppercase letters in passwords",
		UpdatedAt:   time.Now(),
	},

	// Scheduling Settings
	{
		Key:         "scheduling.auto_collect",
		Value:       false,
		Type:        "bool",
		Category:    "scheduling",
		Description: "Automatically collect recommendations on schedule",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "scheduling.collect_schedule",
		Value:       "rate(1 day)",
		Type:        "string",
		Category:    "scheduling",
		Description: "Schedule for automatic recommendation collection (EventBridge format)",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "scheduling.auto_purchase",
		Value:       false,
		Type:        "bool",
		Category:    "scheduling",
		Description: "Automatically execute approved purchase plans",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "scheduling.purchase_schedule",
		Value:       "rate(1 day)",
		Type:        "string",
		Category:    "scheduling",
		Description: "Schedule for checking and executing purchase plans (EventBridge format)",
		UpdatedAt:   time.Now(),
	},

	// AWS-specific Settings
	{
		Key:         "aws.rds.min_utilization_percent",
		Value:       50.0,
		Type:        "float",
		Category:    "aws",
		Description: "Minimum RDS instance utilization for RI recommendations",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "aws.elasticache.min_utilization_percent",
		Value:       50.0,
		Type:        "float",
		Category:    "aws",
		Description: "Minimum ElastiCache node utilization for RI recommendations",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "aws.opensearch.min_utilization_percent",
		Value:       50.0,
		Type:        "float",
		Category:    "aws",
		Description: "Minimum OpenSearch instance utilization for RI recommendations",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "aws.ec2.include_convertible",
		Value:       true,
		Type:        "bool",
		Category:    "aws",
		Description: "Include convertible EC2 Reserved Instances in recommendations",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "aws.savings_plans.compute_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "aws",
		Description: "Include Compute Savings Plans in recommendations",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "aws.savings_plans.ec2_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "aws",
		Description: "Include EC2 Instance Savings Plans in recommendations",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "aws.savings_plans.sagemaker_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "aws",
		Description: "Include SageMaker Savings Plans in recommendations",
		UpdatedAt:   time.Now(),
	},

	// Cost and Savings Thresholds
	{
		Key:         "thresholds.min_monthly_savings",
		Value:       10.0,
		Type:        "float",
		Category:    "thresholds",
		Description: "Minimum monthly savings ($) to include recommendation",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "thresholds.min_savings_percentage",
		Value:       5.0,
		Type:        "float",
		Category:    "thresholds",
		Description: "Minimum savings percentage to include recommendation",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "thresholds.max_upfront_cost",
		Value:       0.0,
		Type:        "float",
		Category:    "thresholds",
		Description: "Maximum upfront cost ($) per purchase (0 = no limit)",
		UpdatedAt:   time.Now(),
	},

	// Data Retention
	{
		Key:         "retention.purchase_history_days",
		Value:       1095,
		Type:        "int",
		Category:    "retention",
		Description: "Days to retain purchase history (3 years default)",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "retention.execution_history_days",
		Value:       90,
		Type:        "int",
		Category:    "retention",
		Description: "Days to retain execution history records",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "retention.recommendation_cache_hours",
		Value:       24,
		Type:        "int",
		Category:    "retention",
		Description: "Hours to cache recommendation data",
		UpdatedAt:   time.Now(),
	},

	// API Rate Limiting
	{
		Key:         "api.rate_limit_requests_per_minute",
		Value:       100,
		Type:        "int",
		Category:    "api",
		Description: "Maximum API requests per minute per user",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "api.rate_limit_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "api",
		Description: "Enable API rate limiting",
		UpdatedAt:   time.Now(),
	},
	{
		Key:         "api.timeout_seconds",
		Value:       30,
		Type:        "int",
		Category:    "api",
		Description: "Default API request timeout in seconds",
		UpdatedAt:   time.Now(),
	},
}

// GetDefaultValue returns the default value for a given key
func GetDefaultValue(key string) any {
	for _, setting := range DefaultSettings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return nil
}

// GetDefaultSetting returns the complete default setting for a given key
func GetDefaultSetting(key string) *ConfigSetting {
	for _, setting := range DefaultSettings {
		if setting.Key == key {
			// Return a copy
			s := setting
			return &s
		}
	}
	return nil
}

// GetDefaultsByCategory returns all default settings for a given category
func GetDefaultsByCategory(category string) []ConfigSetting {
	var result []ConfigSetting
	for _, setting := range DefaultSettings {
		if setting.Category == category {
			result = append(result, setting)
		}
	}
	return result
}

// GetAllCategories returns a list of all configuration categories
func GetAllCategories() []string {
	categoryMap := make(map[string]bool)
	for _, setting := range DefaultSettings {
		categoryMap[setting.Category] = true
	}

	categories := make([]string, 0, len(categoryMap))
	for category := range categoryMap {
		categories = append(categories, category)
	}
	return categories
}
