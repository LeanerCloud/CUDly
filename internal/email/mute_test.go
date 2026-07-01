package email

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Stub MuteChecker
// ---------------------------------------------------------------------------

type mockMuteChecker struct {
	mock.Mock
}

func (m *mockMuteChecker) IsNotificationMuted(ctx context.Context, email, scope string) (bool, error) {
	args := m.Called(ctx, email, scope)
	return args.Bool(0), args.Error(1)
}

// ---------------------------------------------------------------------------
// SendPurchaseApprovalRequest + mute check
// ---------------------------------------------------------------------------

// newSenderWithMute builds a testable *Sender with a mock SES client and a
// mock MuteChecker, bypassing sandbox checks by having GetAccount return
// production mode.
func newSenderWithMute(ses *MockSESClient, mc *mockMuteChecker) *Sender {
	// GetAccount returning ProductionAccessEnabled=true means no sandbox path.
	ses.On("GetAccount", mock.Anything, mock.Anything).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil).Maybe()
	return &Sender{
		sesClient:   ses,
		fromEmail:   "noreply@example.com",
		muteChecker: mc,
	}
}

func TestSendPurchaseApprovalRequest_MutedRecipient_NoSESCall(t *testing.T) {
	ctx := context.Background()
	ses := new(MockSESClient)
	mc := new(mockMuteChecker)

	mc.On("IsNotificationMuted", mock.Anything, "approver@example.com", string(common.ScopePurchaseApprovals)).
		Return(true, nil)
	t.Cleanup(func() {
		mc.AssertExpectations(t)
		ses.AssertNotCalled(t, "SendEmail")
	})

	s := newSenderWithMute(ses, mc)

	data := NotificationData{
		RecipientEmail: "approver@example.com",
		Recommendations: []RecommendationSummary{
			{Service: "ec2", Region: "us-east-1", Count: 1, MonthlySavings: 100},
		},
		DashboardURL:  "https://dashboard.example.com",
		ApprovalToken: "tok",
	}
	err := s.SendPurchaseApprovalRequest(ctx, data)
	require.NoError(t, err)
}

func TestSendPurchaseApprovalRequest_NotMuted_SendsEmail(t *testing.T) {
	ctx := context.Background()
	ses := new(MockSESClient)
	mc := new(mockMuteChecker)

	mc.On("IsNotificationMuted", mock.Anything, "approver@example.com", string(common.ScopePurchaseApprovals)).
		Return(false, nil)
	mc.On("IsNotificationMuted", mock.Anything, mock.Anything, mock.Anything).
		Return(false, nil).Maybe() // for CC filter if any
	ses.On("GetAccount", mock.Anything, mock.Anything).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	ses.On("SendEmail", mock.Anything, mock.Anything).
		Return(&sesv2.SendEmailOutput{}, nil)
	t.Cleanup(func() {
		mc.AssertExpectations(t)
		ses.AssertExpectations(t)
	})

	s := newSenderWithMute(ses, mc)
	data := NotificationData{
		RecipientEmail: "approver@example.com",
		Recommendations: []RecommendationSummary{
			{Service: "ec2", Region: "us-east-1", Count: 1, MonthlySavings: 100},
		},
		DashboardURL:  "https://dashboard.example.com",
		ApprovalToken: "tok",
	}
	err := s.SendPurchaseApprovalRequest(ctx, data)
	require.NoError(t, err)
	ses.AssertCalled(t, "SendEmail", mock.Anything, mock.Anything)
}

func TestSendPurchaseApprovalRequest_MuteCheckError_FailOpen(t *testing.T) {
	// When the mute store returns an error, the email is still sent (fail-open
	// so a DB hiccup doesn't permanently block approval notifications).
	ctx := context.Background()
	ses := new(MockSESClient)
	mc := new(mockMuteChecker)

	mc.On("IsNotificationMuted", mock.Anything, "approver@example.com", string(common.ScopePurchaseApprovals)).
		Return(false, errors.New("db error"))
	mc.On("IsNotificationMuted", mock.Anything, mock.Anything, mock.Anything).
		Return(false, nil).Maybe()
	ses.On("GetAccount", mock.Anything, mock.Anything).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)
	ses.On("SendEmail", mock.Anything, mock.Anything).
		Return(&sesv2.SendEmailOutput{}, nil)
	t.Cleanup(func() {
		ses.AssertCalled(t, "SendEmail", mock.Anything, mock.Anything)
	})

	s := newSenderWithMute(ses, mc)
	data := NotificationData{
		RecipientEmail: "approver@example.com",
		Recommendations: []RecommendationSummary{
			{Service: "ec2", Region: "us-east-1", Count: 1, MonthlySavings: 100},
		},
		DashboardURL:  "https://dashboard.example.com",
		ApprovalToken: "tok",
	}
	err := s.SendPurchaseApprovalRequest(ctx, data)
	require.NoError(t, err)
}

