package api

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Tests moved from handler_groups_test.go - these test router error handling

func TestHandler_getGroup_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("GetGroupAPI", ctx, "11111111-1111-1111-1111-111111111111").Return(nil, assert.AnError)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}

	result, err := handler.getGroup(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_updateGroup_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("UpdateGroupAPI", ctx, "11111111-1111-1111-1111-111111111111", mock.Anything).Return(nil, assert.AnError)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"name": "Updated Group"}`,
	}

	result, err := handler.updateGroup(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_listGroups_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("ListGroupsAPI", ctx).Return(nil, assert.AnError)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}

	result, err := handler.listGroups(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_createGroup_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("CreateGroupAPI", ctx, mock.Anything).Return(nil, assert.AnError)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"name": "New Group", "permissions": []}`,
	}

	result, err := handler.createGroup(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// Tests for notFoundError type
func TestNotFoundError_Error(t *testing.T) {
	err := &notFoundError{}
	assert.Equal(t, "not found", err.Error())
}

func TestIsNotFoundError_True(t *testing.T) {
	err := errNotFound
	assert.True(t, IsNotFoundError(err))
}

func TestIsNotFoundError_False(t *testing.T) {
	err := assert.AnError
	assert.False(t, IsNotFoundError(err))
}

func TestIsNotFoundError_Nil(t *testing.T) {
	assert.False(t, IsNotFoundError(nil))
}

func TestFormatNotFoundError(t *testing.T) {
	err := formatNotFoundError("GET", "/api/unknown")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GET")
	assert.Contains(t, err.Error(), "/api/unknown")
	assert.Contains(t, err.Error(), "not found")
}

// Tests for clientError type
func TestClientError_Error(t *testing.T) {
	err := &clientError{message: "bad request", code: 400}
	assert.Equal(t, "bad request", err.Error())
}

func TestNewClientError(t *testing.T) {
	err := NewClientError(401, "unauthorized")
	assert.Error(t, err)
	assert.Equal(t, "unauthorized", err.Error())

	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 401, ce.code)
}

func TestIsClientError_True(t *testing.T) {
	err := NewClientError(400, "bad request")
	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Equal(t, "bad request", ce.message)
}

func TestIsClientError_False(t *testing.T) {
	err := assert.AnError
	ce, ok := IsClientError(err)
	assert.False(t, ok)
	assert.Nil(t, ce)
}
