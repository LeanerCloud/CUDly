package api

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
		Role:   "admin",
	}

	users := []interface{}{
		map[string]interface{}{"id": "11111111-1111-1111-1111-111111111111", "email": "user1@example.com"},
		map[string]interface{}{"id": "22222222-2222-2222-2222-222222222222", "email": "user2@example.com"},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
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

func TestHandler_listUsers_NotAdmin(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	userSession := &Session{
		UserID: "11111111-1111-1111-1111-111111111111",
		Email:  "user@example.com",
		Role:   "user",
	}

	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer user-token",
		},
	}

	result, err := handler.listUsers(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "admin access required")
}

func TestHandler_createUser_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	createdUser := &auth.APIUser{
		ID:    "33333333-3333-3333-3333-333333333333",
		Email: "newuser@example.com",
		Role:  "user",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("CreateUserAPI", ctx, mock.Anything).Return(createdUser, nil)

	handler := &Handler{auth: mockAuth}

	password := base64.StdEncoding.EncodeToString([]byte("password123"))
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"email": "newuser@example.com", "password": "` + password + `", "role": "user"}`,
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
		Role:   "admin",
	}

	user := &User{
		ID:    "11111111-1111-1111-1111-111111111111",
		Email: "user@example.com",
		Role:  "user",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
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
		Role:   "admin",
	}

	updatedUser := map[string]interface{}{
		"id":    "11111111-1111-1111-1111-111111111111",
		"email": "user@example.com",
		"role":  "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("UpdateUserAPI", ctx, "11111111-1111-1111-1111-111111111111", mock.Anything).Return(updatedUser, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"role": "admin"}`,
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
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
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
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

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

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

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

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

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
