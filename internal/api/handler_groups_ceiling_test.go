package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Handler-level coverage for the group grant ceiling (issues #1550, #1629).
//
// The ceiling itself is enforced in internal/auth; what these tests pin is
// the wiring the auth package cannot see: that the AUTHENTICATED session's
// user ID reaches the service as the actor (a ceiling measured against the
// wrong principal is no ceiling), and that a refusal surfaces as a 403
// naming the permission rather than a generic 500.

const ceilingGroupID = "11111111-1111-1111-1111-111111111111"

func TestUpdateGroup_ThreadsSessionActorAndMaps403(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	session := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(session, nil)
	mockAuth.grantAdmin()

	refusal := fmt.Errorf("%w: execute:purchases is reserved for separation of duties (issue #923) and cannot be granted through the API",
		auth.ErrPermissionNotGrantable)
	// The actor argument is asserted EXACTLY: if updateGroup passed anything
	// other than the validated session's user ID, this expectation would not
	// match and the mock would fail the test.
	mockAuth.On("UpdateGroupAPI", ctx, session.UserID, ceilingGroupID, mock.Anything).
		Return(nil, refusal).Once()

	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"permissions":[{"action":"execute","resource":"purchases"}]}`,
	}

	result, err := h.updateGroup(ctx, req, ceilingGroupID)

	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "a refused grant must map to a client error, not a 500")
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, err.Error(), "execute:purchases")
}

func TestCreateGroup_ThreadsSessionActorAndMaps403(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	session := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(session, nil)
	mockAuth.grantAdmin()

	refusal := fmt.Errorf("%w: cannot grant delete:accounts because your own permissions do not include it (or not at the requested scope)",
		auth.ErrPermissionCeiling)
	mockAuth.On("CreateGroupAPI", ctx, session.UserID, mock.Anything).
		Return(nil, refusal).Once()

	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"name":"Escalated","permissions":[{"action":"delete","resource":"accounts"}]}`,
	}

	result, err := h.createGroup(ctx, req)

	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "a refused grant must map to a client error, not a 500")
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, err.Error(), "delete:accounts")
}

func TestDeleteGroup_SystemManagedMaps403(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	session := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(session, nil)
	mockAuth.grantAdmin()

	refusal := fmt.Errorf("%w: %q is seeded and maintained by migrations", auth.ErrSystemManagedGroup, "Purchaser")
	mockAuth.On("DeleteGroup", ctx, ceilingGroupID).Return(refusal).Once()

	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}

	result, err := h.deleteGroup(ctx, req, ceilingGroupID)

	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "a system-managed refusal must map to a client error, not a 500")
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, err.Error(), "Purchaser")
}

// TestUpdateGroup_AdminAPIKeyActorIsSentinel pins the admin-API-key branch:
// the key has no user row, so the handler must hand the auth service the
// sentinel the ceiling recognizes. Passing an empty string here would make
// the ceiling fail closed on every admin-key group write instead.
func TestUpdateGroup_AdminAPIKeyActorIsSentinel(t *testing.T) {
	assert.Equal(t, auth.AdminAPIKeyActorID, apiKeyAdminUserID,
		"the admin-API-key session identity must be the sentinel auth.grantCeilingPermissions recognizes")

	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	updated := map[string]any{"id": ceilingGroupID}
	mockAuth.On("UpdateGroupAPI", ctx, apiKeyAdminUserID, ceilingGroupID, mock.Anything).
		Return(updated, nil).Once()

	h := &Handler{auth: mockAuth, apiKey: "super-secret-admin-key"}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"x-api-key": "super-secret-admin-key"},
		Body:    `{"name":"Renamed"}`,
	}

	result, err := h.updateGroup(ctx, req, ceilingGroupID)
	require.NoError(t, err)
	assert.Equal(t, updated, result)
}
