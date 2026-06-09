package secrets

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestAWSResolver creates an AWSResolver backed by a mock HTTP server.
// The handler receives all API calls and must respond with appropriate JSON.
func newTestAWSResolver(t *testing.T, handler http.HandlerFunc) (*AWSResolver, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)

	client := secretsmanager.New(secretsmanager.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(server.URL),
		Credentials: aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				SessionToken:    "test-session-token",
			}, nil
		}),
		RetryMaxAttempts: 1,
	})

	return &AWSResolver{
		client: client,
		region: "us-east-1",
	}, server
}

func TestAWSResolverReal_GetSecret_Success(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		resp := map[string]interface{}{
			"SecretString": "my-production-secret-value",
			"Name":         req["SecretId"],
			"ARN":          "arn:aws:secretsmanager:us-east-1:123456789012:secret:test-secret",
			"VersionId":    "test-version-id",
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecret(ctx, "test-secret")

	require.NoError(t, err)
	assert.Equal(t, "my-production-secret-value", result)
}

func TestAWSResolverReal_GetSecret_Error(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(http.StatusBadRequest)
		resp := map[string]interface{}{
			"__type":  "ResourceNotFoundException",
			"Message": "Secrets Manager can't find the specified secret.",
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecret(ctx, "non-existent-secret")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "failed to get secret")
}

func TestAWSResolverReal_GetSecret_NoStringValue(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		// Return a response with SecretBinary but no SecretString
		resp := map[string]interface{}{
			"Name":         "binary-secret",
			"ARN":          "arn:aws:secretsmanager:us-east-1:123456789012:secret:binary-secret",
			"VersionId":    "test-version-id",
			"SecretBinary": "SGVsbG8gV29ybGQ=",
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecret(ctx, "binary-secret")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "has no string value")
}

func TestAWSResolverReal_GetSecretJSON_Success(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		jsonValue := `{"username":"admin","password":"secret123","port":5432}`
		resp := map[string]interface{}{
			"SecretString": jsonValue,
			"Name":         "json-secret",
			"ARN":          "arn:aws:secretsmanager:us-east-1:123456789012:secret:json-secret",
			"VersionId":    "test-version-id",
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecretJSON(ctx, "json-secret")

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "admin", result["username"])
	assert.Equal(t, "secret123", result["password"])
	assert.Equal(t, float64(5432), result["port"])
}

func TestAWSResolverReal_GetSecretJSON_InvalidJSON(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"SecretString": "not-valid-json-content",
			"Name":         "invalid-json-secret",
			"ARN":          "arn:aws:secretsmanager:us-east-1:123456789012:secret:invalid-json-secret",
			"VersionId":    "test-version-id",
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecretJSON(ctx, "invalid-json-secret")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to parse secret as JSON")
}

func TestAWSResolverReal_GetSecretJSON_GetSecretError(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(http.StatusBadRequest)
		resp := map[string]interface{}{
			"__type":  "ResourceNotFoundException",
			"Message": "Secret not found",
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.GetSecretJSON(ctx, "missing-secret")

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestAWSResolverReal_ListSecrets_Success_NoFilter(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"SecretList": []map[string]interface{}{
				{"Name": "secret-1", "ARN": "arn:aws:secretsmanager:us-east-1:123456789012:secret:secret-1"},
				{"Name": "secret-2", "ARN": "arn:aws:secretsmanager:us-east-1:123456789012:secret:secret-2"},
				{"Name": "secret-3", "ARN": "arn:aws:secretsmanager:us-east-1:123456789012:secret:secret-3"},
			},
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Equal(t, "secret-1", result[0])
	assert.Equal(t, "secret-2", result[1])
	assert.Equal(t, "secret-3", result[2])
}

func TestAWSResolverReal_ListSecrets_WithFilter(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		// Verify filter was sent
		_, ok := req["Filters"]
		assert.True(t, ok, "expected Filters in request")

		resp := map[string]interface{}{
			"SecretList": []map[string]interface{}{
				{"Name": "prod-db-creds", "ARN": "arn:aws:secretsmanager:us-east-1:123456789012:secret:prod-db-creds"},
				{"Name": "prod-api-key", "ARN": "arn:aws:secretsmanager:us-east-1:123456789012:secret:prod-api-key"},
			},
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "prod-")

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "prod-db-creds")
	assert.Contains(t, result, "prod-api-key")
}

func TestAWSResolverReal_ListSecrets_Error(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(http.StatusForbidden)
		resp := map[string]interface{}{
			"__type":  "AccessDeniedException",
			"Message": "Access denied",
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

func TestAWSResolverReal_ListSecrets_EmptyResult(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"SecretList": []map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "nonexistent-prefix-")

	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestAWSResolverReal_Close_Idempotent(t *testing.T) {
	resolver, server := newTestAWSResolver(t, func(w http.ResponseWriter, r *http.Request) {})
	defer server.Close()

	err := resolver.Close()
	assert.NoError(t, err)

	// Close is idempotent
	err = resolver.Close()
	assert.NoError(t, err)
}
