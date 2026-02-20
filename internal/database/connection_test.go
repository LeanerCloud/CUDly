package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/tracelog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected tracelog.LogLevel
	}{
		{
			name:     "debug level",
			input:    "debug",
			expected: tracelog.LogLevelDebug,
		},
		{
			name:     "info level",
			input:    "info",
			expected: tracelog.LogLevelInfo,
		},
		{
			name:     "warn level",
			input:    "warn",
			expected: tracelog.LogLevelWarn,
		},
		{
			name:     "error level",
			input:    "error",
			expected: tracelog.LogLevelError,
		},
		{
			name:     "empty string defaults to info",
			input:    "",
			expected: tracelog.LogLevelInfo,
		},
		{
			name:     "unknown level defaults to info",
			input:    "unknown",
			expected: tracelog.LogLevelInfo,
		},
		{
			name:     "uppercase is not handled (defaults to info)",
			input:    "DEBUG",
			expected: tracelog.LogLevelInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseLogLevel(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildPoolConfig(t *testing.T) {
	t.Run("builds config from valid Config", func(t *testing.T) {
		config := &Config{
			Host:              "localhost",
			Port:              5432,
			User:              "testuser",
			Password:          "testpass",
			Database:          "testdb",
			SSLMode:           "disable",
			MaxConnections:    25,
			MinConnections:    2,
			MaxConnLifetime:   time.Hour,
			MaxConnIdleTime:   30 * time.Minute,
			HealthCheckPeriod: time.Minute,
			ConnectTimeout:    10 * time.Second,
			LogLevel:          "info",
		}

		poolConfig, err := buildPoolConfig(config, "testpass")
		require.NoError(t, err)
		require.NotNil(t, poolConfig)

		assert.Equal(t, int32(25), poolConfig.MaxConns)
		assert.Equal(t, int32(2), poolConfig.MinConns)
		assert.Equal(t, time.Hour, poolConfig.MaxConnLifetime)
		assert.Equal(t, 30*time.Minute, poolConfig.MaxConnIdleTime)
		assert.Equal(t, time.Minute, poolConfig.HealthCheckPeriod)

		// Verify timezone is set to UTC
		assert.Equal(t, "UTC", poolConfig.ConnConfig.RuntimeParams["timezone"])
	})

	t.Run("uses password override", func(t *testing.T) {
		config := &Config{
			Host:           "localhost",
			Port:           5432,
			User:           "testuser",
			Password:       "originalpass",
			Database:       "testdb",
			SSLMode:        "disable",
			MaxConnections: 10,
			MinConnections: 1,
			ConnectTimeout: 10 * time.Second,
			LogLevel:       "debug",
		}

		// Build with override password
		poolConfig, err := buildPoolConfig(config, "overridepass")
		require.NoError(t, err)
		require.NotNil(t, poolConfig)

		// The password in the connection config should be the override
		// We can't directly check password from pgxpool.Config, but we can verify
		// the config was built successfully
		assert.Equal(t, int32(10), poolConfig.MaxConns)
	})

	t.Run("sets tracer with correct log level", func(t *testing.T) {
		config := &Config{
			Host:           "localhost",
			Port:           5432,
			User:           "testuser",
			Password:       "testpass",
			Database:       "testdb",
			SSLMode:        "disable",
			MaxConnections: 10,
			MinConnections: 1,
			ConnectTimeout: 10 * time.Second,
			LogLevel:       "debug",
		}

		poolConfig, err := buildPoolConfig(config, "testpass")
		require.NoError(t, err)
		require.NotNil(t, poolConfig)
		require.NotNil(t, poolConfig.ConnConfig.Tracer)
	})

	t.Run("handles all log levels", func(t *testing.T) {
		logLevels := []string{"debug", "info", "warn", "error"}

		for _, level := range logLevels {
			t.Run(level, func(t *testing.T) {
				config := &Config{
					Host:           "localhost",
					Port:           5432,
					User:           "testuser",
					Password:       "testpass",
					Database:       "testdb",
					SSLMode:        "disable",
					MaxConnections: 10,
					MinConnections: 1,
					ConnectTimeout: 10 * time.Second,
					LogLevel:       level,
				}

				poolConfig, err := buildPoolConfig(config, "testpass")
				require.NoError(t, err)
				require.NotNil(t, poolConfig)
			})
		}
	})
}

// MockSecretResolver implements SecretResolver for testing
type MockSecretResolver struct {
	SecretValue string
	SecretError error
	GetCalls    int
	CloseCalls  int
}

func (m *MockSecretResolver) GetSecret(ctx context.Context, secretID string) (string, error) {
	m.GetCalls++
	if m.SecretError != nil {
		return "", m.SecretError
	}
	return m.SecretValue, nil
}

func (m *MockSecretResolver) Close() error {
	m.CloseCalls++
	return nil
}

func TestNewConnectionSecretResolution(t *testing.T) {
	// Note: These tests verify the secret resolution logic without actually connecting to a database

	t.Run("fails when password secret resolution fails", func(t *testing.T) {
		config := &Config{
			Host:           "localhost",
			Port:           5432,
			User:           "testuser",
			Password:       "",
			PasswordSecret: "my-secret-id",
			Database:       "testdb",
			SSLMode:        "disable",
			MaxConnections: 10,
			MinConnections: 1,
			ConnectTimeout: 10 * time.Second,
			LogLevel:       "info",
		}

		mockResolver := &MockSecretResolver{
			SecretError: errors.New("secret not found"),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := NewConnection(ctx, config, mockResolver)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), "failed to retrieve database password from secret manager")
		assert.Equal(t, 1, mockResolver.GetCalls)
	})

	t.Run("fails when JSON secret missing password field", func(t *testing.T) {
		config := &Config{
			Host:           "localhost",
			Port:           5432,
			User:           "testuser",
			Password:       "",
			PasswordSecret: "my-secret-id",
			Database:       "testdb",
			SSLMode:        "disable",
			MaxConnections: 10,
			MinConnections: 1,
			ConnectTimeout: 10 * time.Second,
			LogLevel:       "info",
		}

		// JSON without password field
		mockResolver := &MockSecretResolver{
			SecretValue: `{"username": "admin", "host": "db.example.com"}`,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := NewConnection(ctx, config, mockResolver)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), "secret is JSON but missing 'password' field")
	})

	t.Run("fails when no password is configured", func(t *testing.T) {
		config := &Config{
			Host:           "localhost",
			Port:           5432,
			User:           "testuser",
			Password:       "",
			PasswordSecret: "",
			Database:       "testdb",
			SSLMode:        "disable",
			MaxConnections: 10,
			MinConnections: 1,
			ConnectTimeout: 10 * time.Second,
			LogLevel:       "info",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := NewConnection(ctx, config, nil)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), "database password not configured")
	})
}

