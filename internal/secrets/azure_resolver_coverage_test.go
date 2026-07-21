package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notFoundHandler simulates the Key Vault response for a missing secret.
func notFoundHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": "SecretNotFound", "message": "Secret not found"},
	})
}

// TestAzureResolver_DirectMethods tests the real constructor wiring. It makes
// no requests, and TestMain pins the credential chain to a dummy
// EnvironmentCredential, so construction is deterministic and offline.
func TestAzureResolver_DirectMethods(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAzureResolver(ctx, "https://test-vault.vault.azure.net/")
	require.NoError(t, err)
	defer resolver.Close()

	// Test that the resolver is properly configured
	assert.Equal(t, "https://test-vault.vault.azure.net/", resolver.vaultURL)
	assert.NotNil(t, resolver.client)
}

// TestAzureResolver_GetSecret_NonExistent tests getting a non-existent secret.
func TestAzureResolver_GetSecret_NonExistent(t *testing.T) {
	resolver, server := newTestAzureResolver(t, notFoundHandler)
	defer server.Close()

	_, err := resolver.GetSecret(context.Background(), "cudly-test-nonexistent-secret-12345-xyz")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get secret")
}

// TestAzureResolver_GetSecretJSON_NonExistent tests getting a non-existent JSON secret.
func TestAzureResolver_GetSecretJSON_NonExistent(t *testing.T) {
	resolver, server := newTestAzureResolver(t, notFoundHandler)
	defer server.Close()

	result, err := resolver.GetSecretJSON(context.Background(), "cudly-test-nonexistent-json-secret-12345-xyz")

	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestAzureResolver_ListSecrets tests listing secrets.
func TestAzureResolver_ListSecrets(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]any{}})
	})
	defer server.Close()

	result, err := resolver.ListSecrets(context.Background(), "")

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// TestAzureResolver_ListSecrets_WithFilter_Coverage_Direct tests that the
// client-side prefix filter is applied while listing.
func TestAzureResolver_ListSecrets_WithFilter_Coverage_Direct(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{"id": "https://myvault.vault.azure.net/secrets/prod-db-creds"},
				{"id": "https://myvault.vault.azure.net/secrets/dev-api-key"},
			},
		})
	})
	defer server.Close()

	result, err := resolver.ListSecrets(context.Background(), "prod")

	require.NoError(t, err)
	assert.Equal(t, []string{"prod-db-creds"}, result)
}

// TestAzureResolver_Close_Idempotent tests that Close can be called multiple
// times. Constructor-only: no requests are made.
func TestAzureResolver_Close_Idempotent(t *testing.T) {
	ctx := context.Background()

	resolver, err := NewAzureResolver(ctx, "https://test-vault.vault.azure.net/")
	require.NoError(t, err)

	// Close multiple times should not panic
	err1 := resolver.Close()
	assert.NoError(t, err1)

	err2 := resolver.Close()
	assert.NoError(t, err2)
}

// TestAzureResolver_DifferentVaultURLs tests creating resolvers for different vaults.
func TestAzureResolver_DifferentVaultURLs(t *testing.T) {
	ctx := context.Background()

	vaultURLs := []string{
		"https://vault1.vault.azure.net/",
		"https://vault2.vault.azure.net/",
		"https://my-prod-vault.vault.azure.net/",
	}

	for _, vaultURL := range vaultURLs {
		t.Run(vaultURL, func(t *testing.T) {
			resolver, err := NewAzureResolver(ctx, vaultURL)
			require.NoError(t, err)
			defer resolver.Close()

			assert.Equal(t, vaultURL, resolver.vaultURL)
			assert.NotNil(t, resolver.client)
		})
	}
}

// TestAzureResolver_ContextHandling tests context handling in Azure resolver.
func TestAzureResolver_ContextHandling(t *testing.T) {
	var handlerCalls atomic.Int32
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		handlerCalls.Add(1)
		notFoundHandler(w, r)
	})
	defer server.Close()

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// GetSecret with canceled context should fail before reaching the server.
	_, err := resolver.GetSecret(cancelledCtx, "test-secret")
	require.Error(t, err)
	assert.Zero(t, handlerCalls.Load(), "canceled request must not reach the HTTP handler")
}

// TestAzureResolver_EmptySecretID tests getting a secret with empty ID.
func TestAzureResolver_EmptySecretID(t *testing.T) {
	resolver, server := newTestAzureResolver(t, notFoundHandler)
	defer server.Close()

	_, err := resolver.GetSecret(context.Background(), "")
	assert.Error(t, err)
}

