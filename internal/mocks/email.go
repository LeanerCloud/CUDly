package mocks

import (
	"context"

	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/stretchr/testify/mock"
)

// MockEmailSender is a mock implementation of email.SenderInterface.
type MockEmailSender struct {
	mock.Mock
}

// SendNotification mocks the SendNotification operation.
func (m *MockEmailSender) SendNotification(ctx context.Context, subject, message string) error {
	args := m.Called(ctx, subject, message)
	return args.Error(0)
}

// SendToEmail mocks the SendToEmail operation.
func (m *MockEmailSender) SendToEmail(ctx context.Context, toEmail, subject, body string) error {
	args := m.Called(ctx, toEmail, subject, body)
	return args.Error(0)
}

// SendToEmailWithCCMultipart mocks the SendToEmailWithCCMultipart operation.
func (m *MockEmailSender) SendToEmailWithCCMultipart(ctx context.Context, toEmail string, ccEmails []string, subject, textBody, htmlBody string) error {
	args := m.Called(ctx, toEmail, ccEmails, subject, textBody, htmlBody)
	return args.Error(0)
}

// SendNewRecommendationsNotification mocks the SendNewRecommendationsNotification operation.
func (m *MockEmailSender) SendNewRecommendationsNotification(ctx context.Context, data *email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendScheduledPurchaseNotification mocks the SendScheduledPurchaseNotification operation.
func (m *MockEmailSender) SendScheduledPurchaseNotification(ctx context.Context, data *email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendPurchaseConfirmation mocks the SendPurchaseConfirmation operation.
func (m *MockEmailSender) SendPurchaseConfirmation(ctx context.Context, data *email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendPurchaseFailedNotification mocks the SendPurchaseFailedNotification operation.
func (m *MockEmailSender) SendPurchaseFailedNotification(ctx context.Context, data *email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendPasswordResetEmail mocks the SendPasswordResetEmail operation.
func (m *MockEmailSender) SendPasswordResetEmail(ctx context.Context, emailAddr, resetURL string) error {
	args := m.Called(ctx, emailAddr, resetURL)
	return args.Error(0)
}

// SendWelcomeEmail mocks the SendWelcomeEmail operation.
func (m *MockEmailSender) SendWelcomeEmail(ctx context.Context, emailAddr, dashboardURL, role string) error {
	args := m.Called(ctx, emailAddr, dashboardURL, role)
	return args.Error(0)
}

// SendUserInviteEmail mocks the SendUserInviteEmail operation.
func (m *MockEmailSender) SendUserInviteEmail(ctx context.Context, emailAddr, setupURL string) error {
	args := m.Called(ctx, emailAddr, setupURL)
	return args.Error(0)
}

// SendRIExchangePendingApproval mocks the SendRIExchangePendingApproval operation.
func (m *MockEmailSender) SendRIExchangePendingApproval(ctx context.Context, data *email.RIExchangeNotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendRIExchangeCompleted mocks the SendRIExchangeCompleted operation.
func (m *MockEmailSender) SendRIExchangeCompleted(ctx context.Context, data *email.RIExchangeNotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendPurchaseApprovalRequest mocks the SendPurchaseApprovalRequest operation.
func (m *MockEmailSender) SendPurchaseApprovalRequest(ctx context.Context, data *email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendPurchaseScheduledNotification mocks the SendPurchaseScheduledNotification operation.
func (m *MockEmailSender) SendPurchaseScheduledNotification(ctx context.Context, data *email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendRegistrationReceivedNotification mocks the SendRegistrationReceivedNotification operation.
func (m *MockEmailSender) SendRegistrationReceivedNotification(ctx context.Context, data *email.RegistrationNotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

// SendRegistrationDecisionNotification mocks the SendRegistrationDecisionNotification operation.
func (m *MockEmailSender) SendRegistrationDecisionNotification(ctx context.Context, toEmail string, data *email.RegistrationDecisionData) error {
	args := m.Called(ctx, toEmail, data)
	return args.Error(0)
}

// Ensure MockEmailSender implements the full SenderInterface.
var _ email.SenderInterface = (*MockEmailSender)(nil)