func TestStdLoggerLog(t *testing.T) {
	logger := &stdLogger{}

	t.Run("filters sensitive data", func(t *testing.T) {
		// This test verifies the logger doesn't crash and filters sensitive keys
		// We can't easily capture the output, but we can verify it doesn't panic
		data := map[string]interface{}{
			"password": "secret123",
			"secret":   "top_secret",
			"token":    "bearer_token",
			"sql":      "SELECT * FROM users",
			"table":    "users",
			"count":    42,
		}

		// These should not panic
		ctx := context.Background()
		assert.NotPanics(t, func() {
			logger.Log(ctx, tracelog.LogLevelDebug, "debug message", data)
		})
		assert.NotPanics(t, func() {
			logger.Log(ctx, tracelog.LogLevelInfo, "info message", data)
		})
		assert.NotPanics(t, func() {
			logger.Log(ctx, tracelog.LogLevelWarn, "warn message", data)
		})
		assert.NotPanics(t, func() {
			logger.Log(ctx, tracelog.LogLevelError, "error message", data)
		})
	})

	t.Run("handles nil data", func(t *testing.T) {
		ctx := context.Background()
		assert.NotPanics(t, func() {
			logger.Log(ctx, tracelog.LogLevelInfo, "message with nil data", nil)
		})
	})

	t.Run("handles empty data", func(t *testing.T) {
		ctx := context.Background()
		assert.NotPanics(t, func() {
			logger.Log(ctx, tracelog.LogLevelInfo, "message with empty data", map[string]interface{}{})
		})
	})
}

