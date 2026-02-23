// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// Credentials handlers

// getCredentialsStatus checks if Azure and GCP credentials are configured
func (h *Handler) getCredentialsStatus(ctx context.Context) *CredentialsStatus {
	status := &CredentialsStatus{
		AzureConfigured: h.checkAzureCredentials(ctx),
		GCPConfigured:   h.checkGCPCredentials(ctx),
	}
	return status
}

// checkAzureCredentials verifies if Azure credentials are configured
func (h *Handler) checkAzureCredentials(ctx context.Context) bool {
	if h.azureCredsARN == "" {
		return false
	}

	creds, err := h.getSecretValue(ctx, h.azureCredsARN)
	if err != nil || creds == "" {
		return false
	}

	var azureCreds AzureCredentialsRequest
	if err := json.Unmarshal([]byte(creds), &azureCreds); err != nil {
		return false
	}

	return azureCreds.TenantID != "" &&
		azureCreds.ClientID != "" &&
		azureCreds.ClientSecret != "" &&
		azureCreds.SubscriptionID != ""
}

// checkGCPCredentials verifies if GCP credentials are configured
func (h *Handler) checkGCPCredentials(ctx context.Context) bool {
	if h.gcpCredsARN == "" {
		return false
	}

	creds, err := h.getSecretValue(ctx, h.gcpCredsARN)
	if err != nil || creds == "" {
		return false
	}

	var gcpCreds GCPCredentialsRequest
	if err := json.Unmarshal([]byte(creds), &gcpCreds); err != nil {
		return false
	}

	return gcpCreds.ProjectID != "" &&
		gcpCreds.PrivateKey != "" &&
		gcpCreds.ClientEmail != ""
}

// getSecretValue retrieves a secret value from Secrets Manager
func (h *Handler) getSecretValue(ctx context.Context, secretARN string) (string, error) {
	if secretARN == "" {
		return "", fmt.Errorf("secret ARN is empty")
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", err
	}

	client := secretsmanager.NewFromConfig(cfg)
	result, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &secretARN,
	})
	if err != nil {
		return "", err
	}

	if result.SecretString == nil {
		return "", fmt.Errorf("secret is empty")
	}

	return *result.SecretString, nil
}

// updateSecretValue updates a secret value in Secrets Manager
func (h *Handler) updateSecretValue(ctx context.Context, secretARN, value string) error {
	if secretARN == "" {
		return fmt.Errorf("secret ARN is empty")
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}

	client := secretsmanager.NewFromConfig(cfg)
	_, err = client.UpdateSecret(ctx, &secretsmanager.UpdateSecretInput{
		SecretId:     &secretARN,
		SecretString: &value,
	})

	return err
}

// saveAzureCredentials handles POST /api/credentials/azure
func (h *Handler) saveAzureCredentials(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	// Require admin access
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	if h.azureCredsARN == "" {
		return nil, fmt.Errorf("Azure credentials secret is not configured")
	}

	var creds AzureCredentialsRequest
	if err := json.Unmarshal([]byte(req.Body), &creds); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Validate required fields
	if creds.TenantID == "" || creds.ClientID == "" || creds.ClientSecret == "" || creds.SubscriptionID == "" {
		return nil, NewClientError(400, "all fields are required: tenant_id, client_id, client_secret, subscription_id")
	}

	// Store credentials
	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal credentials: %w", err)
	}

	if err := h.updateSecretValue(ctx, h.azureCredsARN, string(credsJSON)); err != nil {
		return nil, fmt.Errorf("failed to store credentials: %w", err)
	}

	logging.Infof("Azure credentials updated by admin")
	return &StatusResponse{Status: "Azure credentials saved"}, nil
}

// saveGCPCredentials handles POST /api/credentials/gcp
func (h *Handler) saveGCPCredentials(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	// Require admin access
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	if h.gcpCredsARN == "" {
		return nil, fmt.Errorf("GCP credentials secret is not configured")
	}

	var creds GCPCredentialsRequest
	if err := json.Unmarshal([]byte(req.Body), &creds); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Validate required fields
	if creds.Type != "service_account" {
		return nil, NewClientError(400, "invalid credentials: type must be 'service_account'")
	}
	if creds.ProjectID == "" || creds.PrivateKey == "" || creds.ClientEmail == "" {
		return nil, NewClientError(400, "required fields missing: project_id, private_key, client_email")
	}

	// Store credentials (preserve the original JSON format for GCP SDK compatibility)
	if err := h.updateSecretValue(ctx, h.gcpCredsARN, req.Body); err != nil {
		return nil, fmt.Errorf("failed to store credentials: %w", err)
	}

	logging.Infof("GCP credentials updated by admin for project: %s", creds.ProjectID)
	return &StatusResponse{Status: "GCP credentials saved"}, nil
}
