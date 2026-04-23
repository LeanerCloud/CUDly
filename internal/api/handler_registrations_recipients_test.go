package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_resolveRegistrationRecipients_AdminsBecomeApprovers(t *testing.T) {
	ctx := context.Background()
	globalNotify := "global@cudly.example"

	mockAuth := new(MockAuthService)
	mockAuth.On("ListUsersAPI", ctx).Return([]*auth.APIUser{
		{Email: "admin-a@example.com", Role: "admin"},
		{Email: "user@example.com", Role: "user"}, // non-admin → filtered out
		{Email: "admin-b@example.com", Role: "admin"},
		{Email: "ADMIN-A@example.com", Role: "admin"}, // case dupe → dropped
	}, nil)

	mockConfig := new(MockConfigStore)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &globalNotify,
	}, nil)

	h := &Handler{auth: mockAuth, config: mockConfig}

	to, cc, approvers := h.resolveRegistrationRecipients(ctx)
	assert.Equal(t, "admin-a@example.com", to, "first admin becomes To")
	assert.Equal(t, []string{"admin-b@example.com", globalNotify}, cc,
		"other admins + global notify go on Cc")
	assert.Equal(t, []string{"admin-a@example.com", "admin-b@example.com"}, approvers,
		"admin role users are the authorised reviewers; non-admins stripped")
}

func TestHandler_resolveRegistrationRecipients_NoAdminsTriggersBroadcastFallback(t *testing.T) {
	ctx := context.Background()
	globalNotify := "global@cudly.example"

	mockAuth := new(MockAuthService)
	mockAuth.On("ListUsersAPI", ctx).Return([]*auth.APIUser{
		{Email: "user@example.com", Role: "user"},
	}, nil)

	mockConfig := new(MockConfigStore)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &globalNotify,
	}, nil)

	h := &Handler{auth: mockAuth, config: mockConfig}

	to, cc, approvers := h.resolveRegistrationRecipients(ctx)
	// Legacy SNS broadcast path — caller interprets ("", nil, nil) as
	// "use the static notify email".
	assert.Empty(t, to)
	assert.Empty(t, cc)
	assert.Empty(t, approvers)
}

func TestHandler_resolveRegistrationRecipients_OmitsGlobalWhenAlreadyAdmin(t *testing.T) {
	// The global notification email might itself be one of the admin
	// accounts — in that case it should not appear twice (once as an
	// admin To/Cc entry, once as the explicit global-notify Cc).
	ctx := context.Background()
	globalNotify := "admin-a@example.com"

	mockAuth := new(MockAuthService)
	mockAuth.On("ListUsersAPI", ctx).Return([]*auth.APIUser{
		{Email: "admin-a@example.com", Role: "admin"},
	}, nil)

	mockConfig := new(MockConfigStore)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &globalNotify,
	}, nil)

	h := &Handler{auth: mockAuth, config: mockConfig}

	to, cc, approvers := h.resolveRegistrationRecipients(ctx)
	assert.Equal(t, "admin-a@example.com", to)
	assert.Empty(t, cc, "global notify that matches the admin email must not re-appear on Cc")
	assert.Equal(t, []string{"admin-a@example.com"}, approvers)
}

func TestHandler_gatherAdminEmails_NilAuth(t *testing.T) {
	h := &Handler{} // no auth configured
	emails := h.gatherAdminEmails(context.Background())
	require.Empty(t, emails)
}
