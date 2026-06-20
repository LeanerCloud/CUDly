package mocks

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/stretchr/testify/mock"
)

// SESClient is a mock implementation of SES client.
type SESClient struct {
	mock.Mock
}

// SendEmail mocks the SendEmail operation.
func (m *SESClient) SendEmail(ctx context.Context, input *sesv2.SendEmailInput, opts ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sesv2.SendEmailOutput), args.Error(1) //nolint:errcheck // mock: type assertion is safe; testify panics with a clear message on mismatch
}

// SESAPI defines the interface for SES operations used by our code.
type SESAPI interface {
	SendEmail(ctx context.Context, input *sesv2.SendEmailInput, opts ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// Ensure SESClient implements SESAPI.
var _ SESAPI = (*SESClient)(nil)
