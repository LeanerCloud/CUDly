package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"
)

// GCPResolver implements Resolver for GCP Secret Manager
type GCPResolver struct {
	client    *secretmanager.Client
	projectID string
}

// NewGCPResolver creates a new GCP Secret Manager resolver
func NewGCPResolver(ctx context.Context, projectID string) (*GCPResolver, error) {
	// Create Secret Manager client
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP Secret Manager client: %w", err)
	}

	return &GCPResolver{
		client:    client,
		projectID: projectID,
	}, nil
}

// GetSecret retrieves a secret from GCP Secret Manager
func (r *GCPResolver) GetSecret(ctx context.Context, secretID string) (string, error) {
	// Build the resource name for the latest version
	name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", r.projectID, secretID)

	// Access the secret version
	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: name,
	}

	result, err := r.client.AccessSecretVersion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to access secret %s: %w", secretID, err)
	}

	if result.Payload == nil {
		return "", fmt.Errorf("secret %s returned nil payload", secretID)
	}

	return string(result.Payload.Data), nil
}

// PutSecret adds a new version to an existing secret in GCP Secret Manager.
// Note: unlike AWS, GCP requires the secret resource to already exist — this
// function only appends a new version. It will return an error if the secret
// has not been pre-created via CreateSecret.
func (r *GCPResolver) PutSecret(ctx context.Context, secretID string, value string) error {
	parent := fmt.Sprintf("projects/%s/secrets/%s", r.projectID, secretID)

	req := &secretmanagerpb.AddSecretVersionRequest{
		Parent: parent,
		Payload: &secretmanagerpb.SecretPayload{
			Data: []byte(value),
		},
	}

	_, err := r.client.AddSecretVersion(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to add secret version for %s: %w", secretID, err)
	}

	return nil
}

// GetSecretJSON retrieves and parses a JSON secret
func (r *GCPResolver) GetSecretJSON(ctx context.Context, secretID string) (map[string]any, error) {
	secretString, err := r.GetSecret(ctx, secretID)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(secretString), &result); err != nil {
		return nil, fmt.Errorf("failed to parse secret as JSON: %w", err)
	}

	return result, nil
}

// ListSecrets lists secrets in GCP Secret Manager
func (r *GCPResolver) ListSecrets(ctx context.Context, filter string) ([]string, error) {
	req := &secretmanagerpb.ListSecretsRequest{
		Parent: fmt.Sprintf("projects/%s", r.projectID),
		Filter: filter,
	}

	it := r.client.ListSecrets(ctx, req)
	secrets := make([]string, 0)

	for {
		secret, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list secrets: %w", err)
		}

		// Extract the short name from the full resource path.
		// Format: projects/{project}/secrets/{secret}
		secrets = append(secrets, path.Base(secret.Name))
	}

	return secrets, nil
}

// Close cleans up resources
func (r *GCPResolver) Close() error {
	return r.client.Close()
}
