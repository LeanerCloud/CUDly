package database

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds database configuration
type Config struct {
	// Connection details
	Host     string
	Port     int
	Database string
	User     string

	// Password can be direct value or secret reference
	Password       string // Direct password (local dev only)
	PasswordSecret string // Secret ARN/ID/name for cloud secret managers

	// SSL configuration
	SSLMode string // disable, require, verify-ca, verify-full

	// Connection pool settings
	MaxConnections    int
	MinConnections    int
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration

	// Migration settings
	AutoMigrate    bool
	MigrationsPath string

	// Logging
	LogLevel string // error, warn, info, debug
}

// LoadFromEnv loads database configuration from environment variables
func LoadFromEnv() (*Config, error) {
	config := &Config{
		// Required fields
		Host:     getEnv("DB_HOST", "localhost"),
		Port:     getEnvInt("DB_PORT", 5432),
		Database: getEnv("DB_NAME", "cudly"),
		User:     getEnv("DB_USER", "cudly"),

		// Password (one of these must be set)
		Password:       getEnv("DB_PASSWORD", ""),
		PasswordSecret: getEnv("DB_PASSWORD_SECRET", ""),

		// SSL (default to require for security)
		SSLMode: getEnv("DB_SSL_MODE", "require"),

		// Connection pool defaults
		MaxConnections:    getEnvInt("DB_MAX_CONNECTIONS", 25),
		MinConnections:    getEnvInt("DB_MIN_CONNECTIONS", 2),
		MaxConnLifetime:   getEnvDuration("DB_MAX_CONN_LIFETIME", time.Hour),
		MaxConnIdleTime:   getEnvDuration("DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
		HealthCheckPeriod: getEnvDuration("DB_HEALTH_CHECK_PERIOD", time.Minute),
		ConnectTimeout:    getEnvDuration("DB_CONNECT_TIMEOUT", 10*time.Second),

		// Migrations
		AutoMigrate:    getEnvBool("DB_AUTO_MIGRATE", false),
		MigrationsPath: getEnv("DB_MIGRATIONS_PATH", "internal/database/postgres/migrations"),

		// Logging
		LogLevel: getEnv("DB_LOG_LEVEL", "info"),
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if err := c.validateRequiredFields(); err != nil {
		return err
	}
	if err := c.validateSSLMode(); err != nil {
		return err
	}
	return c.validatePoolSettings()
}

// validateRequiredFields checks that all required configuration fields are set
func (c *Config) validateRequiredFields() error {
	if c.Host == "" {
		return fmt.Errorf("DB_HOST is required")
	}
	if c.Database == "" {
		return fmt.Errorf("DB_NAME is required")
	}
	if c.User == "" {
		return fmt.Errorf("DB_USER is required")
	}
	if c.Password == "" && c.PasswordSecret == "" {
		return fmt.Errorf("either DB_PASSWORD or DB_PASSWORD_SECRET must be set")
	}
	return nil
}

// validateSSLMode checks that SSL mode is valid and warns about insecure production settings
func (c *Config) validateSSLMode() error {
	validSSLModes := map[string]bool{
		"disable":     true,
		"require":     true,
		"verify-ca":   true,
		"verify-full": true,
	}
	if !validSSLModes[c.SSLMode] {
		return fmt.Errorf("invalid DB_SSL_MODE: %s (must be one of: disable, require, verify-ca, verify-full)", c.SSLMode)
	}

	if c.SSLMode == "disable" && os.Getenv("ENVIRONMENT") == "production" {
		fmt.Fprintf(os.Stderr, "WARNING: DB_SSL_MODE=disable should not be used in production\n")
	}

	return nil
}

// validatePoolSettings validates connection pool configuration
func (c *Config) validatePoolSettings() error {
	if c.MaxConnections < 1 {
		return fmt.Errorf("DB_MAX_CONNECTIONS must be at least 1")
	}
	if c.MinConnections < 0 {
		return fmt.Errorf("DB_MIN_CONNECTIONS cannot be negative")
	}
	if c.MinConnections > c.MaxConnections {
		return fmt.Errorf("DB_MIN_CONNECTIONS cannot be greater than DB_MAX_CONNECTIONS")
	}
	return nil
}

// DSN generates a PostgreSQL connection string
// If passwordOverride is provided, it's used instead of config.Password
func (c *Config) DSN(passwordOverride string) string {
	password := c.Password
	if passwordOverride != "" {
		password = passwordOverride
	}

	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d",
		c.Host,
		c.Port,
		c.User,
		password,
		c.Database,
		c.SSLMode,
		int(c.ConnectTimeout.Seconds()),
	)
}

// RedactedDSN returns a DSN string with the password masked, safe for logging
func (c *Config) RedactedDSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=***** dbname=%s sslmode=%s connect_timeout=%d",
		c.Host,
		c.Port,
		c.User,
		c.Database,
		c.SSLMode,
		int(c.ConnectTimeout.Seconds()),
	)
}

// Helper functions for environment variable parsing

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