// TestAzureResolver_SpecialCharactersInSecretID tests secret IDs with special characters.
func TestAzureResolver_SpecialCharactersInSecretID(t *testing.T) {
	resolver, server := newTestAzureResolver(t, notFoundHandler)
	defer server.Close()

	// Azure Key Vault secret names can contain alphanumeric and dashes
	testIDs := []string{
		"secret-with-dashes",
		"SecretWithCaps",
		"secret123",
	}

	for _, id := range testIDs {
		t.Run(id, func(t *testing.T) {
			// These fail because the mock vault returns 404 for every secret.
			_, err := resolver.GetSecret(context.Background(), id)
			assert.Error(t, err)
		})
	}
}

// TestAzureResolver_GetSecretJSON_RealMethod tests the GetSecretJSON error propagation.
func TestAzureResolver_GetSecretJSON_RealMethod(t *testing.T) {
	resolver, server := newTestAzureResolver(t, notFoundHandler)
	defer server.Close()

	result, err := resolver.GetSecretJSON(context.Background(), "cudly-test-nonexistent-json-secret-xyz")

	require.Error(t, err)
	assert.Nil(t, result)
}

// TestAzureResolver_VaultURLFormat tests various vault URL formats.
func TestAzureResolver_VaultURLFormat(t *testing.T) {
	// The Azure resolver stores the vault URL as-is
	resolver := &AzureResolver{
		vaultURL: "https://my-vault.vault.azure.net/",
		client:   nil,
	}

	// Verify the vaultURL is stored correctly
	assert.Equal(t, "https://my-vault.vault.azure.net/", resolver.vaultURL)
}

// TestMockSecretID_VersionMethod tests the Version method of MockSecretID.
func TestMockSecretID_VersionMethod(t *testing.T) {
	id := MockSecretID("https://myvault.vault.azure.net/secrets/my-secret")
	version := id.Version()
	assert.Equal(t, "", version)
}

// TestMockSecretID_EdgeCases tests edge cases in MockSecretID.Name().
func TestMockSecretID_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		id       MockSecretID
		expected string
	}{
		{
			name:     "standard format",
			id:       MockSecretID("https://vault.vault.azure.net/secrets/mysecret"),
			expected: "mysecret",
		},
		{
			name:     "with version suffix",
			id:       MockSecretID("https://vault.vault.azure.net/secrets/mysecret/version123"),
			expected: "mysecret/version123",
		},
		{
			name:     "empty string",
			id:       MockSecretID(""),
			expected: "",
		},
		{
			name:     "just /secrets/",
			id:       MockSecretID("/secrets/"),
			expected: "",
		},
		{
			name:     "multiple /secrets/ occurrences",
			id:       MockSecretID("https://vault/secrets/one/secrets/two"),
			expected: "one", // SplitN with 2 returns only first two parts
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.id.Name()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestMockAzureSecretsPager_MultiplePages tests the mock pager with multiple pages.
func TestMockAzureSecretsPager_MultiplePages(t *testing.T) {
	pager := &MockAzureSecretsPager{
		pages: [][]*azsecrets.SecretProperties{
			{}, // empty first page
			{}, // empty second page
		},
	}

	assert.True(t, pager.More())
	_, err := pager.NextPage(context.Background())
	assert.NoError(t, err)

	assert.True(t, pager.More())
	_, err = pager.NextPage(context.Background())
	assert.NoError(t, err)

	assert.False(t, pager.More())
}

// TestMockAzureSecretsPager_Error tests the mock pager error handling.
func TestMockAzureSecretsPager_Error(t *testing.T) {
	pager := &MockAzureSecretsPager{
		err: assert.AnError,
	}

	// Should indicate more pages due to pending error
	assert.True(t, pager.More())

	_, err := pager.NextPage(context.Background())
	assert.Error(t, err)
}

// TestMockAzureSecretsPager_EmptyPages tests the mock pager with empty pages slice.
func TestMockAzureSecretsPager_EmptyPages(t *testing.T) {
	pager := &MockAzureSecretsPager{
		pages: [][]*azsecrets.SecretProperties{},
	}

	assert.False(t, pager.More())
}

// TestMockAzureSecretsPager_NoMorePages tests NextPage when there are no more pages.
func TestMockAzureSecretsPager_NoMorePages(t *testing.T) {
	pager := &MockAzureSecretsPager{
		pages:       [][]*azsecrets.SecretProperties{{}},
		currentPage: 1, // Already past the first page
	}

	// Should return error when trying to get next page
	_, err := pager.NextPage(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no more pages")
}
