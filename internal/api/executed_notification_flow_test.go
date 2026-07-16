package api

import (
	"context"
	"errors"
	"testing"
	"time"

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
// carried the expected revocation token + the executor in the body.
//
// For the token-authed approve path, expectedToken is the fresh revocation
// token minted by mintRevocationToken (obtained from the re-fetched execution).
// For the session-approve and direct-execute paths, it is the original
// approval token (those paths do not rotate the token).
func assertExecutedNotificationFingerprints(t *testing.T, n *recordingExecutedNotifier, contact, executedBy, expectedToken string) {
	t.Helper()
	require.Equal(t, 1, n.calls, "SendPurchaseExecutedNotification must fire exactly once")
	assert.Equal(t, contact, n.captured.RecipientEmail,
		"primary To must be the per-account contact email")
	assert.Equal(t, executedBy, n.captured.ExecutedBy,
		"body must record the executing actor")
	assert.Equal(t, expectedToken, n.captured.RevocationToken,
		"email must carry the expected revocation token")
	assert.NotEmpty(t, n.captured.ExecutedAt, "executed-at timestamp must be set")
}

// TestExecutedNotification_TokenApprovePath covers the email one-click
// (token-authed) approve branch of approvePurchase: after ApproveExecution
// succeeds, sendPurchaseExecutedEmail must fire with the fresh revocation
// token from the re-fetched execution, not the stale pre-approve token.
//
// This is the regression test for the defect where approveViaToken passed
// the stale pre-approve execution struct to sendPurchaseExecutedEmail.
// mintRevocationToken (called inside ApproveExecution) had already overwritten
// ApprovalToken in the DB, so the email embedded the old consumed token which
// validateRevokeToken rejected with 403 on every revoke attempt.
//
// Fail-before: without the re-fetch, GetExecutionByID is called only once
// (the second .Once() expectation goes unconsumed), the email carries the
// stale "valid-token", and the RevocationToken assertion fails.
// Pass-after: approveViaToken re-fetches, both .Once() expectations are
// consumed, and the email carries the fresh "fresh-revoke-token".
func TestExecutedNotification_TokenApprovePath(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	contact := "contact@example.com"
	freshToken := "fresh-revoke-token"
	accountID := "acct-1"
	recentCompleted := time.Now().Add(-1 * time.Minute)

	// pre-approve: pending execution with the original approval token.
	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, contact, mockConfig)

	// post-approve: completed execution with the fresh revocation token written
	// by mintRevocationToken. Recommendations must be present so
	// gatherAccountContactEmails can resolve the contact email via GetCloudAccountFn.
	freshExec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: freshToken,
		Status:        "completed",
		CompletedAt:   &recentCompleted,
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}

	// First call: loadApproveExecution fetches the pending execution.
	// Second call: approveViaToken re-fetches after ApproveExecution returns to
	// pick up the fresh revocation token written by mintRevocationToken.
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil).Once()
	mockConfig.On("GetExecutionByID", ctx, execID).Return(freshExec, nil).Once()
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

	// The email must carry the fresh revocation token from the re-fetched row,
	// not the stale "valid-token" from the pre-approve struct.
	assertExecutedNotificationFingerprints(t, notifier, contact, contact, freshToken)

	// Confirm the fresh token is actually valid for revocation: validateRevokeToken
	// against the post-approve execution must succeed. This guards the end-to-end
	// scenario: recipient clicks "Revoke" in the email -> token validates -> 200.
	require.NoError(t, validateRevokeToken(freshExec, freshToken),
		"the token embedded in the email must pass validateRevokeToken on the post-approve execution")

	mockPurchase.AssertExpectations(t)
	mockConfig.AssertExpectations(t)
}

// TestExecutedNotification_TokenApprovePath_RefetchFailureSuppressesPanel is the
// regression test for the degraded-path nit: when the post-approve re-fetch
// fails, approveViaToken must NOT email the stale pre-approve execution struct
// (which still carries the OLD approval token that mintRevocationToken has
// already replaced in the DB -- that token would 403 on every Revoke click,
// resurrecting the original defect). Instead the email must carry an EMPTY
// RevocationToken so the email template's {{if .RevocationToken}} suppresses the
// Revoke panel entirely (no broken button).
func TestExecutedNotification_TokenApprovePath_RefetchFailureSuppressesPanel(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	contact := "contact@example.com"

	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, contact, mockConfig)

	// First call: loadApproveExecution fetches the pending execution.
	// Second call: approveViaToken re-fetches after ApproveExecution returns,
	// but this time the store errors -- the fresh token cannot be obtained.
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil).Once()
	mockConfig.On("GetExecutionByID", ctx, execID).
		Return(nil, errors.New("transient store failure")).Once()
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
	require.NoError(t, err, "approve must still succeed even when the email re-fetch fails")
	assert.Equal(t, "completed", result.(map[string]string)["status"])

	// The notification still fires (best-effort), but with an EMPTY revocation
	// token so the Revoke panel is suppressed rather than showing a broken button.
	require.Equal(t, 1, notifier.calls, "notification must still fire on the degraded path")
	assert.Empty(t, notifier.captured.RevocationToken,
		"re-fetch failure must blank the revocation token (suppress panel), never email the stale token")

	// The stale pre-approve struct must be untouched: blanking happens on a COPY.
	assert.Equal(t, "valid-token", exec.ApprovalToken,
		"the fallback must blank a COPY, not mutate the caller's execution struct")

	mockPurchase.AssertExpectations(t)
	mockConfig.AssertExpectations(t)
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
