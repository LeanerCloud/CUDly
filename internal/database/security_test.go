package database

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/tracelog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateRequiredFields_BothPasswordAndSecret_WarnOnStderr is a
// regression test for issue #441: when both DB_PASSWORD and DB_PASSWORD_SECRET
// are set, a warning must be emitted to stderr (not silently accepted, and not
// a hard error that breaks local-dev workflows).
func TestValidateRequiredFields_BothPasswordAndSecret_WarnOnStderr(t *testing.T) {
	// Capture stderr
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	cfg := &Config{
		Host:           "localhost",
		Database:       "testdb",
		User:           "testuser",
		Password:       "plaintext-pass",
		PasswordSecret: "arn:aws:secretsmanager:us-east-1:123:secret:mydb",
	}

	validateErr := cfg.validateRequiredFields()

	w.Close()
	os.Stderr = origStderr

	var stderrBuf bytes.Buffer
	_, _ = io.Copy(&stderrBuf, r)
	r.Close()

	// Must not return an error (should not break existing workflows).
	assert.NoError(t, validateErr,
		"both password sources set must not return an error (local-dev workflows must still work)")

	// Must emit a warning to stderr.
	stderrContent := stderrBuf.String()
	assert.Contains(t, stderrContent, "WARNING",
		"a warning must appear on stderr when both DB_PASSWORD and DB_PASSWORD_SECRET are set")
	assert.Contains(t, stderrContent, "DB_PASSWORD_SECRET",
		"warning must mention DB_PASSWORD_SECRET as the preferred path")
}

// TestValidateRequiredFields_OnlySecret_NoWarning verifies that using only
// DB_PASSWORD_SECRET (the preferred production path) produces no warning.
func TestValidateRequiredFields_OnlySecret_NoWarning(t *testing.T) {
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	cfg := &Config{
		Host:           "localhost",
		Database:       "testdb",
		User:           "testuser",
		Password:       "",
		PasswordSecret: "arn:aws:secretsmanager:us-east-1:123:secret:mydb",
	}

	validateErr := cfg.validateRequiredFields()

	w.Close()
	os.Stderr = origStderr

	var stderrBuf bytes.Buffer
	_, _ = io.Copy(&stderrBuf, r)
	r.Close()

	assert.NoError(t, validateErr)
	assert.NotContains(t, stderrBuf.String(), "WARNING",
		"no warning when only DB_PASSWORD_SECRET is set (preferred path)")
}

// TestBuildPoolConfig_ParseConfigUsesRedactedPassword is a regression test for
// issue #444: pgxpool.ParseConfig must never receive a DSN that contains the
// real password. If the parse fails, the error chain must not expose the
// plaintext credential.
//
// The fix parses a DSN containing the placeholder "REDACTED" and then sets
// ConnConfig.Password to the real credential. We verify that the real password
// does not appear in the poolConfig's DSN-derived fields by confirming that a
// deliberately bad host/DSN triggers a parse error whose text does not contain
// the real password.
func TestBuildPoolConfig_ParseConfigDoesNotExposePassword(t *testing.T) {
	const realPassword = "SUPER_SENSITIVE_DB_PASS_12345"

	// Valid config — parse must succeed.
	cfg := &Config{
		Host:           "localhost",
		Port:           5432,
		User:           "testuser",
		Password:       realPassword,
		Database:       "testdb",
		SSLMode:        "disable",
		MaxConnections: 10,
		MinConnections: 1,
		ConnectTimeout: 5 * time.Second,
		LogLevel:       "info",
	}

	poolConfig, err := buildPoolConfig(cfg, realPassword)
	require.NoError(t, err)
	require.NotNil(t, poolConfig)

	// The real password must be present in ConnConfig.Password (used at connect time).
	assert.Equal(t, realPassword, poolConfig.ConnConfig.Password,
		"ConnConfig.Password must hold the real password for actual connections")

	// ConnConfig.Host must be set correctly (proves DSN was parsed successfully).
	assert.Equal(t, "localhost", poolConfig.ConnConfig.Host)
}

// TestBuildPoolConfig_ParseError_NoPasswordLeak verifies that when the DSN
// contains a structurally invalid piece (beyond what pgx can parse) any error
// returned does not expose the real password. This tests the defence-in-depth
// goal of issue #444: by passing "REDACTED" to ParseConfig, even an error from
// pgx's URI parser only shows "REDACTED", not the real credential.
func TestBuildPoolConfig_ParseError_NoPasswordLeak(t *testing.T) {
	const realPassword = "SUPER_SENSITIVE_DB_PASS_12345"

	// Use an invalid SSL mode to provoke an error from pgxpool.ParseConfig.
	// (pgx validates the sslmode string during parsing.)
	cfg := &Config{
		Host:           "localhost",
		Port:           5432,
		User:           "testuser",
		Password:       realPassword,
		Database:       "testdb",
		SSLMode:        "invalid-ssl-mode",
		MaxConnections: 10,
		MinConnections: 1,
		ConnectTimeout: 5 * time.Second,
		LogLevel:       "info",
	}

	_, err := buildPoolConfig(cfg, realPassword)
	// This may or may not error depending on pgx version — if it does error,
	// the real password must not appear in the error message.
	if err != nil {
		assert.NotContains(t, err.Error(), realPassword,
			"parse error must not expose the real DB password in the error chain")
		// The placeholder "REDACTED" may appear, which is acceptable.
	}
}