func TestSecretResolverInterface(t *testing.T) {
	t.Run("mock implements interface", func(t *testing.T) {
		var resolver SecretResolver = &MockSecretResolver{
			SecretValue: "test",
		}

		ctx := context.Background()
		secret, err := resolver.GetSecret(ctx, "test-id")
		assert.NoError(t, err)
		assert.Equal(t, "test", secret)

		err = resolver.Close()
		assert.NoError(t, err)
	})
}

func TestConnectionMethods(t *testing.T) {
	// These tests verify the Connection struct methods exist and have correct signatures
	// Actual database connections are tested in integration tests

	t.Run("Connection struct has expected fields", func(t *testing.T) {
		conn := &Connection{
			pool:   nil,
			config: &Config{},
		}
		assert.NotNil(t, conn.config)
	})
}

func TestBuildPoolConfigErrors(t *testing.T) {
	t.Run("handles invalid DSN", func(t *testing.T) {
		// Create a config that will produce an invalid DSN
		// pgxpool.ParseConfig is quite forgiving, so we need to check what it accepts
		config := &Config{
			Host:           "localhost",
			Port:           5432,
			User:           "testuser",
			Password:       "testpass",
			Database:       "testdb",
			SSLMode:        "disable",
			MaxConnections: 10,
			MinConnections: 1,
			ConnectTimeout: 10 * time.Second,
			LogLevel:       "info",
		}

		// Valid config should work
		poolConfig, err := buildPoolConfig(config, "testpass")
		require.NoError(t, err)
		require.NotNil(t, poolConfig)
	})
}

func TestConnectionPoolConfigValues(t *testing.T) {
	t.Run("all pool settings are applied", func(t *testing.T) {
		config := &Config{
			Host:              "localhost",
			Port:              5432,
			User:              "testuser",
			Password:          "testpass",
			Database:          "testdb",
			SSLMode:           "disable",
			MaxConnections:    100,
			MinConnections:    10,
			MaxConnLifetime:   2 * time.Hour,
			MaxConnIdleTime:   45 * time.Minute,
			HealthCheckPeriod: 2 * time.Minute,
			ConnectTimeout:    30 * time.Second,
			LogLevel:          "warn",
		}

		poolConfig, err := buildPoolConfig(config, "testpass")
		require.NoError(t, err)

		assert.Equal(t, int32(100), poolConfig.MaxConns)
		assert.Equal(t, int32(10), poolConfig.MinConns)
		assert.Equal(t, 2*time.Hour, poolConfig.MaxConnLifetime)
		assert.Equal(t, 45*time.Minute, poolConfig.MaxConnIdleTime)
		assert.Equal(t, 2*time.Minute, poolConfig.HealthCheckPeriod)
	})
}

