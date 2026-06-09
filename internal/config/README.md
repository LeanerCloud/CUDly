# Configuration Database for CUDly

This package provides a DynamoDB-backed configuration database with caching for CUDly application settings.

## Overview

The configuration database provides:

- **Type-safe configuration storage** with automatic type detection
- **In-memory caching** with configurable TTL (default 5 minutes)
- **Default settings** for all CUDly configuration categories
- **Thread-safe operations** using read-write locks
- **Comprehensive test coverage** with mocked DynamoDB

## Files

### `configdb.go`

Main implementation of the configuration database client.

**Key Types:**

- `ConfigDBClient` - Main client with caching support
- `ConfigSetting` - Represents a single configuration key-value pair
- `cachedSetting` - Internal type wrapping settings with cache timestamps

**Key Methods:**

```go
// Create new client
func NewConfigDBClient(dynamodbClient DynamoDBClient, tableName string) *ConfigDBClient

// Basic operations
func (c *ConfigDBClient) Get(ctx context.Context, key string) (*ConfigSetting, error)
func (c *ConfigDBClient) Set(ctx context.Context, key string, value interface{}) error
func (c *ConfigDBClient) Delete(ctx context.Context, key string) error
func (c *ConfigDBClient) GetAll(ctx context.Context) ([]ConfigSetting, error)
func (c *ConfigDBClient) GetByCategory(ctx context.Context, category string) ([]ConfigSetting, error)

// Type-safe getters with defaults
func (c *ConfigDBClient) GetInt(ctx context.Context, key string, defaultValue int) (int, error)
func (c *ConfigDBClient) GetFloat(ctx context.Context, key string, defaultValue float64) (float64, error)
func (c *ConfigDBClient) GetBool(ctx context.Context, key string, defaultValue bool) (bool, error)
func (c *ConfigDBClient) GetString(ctx context.Context, key string, defaultValue string) (string, error)

// Cache management
func (c *ConfigDBClient) SetCacheTTL(ttl time.Duration)
func (c *ConfigDBClient) InvalidateCache()
```

### `defaults.go`

Comprehensive default settings for all CUDly configuration categories.

**Configuration Categories:**

1. **purchase_defaults** - Default purchase settings (term, payment option, coverage, ramp schedule)
2. **notification** - Email notification settings
3. **providers** - Cloud provider enablement (AWS, Azure, GCP)
4. **security** - Security settings (session duration, lockout, password requirements)
5. **scheduling** - Automated collection and purchase scheduling
6. **aws** - AWS-specific settings (utilization thresholds, Savings Plans options)
7. **thresholds** - Cost and savings thresholds
8. **retention** - Data retention periods
9. **api** - API rate limiting and timeout settings

**Helper Functions:**

```go
func GetDefaultValue(key string) interface{}
func GetDefaultSetting(key string) *ConfigSetting
func GetDefaultsByCategory(category string) []ConfigSetting
func GetAllCategories() []string
```

### `configdb_test.go` & `defaults_test.go`

Comprehensive test suites with 100% coverage of all functionality.

## DynamoDB Schema

**Table Structure:**

- **PK** (Partition Key): `"CONFIG"` (constant for all config items)
- **SK** (Sort Key): Configuration key (e.g., `"purchase_defaults.term"`)
- **Value**: The configuration value (supports multiple types)
- **Type**: Type indicator (`"int"`, `"float"`, `"bool"`, `"string"`, `"json"`)
- **Category**: Logical grouping (e.g., `"purchase_defaults"`, `"notification"`)
- **Description**: Human-readable description
- **UpdatedAt**: ISO 8601 timestamp of last update

## Usage Examples

### Basic Usage

```go
// Create client
client := config.NewConfigDBClient(dynamodbClient, "cudly-config-table")

// Get a setting with type safety
term, err := client.GetInt(ctx, "purchase_defaults.term", 3)
coverage, err := client.GetFloat(ctx, "purchase_defaults.coverage", 80.0)
emailEnabled, err := client.GetBool(ctx, "notification.email_enabled", true)

// Set a setting
err := client.Set(ctx, "purchase_defaults.term", 1)

// Get all settings in a category
notifications, err := client.GetByCategory(ctx, "notification")

// Get all settings as a map
allSettings, err := client.GetAsMap(ctx)
```

### With Caching

