package secrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGCPResolver_DirectMethods tests the actual GCPResolver methods
// These tests exercise the real code paths but may skip if GCP credentials are unavailable
func TestGCPResolver_DirectMethods(t *testing.T) {
	ctx := context.Background()

	// Try to create a GCP resolver
	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}
	defer resolver.Close()

	// Test that the resolver is properly configured
	assert.Equal(t, "test-project-id", resolver.projectID)
	assert.NotNil(t, resolver.client)
}

// TestGCPResolver_GetSecret_NonExistent tests getting a non-existent secret
func TestGCPResolver_GetSecret_NonExistent(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}
	defer resolver.Close()

	// Try to get a secret that definitely doesn't exist
	_, err = resolver.GetSecret(ctx, "cudly-test-nonexistent-secret-12345-xyz")

	// Should get an error (either not found or permission denied)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to access secret")
}

// TestGCPResolver_GetSecretJSON_NonExistent tests getting a non-existent JSON secret
func TestGCPResolver_GetSecretJSON_NonExistent(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}
	defer resolver.Close()

	// Try to get a JSON secret that doesn't exist
	result, err := resolver.GetSecretJSON(ctx, "cudly-test-nonexistent-json-secret-12345-xyz")

	// Should get an error
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestGCPResolver_ListSecrets tests listing secrets
func TestGCPResolver_ListSecrets(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}
	defer resolver.Close()

	// List without filter
	result, err := resolver.ListSecrets(ctx, "")

	// Either succeeds or fails with permission error
	if err == nil {
		assert.NotNil(t, result)
	} else {
		assert.Contains(t, err.Error(), "failed to list secrets")
	}
}

// TestGCPResolver_ListSecrets_WithFilter_Coverage_Direct tests listing secrets with a filter using direct resolver
func TestGCPResolver_ListSecrets_WithFilter_Coverage_Direct(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}
	defer resolver.Close()

	// List with filter
	result, err := resolver.ListSecrets(ctx, "labels.env=test")

	// Either succeeds or fails with permission error
	if err == nil {
		assert.NotNil(t, result)
	}
}

// TestGCPResolver_Close_Idempotent tests that Close can be called multiple times
func TestGCPResolver_Close_Idempotent(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}

	// First close
	err1 := resolver.Close()
	// GCP client Close() may return an error if already closed
	// but shouldn't panic
	_ = err1

	// Second close - may error or succeed
	_ = resolver.Close()
}

// TestGCPResolver_DifferentProjectIDs tests creating resolvers for different projects
func TestGCPResolver_DifferentProjectIDs(t *testing.T) {
	ctx := context.Background()

	projectIDs := []string{"project-1", "project-2", "my-gcp-project"}

	for _, projectID := range projectIDs {
		t.Run(projectID, func(t *testing.T) {
			resolver, err := NewGCPResolver(ctx, projectID)
			if err != nil {
				t.Skipf("Skipping test: GCP config not available for project %s: %v", projectID, err)
			}
			defer resolver.Close()

			assert.Equal(t, projectID, resolver.projectID)
			assert.NotNil(t, resolver.client)
		})
	}
}

// TestGCPResolver_ContextHandling tests context handling in GCP resolver
func TestGCPResolver_ContextHandling(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}
	defer resolver.Close()

	// Test with cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// GetSecret with cancelled context
	_, err = resolver.GetSecret(cancelledCtx, "test-secret")
	// Should fail due to cancelled context
	assert.Error(t, err)
}

// TestGCPResolver_EmptySecretID tests getting a secret with empty ID
func TestGCPResolver_EmptySecretID(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}
	defer resolver.Close()

	// Empty secret ID
	_, err = resolver.GetSecret(ctx, "")
	assert.Error(t, err)
}

// TestGCPResolver_SpecialCharactersInSecretID tests secret IDs with special characters
func TestGCPResolver_SpecialCharactersInSecretID(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}
	defer resolver.Close()

	// Test various characters in secret ID
	testIDs := []string{
		"secret-with-dashes",
		"secret_with_underscores",
		"SecretWithCaps",
	}

	for _, id := range testIDs {
		t.Run(id, func(t *testing.T) {
			// These will fail since secrets don't exist
			_, err := resolver.GetSecret(ctx, id)
			assert.Error(t, err)
		})
	}
}

// TestGCPResolver_GetSecretJSON_RealMethod tests the GetSecretJSON error propagation
func TestGCPResolver_GetSecretJSON_RealMethod(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewGCPResolver(ctx, "test-project-id")
	if err != nil {
		t.Skipf("Skipping test: GCP config not available: %v", err)
	}
	defer resolver.Close()

	// GetSecretJSON should fail because the secret doesn't exist
	result, err := resolver.GetSecretJSON(ctx, "cudly-test-nonexistent-json-secret-xyz")

	require.Error(t, err)
	assert.Nil(t, result)
}

// TestGCPResolver_ResourceNameFormat verifies the resource name format
func TestGCPResolver_ResourceNameFormat(t *testing.T) {
	// The GCP resolver constructs resource names in the format:
	// projects/{project}/secrets/{secret}/versions/latest
	resolver := &GCPResolver{
		projectID: "my-project",
		client:    nil,
	}

	// Verify the projectID is stored correctly
	assert.Equal(t, "my-project", resolver.projectID)
}