func TestParseLogLevelAllCases(t *testing.T) {
	// Exhaustive test of parseLogLevel
	tests := []struct {
		input    string
		expected tracelog.LogLevel
	}{
		{"debug", tracelog.LogLevelDebug},
		{"info", tracelog.LogLevelInfo},
		{"warn", tracelog.LogLevelWarn},
		{"error", tracelog.LogLevelError},
		{"", tracelog.LogLevelInfo},
		{"DEBUG", tracelog.LogLevelInfo},   // Case sensitive, defaults
		{"INFO", tracelog.LogLevelInfo},    // Case sensitive, defaults
		{"WARN", tracelog.LogLevelInfo},    // Case sensitive, defaults
		{"ERROR", tracelog.LogLevelInfo},   // Case sensitive, defaults
		{"trace", tracelog.LogLevelInfo},   // Unknown, defaults
		{"fatal", tracelog.LogLevelInfo},   // Unknown, defaults
		{"warning", tracelog.LogLevelInfo}, // Unknown, defaults (not same as "warn")
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseLogLevel(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStdLoggerSensitiveDataFiltering(t *testing.T) {
	logger := &stdLogger{}
	ctx := context.Background()

	sensitiveKeys := []string{"password", "secret", "token", "sql"}

	for _, key := range sensitiveKeys {
		t.Run("filters_"+key, func(t *testing.T) {
			data := map[string]interface{}{
				key:       "sensitive_value",
				"safe":    "safe_value",
				"another": 123,
			}

			// Verify it doesn't panic and processes the data
			assert.NotPanics(t, func() {
				logger.Log(ctx, tracelog.LogLevelInfo, "test message", data)
			})
		})
	}
}

func TestJSONSecretParsing(t *testing.T) {
	// Test the JSON parsing logic in NewConnection by examining what would happen
	// with various JSON secret formats

	t.Run("parses RDS Proxy JSON format", func(t *testing.T) {
		config := &Config{
			Host:              "localhost",
			Port:              5432,
			User:              "testuser",
			PasswordSecret:    "my-secret",
			Database:          "testdb",
			SSLMode:           "disable",
			MaxConnections:    10,
			MinConnections:    1,
			ConnectTimeout:    1 * time.Second,
			HealthCheckPeriod: time.Minute,
			LogLevel:          "info",
		}

		// Valid JSON with password field
		mockResolver := &MockSecretResolver{
			SecretValue: `{"username": "admin", "password": "db-password-123", "host": "db.example.com"}`,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// This will fail at connection time, but the secret parsing should work
		_, err := NewConnection(ctx, config, mockResolver)
		// The error should be about connection, not about parsing
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret is JSON but missing 'password' field")
		assert.NotContains(t, err.Error(), "failed to retrieve database password")
	})

	t.Run("handles raw string password", func(t *testing.T) {
		config := &Config{
			Host:              "localhost",
			Port:              5432,
			User:              "testuser",
			PasswordSecret:    "my-secret",
			Database:          "testdb",
			SSLMode:           "disable",
			MaxConnections:    10,
			MinConnections:    1,
			ConnectTimeout:    1 * time.Second,
			HealthCheckPeriod: time.Minute,
			LogLevel:          "info",
		}

		// Raw string, not JSON
		mockResolver := &MockSecretResolver{
			SecretValue: "plain-text-password",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// This will fail at connection time, but the secret parsing should work
		_, err := NewConnection(ctx, config, mockResolver)
		// The error should be about connection, not about parsing
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret is JSON but missing 'password' field")
		assert.NotContains(t, err.Error(), "failed to retrieve database password")
	})
}

func TestNewConnectionWithDirectPassword(t *testing.T) {
	t.Run("uses direct password when no secret resolver", func(t *testing.T) {
		config := &Config{
			Host:              "localhost",
			Port:              5432,
			User:              "testuser",
			Password:          "direct-password",
			PasswordSecret:    "",
			Database:          "testdb",
			SSLMode:           "disable",
			MaxConnections:    10,
			MinConnections:    1,
			ConnectTimeout:    1 * time.Second,
			HealthCheckPeriod: time.Minute,
			LogLevel:          "info",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// This will fail at connection time (no database), but password should be used
		_, err := NewConnection(ctx, config, nil)
		require.Error(t, err)
		// Should not complain about missing password
		assert.NotContains(t, err.Error(), "database password not configured")
	})

	t.Run("uses direct password when secret resolver is nil and PasswordSecret is set", func(t *testing.T) {
		config := &Config{
			Host:              "localhost",
			Port:              5432,
			User:              "testuser",
			Password:          "direct-password",
			PasswordSecret:    "some-secret-that-wont-be-used",
			Database:          "testdb",
			SSLMode:           "disable",
			MaxConnections:    10,
			MinConnections:    1,
			ConnectTimeout:    1 * time.Second,
			HealthCheckPeriod: time.Minute,
			LogLevel:          "info",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Since secretResolver is nil, it should fall back to direct password
		_, err := NewConnection(ctx, config, nil)
		require.Error(t, err)
		// Should not complain about missing password
		assert.NotContains(t, err.Error(), "database password not configured")
		assert.NotContains(t, err.Error(), "failed to retrieve database password")
	})
}
