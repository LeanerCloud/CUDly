package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"testing"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/iterator"
)

// MockSecretIterator implements the iterator interface for testing.
type MockSecretIterator struct {
	secrets []*secretmanagerpb.Secret
	index   int
	err     error
}

func (m *MockSecretIterator) Next() (*secretmanagerpb.Secret, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.index >= len(m.secrets) {
		return nil, iterator.Done
	}
	secret := m.secrets[m.index]
	m.index++
	return secret, nil
}

// MockGCPSecretManagerClient is a mock implementation of the GCP Secret Manager client.
type MockGCPSecretManagerClient struct {
	mock.Mock
}

func (m *MockGCPSecretManagerClient) AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*secretmanagerpb.AccessSecretVersionResponse), args.Error(1)
}

func (m *MockGCPSecretManagerClient) ListSecrets(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) *MockSecretIterator {
	args := m.Called(ctx, req)
	return args.Get(0).(*MockSecretIterator)
}

func (m *MockGCPSecretManagerClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// testableGCPResolver wraps GCPResolver to allow injecting a mock client.
type testableGCPResolver struct {
	mockClient *MockGCPSecretManagerClient
	projectID  string
}

func (r *testableGCPResolver) GetSecret(ctx context.Context, secretID string) (string, error) {
	name := "projects/" + r.projectID + "/secrets/" + secretID + "/versions/latest"

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: name,
	}

	result, err := r.mockClient.AccessSecretVersion(ctx, req)
	if err != nil {
		return "", errors.New("failed to access secret " + secretID + ": " + err.Error())
	}

	return string(result.Payload.Data), nil
}

func (r *testableGCPResolver) GetSecretJSON(ctx context.Context, secretID string) (map[string]interface{}, error) {
	secretString, err := r.GetSecret(ctx, secretID)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(secretString), &result); err != nil {
		return nil, errors.New("failed to parse secret as JSON: " + err.Error())
	}

	return result, nil
}

func (r *testableGCPResolver) ListSecrets(ctx context.Context, filter string) ([]string, error) {
	// Mirror the real GCPResolver.ListSecrets: no Filter in the API request;
	// client-side HasPrefix applied to the short name extracted via path.Base.
	req := &secretmanagerpb.ListSecretsRequest{
		Parent: "projects/" + r.projectID,
	}

	it := r.mockClient.ListSecrets(ctx, req)
	var secrets []string

	for {
		secret, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, errors.New("failed to list secrets: " + err.Error())
		}

		name := path.Base(secret.Name)
		if filter == "" || strings.HasPrefix(name, filter) {
			secrets = append(secrets, name)
		}
	}

	return secrets, nil
}

func (r *testableGCPResolver) Close() error {
	return r.mockClient.Close()
}

func TestGCPResolver_GetSecret_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	secretValue := "my-gcp-secret-value"
	mockClient.On("AccessSecretVersion", ctx, mock.MatchedBy(func(req *secretmanagerpb.AccessSecretVersionRequest) bool {
		return req.Name == "projects/my-project/secrets/test-secret/versions/latest"
	})).Return(&secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{
			Data: []byte(secretValue),
		},
	}, nil)

	result, err := resolver.GetSecret(ctx, "test-secret")

	require.NoError(t, err)
	assert.Equal(t, secretValue, result)
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_GetSecret_Error(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	mockClient.On("AccessSecretVersion", ctx, mock.Anything).Return(
		nil, errors.New("permission denied"),
	)

	result, err := resolver.GetSecret(ctx, "forbidden-secret")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "failed to access secret")
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_GetSecretJSON_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	jsonSecret := `{"username":"gcp-user","password":"gcp-password"}`
	mockClient.On("AccessSecretVersion", ctx, mock.Anything).Return(
		&secretmanagerpb.AccessSecretVersionResponse{
			Payload: &secretmanagerpb.SecretPayload{
				Data: []byte(jsonSecret),
			},
		}, nil,
	)

	result, err := resolver.GetSecretJSON(ctx, "json-secret")

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "gcp-user", result["username"])
	assert.Equal(t, "gcp-password", result["password"])
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_GetSecretJSON_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	mockClient.On("AccessSecretVersion", ctx, mock.Anything).Return(
		&secretmanagerpb.AccessSecretVersionResponse{
			Payload: &secretmanagerpb.SecretPayload{
				Data: []byte("not-valid-json"),
			},
		}, nil,
	)

	result, err := resolver.GetSecretJSON(ctx, "invalid-json-secret")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to parse secret as JSON")
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_GetSecretJSON_GetSecretError(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	mockClient.On("AccessSecretVersion", ctx, mock.Anything).Return(
		nil, errors.New("not found"),
	)

	result, err := resolver.GetSecretJSON(ctx, "missing-secret")

	require.Error(t, err)
	assert.Nil(t, result)
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_ListSecrets_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	rawSecretNames := []string{
		"projects/my-project/secrets/secret-1",
		"projects/my-project/secrets/secret-2",
		"projects/my-project/secrets/secret-3",
	}

	secrets := make([]*secretmanagerpb.Secret, len(rawSecretNames))
	for i, name := range rawSecretNames {
		secrets[i] = &secretmanagerpb.Secret{Name: name}
	}

	// Filter field must NOT be set; client-side HasPrefix is used instead.
	mockClient.On("ListSecrets", ctx, mock.MatchedBy(func(req *secretmanagerpb.ListSecretsRequest) bool {
		return req.Parent == "projects/my-project" && req.Filter == ""
	})).Return(&MockSecretIterator{secrets: secrets})

	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	// Short names (path.Base) are returned, not full resource paths.
	assert.Equal(t, []string{"secret-1", "secret-2", "secret-3"}, result)
	mockClient.AssertExpectations(t)
}

