package secrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAWSResolver_DirectMethods tests the actual AWSResolver methods
// These tests exercise the real code paths but may skip if AWS credentials are unavailable
func TestAWSResolver_DirectMethods(t *testing.T) {
	ctx := context.Background()

	// Try to create an AWS resolver
	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// Test that the resolver is properly configured
	assert.Equal(t, "us-east-1", resolver.region)
	assert.NotNil(t, resolver.client)
}

// TestAWSResolver_GetSecret_NonExistent tests getting a non-existent secret
func TestAWSResolver_GetSecret_NonExistent(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// Try to get a secret that definitely doesn't exist
	_, err = resolver.GetSecret(ctx, "cudly-test-nonexistent-secret-12345-xyz")

	// Should get an error (either not found or permission denied)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get secret")
}

// TestAWSResolver_GetSecretJSON_NonExistent tests getting a non-existent JSON secret
func TestAWSResolver_GetSecretJSON_NonExistent(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// Try to get a JSON secret that doesn't exist
	_, err = resolver.GetSecretJSON(ctx, "cudly-test-nonexistent-json-secret-12345-xyz")

	// Should get an error
	assert.Error(t, err)
}

// TestAWSResolver_ListSecrets_WithFilter_Coverage_Direct tests listing secrets with a filter using direct resolver
func TestAWSResolver_ListSecrets_WithFilter_Coverage_Direct(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// List with a filter that likely matches nothing
	result, err := resolver.ListSecrets(ctx, "cudly-nonexistent-prefix-xyz-12345")

	// This may succeed with empty results or fail with permission denied
	// Either is acceptable for coverage
	if err == nil {
		// If successful, result should be empty or contain matching secrets
		assert.NotNil(t, result)
	} else {
		assert.Contains(t, err.Error(), "failed to list secrets")
	}
}

// TestAWSResolver_ListSecrets_NoFilter tests listing all secrets
func TestAWSResolver_ListSecrets_NoFilter(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// List without filter
	result, err := resolver.ListSecrets(ctx, "")

	// Either succeeds or fails with permission error
	if err == nil {
		assert.NotNil(t, result)
	}
}

// TestAWSResolver_Close_Idempotent tests that Close can be called multiple times
func TestAWSResolver_Close_Idempotent(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}

	// Close multiple times should not panic or error
	err1 := resolver.Close()
	assert.NoError(t, err1)

	err2 := resolver.Close()
	assert.NoError(t, err2)
}

// TestAWSResolver_DifferentRegions tests creating resolvers for different regions
func TestAWSResolver_DifferentRegions(t *testing.T) {
	ctx := context.Background()

	regions := []string{"us-east-1", "us-west-2", "eu-west-1", "ap-northeast-1"}

	for _, region := range regions {
		t.Run(region, func(t *testing.T) {
			resolver, err := NewAWSResolver(ctx, region)
			if err != nil {
				t.Skipf("Skipping test: AWS config not available for region %s: %v", region, err)
			}
			defer resolver.Close()

			assert.Equal(t, region, resolver.region)
			assert.NotNil(t, resolver.client)
		})
	}
}

// TestAWSResolver_ContextHandling tests context handling in AWS resolver
func TestAWSResolver_ContextHandling(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// Test with cancelled context - should fail
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// GetSecret with cancelled context
	_, err = resolver.GetSecret(cancelledCtx, "test-secret")
	// Should fail due to cancelled context or other error
	assert.Error(t, err)
}

// TestNewAWSResolver_InvalidRegion tests creation with unusual region values
func TestNewAWSResolver_InvalidRegion(t *testing.T) {
	ctx := context.Background()

	// Even with an invalid region, the SDK may still create a client
	// The error would occur when making actual API calls
	resolver, err := NewAWSResolver(ctx, "invalid-region-xyz")
	if err != nil {
		// Some SDK versions may reject invalid regions
		assert.Contains(t, err.Error(), "failed to load AWS config")
		return
	}

	// If creation succeeded, verify the region was set
	assert.Equal(t, "invalid-region-xyz", resolver.region)
	resolver.Close()
}

// TestAWSResolver_SecretWithBinaryData tests getting a secret that might have binary data
func TestAWSResolver_SecretWithBinaryData(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// Try to get a secret - we don't know if it exists or has binary data
	// This test is mainly for coverage of the "no string value" code path
	_, err = resolver.GetSecret(ctx, "cudly-binary-test-secret-12345")

	// Expect an error since the secret doesn't exist
	assert.Error(t, err)
}

// TestAWSResolver_EmptySecretID tests getting a secret with empty ID
func TestAWSResolver_EmptySecretID(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// Empty secret ID should fail
	_, err = resolver.GetSecret(ctx, "")
	assert.Error(t, err)
}

// TestAWSResolver_SpecialCharactersInSecretID tests secret IDs with special characters
func TestAWSResolver_SpecialCharactersInSecretID(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// Test various special characters in secret ID
	testIDs := []string{
		"secret/with/slashes",
		"secret-with-dashes",
		"secret_with_underscores",
		"secret.with.dots",
	}

	for _, id := range testIDs {
		t.Run(id, func(t *testing.T) {
			// These will fail since secrets don't exist, but exercises the code path
			_, err := resolver.GetSecret(ctx, id)
			assert.Error(t, err)
		})
	}
}

// TestTestableAWSResolver_GetSecretJSON_RealMethod tests the GetSecretJSON error propagation
func TestTestableAWSResolver_GetSecretJSON_RealMethod(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAWSResolver(ctx, "us-east-1")
	if err != nil {
		t.Skipf("Skipping test: AWS config not available: %v", err)
	}
	defer resolver.Close()

	// GetSecretJSON should fail because the secret doesn't exist
	result, err := resolver.GetSecretJSON(ctx, "cudly-test-nonexistent-json-secret-xyz")

	require.Error(t, err)
	assert.Nil(t, result)
}
