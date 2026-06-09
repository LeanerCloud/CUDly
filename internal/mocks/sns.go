package mocks

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/mock"
)

// MockSNSClient is a mock implementation of SNS client
type MockSNSClient struct {
	mock.Mock
}

// Publish mocks the Publish operation
func (m *MockSNSClient) Publish(ctx context.Context, input *sns.PublishInput, opts ...func(*sns.Options)) (*sns.PublishOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sns.PublishOutput), args.Error(1)
}

// SNSAPI defines the interface for SNS operations used by our code
type SNSAPI interface {
	Publish(ctx context.Context, input *sns.PublishInput, opts ...func(*sns.Options)) (*sns.PublishOutput, error)
}

// Ensure MockSNSClient implements SNSAPI
var _ SNSAPI = (*MockSNSClient)(nil)