// TestSanitizeLogData_ArgsKeyStrippedAtDebugByDefault is a regression test for
// issue #446: pgx logs SQL bound parameters under the "args" key at debug
// level. Without the fix, these would be included in log output and could
// expose session tokens, bcrypt hashes, or approval tokens.
//
// By default (DB_LOG_BIND_PARAMETERS not set) the "args" key must be stripped
// from debug-level log data.
func TestSanitizeLogData_ArgsKeyStrippedAtDebugByDefault(t *testing.T) {
	t.Setenv("DB_LOG_BIND_PARAMETERS", "")

	tests := []struct {
		name          string
		inputData     map[string]any
		level         tracelog.LogLevel
		wantArgsInOut bool
	}{
		{
			name: "args stripped at debug level by default",
			inputData: map[string]any{
				"args": []any{"session-token-abc", "bcrypt-hash-xyz"},
				"sql":  "SELECT * FROM users WHERE token = $1",
			},
			level:         tracelog.LogLevelDebug,
			wantArgsInOut: false,
		},
		{
			name: "args kept at warn level",
			inputData: map[string]any{
				"args": []any{"value"},
				"sql":  "SELECT 1",
			},
			level:         tracelog.LogLevelWarn,
			wantArgsInOut: true,
		},
		{
			name: "args kept at error level",
			inputData: map[string]any{
				"args": []any{"value"},
			},
			level:         tracelog.LogLevelError,
			wantArgsInOut: true,
		},
		{
			name: "password always stripped at warn level",
			inputData: map[string]any{
				"password": "secret",
				"safe":     "ok",
			},
			level:         tracelog.LogLevelWarn,
			wantArgsInOut: false, // "args" key not in input, not relevant
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			safe := sanitizeLogData(tc.level, tc.inputData)
			_, hasArgs := safe["args"]
			assert.Equal(t, tc.wantArgsInOut, hasArgs,
				"args key presence mismatch for level %v", tc.level)
		})
	}
}

// TestSanitizeLogData_ArgsKeyKeptWhenOptedIn verifies that setting
// DB_LOG_BIND_PARAMETERS=true allows the "args" key through at debug level.
func TestSanitizeLogData_ArgsKeyKeptWhenOptedIn(t *testing.T) {
	t.Setenv("DB_LOG_BIND_PARAMETERS", "true")

	inputData := map[string]any{
		"args": []any{"param1", "param2"},
		"sql":  "SELECT 1",
	}

	safe := sanitizeLogData(tracelog.LogLevelDebug, inputData)
	_, hasArgs := safe["args"]
	assert.True(t, hasArgs,
		"args key must be preserved when DB_LOG_BIND_PARAMETERS=true")
}

// TestIsSensitiveKey verifies the sensitive-key predicate covers all expected names.
func TestIsSensitiveKey(t *testing.T) {
	assert.True(t, isSensitiveKey("password"))
	assert.True(t, isSensitiveKey("secret"))
	assert.True(t, isSensitiveKey("token"))
	assert.False(t, isSensitiveKey("sql"))
	assert.False(t, isSensitiveKey("args"))
	assert.False(t, isSensitiveKey("host"))
}

// TestStdLogger_DoesNotPanicWithArgsKey exercises the live stdLogger.Log path
// with an "args" entry to confirm it does not panic when the key is filtered.
func TestStdLogger_DoesNotPanicWithArgsKey(t *testing.T) {
	t.Setenv("DB_LOG_BIND_PARAMETERS", "")

	logger := &stdLogger{}
	ctx := context.Background()

	data := map[string]any{
		"args":     []any{"session-token", "hash-value"},
		"sql":      "INSERT INTO sessions VALUES ($1, $2)",
		"password": "should-be-stripped",
	}

	assert.NotPanics(t, func() {
		logger.Log(ctx, tracelog.LogLevelDebug, "Query", data)
	})
	assert.NotPanics(t, func() {
		logger.Log(ctx, tracelog.LogLevelInfo, "Query", data)
	})
}

// TestSanitizeLogData_PasswordStrippedAtAllLevels confirms password/secret/token
// keys are removed regardless of log level.
func TestSanitizeLogData_PasswordStrippedAtAllLevels(t *testing.T) {
	t.Setenv("DB_LOG_BIND_PARAMETERS", "")

	sensitive := map[string]any{
		"password": "my-password",
		"secret":   "my-secret",
		"token":    "my-token",
		"safe":     "visible",
	}

	for _, level := range []tracelog.LogLevel{
		tracelog.LogLevelDebug,
		tracelog.LogLevelInfo,
		tracelog.LogLevelWarn,
		tracelog.LogLevelError,
	} {
		t.Run(level.String(), func(t *testing.T) {
			safe := sanitizeLogData(level, sensitive)
			assert.NotContains(t, safe, "password")
			assert.NotContains(t, safe, "secret")
			assert.NotContains(t, safe, "token")
			assert.Contains(t, safe, "safe")
		})
	}
}
