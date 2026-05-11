package email

import (
	"context"

	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// NopSender is a SenderInterface implementation that does not send anything.
// It logs every invocation at debug level so local-dev / EMAIL_ENABLED=false
// deployments can still trace where an email would have gone without
// requiring real SES / SNS / Azure / GCP credentials.
type NopSender struct{}

// NewNopSender constructs a no-op sender. Used when EMAIL_ENABLED=false.
func NewNopSender() *NopSender { return &NopSender{} }

// Verify the no-op satisfies the full sender contract.
var _ SenderInterface = (*NopSender)(nil)

func (n *NopSender) SendNotification(_ context.Context, subject, _ string) error {
	logging.Debugf("email/nop: SendNotification suppressed (subject=%q)", subject)
	return nil
}

func (n *NopSender) SendToEmail(_ context.Context, toEmail, subject, _ string) error {
	logging.Debugf("email/nop: SendToEmail suppressed (to=%q subject=%q)", toEmail, subject)
	return nil
}

func (n *NopSender) SendToEmailWithCCMultipart(_ context.Context, toEmail string, ccEmails []string, subject, _, _ string) error {
	logging.Debugf("email/nop: SendToEmailWithCCMultipart suppressed (to=%q cc=%v subject=%q)", toEmail, ccEmails, subject)
	return nil
}

func (n *NopSender) SendNewRecommendationsNotification(_ context.Context, _ NotificationData) error {
	logging.Debugf("email/nop: SendNewRecommendationsNotification suppressed")
	return nil
}

func (n *NopSender) SendScheduledPurchaseNotification(_ context.Context, _ NotificationData) error {
	logging.Debugf("email/nop: SendScheduledPurchaseNotification suppressed")
	return nil
}

func (n *NopSender) SendPurchaseConfirmation(_ context.Context, _ NotificationData) error {
	logging.Debugf("email/nop: SendPurchaseConfirmation suppressed")
	return nil
}

func (n *NopSender) SendPurchaseFailedNotification(_ context.Context, _ NotificationData) error {
	logging.Debugf("email/nop: SendPurchaseFailedNotification suppressed")
	return nil
}

func (n *NopSender) SendPasswordResetEmail(_ context.Context, email, _ string) error {
	logging.Debugf("email/nop: SendPasswordResetEmail suppressed (to=%q)", email)
	return nil
}

func (n *NopSender) SendWelcomeEmail(_ context.Context, email, _, role string) error {
	logging.Debugf("email/nop: SendWelcomeEmail suppressed (to=%q role=%q)", email, role)
	return nil
}

func (n *NopSender) SendRIExchangePendingApproval(_ context.Context, _ RIExchangeNotificationData) error {
	logging.Debugf("email/nop: SendRIExchangePendingApproval suppressed")
	return nil
}

func (n *NopSender) SendRIExchangeCompleted(_ context.Context, _ RIExchangeNotificationData) error {
	logging.Debugf("email/nop: SendRIExchangeCompleted suppressed")
	return nil
}

func (n *NopSender) SendPurchaseApprovalRequest(_ context.Context, _ NotificationData) error {
	logging.Debugf("email/nop: SendPurchaseApprovalRequest suppressed")
	return nil
}

func (n *NopSender) SendRegistrationReceivedNotification(_ context.Context, _ RegistrationNotificationData) error {
	logging.Debugf("email/nop: SendRegistrationReceivedNotification suppressed")
	return nil
}

func (n *NopSender) SendRegistrationDecisionNotification(_ context.Context, toEmail string, _ RegistrationDecisionData) error {
	logging.Debugf("email/nop: SendRegistrationDecisionNotification suppressed (to=%q)", toEmail)
	return nil
}
