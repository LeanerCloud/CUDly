package apihttp

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandler_listUsers_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	users := []interface{}{
		map[string]interface{}{"id": "11111111-1111-1111-1111-111111111111", "email": "user1@example.com"},
		map[string]interface{}{"id": "22222222-2222-2222-2222-222222222222", "email": "user2@example.com"},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockAuth.On("ListUsersAPI", ctx).Return(users, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}

	result, err := handler.listUsers(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]interface{})
	assert.NotNil(t, resp["users"])
}

func TestHandler_listUsers_NoPermission(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	userSession := &Session{
		UserID: "11111111-1111-1111-1111-111111111111",
		Email:  "user@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	mockAuth.On("HasPermissionAPI", ctx, "11111111-1111-1111-1111-111111111111", "view", "users").Return(false, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer user-token",
		},
	}

	result, err := handler.listUsers(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestHandler_createUser_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	createdUser := &auth.APIUser{
		ID:     "33333333-3333-3333-3333-333333333333",
		Email:  "newuser@example.com",
		Groups: []string{"00000000-0000-5000-8000-000000000005"},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockAuth.On("CreateUserAPI", ctx, mock.Anything).Return(createdUser, nil)

	handler := &Handler{auth: mockAuth}

	password := base64.StdEncoding.EncodeToString([]byte("password123"))
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"email": "newuser@example.com", "password": "` + password + `", "groups": ["00000000-0000-5000-8000-000000000005"]}`,
	}

	result, err := handler.createUser(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestHandler_getUser_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	user := &User{
		ID:     "11111111-1111-1111-1111-111111111111",
		Email:  "user@example.com",
		Groups: []string{"00000000-0000-5000-8000-000000000005"},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockAuth.On("GetUser", ctx, "11111111-1111-1111-1111-111111111111").Return(user, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}

	result, err := handler.getUser(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	userResult := result.(*User)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", userResult.ID)
}

func TestHandler_updateUser_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	updatedUser := map[string]interface{}{
		"id":     "11111111-1111-1111-1111-111111111111",
		"email":  "user@example.com",
		"groups": []string{"00000000-0000-5000-8000-000000000001"},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockAuth.On("UpdateUserAPI", ctx, adminSession.UserID, "11111111-1111-1111-1111-111111111111", mock.Anything).Return(updatedUser, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"groups": ["00000000-0000-5000-8000-000000000001"]}`,
	}

	result, err := handler.updateUser(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestHandler_deleteUser_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockAuth.On("DeleteUser", ctx, "22222222-2222-2222-2222-222222222222").Return(nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}

	result, err := handler.deleteUser(ctx, req, "22222222-2222-2222-2222-222222222222")
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "user deleted", resp["status"])
}

func TestHandler_deleteUser_SelfDeletion(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}

	result, err := handler.deleteUser(ctx, req, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot delete your own account")
}

// Group management endpoint tests
func TestHandler_createUser_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{invalid json}`,
	}

	result, err := handler.createUser(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_updateUser_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{invalid json}`,
	}

	result, err := handler.updateUser(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}
