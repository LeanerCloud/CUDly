package secrets

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEnvResolver(t *testing.T) {
	resolver := NewEnvResolver()
	assert.NotNil(t, resolver)
	assert.IsType(t, &EnvResolver{}, resolver)
}

func TestEnvResolver_GetSecret(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	tests := []struct {
		name        string
		secretID    string
		envValue    string
		setEnv      bool
		wantErr     bool
		errContains string
	}{
		{
			name:     "successfully retrieves existing env var",
			secretID: "TEST_SECRET_VALUE",
			envValue: "my-secret-value",
			setEnv:   true,
			wantErr:  false,
		},
		{
			name:        "returns error for non-existent env var",
			secretID:    "NON_EXISTENT_SECRET_VAR_12345",
			setEnv:      false,
			wantErr:     true,
			errContains: "not found or empty",
		},
		{
			name:        "returns error for empty env var",
			secretID:    "EMPTY_SECRET_VAR",
			envValue:    "",
			setEnv:      true,
			wantErr:     true,
			errContains: "not found or empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.setEnv {
				os.Setenv(tt.secretID, tt.envValue)
				defer os.Unsetenv(tt.secretID)
			} else {
				os.Unsetenv(tt.secretID)
			}

			// Execute
			result, err := resolver.GetSecret(ctx, tt.secretID)

			// Assert
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Empty(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.envValue, result)
			}
		})
	}
}

func TestEnvResolver_GetSecretJSON(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	tests := []struct {
		name        string
		secretID    string
		envValue    string
		setEnv      bool
		wantKeys    []string
		wantErr     bool
		errContains string
	}{
		{
			name:     "successfully parses valid JSON",
			secretID: "JSON_SECRET",
			envValue: `{"username":"admin","password":"secret123"}`,
			setEnv:   true,
			wantKeys: []string{"username", "password"},
			wantErr:  false,
		},
		{
			name:     "successfully parses nested JSON",
			secretID: "NESTED_JSON_SECRET",
			envValue: `{"database":{"host":"localhost","port":5432},"credentials":{"user":"test"}}`,
			setEnv:   true,
			wantKeys: []string{"database", "credentials"},
			wantErr:  false,
		},
		{
			name:        "returns error for invalid JSON",
			secretID:    "INVALID_JSON_SECRET",
			envValue:    "not-valid-json",
			setEnv:      true,
			wantErr:     true,
			errContains: "failed to parse environment variable as JSON",
		},
		{
			name:        "returns error for non-existent env var",
			secretID:    "NON_EXISTENT_JSON_VAR",
			setEnv:      false,
			wantErr:     true,
			errContains: "not found or empty",
		},
		{
			name:        "returns error for JSON array (not object)",
			secretID:    "JSON_ARRAY_SECRET",
			envValue:    `["item1", "item2"]`,
			setEnv:      true,
			wantErr:     true,
			errContains: "failed to parse environment variable as JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.setEnv {
				os.Setenv(tt.secretID, tt.envValue)
				defer os.Unsetenv(tt.secretID)
			} else {
				os.Unsetenv(tt.secretID)
			}

			// Execute
			result, err := resolver.GetSecretJSON(ctx, tt.secretID)

			// Assert
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
				for _, key := range tt.wantKeys {
					assert.Contains(t, result, key)
				}
			}
		})
	}
}

func TestEnvResolver_ListSecrets(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	// Setup test environment variables with a unique prefix
	testPrefix := "CUDLY_TEST_SECRET_"
	testVars := map[string]string{
		testPrefix + "ONE":   "value1",
		testPrefix + "TWO":   "value2",
		testPrefix + "THREE": "value3",
	}

	// Set up test env vars
	for key, value := range testVars {
		os.Setenv(key, value)
	}
	defer func() {
		for key := range testVars {
			os.Unsetenv(key)
		}
	}()

	tests := []struct {
		name            string
		filter          string
		expectMinCount  int
		expectContains  []string
		expectNotContain []string
	}{
		{
			name:           "lists all env vars without filter",
			filter:         "",
			expectMinCount: 3, // At least our test vars
		},
		{
			name:           "filters by prefix",
			filter:         testPrefix,
			expectMinCount: 3,
			expectContains: []string{testPrefix + "ONE", testPrefix + "TWO", testPrefix + "THREE"},
		},
		{
			name:             "filter with no matches returns empty",
			filter:           "NONEXISTENT_PREFIX_XYZ_12345_",
			expectMinCount:   0,
			expectNotContain: []string{testPrefix + "ONE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := resolver.ListSecrets(ctx, tt.filter)

			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(result), tt.expectMinCount)

			for _, expected := range tt.expectContains {
				assert.Contains(t, result, expected)
			}

			for _, notExpected := range tt.expectNotContain {
				assert.NotContains(t, result, notExpected)
			}
		})
	}
}

func TestEnvResolver_Close(t *testing.T) {
	resolver := NewEnvResolver()

	err := resolver.Close()

	assert.NoError(t, err)
}

func TestEnvResolver_ImplementsResolverInterface(t *testing.T) {
	// Verify EnvResolver implements the Resolver interface
	var _ Resolver = (*EnvResolver)(nil)
}
