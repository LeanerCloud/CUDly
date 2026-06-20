package email

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNopSender_AllMethodsReturnNil verifies that every SenderInterface
// method on NopSender returns nil - the contract is "swallow the call,
// never error". A future bug that returns an error from any of these
// would propagate into the application path that called the sender,
// breaking the EMAIL_ENABLED=false promise that no work happens.
func TestNopSender_AllMethodsReturnNil(t *testing.T) {
	n := NewNopSender()
	require.NotNil(t, n, "NewNopSender must return a non-nil instance")

	ctx := context.Background()

	assert.NoError(t, n.SendNotification(ctx, "subj", "msg"))
	assert.NoError(t, n.SendToEmail(ctx, "to@example.com", "subj", "body"))
	assert.NoError(t, n.SendToEmailWithCCMultipart(ctx, "to@example.com",
		[]string{"cc1@example.com", "cc2@example.com"}, "subj", "text", "<html/>"))
	assert.NoError(t, n.SendNewRecommendationsNotification(ctx, &NotificationData{}))
	assert.NoError(t, n.SendScheduledPurchaseNotification(ctx, &NotificationData{}))
	assert.NoError(t, n.SendPurchaseConfirmation(ctx, &NotificationData{}))
	assert.NoError(t, n.SendPurchaseFailedNotification(ctx, &NotificationData{}))
	assert.NoError(t, n.SendPasswordResetEmail(ctx, "user@example.com", "https://example/reset"))
	assert.NoError(t, n.SendWelcomeEmail(ctx, "user@example.com", "https://example/dashboard", "admin"))
	assert.NoError(t, n.SendRIExchangePendingApproval(ctx, &RIExchangeNotificationData{}))
	assert.NoError(t, n.SendRIExchangeCompleted(ctx, &RIExchangeNotificationData{}))
	assert.NoError(t, n.SendPurchaseApprovalRequest(ctx, &NotificationData{}))
	assert.NoError(t, n.SendRegistrationReceivedNotification(ctx, &RegistrationNotificationData{}))
	assert.NoError(t, n.SendRegistrationDecisionNotification(ctx, "user@example.com", &RegistrationDecisionData{}))
}

// TestNopSender_NilSafe verifies the no-op tolerates the nil/empty
// inputs that callers may legitimately pass when the data isn't
// available (e.g. SendToEmail with empty CC list, empty body). A
// regression that nil-derefs would surface a different failure mode
// than the silent-swallow contract.
func TestNopSender_NilSafe(t *testing.T) {
	n := NewNopSender()
	ctx := context.Background()

	assert.NoError(t, n.SendToEmailWithCCMultipart(ctx, "", nil, "", "", ""))
	assert.NoError(t, n.SendNotification(ctx, "", ""))
}

// TestNopSender_NilContext asserts the no-op doesn't deref ctx. Callers
// in dev paths may not set up a context (e.g. background scheduled tasks
// during startup); the no-op must not be the thing that surfaces a panic.
func TestNopSender_NilContext(t *testing.T) {
	n := NewNopSender()

	var nilCtx context.Context
	assert.NoError(t, n.SendToEmail(nilCtx, "to@example.com", "s", "b"))
}

// TestNopSender_SatisfiesInterface is a runtime echo of the compile-time
// guard in nop_sender.go. Useful for catching the case where the compile
// guard is silently removed by a refactor - the test would still fail
// because Go interfaces are structurally typed but require the methods
// to be defined on the concrete type.
func TestNopSender_SatisfiesInterface(t *testing.T) {
	var s SenderInterface = NewNopSender()
	assert.NotNil(t, s)
}