```go
// Create client with custom cache TTL
client := config.NewConfigDBClient(dynamodbClient, "cudly-config-table")
client.SetCacheTTL(10 * time.Minute)

// First call hits DynamoDB
setting1, _ := client.Get(ctx, "purchase_defaults.term")

// Second call uses cache (no DynamoDB call)
setting2, _ := client.Get(ctx, "purchase_defaults.term")

// Invalidate cache when needed
client.InvalidateCache()
```

### Loading Defaults

```go
// Get default value
defaultTerm := config.GetDefaultValue("purchase_defaults.term") // Returns: 3

// Get full default setting
setting := config.GetDefaultSetting("purchase_defaults.term")
// Returns: &ConfigSetting{
//   Key: "purchase_defaults.term",
//   Value: 3,
//   Type: "int",
//   Category: "purchase_defaults",
//   Description: "Default commitment term in years (1 or 3)",
// }

// Initialize database with defaults
for _, setting := range config.DefaultSettings {
    client.SaveSetting(ctx, &setting)
}

// Get all defaults for a category
purchaseDefaults := config.GetDefaultsByCategory("purchase_defaults")
```

## Default Settings Reference

### Purchase Defaults

- `purchase_defaults.term`: `3` (int) - Commitment term in years
- `purchase_defaults.payment_option`: `"no-upfront"` (string) - Payment option
- `purchase_defaults.coverage`: `80.0` (float) - Coverage percentage
- `purchase_defaults.ramp_schedule`: `"immediate"` (string) - Ramp schedule

### Notification Settings

- `notification.days_before`: `3` (int) - Days before purchase to notify
- `notification.email_enabled`: `true` (bool) - Enable email notifications
- `notification.approval_required`: `true` (bool) - Require approval
- `notification.email_from`: `"noreply@cudly.io"` (string) - Sender email

### Provider Settings

- `providers.aws_enabled`: `true` (bool)
- `providers.azure_enabled`: `false` (bool)
- `providers.gcp_enabled`: `false` (bool)

### Security Settings

- `security.session_duration_hours`: `24` (int)
- `security.lockout_attempts`: `5` (int)
- `security.lockout_duration_minutes`: `15` (int)
- `security.password_min_length`: `12` (int)
- `security.password_require_special`: `true` (bool)
- `security.password_require_number`: `true` (bool)
- `security.password_require_uppercase`: `true` (bool)

### Scheduling Settings

- `scheduling.auto_collect`: `false` (bool)
- `scheduling.collect_schedule`: `"rate(1 day)"` (string)
- `scheduling.auto_purchase`: `false` (bool)
- `scheduling.purchase_schedule`: `"rate(1 day)"` (string)

### AWS-Specific Settings

- `aws.rds.min_utilization_percent`: `50.0` (float)
- `aws.elasticache.min_utilization_percent`: `50.0` (float)
- `aws.opensearch.min_utilization_percent`: `50.0` (float)
- `aws.ec2.include_convertible`: `true` (bool)
- `aws.savings_plans.compute_enabled`: `true` (bool)
- `aws.savings_plans.ec2_enabled`: `true` (bool)
- `aws.savings_plans.sagemaker_enabled`: `true` (bool)

### Thresholds

- `thresholds.min_monthly_savings`: `10.0` (float)
- `thresholds.min_savings_percentage`: `5.0` (float)
- `thresholds.max_upfront_cost`: `0.0` (float) - 0 = no limit

### Data Retention

- `retention.purchase_history_days`: `1095` (int) - 3 years
- `retention.execution_history_days`: `90` (int)
- `retention.recommendation_cache_hours`: `24` (int)

### API Settings

- `api.rate_limit_requests_per_minute`: `100` (int)
- `api.rate_limit_enabled`: `true` (bool)
- `api.timeout_seconds`: `30` (int)

## Performance Characteristics

- **Cache Hit**: O(1) - In-memory map lookup
- **Cache Miss**: O(1) - Single DynamoDB GetItem call
- **GetAll**: O(n) - DynamoDB Query with single partition key
- **GetByCategory**: O(n) - In-memory filtering after GetAll
- **Thread Safety**: Read-write locks minimize contention

## Testing

Run tests:

```bash
go test ./internal/config/...
```

Run with coverage:

```bash
go test -cover ./internal/config/...
```

Test features:

- Unit tests with mocked DynamoDB client
- Cache behavior tests (hit, miss, expiration, invalidation)
- Type conversion tests
- Default settings validation
- Concurrent access simulation
