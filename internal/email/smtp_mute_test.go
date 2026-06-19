package email

import (
	"context"
	"strings"
	"testing"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// smtpApprovalData builds a minimal purchase-approval NotificationData.
func smtpApprovalData(recipient string) NotificationData {
	return NotificationData{
		RecipientEmail: recipient,
		DashboardURL:   "https://dash.example.com",
		ApprovalToken:  "tok",
		Recommendations: []RecommendationSummary{
			{Service: "ec2", Region: "us-east-1", Count: 1, MonthlySavings: 100},
		},
	}
}

// TestSMTPSender_PurchaseApproval_MutedRecipient_NoSend verifies the SMTP
// transport applies the same per-recipient mute suppression as SES: a muted
// recipient never reaches the wire (no DATA section is transmitted).
func TestSMTPSender_PurchaseApproval_MutedRecipient_NoSend(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	base := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		fromEmail: "sender@test.com",
		useTLS:    false,
	}
	mc := &wiringMuteCheckerSMTP{muted: map[string]bool{"muted@test.com": true}}
	sender := base.WithMuteChecker(mc).WithUnsubscribeBaseURL("https://dash.example.com")

	err := sender.SendPurchaseApprovalRequest(context.Background(), smtpApprovalData("muted@test.com"))
	require.NoError(t, err)

	server.stop() // flush the connection goroutine before reading receivedMsg
	server.mu.Lock()
	got := server.receivedMsg
	server.mu.Unlock()
	assert.NotContains(t, got, "Purchase Approval Required",
		"muted recipient must not have a message body transmitted over SMTP")
}

// TestSMTPSender_PurchaseApproval_EmitsListUnsubscribe verifies the SMTP
// approval send attaches the RFC 8058 List-Unsubscribe header sourced from the
// wired-in unsubscribe base URL.
func TestSMTPSender_PurchaseApproval_EmitsListUnsubscribe(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	base := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		fromEmail: "sender@test.com",
		useTLS:    false,
	}
	mc := &wiringMuteCheckerSMTP{muted: map[string]bool{}}
	sender := base.WithMuteChecker(mc).WithUnsubscribeBaseURL("https://dash.example.com")

	err := sender.SendPurchaseApprovalRequest(context.Background(), smtpApprovalData("approver@test.com"))
	require.NoError(t, err)

	server.stop()
	server.mu.Lock()
	got := server.receivedMsg
	server.mu.Unlock()
	assert.Contains(t, got, "List-Unsubscribe:",
		"SMTP approval send must carry a List-Unsubscribe header")
	assert.Contains(t, got, "https://dash.example.com/api/notifications/unsubscribe")
	assert.Contains(t, got, "scope="+string(common.ScopePurchaseApprovals))
	assert.True(t, strings.Contains(got, "List-Unsubscribe-Post:"),
		"SMTP approval send must carry a List-Unsubscribe-Post header")
}

// TestSMTPSender_PurchaseApproval_WithCC_SuppressesListUnsubscribe verifies the
// SMTP transport suppresses the primary-recipient-bound List-Unsubscribe header
// when CC recipients share the envelope, mirroring the SES path.
func TestSMTPSender_PurchaseApproval_WithCC_SuppressesListUnsubscribe(t *testing.T) {
	server := newMockSMTPServer(t, false)
	server.start(t)
	defer server.stop()

	base := &SMTPSender{
		host:      "127.0.0.1",
		port:      server.port,
		fromEmail: "sender@test.com",
		useTLS:    false,
	}
	mc := &wiringMuteCheckerSMTP{muted: map[string]bool{}}
	sender := base.WithMuteChecker(mc).WithUnsubscribeBaseURL("https://dash.example.com")

	data := smtpApprovalData("approver@test.com")
	data.CCEmails = []string{"observer@test.com"}
	require.NoError(t, sender.SendPurchaseApprovalRequest(context.Background(), data))

	server.stop()
	server.mu.Lock()
	got := server.receivedMsg
	server.mu.Unlock()
	assert.NotContains(t, got, "List-Unsubscribe:",
		"List-Unsubscribe must be suppressed when CC recipients are present")
}

type wiringMuteCheckerSMTP struct {
	muted map[string]bool
}

func (w *wiringMuteCheckerSMTP) IsNotificationMuted(_ context.Context, recipientEmail, _ string) (bool, error) {
	return w.muted[recipientEmail], nil
}