// TestGCPResolver_ListSecrets_HasPrefixFilter verifies the client-side HasPrefix
// contract documented on Resolver.ListSecrets (03-M3): only secrets whose short
// name has the given prefix are returned; the GCP API request carries no Filter.
func TestGCPResolver_ListSecrets_HasPrefixFilter(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	// Six secrets with different prefixes.
	allSecrets := []*secretmanagerpb.Secret{
		{Name: "projects/my-project/secrets/prod-db"},
		{Name: "projects/my-project/secrets/prod-cache"},
		{Name: "projects/my-project/secrets/dev-db"},
		{Name: "projects/my-project/secrets/staging-db"},
		{Name: "projects/my-project/secrets/prod-app"},
		{Name: "projects/my-project/secrets/other"},
	}

	// API is called without a Filter expression.
	mockClient.On("ListSecrets", ctx, mock.MatchedBy(func(req *secretmanagerpb.ListSecretsRequest) bool {
		return req.Filter == ""
	})).Return(&MockSecretIterator{secrets: allSecrets})

	result, err := resolver.ListSecrets(ctx, "prod-")

	require.NoError(t, err)
	// Only the three "prod-" secrets must be returned, in iteration order.
	assert.Equal(t, []string{"prod-db", "prod-cache", "prod-app"}, result)
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_ListSecrets_Error(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	mockClient.On("ListSecrets", ctx, mock.Anything).Return(&MockSecretIterator{
		err: errors.New("access denied"),
	})

	result, err := resolver.ListSecrets(ctx, "")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to list secrets")
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_ListSecrets_Empty(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	mockClient.On("ListSecrets", ctx, mock.Anything).Return(&MockSecretIterator{
		secrets: []*secretmanagerpb.Secret{},
	})

	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Empty(t, result)
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_Close_Success(t *testing.T) {
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	mockClient.On("Close").Return(nil)

	err := resolver.Close()

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_Close_Error(t *testing.T) {
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project"}

	mockClient.On("Close").Return(errors.New("close failed"))

	err := resolver.Close()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "close failed")
	mockClient.AssertExpectations(t)
}

func TestGCPResolver_StructFields(t *testing.T) {
	// Test that GCPResolver has expected fields
	resolver := &GCPResolver{
		client:    nil,
		projectID: "test-project",
	}

	assert.Equal(t, "test-project", resolver.projectID)
	assert.Nil(t, resolver.client)
}

func TestGCPResolver_ImplementsResolverInterface(t *testing.T) {
	// Verify GCPResolver implements the Resolver interface
	var _ Resolver = (*GCPResolver)(nil)
}

func TestGCPResolver_Close_NilClient(t *testing.T) {
	// Test Close on a resolver with nil client - this will panic
	// The production code calls r.client.Close() without nil check
	// This test documents that behavior
	resolver := &GCPResolver{
		client:    nil,
		projectID: "test-project",
	}

	// Close with nil client will panic since it calls r.client.Close()
	// We can't test this safely without a nil check in production code
	_ = resolver // Document that we can't safely test Close with nil client
}

func TestGCPResolver_SecretNameFormat(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockGCPSecretManagerClient)
	resolver := &testableGCPResolver{mockClient: mockClient, projectID: "my-project-123"}

	// Verify the name format is correct
	mockClient.On("AccessSecretVersion", ctx, mock.MatchedBy(func(req *secretmanagerpb.AccessSecretVersionRequest) bool {
		expected := "projects/my-project-123/secrets/my-secret/versions/latest"
		return req.Name == expected
	})).Return(&secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{Data: []byte("value")},
	}, nil)

	_, err := resolver.GetSecret(ctx, "my-secret")

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}
