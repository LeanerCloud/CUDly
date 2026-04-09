package mocks

import (
	"context"

	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/stretchr/testify/mock"
)

// MockEmailSender is a mock implementation of email.Sender
type MockEmailSender struct {
	mock.Mock
}

// SendNotification mocks the SendNotification operation
func (m *MockEmailSender) SendNotification(ctx context.Context, subject, message string) error {
	args := m.Called(ctx, subject, message)
	return args.Error(0)
}

// SendToEmail mocks the SendToEmail operation
func (m *MockEmailSender) SendToEmail(ctx context.Context, toEmail, subject, body string) error {
	args := m.Called(ctx, toEmail, subject, body)
	return args.Error(0)
}

// SendNewRecommendationsNotification mocks the SendNewRecommendationsNotification operation
func (m *MockEmailSender) SendNewRecommendationsNotification(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendScheduledPurchaseNotification mocks the SendScheduledPurchaseNotification operation
func (m *MockEmailSender) SendScheduledPurchaseNotification(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendPurchaseConfirmation mocks the SendPurchaseConfirmation operation
func (m *MockEmailSender) SendPurchaseConfirmation(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendPurchaseFailedNotification mocks the SendPurchaseFailedNotification operation
func (m *MockEmailSender) SendPurchaseFailedNotification(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendPasswordResetEmail mocks the SendPasswordResetEmail operation
func (m *MockEmailSender) SendPasswordResetEmail(ctx context.Context, email, resetURL string) error {
	args := m.Called(ctx, email, resetURL)
	return args.Error(0)
}

// SendWelcomeEmail mocks the SendWelcomeEmail operation
func (m *MockEmailSender) SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error {
	args := m.Called(ctx, email, dashboardURL, role)
	return args.Error(0)
}

// SendPurchaseApprovalRequest mocks the SendPurchaseApprovalRequest operation
func (m *MockEmailSender) SendPurchaseApprovalRequest(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// EmailSenderAPI defines the interface for email sender operations
type EmailSenderAPI interface {
	SendNotification(ctx context.Context, subject, message string) error
	SendToEmail(ctx context.Context, toEmail, subject, body string) error
	SendNewRecommendationsNotification(ctx context.Context, data email.NotificationData) error
	SendScheduledPurchaseNotification(ctx context.Context, data email.NotificationData) error
	SendPurchaseConfirmation(ctx context.Context, data email.NotificationData) error
	SendPurchaseFailedNotification(ctx context.Context, data email.NotificationData) error
	SendPasswordResetEmail(ctx context.Context, email, resetURL string) error
	SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error
	SendPurchaseApprovalRequest(ctx context.Context, data email.NotificationData) error
}

// Ensure MockEmailSender implements EmailSenderAPI
var _ EmailSenderAPI = (*MockEmailSender)(nil)
