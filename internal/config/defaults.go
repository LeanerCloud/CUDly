package config

import "time"

// DefaultSettings defines the default configuration values for CUDly.
// UpdatedAt is the zero time.Time{} for every entry: these are static
// compile-time defaults and have never been "updated" by a user.
var DefaultSettings = []Setting{
	// Purchase Defaults
	{
		Key:         "purchase_defaults.term",
		Value:       3,
		Type:        "int",
		Category:    "purchase_defaults",
		Description: "Default commitment term in years (1 or 3)",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "purchase_defaults.payment_option",
		Value:       "no-upfront",
		Type:        "string",
		Category:    "purchase_defaults",
		Description: "Default payment option: no-upfront, partial-upfront, all-upfront",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "purchase_defaults.coverage",
		Value:       80.0,
		Type:        "float",
		Category:    "purchase_defaults",
		Description: "Default coverage percentage (0-100)",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "purchase_defaults.ramp_schedule",
		Value:       "immediate",
		Type:        "string",
		Category:    "purchase_defaults",
		Description: "Default ramp schedule: immediate, weekly-25pct, monthly-10pct",
		UpdatedAt:   time.Time{},
	},

	// Notification Settings
	{
		Key:         "notification.days_before",
		Value:       3,
		Type:        "int",
		Category:    "notification",
		Description: "Days before purchase to send notification",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "notification.email_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "notification",
		Description: "Enable email notifications for purchases",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "notification.approval_required",
		Value:       true,
		Type:        "bool",
		Category:    "notification",
		Description: "Require approval before executing purchases",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "notification.email_from",
		Value:       "noreply@cudly.io",
		Type:        "string",
		Category:    "notification",
		Description: "Email sender address for notifications",
		UpdatedAt:   time.Time{},
	},

	// Provider Settings
	{
		Key:         "providers.aws_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "providers",
		Description: "Enable AWS provider for recommendations and purchases",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "providers.azure_enabled",
		Value:       false,
		Type:        "bool",
		Category:    "providers",
		Description: "Enable Azure provider for recommendations and purchases",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "providers.gcp_enabled",
		Value:       false,
		Type:        "bool",
		Category:    "providers",
		Description: "Enable GCP provider for recommendations and purchases",
		UpdatedAt:   time.Time{},
	},

	// Security Settings
	{
		Key:         "security.session_duration_hours",
		Value:       24,
		Type:        "int",
		Category:    "security",
		Description: "Session duration in hours before re-authentication required",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "security.lockout_attempts",
		Value:       5,
		Type:        "int",
		Category:    "security",
		Description: "Failed login attempts before account lockout",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "security.lockout_duration_minutes",
		Value:       15,
		Type:        "int",
		Category:    "security",
		Description: "Account lockout duration in minutes",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "security.password_min_length",
		Value:       12,
		Type:        "int",
		Category:    "security",
		Description: "Minimum password length requirement",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "security.password_require_special",
		Value:       true,
		Type:        "bool",
		Category:    "security",
		Description: "Require special characters in passwords",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "security.password_require_number",
		Value:       true,
		Type:        "bool",
		Category:    "security",
		Description: "Require numbers in passwords",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "security.password_require_uppercase",
		Value:       true,
		Type:        "bool",
		Category:    "security",
		Description: "Require uppercase letters in passwords",
		UpdatedAt:   time.Time{},
	},

	// Scheduling Settings
	{
		Key:         "scheduling.auto_collect",
		Value:       false,
		Type:        "bool",
		Category:    "scheduling",
		Description: "Automatically collect recommendations on schedule",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "scheduling.collect_schedule",
		Value:       "rate(1 day)",
		Type:        "string",
		Category:    "scheduling",
		Description: "Schedule for automatic recommendation collection (EventBridge format)",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "scheduling.auto_purchase",
		Value:       false,
		Type:        "bool",
		Category:    "scheduling",
		Description: "Automatically execute approved purchase plans",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "scheduling.purchase_schedule",
		Value:       "rate(1 day)",
		Type:        "string",
		Category:    "scheduling",
		Description: "Schedule for checking and executing purchase plans (EventBridge format)",
		UpdatedAt:   time.Time{},
	},

	// AWS-specific Settings
	{
		Key:         "aws.rds.min_utilization_percent",
		Value:       50.0,
		Type:        "float",
		Category:    "aws",
		Description: "Minimum RDS instance utilization for RI recommendations",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "aws.elasticache.min_utilization_percent",
		Value:       50.0,
		Type:        "float",
		Category:    "aws",
		Description: "Minimum ElastiCache node utilization for RI recommendations",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "aws.opensearch.min_utilization_percent",
		Value:       50.0,
		Type:        "float",
		Category:    "aws",
		Description: "Minimum OpenSearch instance utilization for RI recommendations",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "aws.ec2.include_convertible",
		Value:       true,
		Type:        "bool",
		Category:    "aws",
		Description: "Include convertible EC2 Reserved Instances in recommendations",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "aws.savings_plans.compute_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "aws",
		Description: "Include Compute Savings Plans in recommendations",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "aws.savings_plans.ec2_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "aws",
		Description: "Include EC2 Instance Savings Plans in recommendations",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "aws.savings_plans.sagemaker_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "aws",
		Description: "Include SageMaker Savings Plans in recommendations",
		UpdatedAt:   time.Time{},
	},

	// Cost and Savings Thresholds
	{
		Key:         "thresholds.min_monthly_savings",
		Value:       10.0,
		Type:        "float",
		Category:    "thresholds",
		Description: "Minimum monthly savings ($) to include recommendation",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "thresholds.min_savings_percentage",
		Value:       5.0,
		Type:        "float",
		Category:    "thresholds",
		Description: "Minimum savings percentage to include recommendation",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "thresholds.max_upfront_cost",
		Value:       0.0,
		Type:        "float",
		Category:    "thresholds",
		Description: "Maximum upfront cost ($) per purchase (0 = no limit)",
		UpdatedAt:   time.Time{},
	},

	// Data Retention
	{
		Key:         "retention.purchase_history_days",
		Value:       1095,
		Type:        "int",
		Category:    "retention",
		Description: "Days to retain purchase history (3 years default)",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "retention.execution_history_days",
		Value:       90,
		Type:        "int",
		Category:    "retention",
		Description: "Days to retain execution history records",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "retention.recommendation_cache_hours",
		Value:       24,
		Type:        "int",
		Category:    "retention",
		Description: "Hours to cache recommendation data",
		UpdatedAt:   time.Time{},
	},

	// RI Exchange Automation
	{
		Key:         "ri_exchange.auto_exchange_enabled",
		Value:       false,
		Type:        "bool",
		Category:    "ri_exchange",
		Description: "Master toggle for automated RI exchange",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "ri_exchange.mode",
		Value:       "manual",
		Type:        "string",
		Category:    "ri_exchange",
		Description: "Exchange mode: manual (email approval) or auto (fully automated)",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "ri_exchange.utilization_threshold",
		Value:       95.0,
		Type:        "float",
		Category:    "ri_exchange",
		Description: "Utilization percentage below which an RI triggers exchange consideration",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "ri_exchange.max_payment_per_exchange_usd",
		Value:       0.0,
		Type:        "float",
		Category:    "ri_exchange",
		Description: "Maximum payment per single exchange in USD (0 = refuse any payment)",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "ri_exchange.max_payment_daily_usd",
		Value:       0.0,
		Type:        "float",
		Category:    "ri_exchange",
		Description: "Maximum total daily exchange spend in USD (0 = refuse any payment)",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "ri_exchange.lookback_days",
		Value:       30,
		Type:        "int",
		Category:    "ri_exchange",
		Description: "Days of utilization data to consider for exchange recommendations",
		UpdatedAt:   time.Time{},
	},

	// API Rate Limiting
	{
		Key:         "api.rate_limit_requests_per_minute",
		Value:       100,
		Type:        "int",
		Category:    "api",
		Description: "Maximum API requests per minute per user",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "api.rate_limit_enabled",
		Value:       true,
		Type:        "bool",
		Category:    "api",
		Description: "Enable API rate limiting",
		UpdatedAt:   time.Time{},
	},
	{
		Key:         "api.timeout_seconds",
		Value:       30,
		Type:        "int",
		Category:    "api",
		Description: "Default API request timeout in seconds",
		UpdatedAt:   time.Time{},
	},
}

// GetDefaultValue returns the default value for a given key.
func GetDefaultValue(key string) any {
	for _, setting := range DefaultSettings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return nil
}

// GetDefaultSetting returns the complete default setting for a given key.
func GetDefaultSetting(key string) *Setting {
	for _, setting := range DefaultSettings {
		if setting.Key == key {
			// Return a copy
			s := setting
			return &s
		}
	}
	return nil
}

// GetDefaultsByCategory returns all default settings for a given category.
func GetDefaultsByCategory(category string) []Setting {
	var result []Setting
	for _, setting := range DefaultSettings {
		if setting.Category == category {
			result = append(result, setting)
		}
	}
	return result
}

// GetAllCategories returns a list of all configuration categories.
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
