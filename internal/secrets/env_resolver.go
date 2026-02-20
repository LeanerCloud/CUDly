package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// EnvResolver implements Resolver using environment variables
// This is useful for local development where secrets are stored as env vars
type EnvResolver struct{}

// NewEnvResolver creates a new environment variable resolver
func NewEnvResolver() *EnvResolver {
	return &EnvResolver{}
}

// GetSecret retrieves a secret from environment variables
// secretID is the environment variable name
func (r *EnvResolver) GetSecret(ctx context.Context, secretID string) (string, error) {
	value := os.Getenv(secretID)
	if value == "" {
		return "", fmt.Errorf("environment variable %s not found or empty", secretID)
	}
	return value, nil
}

// GetSecretJSON retrieves and parses a JSON secret from environment variable
func (r *EnvResolver) GetSecretJSON(ctx context.Context, secretID string) (map[string]any, error) {
	secretString, err := r.GetSecret(ctx, secretID)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(secretString), &result); err != nil {
		return nil, fmt.Errorf("failed to parse environment variable as JSON: %w", err)
	}

	return result, nil
}

// ListSecrets lists all environment variables matching the filter (prefix)
func (r *EnvResolver) ListSecrets(ctx context.Context, filter string) ([]string, error) {
	secrets := make([]string, 0)

	// Get all environment variables
	for _, env := range os.Environ() {
		// Split into key=value
		pair := strings.SplitN(env, "=", 2)
		if len(pair) != 2 {
			continue
		}

		key := pair[0]

		// Apply filter if provided (prefix match)
		if filter == "" || strings.HasPrefix(key, filter) {
			secrets = append(secrets, key)
		}
	}

	return secrets, nil
}

// Close cleans up resources (no-op for environment variables)
func (r *EnvResolver) Close() error {
	return nil
}