// TestSendPurchaseApprovalRequest_WithCC_SuppressesListUnsubscribe verifies the
// List-Unsubscribe header (whose token is bound to the primary recipient) is NOT
// emitted when the message also goes to CC recipients. A shared-envelope CC
// recipient could otherwise one-click-mute the primary recipient.
func TestSendPurchaseApprovalRequest_WithCC_SuppressesListUnsubscribe(t *testing.T) {
	ctx := context.Background()
	ses := new(MockSESClient)
	mc := new(mockMuteChecker)

	mc.On("IsNotificationMuted", mock.Anything, mock.Anything, mock.Anything).
		Return(false, nil)
	ses.On("GetAccount", mock.Anything, mock.Anything).
		Return(&sesv2.GetAccountOutput{ProductionAccessEnabled: true}, nil)

	var captured *sesv2.SendEmailInput
	ses.On("SendEmail", mock.Anything, mock.MatchedBy(func(in *sesv2.SendEmailInput) bool {
		captured = in
		return true
	})).Return(&sesv2.SendEmailOutput{}, nil)
	t.Cleanup(func() { mc.AssertExpectations(t) })

	s := newSenderWithMute(ses, mc).WithUnsubscribeBaseURL("https://dash.example.com")
	data := NotificationData{
		RecipientEmail: "approver@example.com",
		CCEmails:       []string{"observer@example.com"},
		Recommendations: []RecommendationSummary{
			{Service: "ec2", Region: "us-east-1", Count: 1, MonthlySavings: 100},
		},
		DashboardURL:  "https://dashboard.example.com",
		ApprovalToken: "tok",
	}
	require.NoError(t, s.SendPurchaseApprovalRequest(ctx, data))
	require.NotNil(t, captured)
	require.NotNil(t, captured.Content.Simple)
	for _, h := range captured.Content.Simple.Headers {
		if h.Name != nil {
			assert.NotEqual(t, "List-Unsubscribe", *h.Name,
				"List-Unsubscribe must be suppressed when CC recipients are present")
		}
	}
}

// ---------------------------------------------------------------------------
// List-Unsubscribe header injection
// ---------------------------------------------------------------------------

func TestBuildUnsubscribeURL_EmptyBaseURL_ReturnsEmpty(t *testing.T) {
	s := &Sender{}
	u, _ := s.buildUnsubscribeURL("user@example.com", "purchase_approvals")
	assert.Empty(t, u)
}

func TestBuildUnsubscribeURL_WithBaseURL_ContainsParams(t *testing.T) {
	s := &Sender{unsubscribeBaseURL: "https://dash.example.com"}
	u, _ := s.buildUnsubscribeURL("user@example.com", "purchase_approvals")
	assert.Contains(t, u, "email=user%40example.com")
	assert.Contains(t, u, "scope=purchase_approvals")
	assert.Contains(t, u, "token=")
}

func TestListUnsubscribeHeaders_EmptyBase_ReturnsEmpty(t *testing.T) {
	s := &Sender{}
	hdr, post := s.listUnsubscribeHeaders("u@e.com", "purchase_approvals")
	assert.Empty(t, hdr)
	assert.Empty(t, post)
}

func TestListUnsubscribeHeaders_WithBase(t *testing.T) {
	s := &Sender{unsubscribeBaseURL: "https://dash.example.com"}
	hdr, post := s.listUnsubscribeHeaders("u@e.com", "purchase_approvals")
	assert.Contains(t, hdr, "<https://dash.example.com/api/notifications/unsubscribe?")
	assert.Equal(t, "List-Unsubscribe=One-Click", post)
}

func TestAddListUnsubscribeHeaders_EmptyValue_ReturnsNil(t *testing.T) {
	hdrs := addListUnsubscribeHeaders("", "")
	assert.Nil(t, hdrs)
}

func TestAddListUnsubscribeHeaders_WithValues(t *testing.T) {
	hdrs := addListUnsubscribeHeaders("<https://example.com/unsub>", "List-Unsubscribe=One-Click")
	require.Len(t, hdrs, 2)
	assert.Equal(t, "List-Unsubscribe", *hdrs[0].Name)
	assert.Equal(t, "List-Unsubscribe-Post", *hdrs[1].Name)
	assert.Equal(t, "List-Unsubscribe=One-Click", *hdrs[1].Value)
}

// ---------------------------------------------------------------------------
// DeriveMuteToken / VerifyMuteToken
// ---------------------------------------------------------------------------

func TestDeriveMuteToken_Stable(t *testing.T) {
	key := []byte("test-secret")
	t1 := common.DeriveMuteToken(key, "user@example.com", "purchase_approvals")
	t2 := common.DeriveMuteToken(key, "user@example.com", "purchase_approvals")
	assert.Equal(t, t1, t2)
}

func TestDeriveMuteToken_CaseInsensitive(t *testing.T) {
	key := []byte("test-secret")
	lower := common.DeriveMuteToken(key, "user@example.com", "purchase_approvals")
	upper := common.DeriveMuteToken(key, "USER@EXAMPLE.COM", "purchase_approvals")
	assert.Equal(t, lower, upper, "token must be case-insensitive on email")
}

func TestDeriveMuteToken_DiffersByScope(t *testing.T) {
	key := []byte("test-secret")
	t1 := common.DeriveMuteToken(key, "user@example.com", "purchase_approvals")
	t2 := common.DeriveMuteToken(key, "user@example.com", "ri_exchange_approvals")
	assert.NotEqual(t, t1, t2)
}

func TestVerifyMuteToken_Valid(t *testing.T) {
	key := []byte("test-secret")
	tok := common.DeriveMuteToken(key, "user@example.com", "purchase_approvals")
	assert.True(t, common.VerifyMuteToken(key, "user@example.com", "purchase_approvals", tok))
}

func TestVerifyMuteToken_Forged(t *testing.T) {
	key := []byte("test-secret")
	assert.False(t, common.VerifyMuteToken(key, "user@example.com", "purchase_approvals", "forgedtoken"))
}
