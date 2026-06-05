package server

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wiringMockSES is a minimal SESEmailSender used by the wiring tests. It records
// the SendEmail inputs it receives so the test can assert both that a send did
// (or did not) happen and what headers it carried. GetAccount returns
// production mode so the sandbox-verification path is skipped.
type wiringMockSES struct {
	sent []*sesv2.SendEmailInput
}

func (m *wiringMockSES) SendEmail(_ context.Context, in *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	m.sent = append(m.sent, in)
	return &sesv2.SendEmailOutput{}, nil
}

func (m *wiringMockSES) GetAccount(_ context.Context, _ *sesv2.GetAccountInput, _ ...func(*sesv2.Options)) (*sesv2.GetAccountOutput, error) {
	return &sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil
}

func (m *wiringMockSES) GetEmailIdentity(_ context.Context, _ *sesv2.GetEmailIdentityInput, _ ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error) {
	return &sesv2.GetEmailIdentityOutput{VerifiedForSendingStatus: true}, nil
}

func (m *wiringMockSES) CreateEmailIdentity(_ context.Context, _ *sesv2.CreateEmailIdentityInput, _ ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error) {
	return &sesv2.CreateEmailIdentityOutput{}, nil
}

// wiringMuteChecker is a fixed-set mute checker keyed by recipient email.
type wiringMuteChecker struct {
	muted map[string]bool
}

func (w *wiringMuteChecker) IsNotificationMuted(_ context.Context, recipientEmail, _ string) (bool, error) {
	return w.muted[recipientEmail], nil
}

// approvalData builds a minimal purchase-approval NotificationData for the
// given recipient.
func approvalData(recipient string) email.NotificationData {
	return email.NotificationData{
		RecipientEmail: recipient,
		DashboardURL:   "https://dash.example.com",
		ApprovalToken:  "tok",
		Recommendations: []email.RecommendationSummary{
			{Service: "ec2", Region: "us-east-1", Count: 1, MonthlySavings: 100},
		},
	}
}

// TestDecorateSenderWithMute_SuppressesMutedRecipient verifies the real app
// wiring path (decorateSenderWithMute, the same call NewApplication makes)
// actually enables per-recipient mute suppression on a factory-shaped sender.
//
// This test exercises the decoration the production factory applies rather than
// hand-injecting the mute checker into a Sender literal. Before the wiring fix
// the production sender's mute checker was always nil, so a muted recipient
// would still be sent an approval email; this test would fail in that state.
func TestDecorateSenderWithMute_SuppressesMutedRecipient(t *testing.T) {
	ctx := context.Background()
	ses := &wiringMockSES{}
	// NewSenderWithClients yields the same concrete *email.Sender type the AWS
	// branch of NewSenderFromEnvironment produces, so the type switch in
	// decorateSenderWithMute takes the same path it does in production.
	base := email.NewSenderWithClients(nil, ses, email.SenderConfig{FromEmail: "noreply@example.com"})
	mc := &wiringMuteChecker{muted: map[string]bool{"muted@example.com": true}}

	decorated := decorateSenderWithMute(base, mc, "https://dash.example.com")

	err := decorated.SendPurchaseApprovalRequest(ctx, approvalData("muted@example.com"))
	require.NoError(t, err)
	assert.Empty(t, ses.sent, "muted recipient must not receive a SendEmail call after wiring")
}

// TestDecorateSenderWithMute_EmitsListUnsubscribe verifies the decorated
// production sender both sends to a non-muted recipient and attaches the RFC
// 8058 List-Unsubscribe header sourced from the wired-in dashboard base URL.
// Pre-fix the production sender had no unsubscribe base URL, so no such header
// was emitted.
func TestDecorateSenderWithMute_EmitsListUnsubscribe(t *testing.T) {
	ctx := context.Background()
	ses := &wiringMockSES{}
	base := email.NewSenderWithClients(nil, ses, email.SenderConfig{FromEmail: "noreply@example.com"})
	mc := &wiringMuteChecker{muted: map[string]bool{}}

	decorated := decorateSenderWithMute(base, mc, "https://dash.example.com")

	err := decorated.SendPurchaseApprovalRequest(ctx, approvalData("approver@example.com"))
	require.NoError(t, err)
	require.Len(t, ses.sent, 1, "non-muted recipient must receive exactly one SendEmail call")

	msg := ses.sent[0].Content.Simple
	require.NotNil(t, msg)
	var foundUnsub bool
	for _, h := range msg.Headers {
		if h.Name != nil && *h.Name == "List-Unsubscribe" {
			foundUnsub = true
			assert.Contains(t, *h.Value, "https://dash.example.com/api/notifications/unsubscribe", "List-Unsubscribe must use the wired dashboard base URL")
			assert.Contains(t, *h.Value, "scope="+string(common.ScopePurchaseApprovals))
		}
	}
	assert.True(t, foundUnsub, "decorated production sender must emit a List-Unsubscribe header")
}
