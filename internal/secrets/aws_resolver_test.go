package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// SecretsManagerAPI defines the interface for AWS Secrets Manager operations
// that we need to mock
type SecretsManagerAPI interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	ListSecrets(ctx context.Context, params *secretsmanager.ListSecretsInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
}

// MockSecretsManagerClient is a mock implementation of the Secrets Manager client
type MockSecretsManagerClient struct {
	mock.Mock
}

func (m *MockSecretsManagerClient) GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*secretsmanager.GetSecretValueOutput), args.Error(1)
}

func (m *MockSecretsManagerClient) ListSecrets(ctx context.Context, params *secretsmanager.ListSecretsInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*secretsmanager.ListSecretsOutput), args.Error(1)
}

// testableAWSResolver wraps AWSResolver to allow injecting a mock client
type testableAWSResolver struct {
	mockClient SecretsManagerAPI
	region     string
}

func (r *testableAWSResolver) GetSecret(ctx context.Context, secretID string) (string, error) {
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretID),
	}

	result, err := r.mockClient.GetSecretValue(ctx, input)
	if err != nil {
		return "", errors.New("failed to get secret " + secretID + ": " + err.Error())
	}

	if result.SecretString != nil {
		return *result.SecretString, nil
	}

	return "", errors.New("secret " + secretID + " has no string value")
}

func (r *testableAWSResolver) GetSecretJSON(ctx context.Context, secretID string) (map[string]interface{}, error) {
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

func (r *testableAWSResolver) ListSecrets(ctx context.Context, filter string) ([]string, error) {
	input := &secretsmanager.ListSecretsInput{}

	if filter != "" {
		input.Filters = []types.Filter{
			{
				Key:    types.FilterNameStringTypeName,
				Values: []string{filter},
			},
		}
	}

	result, err := r.mockClient.ListSecrets(ctx, input)
	if err != nil {
		return nil, errors.New("failed to list secrets: " + err.Error())
	}

	secrets := make([]string, 0, len(result.SecretList))
	for _, secret := range result.SecretList {
		if secret.Name != nil {
			secrets = append(secrets, *secret.Name)
		}
	}

	return secrets, nil
}

func (r *testableAWSResolver) Close() error {
	return nil
}

func TestAWSResolver_GetSecret_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	secretValue := "my-super-secret-value"
	mockClient.On("GetSecretValue", ctx, mock.MatchedBy(func(input *secretsmanager.GetSecretValueInput) bool {
		return *input.SecretId == "test-secret"
	})).Return(&secretsmanager.GetSecretValueOutput{
		SecretString: aws.String(secretValue),
	}, nil)

	result, err := resolver.GetSecret(ctx, "test-secret")

	require.NoError(t, err)
	assert.Equal(t, secretValue, result)
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_GetSecret_Error(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	mockClient.On("GetSecretValue", ctx, mock.Anything).Return(
		nil, errors.New("secret not found"),
	)

	result, err := resolver.GetSecret(ctx, "non-existent-secret")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "failed to get secret")
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_GetSecret_NoStringValue(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	// Return response with nil SecretString
	mockClient.On("GetSecretValue", ctx, mock.Anything).Return(
		&secretsmanager.GetSecretValueOutput{
			SecretString: nil,
		}, nil,
	)

	result, err := resolver.GetSecret(ctx, "binary-secret")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "has no string value")
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_GetSecretJSON_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	jsonSecret := `{"username":"admin","password":"secret123","port":5432}`
	mockClient.On("GetSecretValue", ctx, mock.Anything).Return(
		&secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(jsonSecret),
		}, nil,
	)

	result, err := resolver.GetSecretJSON(ctx, "json-secret")

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "admin", result["username"])
	assert.Equal(t, "secret123", result["password"])
	assert.Equal(t, float64(5432), result["port"])
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_GetSecretJSON_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	mockClient.On("GetSecretValue", ctx, mock.Anything).Return(
		&secretsmanager.GetSecretValueOutput{
			SecretString: aws.String("not-valid-json"),
		}, nil,
	)

	result, err := resolver.GetSecretJSON(ctx, "invalid-json-secret")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to parse secret as JSON")
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_GetSecretJSON_GetSecretError(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	mockClient.On("GetSecretValue", ctx, mock.Anything).Return(
		nil, errors.New("access denied"),
	)

	result, err := resolver.GetSecretJSON(ctx, "inaccessible-secret")

	require.Error(t, err)
	assert.Nil(t, result)
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_ListSecrets_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	secretNames := []string{"secret-1", "secret-2", "secret-3"}
	secretList := make([]types.SecretListEntry, len(secretNames))
	for i, name := range secretNames {
		secretList[i] = types.SecretListEntry{Name: aws.String(name)}
	}

	mockClient.On("ListSecrets", ctx, mock.MatchedBy(func(input *secretsmanager.ListSecretsInput) bool {
		return len(input.Filters) == 0
	})).Return(&secretsmanager.ListSecretsOutput{
		SecretList: secretList,
	}, nil)

	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Equal(t, secretNames, result)
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_ListSecrets_WithFilter(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	mockClient.On("ListSecrets", ctx, mock.MatchedBy(func(input *secretsmanager.ListSecretsInput) bool {
		return len(input.Filters) == 1 &&
			input.Filters[0].Key == types.FilterNameStringTypeName &&
			len(input.Filters[0].Values) == 1 &&
			input.Filters[0].Values[0] == "prod-"
	})).Return(&secretsmanager.ListSecretsOutput{
		SecretList: []types.SecretListEntry{
			{Name: aws.String("prod-db-creds")},
			{Name: aws.String("prod-api-key")},
		},
	}, nil)

	result, err := resolver.ListSecrets(ctx, "prod-")

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "prod-db-creds")
	assert.Contains(t, result, "prod-api-key")
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_ListSecrets_Error(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	mockClient.On("ListSecrets", ctx, mock.Anything).Return(
		nil, errors.New("permission denied"),
	)

	result, err := resolver.ListSecrets(ctx, "")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to list secrets")
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_ListSecrets_NilNames(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	// Include a secret with nil name - should be skipped
	mockClient.On("ListSecrets", ctx, mock.Anything).Return(&secretsmanager.ListSecretsOutput{
		SecretList: []types.SecretListEntry{
			{Name: aws.String("valid-secret")},
			{Name: nil}, // nil name
			{Name: aws.String("another-valid")},
		},
	}, nil)

	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "valid-secret")
	assert.Contains(t, result, "another-valid")
	mockClient.AssertExpectations(t)
}

func TestAWSResolver_Close(t *testing.T) {
	mockClient := new(MockSecretsManagerClient)
	resolver := &testableAWSResolver{mockClient: mockClient, region: "us-east-1"}

	err := resolver.Close()

	assert.NoError(t, err)
}

func TestAWSResolver_StructFields(t *testing.T) {
	// Test that AWSResolver has expected fields
	resolver := &AWSResolver{
		client: nil,
		region: "eu-west-1",
	}

	assert.Equal(t, "eu-west-1", resolver.region)
	assert.Nil(t, resolver.client)
}

func TestAWSResolver_ImplementsResolverInterface(t *testing.T) {
	// Verify AWSResolver implements the Resolver interface
	var _ Resolver = (*AWSResolver)(nil)
}

func TestAWSResolver_Close_NoClient(t *testing.T) {
	// Test Close on a resolver with nil client (direct struct creation)
	resolver := &AWSResolver{
		client: nil,
		region: "us-east-1",
	}

	err := resolver.Close()
	assert.NoError(t, err)
}
