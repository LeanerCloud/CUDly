package database

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue string
		expected     string
	}{
		{
			name:         "returns env value when set",
			key:          "TEST_GET_ENV_1",
			envValue:     "custom_value",
			defaultValue: "default",
			expected:     "custom_value",
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_GET_ENV_2",
			envValue:     "",
			defaultValue: "default",
			expected:     "default",
		},
		{
			name:         "returns empty string as default",
			key:          "TEST_GET_ENV_3",
			envValue:     "",
			defaultValue: "",
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up env before test
			os.Unsetenv(tt.key)

			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			result := getEnv(tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue int
		expected     int
	}{
		{
			name:         "returns parsed int when valid",
			key:          "TEST_GET_ENV_INT_1",
			envValue:     "42",
			defaultValue: 10,
			expected:     42,
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_GET_ENV_INT_2",
			envValue:     "",
			defaultValue: 10,
			expected:     10,
		},
		{
			name:         "returns default when env is invalid int",
			key:          "TEST_GET_ENV_INT_3",
			envValue:     "not_a_number",
			defaultValue: 10,
			expected:     10,
		},
		{
			name:         "handles negative numbers",
			key:          "TEST_GET_ENV_INT_4",
			envValue:     "-5",
			defaultValue: 10,
			expected:     -5,
		},
		{
			name:         "handles zero",
			key:          "TEST_GET_ENV_INT_5",
			envValue:     "0",
			defaultValue: 10,
			expected:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv(tt.key)

			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			result := getEnvInt(tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue bool
		expected     bool
	}{
		{
			name:         "returns true for 'true'",
			key:          "TEST_GET_ENV_BOOL_1",
			envValue:     "true",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns true for '1'",
			key:          "TEST_GET_ENV_BOOL_2",
			envValue:     "1",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns false for 'false'",
			key:          "TEST_GET_ENV_BOOL_3",
			envValue:     "false",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns false for '0'",
			key:          "TEST_GET_ENV_BOOL_4",
			envValue:     "0",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_GET_ENV_BOOL_5",
			envValue:     "",
			defaultValue: true,
			expected:     true,
		},
		{
			name:         "returns default for invalid bool",
			key:          "TEST_GET_ENV_BOOL_6",
			envValue:     "not_a_bool",
			defaultValue: true,
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv(tt.key)

			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			result := getEnvBool(tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEnvDuration(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue time.Duration
		expected     time.Duration
	}{
		{
			name:         "parses seconds",
			key:          "TEST_GET_ENV_DUR_1",
			envValue:     "30s",
			defaultValue: time.Minute,
			expected:     30 * time.Second,
		},
		{
			name:         "parses minutes",
			key:          "TEST_GET_ENV_DUR_2",
			envValue:     "5m",
			defaultValue: time.Minute,
			expected:     5 * time.Minute,
		},
		{
			name:         "parses hours",
			key:          "TEST_GET_ENV_DUR_3",
			envValue:     "2h",
			defaultValue: time.Minute,
			expected:     2 * time.Hour,
		},
		{
			name:         "parses complex duration",
			key:          "TEST_GET_ENV_DUR_4",
			envValue:     "1h30m",
			defaultValue: time.Minute,
			expected:     time.Hour + 30*time.Minute,
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_GET_ENV_DUR_5",
			envValue:     "",
			defaultValue: time.Hour,
			expected:     time.Hour,
		},
		{
			name:         "returns default for invalid duration",
			key:          "TEST_GET_ENV_DUR_6",
			envValue:     "invalid",
			defaultValue: time.Hour,
			expected:     time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv(tt.key)

			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			result := getEnvDuration(tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfigDSN(t *testing.T) {
	tests := []struct {
		name             string
		config           Config
		passwordOverride string
		expected         string
	}{
		{
			name: "generates basic DSN",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "secret",
				Database:       "testdb",
				SSLMode:        "disable",
				ConnectTimeout: 10 * time.Second,
			},
			passwordOverride: "",
			expected:         "host=localhost port=5432 user=postgres password=secret dbname=testdb sslmode=disable connect_timeout=10",
		},
		{
			name: "uses password override when provided",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "original",
				Database:       "testdb",
				SSLMode:        "require",
				ConnectTimeout: 30 * time.Second,
			},
			passwordOverride: "override_pass",
			expected:         "host=localhost port=5432 user=postgres password=override_pass dbname=testdb sslmode=require connect_timeout=30",
		},
		{
			name: "handles different SSL modes",
			config: Config{
				Host:           "db.example.com",
				Port:           5433,
				User:           "admin",
				Password:       "pass123",
				Database:       "production",
				SSLMode:        "verify-full",
				ConnectTimeout: 15 * time.Second,
			},
			passwordOverride: "",
			expected:         "host=db.example.com port=5433 user=admin password=pass123 dbname=production sslmode=verify-full connect_timeout=15",
		},
		{
			name: "handles zero connect timeout",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "pass",
				Database:       "testdb",
				SSLMode:        "disable",
				ConnectTimeout: 0,
			},
			passwordOverride: "",
			expected:         "host=localhost port=5432 user=postgres password=pass dbname=testdb sslmode=disable connect_timeout=0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.DSN(tt.passwordOverride)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config with password",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "secret",
				Database:       "testdb",
				SSLMode:        "disable",
				MaxConnections: 10,
				MinConnections: 2,
			},
			expectError: false,
		},
		{
			name: "valid config with password secret",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				PasswordSecret: "arn:aws:secretsmanager:us-east-1:123456789012:secret:mydb",
				Database:       "testdb",
				SSLMode:        "require",
				MaxConnections: 25,
				MinConnections: 5,
			},
			expectError: false,
		},
		{
			name: "missing host",
			config: Config{
				Host:           "",
				Port:           5432,
				User:           "postgres",
				Password:       "secret",
				Database:       "testdb",
				SSLMode:        "disable",
				MaxConnections: 10,
				MinConnections: 2,
			},
			expectError: true,
			errorMsg:    "DB_HOST is required",
		},
		{
			name: "missing database",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "secret",
				Database:       "",
				SSLMode:        "disable",
				MaxConnections: 10,
				MinConnections: 2,
			},
			expectError: true,
			errorMsg:    "DB_NAME is required",
		},
		{
			name: "missing user",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "",
				Password:       "secret",
				Database:       "testdb",
				SSLMode:        "disable",
				MaxConnections: 10,
				MinConnections: 2,
			},
			expectError: true,
			errorMsg:    "DB_USER is required",
		},
		{
			name: "missing password and password secret",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "",
				PasswordSecret: "",
				Database:       "testdb",
				SSLMode:        "disable",
				MaxConnections: 10,
				MinConnections: 2,
			},
			expectError: true,
			errorMsg:    "either DB_PASSWORD or DB_PASSWORD_SECRET must be set",
		},
		{
			name: "invalid SSL mode",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "secret",
				Database:       "testdb",
				SSLMode:        "invalid",
				MaxConnections: 10,
				MinConnections: 2,
			},
			expectError: true,
			errorMsg:    "invalid DB_SSL_MODE",
		},
		{
			name: "max connections less than 1",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "secret",
				Database:       "testdb",
				SSLMode:        "disable",
				MaxConnections: 0,
				MinConnections: 0,
			},
			expectError: true,
			errorMsg:    "DB_MAX_CONNECTIONS must be at least 1",
		},
		{
			name: "negative min connections",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "secret",
				Database:       "testdb",
				SSLMode:        "disable",
				MaxConnections: 10,
				MinConnections: -1,
			},
			expectError: true,
			errorMsg:    "DB_MIN_CONNECTIONS cannot be negative",
		},
		{
			name: "min connections greater than max",
			config: Config{
				Host:           "localhost",
				Port:           5432,
				User:           "postgres",
				Password:       "secret",
				Database:       "testdb",
				SSLMode:        "disable",
				MaxConnections: 5,
				MinConnections: 10,
			},
			expectError: true,
			errorMsg:    "DB_MIN_CONNECTIONS cannot be greater than DB_MAX_CONNECTIONS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSSLMode(t *testing.T) {
	validModes := []string{"disable", "require", "verify-ca", "verify-full"}

	for _, mode := range validModes {
		t.Run("valid_"+mode, func(t *testing.T) {
			config := Config{
				Host:           "localhost",
				User:           "postgres",
				Password:       "pass",
				Database:       "test",
				SSLMode:        mode,
				MaxConnections: 10,
				MinConnections: 1,
			}
			err := config.validateSSLMode()
			assert.NoError(t, err)
		})
	}

	t.Run("invalid mode", func(t *testing.T) {
		config := Config{SSLMode: "invalid_mode"}
		err := config.validateSSLMode()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid DB_SSL_MODE")
	})
}

func TestValidateSSLModeProductionWarning(t *testing.T) {
	// Save and restore ENVIRONMENT variable
	originalEnv := os.Getenv("ENVIRONMENT")
	defer os.Setenv("ENVIRONMENT", originalEnv)

	t.Run("no warning in non-production", func(t *testing.T) {
		os.Setenv("ENVIRONMENT", "development")
		config := Config{SSLMode: "disable"}
		// Should not error, only warn to stderr
		err := config.validateSSLMode()
		assert.NoError(t, err)
	})

	t.Run("warns in production with ssl disabled", func(t *testing.T) {
		os.Setenv("ENVIRONMENT", "production")
		config := Config{SSLMode: "disable"}
		// Should not error, but would print warning to stderr
		err := config.validateSSLMode()
		assert.NoError(t, err)
	})
}

func TestLoadFromEnv(t *testing.T) {
	// Helper to set up env vars and clean up after
	setEnvVars := func(vars map[string]string) func() {
		originals := make(map[string]string)
		for k := range vars {
			originals[k] = os.Getenv(k)
		}
		for k, v := range vars {
			os.Setenv(k, v)
		}
		return func() {
			for k, v := range originals {
				if v == "" {
					os.Unsetenv(k)
				} else {
					os.Setenv(k, v)
				}
			}
		}
	}

	t.Run("loads defaults", func(t *testing.T) {
		// Set minimal required env vars
		cleanup := setEnvVars(map[string]string{
			"DB_HOST":            "",
			"DB_PORT":            "",
			"DB_NAME":            "",
			"DB_USER":            "",
			"DB_PASSWORD":        "testpass",
			"DB_PASSWORD_SECRET": "",
			"DB_SSL_MODE":        "",
			"DB_MAX_CONNECTIONS": "",
			"DB_MIN_CONNECTIONS": "",
			"DB_LOG_LEVEL":       "",
		})
		defer cleanup()

		config, err := LoadFromEnv()
		require.NoError(t, err)

		assert.Equal(t, "localhost", config.Host)
		assert.Equal(t, 5432, config.Port)
		assert.Equal(t, "cudly", config.Database)
		assert.Equal(t, "cudly", config.User)
		assert.Equal(t, "testpass", config.Password)
		assert.Equal(t, "require", config.SSLMode)
		assert.Equal(t, 25, config.MaxConnections)
		assert.Equal(t, 2, config.MinConnections)
		assert.Equal(t, time.Hour, config.MaxConnLifetime)
		assert.Equal(t, 30*time.Minute, config.MaxConnIdleTime)
		assert.Equal(t, time.Minute, config.HealthCheckPeriod)
		assert.Equal(t, 10*time.Second, config.ConnectTimeout)
		assert.False(t, config.AutoMigrate)
		assert.Equal(t, "info", config.LogLevel)
	})

	t.Run("loads custom values", func(t *testing.T) {
		cleanup := setEnvVars(map[string]string{
			"DB_HOST":                "db.example.com",
			"DB_PORT":                "5433",
			"DB_NAME":                "myapp",
			"DB_USER":                "admin",
			"DB_PASSWORD":            "secret123",
			"DB_SSL_MODE":            "verify-full",
			"DB_MAX_CONNECTIONS":     "50",
			"DB_MIN_CONNECTIONS":     "5",
			"DB_MAX_CONN_LIFETIME":   "2h",
			"DB_MAX_CONN_IDLE_TIME":  "15m",
			"DB_HEALTH_CHECK_PERIOD": "30s",
			"DB_CONNECT_TIMEOUT":     "5s",
			"DB_AUTO_MIGRATE":        "true",
			"DB_MIGRATIONS_PATH":     "/custom/migrations",
			"DB_LOG_LEVEL":           "debug",
		})
		defer cleanup()

		config, err := LoadFromEnv()
		require.NoError(t, err)

		assert.Equal(t, "db.example.com", config.Host)
		assert.Equal(t, 5433, config.Port)
		assert.Equal(t, "myapp", config.Database)
		assert.Equal(t, "admin", config.User)
		assert.Equal(t, "secret123", config.Password)
		assert.Equal(t, "verify-full", config.SSLMode)
		assert.Equal(t, 50, config.MaxConnections)
		assert.Equal(t, 5, config.MinConnections)
		assert.Equal(t, 2*time.Hour, config.MaxConnLifetime)
		assert.Equal(t, 15*time.Minute, config.MaxConnIdleTime)
		assert.Equal(t, 30*time.Second, config.HealthCheckPeriod)
		assert.Equal(t, 5*time.Second, config.ConnectTimeout)
		assert.True(t, config.AutoMigrate)
		assert.Equal(t, "/custom/migrations", config.MigrationsPath)
		assert.Equal(t, "debug", config.LogLevel)
	})

	t.Run("returns error for invalid config", func(t *testing.T) {
		cleanup := setEnvVars(map[string]string{
			"DB_HOST":            "",
			"DB_PASSWORD":        "",
			"DB_PASSWORD_SECRET": "",
		})
		defer cleanup()

		_, err := LoadFromEnv()
		require.Error(t, err)
	})
}

func TestConfigStruct(t *testing.T) {
	t.Run("default values are zero", func(t *testing.T) {
		config := Config{}

		assert.Empty(t, config.Host)
		assert.Zero(t, config.Port)
		assert.Empty(t, config.Database)
		assert.Empty(t, config.User)
		assert.Empty(t, config.Password)
		assert.Empty(t, config.PasswordSecret)
		assert.Empty(t, config.SSLMode)
		assert.Zero(t, config.MaxConnections)
		assert.Zero(t, config.MinConnections)
		assert.Zero(t, config.MaxConnLifetime)
		assert.Zero(t, config.MaxConnIdleTime)
		assert.Zero(t, config.HealthCheckPeriod)
		assert.Zero(t, config.ConnectTimeout)
		assert.False(t, config.AutoMigrate)
		assert.Empty(t, config.MigrationsPath)
		assert.Empty(t, config.LogLevel)
	})
}

func TestValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "all required fields present",
			config: Config{
				Host:     "localhost",
				Database: "testdb",
				User:     "user",
				Password: "pass",
			},
			expectError: false,
		},
		{
			name: "password secret instead of password",
			config: Config{
				Host:           "localhost",
				Database:       "testdb",
				User:           "user",
				PasswordSecret: "secret-arn",
			},
			expectError: false,
		},
		{
			name: "empty host",
			config: Config{
				Host:     "",
				Database: "testdb",
				User:     "user",
				Password: "pass",
			},
			expectError: true,
			errorMsg:    "DB_HOST is required",
		},
		{
			name: "empty database",
			config: Config{
				Host:     "localhost",
				Database: "",
				User:     "user",
				Password: "pass",
			},
			expectError: true,
			errorMsg:    "DB_NAME is required",
		},
		{
			name: "empty user",
			config: Config{
				Host:     "localhost",
				Database: "testdb",
				User:     "",
				Password: "pass",
			},
			expectError: true,
			errorMsg:    "DB_USER is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validateRequiredFields()
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePoolSettings(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid pool settings",
			config: Config{
				MaxConnections: 25,
				MinConnections: 5,
			},
			expectError: false,
		},
		{
			name: "min equals max",
			config: Config{
				MaxConnections: 10,
				MinConnections: 10,
			},
			expectError: false,
		},
		{
			name: "min zero is valid",
			config: Config{
				MaxConnections: 10,
				MinConnections: 0,
			},
			expectError: false,
		},
		{
			name: "max connections zero",
			config: Config{
				MaxConnections: 0,
				MinConnections: 0,
			},
			expectError: true,
			errorMsg:    "DB_MAX_CONNECTIONS must be at least 1",
		},
		{
			name: "negative min connections",
			config: Config{
				MaxConnections: 10,
				MinConnections: -5,
			},
			expectError: true,
			errorMsg:    "DB_MIN_CONNECTIONS cannot be negative",
		},
		{
			name: "min greater than max",
			config: Config{
				MaxConnections: 5,
				MinConnections: 10,
			},
			expectError: true,
			errorMsg:    "DB_MIN_CONNECTIONS cannot be greater than DB_MAX_CONNECTIONS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validatePoolSettings()
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
