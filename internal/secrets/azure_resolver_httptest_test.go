package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTokenCredential implements azcore.TokenCredential for testing.
type fakeTokenCredential struct{}

func (f *fakeTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "fake-access-token",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

// azureTestHandler creates an HTTP handler that simulates Azure Key Vault responses,
// including the initial 401 challenge flow.
func azureTestHandler(handler http.HandlerFunc) http.HandlerFunc {
	var challengeIssued atomic.Bool
	return func(w http.ResponseWriter, r *http.Request) {
		// Handle the Key Vault challenge authentication flow.
		// First request from the challenge policy has no Authorization header.
		if !challengeIssued.Load() && r.Header.Get("Authorization") == "" {
			challengeIssued.Store(true)
			w.Header().Set("WWW-Authenticate",
				`Bearer authorization="https://login.microsoftonline.com/test-tenant-id" resource="https://vault.azure.net"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Subsequent requests should have the Bearer token
		handler(w, r)
	}
}

// newTestAzureResolver creates an AzureResolver backed by a mock HTTP server.
// The handler must NOT reference the server variable (it is created inside this function).
func newTestAzureResolver(t *testing.T, handler http.HandlerFunc) (*AzureResolver, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(azureTestHandler(handler))

	cred := &fakeTokenCredential{}
	client, err := azsecrets.NewClient(server.URL, cred, &azsecrets.ClientOptions{
		ClientOptions: policy.ClientOptions{
			InsecureAllowCredentialWithHTTP: true,
			Retry: policy.RetryOptions{
				MaxRetries: 0,
			},
		},
		DisableChallengeResourceVerification: true,
	})
	require.NoError(t, err)

	return &AzureResolver{
		client:   client,
		vaultURL: server.URL,
	}, server
}

func TestAzureResolverReal_GetSecret_Success(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasPrefix(r.URL.Path, "/secrets/"))
		resp := map[string]interface{}{
			"value": "my-azure-secret-value",
			"id":    "https://myvault.vault.azure.net/secrets/test-secret/abc123",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecret(ctx, "test-secret")

	require.NoError(t, err)
	assert.Equal(t, "my-azure-secret-value", result)
}

func TestAzureResolverReal_GetSecret_Error(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		resp := map[string]interface{}{
			"error": map[string]interface{}{
				"code":    "SecretNotFound",
				"message": "Secret not found",
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecret(ctx, "non-existent")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "failed to get secret")
}

func TestAzureResolverReal_GetSecret_NilValue(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		// Return a response with no "value" field (value will be nil)
		resp := map[string]interface{}{
			"id": "https://myvault.vault.azure.net/secrets/nil-secret/abc123",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecret(ctx, "nil-secret")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "has no value")
}

func TestAzureResolverReal_GetSecretJSON_Success(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		jsonValue := `{"username":"azure-admin","password":"azure-pass","port":5432}`
		resp := map[string]interface{}{
			"value": jsonValue,
			"id":    "https://myvault.vault.azure.net/secrets/json-secret/abc123",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecretJSON(ctx, "json-secret")

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "azure-admin", result["username"])
	assert.Equal(t, "azure-pass", result["password"])
	assert.Equal(t, float64(5432), result["port"])
}

func TestAzureResolverReal_GetSecretJSON_InvalidJSON(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"value": "not-valid-json",
			"id":    "https://myvault.vault.azure.net/secrets/invalid-json/abc123",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecretJSON(ctx, "invalid-json")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to parse secret as JSON")
}

func TestAzureResolverReal_GetSecretJSON_GetSecretError(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		resp := map[string]interface{}{
			"error": map[string]interface{}{
				"code":    "Forbidden",
				"message": "Access denied",
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecretJSON(ctx, "forbidden-secret")

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestAzureResolverReal_ListSecrets_Success(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"value": []map[string]interface{}{
				{"id": "https://myvault.vault.azure.net/secrets/secret-1"},
				{"id": "https://myvault.vault.azure.net/secrets/secret-2"},
				{"id": "https://myvault.vault.azure.net/secrets/secret-3"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Len(t, result, 3)
}

func TestAzureResolverReal_ListSecrets_WithFilter(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"value": []map[string]interface{}{
				{"id": "https://myvault.vault.azure.net/secrets/prod-db-creds"},
				{"id": "https://myvault.vault.azure.net/secrets/dev-api-key"},
				{"id": "https://myvault.vault.azure.net/secrets/prod-api-key"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "prod")

	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestAzureResolverReal_ListSecrets_Error(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		resp := map[string]interface{}{
			"error": map[string]interface{}{
				"code":    "Forbidden",
				"message": "Access denied",
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to list secrets")
}

func TestAzureResolverReal_ListSecrets_Empty(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"value": []map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestAzureResolverReal_Close(t *testing.T) {
	resolver, server := newTestAzureResolver(t, func(w http.ResponseWriter, r *http.Request) {})
	defer server.Close()

	err := resolver.Close()
	assert.NoError(t, err)

	// Close is idempotent
	err = resolver.Close()
	assert.NoError(t, err)
}
