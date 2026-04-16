package api

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestHandler_getCredentialsStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("no credentials configured", func(t *testing.T) {
		handler := &Handler{}

		status := handler.getCredentialsStatus(ctx)

		assert.False(t, status.AzureConfigured)
		assert.False(t, status.GCPConfigured)
	})

	// Note: Testing with actual credentials requires mocking the AWS SDK
	// which is complex. The getCredentialsStatus function depends on
	// getSecretValue which makes real AWS calls.
}

func TestHandler_getSecretValue_EmptyARN(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	_, err := handler.getSecretValue(ctx, "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "secret ARN is empty")
}

func TestHandler_updateSecretValue_EmptyARN(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	err := handler.updateSecretValue(ctx, "", "test-value")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "secret ARN is empty")
}

func TestHandler_saveAzureCredentials(t *testing.T) {
	ctx := context.Background()

	t.Run("no auth service", func(t *testing.T) {
		handler := &Handler{
			azureCredsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:azure-creds",
		}

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{},
			Body:    `{"tenant_id": "test", "client_id": "test", "client_secret": "test", "subscription_id": "test"}`,
		}

		_, err := handler.saveAzureCredentials(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "authentication service not configured")
	})

	t.Run("no permission", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		userSession := &Session{UserID: "user-id", Email: "user@example.com", Role: "user"}
		mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
		mockAuth.On("HasPermissionAPI", ctx, "user-id", "update", "config").Return(false, nil)

		handler := &Handler{
			auth:          mockAuth,
			azureCredsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:azure-creds",
		}

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{
				"Authorization": "Bearer user-token",
			},
			Body: `{"tenant_id": "test", "client_id": "test", "client_secret": "test", "subscription_id": "test"}`,
		}

		_, err := handler.saveAzureCredentials(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "permission denied")
	})

	t.Run("no Azure credentials ARN configured", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

		handler := &Handler{
			auth:          mockAuth,
			azureCredsARN: "", // Not configured
		}

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{
				"Authorization": "Bearer admin-token",
			},
			Body: `{"tenant_id": "test", "client_id": "test", "client_secret": "test", "subscription_id": "test"}`,
		}

		_, err := handler.saveAzureCredentials(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Azure credentials secret is not configured")
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

		handler := &Handler{
			auth:          mockAuth,
			azureCredsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:azure-creds",
		}

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{
				"Authorization": "Bearer admin-token",
			},
			Body: `invalid json`,
		}

		_, err := handler.saveAzureCredentials(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid request body")
	})

	t.Run("missing required fields", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

		handler := &Handler{
			auth:          mockAuth,
			azureCredsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:azure-creds",
		}

		testCases := []struct {
			name string
			body string
		}{
			{"missing tenant_id", `{"client_id": "test", "client_secret": "test", "subscription_id": "test"}`},
			{"missing client_id", `{"tenant_id": "test", "client_secret": "test", "subscription_id": "test"}`},
			{"missing client_secret", `{"tenant_id": "test", "client_id": "test", "subscription_id": "test"}`},
			{"missing subscription_id", `{"tenant_id": "test", "client_id": "test", "client_secret": "test"}`},
			{"all empty", `{}`},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				req := &events.LambdaFunctionURLRequest{
					Headers: map[string]string{
						"Authorization": "Bearer admin-token",
					},
					Body: tc.body,
				}

				_, err := handler.saveAzureCredentials(ctx, req)

				assert.Error(t, err)
				assert.Contains(t, err.Error(), "all fields are required")
			})
		}
	})
}

func TestHandler_saveGCPCredentials(t *testing.T) {
	ctx := context.Background()

	t.Run("no auth service", func(t *testing.T) {
		handler := &Handler{
			gcpCredsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:gcp-creds",
		}

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{},
			Body:    `{"type": "service_account", "project_id": "test", "private_key": "test", "client_email": "test@test.iam.gserviceaccount.com"}`,
		}

		_, err := handler.saveGCPCredentials(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "authentication service not configured")
	})

	t.Run("no permission", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		userSession := &Session{UserID: "user-id", Email: "user@example.com", Role: "user"}
		mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
		mockAuth.On("HasPermissionAPI", ctx, "user-id", "update", "config").Return(false, nil)

		handler := &Handler{
			auth:        mockAuth,
			gcpCredsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:gcp-creds",
		}

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{
				"Authorization": "Bearer user-token",
			},
			Body: `{"type": "service_account", "project_id": "test", "private_key": "test", "client_email": "test@test.iam.gserviceaccount.com"}`,
		}

		_, err := handler.saveGCPCredentials(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "permission denied")
	})

	t.Run("no GCP credentials ARN configured", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

		handler := &Handler{
			auth:        mockAuth,
			gcpCredsARN: "", // Not configured
		}

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{
				"Authorization": "Bearer admin-token",
			},
			Body: `{"type": "service_account", "project_id": "test", "private_key": "test", "client_email": "test@test.iam.gserviceaccount.com"}`,
		}

		_, err := handler.saveGCPCredentials(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "GCP credentials secret is not configured")
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

		handler := &Handler{
			auth:        mockAuth,
			gcpCredsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:gcp-creds",
		}

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{
				"Authorization": "Bearer admin-token",
			},
			Body: `invalid json`,
		}

		_, err := handler.saveGCPCredentials(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid request body")
	})

	t.Run("invalid type field", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

		handler := &Handler{
			auth:        mockAuth,
			gcpCredsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:gcp-creds",
		}

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{
				"Authorization": "Bearer admin-token",
			},
			Body: `{"type": "user_account", "project_id": "test", "private_key": "test", "client_email": "test@test.iam.gserviceaccount.com"}`,
		}

		_, err := handler.saveGCPCredentials(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "type must be 'service_account'")
	})

	t.Run("missing required fields", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
		mockAuth.On("ValidateSession", mock.Anything, "admin-token").Return(adminSession, nil)

		handler := &Handler{
			auth:        mockAuth,
			gcpCredsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:gcp-creds",
		}

		testCases := []struct {
			name string
			body string
		}{
			{"missing project_id", `{"type": "service_account", "private_key": "test", "client_email": "test@test.iam.gserviceaccount.com"}`},
			{"missing private_key", `{"type": "service_account", "project_id": "test", "client_email": "test@test.iam.gserviceaccount.com"}`},
			{"missing client_email", `{"type": "service_account", "project_id": "test", "private_key": "test"}`},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				req := &events.LambdaFunctionURLRequest{
					Headers: map[string]string{
						"Authorization": "Bearer admin-token",
					},
					Body: tc.body,
				}

				_, err := handler.saveGCPCredentials(ctx, req)

				assert.Error(t, err)
				assert.Contains(t, err.Error(), "required fields missing")
			})
		}
	})
}
