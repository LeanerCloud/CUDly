package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingExecutedNotifier captures the NotificationData passed to
// SendPurchaseExecutedNotification so the execution-flow tests can assert the
// post-execution notification (issue #291) fires with the expected recipients
// and body fingerprints. All other SenderInterface methods are no-ops via the
// embedded stubEmailNotifier.
type recordingExecutedNotifier struct {
	stubEmailNotifier
	calls    int
	captured email.NotificationData
}

func (r *recordingExecutedNotifier) SendPurchaseExecutedNotification(_ context.Context, data email.NotificationData) error {
	r.calls++
	r.captured = data
	return nil
}

// assertExecutedNotificationFingerprints asserts the shared invariants of the
// post-execution notification across all three execution paths: it fired
// exactly once, resolved the per-account contact email as the primary To, and
// carried the execution's approval token as the revocation token + the executor
// in the body.
func assertExecutedNotificationFingerprints(t *testing.T, n *recordingExecutedNotifier, contact, executedBy, token string) {
	t.Helper()
	require.Equal(t, 1, n.calls, "SendPurchaseExecutedNotification must fire exactly once")
	assert.Equal(t, contact, n.captured.RecipientEmail,
		"primary To must be the per-account contact email")
	assert.Equal(t, executedBy, n.captured.ExecutedBy,
		"body must record the executing actor")
	assert.Equal(t, token, n.captured.RevocationToken,
		"revocation token reuses the execution approval token (issue #291)")
	assert.NotEmpty(t, n.captured.ExecutedAt, "executed-at timestamp must be set")
}

// TestExecutedNotification_TokenApprovePath covers the email one-click
// (token-authed) approve branch of approvePurchase: after ApproveExecution
// succeeds, sendPurchaseExecutedEmail must fire with the recording notifier.
func TestExecutedNotification_TokenApprovePath(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	contact := "contact@example.com"

	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, contact, mockConfig)
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &contact,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: contact}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-any", "purchases").Return(false, nil).Maybe()
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-own", "purchases").Return(false, nil).Maybe()

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveExecution", ctx, execID, "valid-token", contact).Return(nil)

	notifier := &recordingExecutedNotifier{}
	handler := &Handler{
		purchase:      mockPurchase,
		config:        mockConfig,
		auth:          mockAuth,
		emailNotifier: notifier,
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	result, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.(map[string]string)["status"])

	assertExecutedNotificationFingerprints(t, notifier, contact, contact, "valid-token")
	mockPurchase.AssertExpectations(t)
}

// TestExecutedNotification_SessionApprovePath covers the dashboard
// (session-authed) approve branch via approvePurchaseViaSession: after
// ApproveAndExecute succeeds, the notification must fire.
func TestExecutedNotification_SessionApprovePath(t *testing.T) {
	ctx := context.Background()
	execID := "23456789-2345-2345-2345-23456789abcd"
	adminEmail := "admin@example.com"
	contact := "contact@example.com"

	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, contact, mockConfig)
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &contact,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: adminEmail}, nil)
	mockAuth.grantAdmin()
	mockAuth.On("ValidateCSRFToken", ctx, "sess-tok", "").Return(nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveAndExecute", ctx, execID, adminEmail, (*string)(nil)).Return(nil)

	notifier := &recordingExecutedNotifier{}
	handler := &Handler{
		purchase:      mockPurchase,
		config:        mockConfig,
		auth:          mockAuth,
		emailNotifier: notifier,
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	// Empty token forces the dashboard (session) branch.
	result, err := handler.approvePurchase(ctx, req, execID, "")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.(map[string]string)["status"])

	// Admin approved, so the executor recorded in the body is the admin.
	assertExecutedNotificationFingerprints(t, notifier, contact, adminEmail, "valid-token")
	mockPurchase.AssertExpectations(t)
	mockPurchase.AssertNotCalled(t, "ApproveExecution")
}

// TestExecutedNotification_DirectExecutePath is the regression test for the
// adversarial-verification blocker: the direct-execute path (issue #289) sent
// NO notification at all before the #291 wiring. After ApproveAndExecute
// succeeds, directExecutePurchase must fire the post-execution notification --
// this is the only email the direct-execute flow emits.
func TestExecutedNotification_DirectExecutePath(t *testing.T) {
	ctx := context.Background()
	execID := "34567890-3456-3456-3456-34567890abcd"
	adminEmail := "admin@example.com"
	contact := "contact@example.com"
	accountID := "acct-1"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: contact}, nil
	}
	// directExecutePurchase stamps audit fields, then sendPurchaseExecutedEmail
	// reads the global config.
	mockConfig.On("SavePurchaseExecution", ctx, exec).Return(nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &contact,
	}, nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveAndExecute", ctx, execID, adminEmail, (*string)(nil)).Return(nil)

	notifier := &recordingExecutedNotifier{}
	handler := &Handler{
		purchase:      mockPurchase,
		config:        mockConfig,
		emailNotifier: notifier,
		// auth left nil: lookupRequesterInfo tolerates a nil auth and the
		// execution has no CreatedByUserID, so the requester lookup is skipped.
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	session := &Session{Email: adminEmail, UserID: "admin-uid"}

	result, err := handler.directExecutePurchase(ctx, req, exec, session)
	require.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, "completed", resultMap["status"])
	assert.Equal(t, true, resultMap["direct_execute"])

	assertExecutedNotificationFingerprints(t, notifier, contact, adminEmail, "valid-token")
	mockPurchase.AssertExpectations(t)
}

// TestExecutedNotification_DirectExecute_NilNotifierNoPanic guards the
// best-effort contract: a direct-execute with no email notifier configured
// must still complete the purchase without panicking.
func TestExecutedNotification_DirectExecute_NilNotifierNoPanic(t *testing.T) {
	ctx := context.Background()
	execID := "45678901-4567-4567-4567-45678901abcd"
	adminEmail := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1"},
		},
	}
	mockConfig.On("SavePurchaseExecution", ctx, exec).Return(nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveAndExecute", ctx, execID, adminEmail, (*string)(nil)).Return(nil)

	handler := &Handler{
		purchase:      mockPurchase,
		config:        mockConfig,
		emailNotifier: nil, // best-effort send is skipped
	}

	req := &events.LambdaFunctionURLRequest{}
	session := &Session{Email: adminEmail, UserID: "admin-uid"}

	result, err := handler.directExecutePurchase(ctx, req, exec, session)
	require.NoError(t, err)
	assert.Equal(t, "completed", result.(map[string]any)["status"])
	mockPurchase.AssertExpectations(t)
}
