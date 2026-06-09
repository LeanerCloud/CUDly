package mocks

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/stretchr/testify/mock"
)

// MockSecretsManagerClient is a mock implementation of Secrets Manager client
type MockSecretsManagerClient struct {
	mock.Mock
}

// GetSecretValue mocks the GetSecretValue operation
func (m *MockSecretsManagerClient) GetSecretValue(ctx context.Context, input *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*secretsmanager.GetSecretValueOutput), args.Error(1)
}

// CreateSecret mocks the CreateSecret operation
func (m *MockSecretsManagerClient) CreateSecret(ctx context.Context, input *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*secretsmanager.CreateSecretOutput), args.Error(1)
}

// UpdateSecret mocks the UpdateSecret operation
func (m *MockSecretsManagerClient) UpdateSecret(ctx context.Context, input *secretsmanager.UpdateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.UpdateSecretOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*secretsmanager.UpdateSecretOutput), args.Error(1)
}

// SecretsManagerAPI defines the interface for Secrets Manager operations used by our code
type SecretsManagerAPI interface {
	GetSecretValue(ctx context.Context, input *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	CreateSecret(ctx context.Context, input *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
	UpdateSecret(ctx context.Context, input *secretsmanager.UpdateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.UpdateSecretOutput, error)
}

// Ensure MockSecretsManagerClient implements SecretsManagerAPI
var _ SecretsManagerAPI = (*MockSecretsManagerClient)(nil)
