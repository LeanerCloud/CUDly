package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"
)

// CloudAccountRequest is the request body for create/update account endpoints.
type CloudAccountRequest struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ContactEmail string `json:"contact_email"`
	Provider     string `json:"provider"`
	ExternalID   string `json:"external_id"`
	Enabled      *bool  `json:"enabled"`
	// AWS
	AWSAuthMode   string `json:"aws_auth_mode"`
	AWSRoleARN    string `json:"aws_role_arn"`
	AWSExternalID string `json:"aws_external_id"`
	AWSBastionID  string `json:"aws_bastion_id"`
	AWSIsOrgRoot  bool   `json:"aws_is_org_root"`
	// Azure
	AzureSubscriptionID string `json:"azure_subscription_id"`
	AzureTenantID       string `json:"azure_tenant_id"`
	AzureClientID       string `json:"azure_client_id"`
	// GCP
	GCPProjectID   string `json:"gcp_project_id"`
	GCPClientEmail string `json:"gcp_client_email"`
}

// CredentialsRequest is the request body for the save-credentials endpoint.
type CredentialsRequest struct {
	CredentialType string                 `json:"credential_type"`
	Payload        map[string]interface{} `json:"payload"`
}

// AccountTestResult is the response for the test-credentials endpoint.
type AccountTestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// AccountServiceOverrideRequest is the request body for service override endpoints.
type AccountServiceOverrideRequest struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	Term           *int     `json:"term,omitempty"`
	Payment        *string  `json:"payment,omitempty"`
	Coverage       *float64 `json:"coverage,omitempty"`
	RampSchedule   *string  `json:"ramp_schedule,omitempty"`
	IncludeEngines []string `json:"include_engines,omitempty"`
	ExcludeEngines []string `json:"exclude_engines,omitempty"`
	IncludeRegions []string `json:"include_regions,omitempty"`
	ExcludeRegions []string `json:"exclude_regions,omitempty"`
	IncludeTypes   []string `json:"include_types,omitempty"`
	ExcludeTypes   []string `json:"exclude_types,omitempty"`
}

// validCredentialTypes is the set of accepted credential type names.
var validCredentialTypes = map[string]bool{
	"aws_access_keys":     true,
	"azure_client_secret": true,
	"gcp_service_account": true,
}

// listAccounts handles GET /api/accounts.
func (h *Handler) listAccounts(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	filter := buildAccountFilter(req.QueryStringParameters)

	accounts, err := h.config.ListCloudAccounts(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	if accounts == nil {
		accounts = []config.CloudAccount{}
	}

	return accounts, nil
}

// buildAccountFilter constructs a CloudAccountFilter from query parameters.
func buildAccountFilter(params map[string]string) config.CloudAccountFilter {
	var filter config.CloudAccountFilter

	if p, ok := params["provider"]; ok && p != "" {
		filter.Provider = &p
	}

	if e, ok := params["enabled"]; ok {
		switch e {
		case "true":
			t := true
			filter.Enabled = &t
		case "false":
			f := false
			filter.Enabled = &f
		}
	}

	if s, ok := params["search"]; ok {
		filter.Search = s
	}

	return filter
}

// createAccount handles POST /api/accounts.
func (h *Handler) createAccount(ctx context.Context, httpReq *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requireAdmin(ctx, httpReq); err != nil {
		return nil, err
	}

	var req CloudAccountRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	if err := validateCloudAccountRequest(req); err != nil {
		return nil, err
	}

	now := time.Now()
	account := cloudAccountFromRequest(req)
	account.ID = uuid.New().String()
	account.CreatedAt = now
	account.UpdatedAt = now

	if err := h.config.CreateCloudAccount(ctx, account); err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	return account, nil
}

// validAccountProviders is the set of concrete providers for an account (excludes empty/"all").
var validAccountProviders = map[string]bool{
	"aws":   true,
	"azure": true,
	"gcp":   true,
}

// validateCloudAccountRequest checks required fields and allowed values.
func validateCloudAccountRequest(req CloudAccountRequest) error {
	if req.Name == "" {
		return NewClientError(400, "name is required")
	}

	if !validAccountProviders[req.Provider] {
		return NewClientError(400, "provider must be one of: aws, azure, gcp")
	}

	if req.ExternalID == "" {
		return NewClientError(400, "external_id is required")
	}

	return nil
}

// cloudAccountFromRequest maps a CloudAccountRequest to a config.CloudAccount.
func cloudAccountFromRequest(req CloudAccountRequest) *config.CloudAccount {
	a := &config.CloudAccount{
		Name:                req.Name,
		Description:         req.Description,
		ContactEmail:        req.ContactEmail,
		Provider:            req.Provider,
		ExternalID:          req.ExternalID,
		AWSAuthMode:         req.AWSAuthMode,
		AWSRoleARN:          req.AWSRoleARN,
		AWSExternalID:       req.AWSExternalID,
		AWSBastionID:        req.AWSBastionID,
		AWSIsOrgRoot:        req.AWSIsOrgRoot,
		AzureSubscriptionID: req.AzureSubscriptionID,
		AzureTenantID:       req.AzureTenantID,
		AzureClientID:       req.AzureClientID,
		GCPProjectID:        req.GCPProjectID,
		GCPClientEmail:      req.GCPClientEmail,
	}

	if req.Enabled != nil {
		a.Enabled = *req.Enabled
	}

	return a
}

// getAccount handles GET /api/accounts/:id.
func (h *Handler) getAccount(ctx context.Context, req *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	account, err := h.config.GetCloudAccount(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	if account == nil {
		return nil, errNotFound
	}

	return account, nil
}

// updateAccount handles PUT /api/accounts/:id.
func (h *Handler) updateAccount(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, httpReq); err != nil {
		return nil, err
	}

	var req CloudAccountRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	if err := validateCloudAccountRequest(req); err != nil {
		return nil, err
	}

	existing, err := h.config.GetCloudAccount(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	if existing == nil {
		return nil, errNotFound
	}

	account := cloudAccountFromRequest(req)
	account.ID = id
	account.CreatedAt = existing.CreatedAt
	account.CreatedBy = existing.CreatedBy
	account.UpdatedAt = time.Now()

	if err := h.config.UpdateCloudAccount(ctx, account); err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	return account, nil
}

// deleteAccount handles DELETE /api/accounts/:id.
func (h *Handler) deleteAccount(ctx context.Context, req *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	if err := h.config.DeleteCloudAccount(ctx, id); err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	return nil, nil
}

// saveAccountCredentials handles POST /api/accounts/:id/credentials.
func (h *Handler) saveAccountCredentials(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, httpReq); err != nil {
		return nil, err
	}

	var req CredentialsRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	if !validCredentialTypes[req.CredentialType] {
		return nil, NewClientError(400, "credential_type must be one of: aws_access_keys, azure_client_secret, gcp_service_account")
	}

	if h.credStore == nil {
		return nil, fmt.Errorf("accounts: credential store not configured")
	}

	// Confirm the account exists so we return 404 rather than a FK violation.
	acct, err := h.config.GetCloudAccount(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	if acct == nil {
		return nil, errNotFound
	}

	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, NewClientError(400, "invalid credentials payload")
	}

	if err := h.credStore.SaveCredential(ctx, id, req.CredentialType, payloadBytes); err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	return nil, nil
}

// testAccountCredentials handles POST /api/accounts/:id/test.
func (h *Handler) testAccountCredentials(ctx context.Context, req *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	acct, err := h.config.GetCloudAccount(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	if acct == nil {
		return nil, errNotFound
	}

	// For role_arn and bastion auth modes, no stored secret is needed.
	if acct.Provider == "aws" && acct.AWSAuthMode != "access_keys" {
		if acct.AWSRoleARN == "" {
			return AccountTestResult{OK: false, Message: "aws_role_arn is required but not set"}, nil
		}
		return AccountTestResult{OK: true, Message: "role assumption configured (no stored credential required)"}, nil
	}

	res, err := h.checkCredentialPresence(ctx, acct)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// checkCredentialPresence checks whether the expected credential is stored for an account.
func (h *Handler) checkCredentialPresence(ctx context.Context, acct *config.CloudAccount) (AccountTestResult, error) {
	if h.credStore != nil {
		credType := credTypeForAccount(acct)
		has, err := h.credStore.HasCredential(ctx, acct.ID, credType)
		if err != nil {
			return AccountTestResult{}, fmt.Errorf("accounts: check credential: %w", err)
		}
		if has {
			return AccountTestResult{OK: true, Message: "credentials are configured"}, nil
		}
		return AccountTestResult{OK: false, Message: fmt.Sprintf("no %s credential stored", credType)}, nil
	}

	// Fallback when credStore is not wired (e.g. in unit tests).
	ok, err := h.config.HasAccountCredentials(ctx, acct.ID)
	if err != nil {
		return AccountTestResult{}, fmt.Errorf("accounts: %w", err)
	}
	if ok {
		return AccountTestResult{OK: true, Message: "credentials are configured"}, nil
	}
	return AccountTestResult{OK: false, Message: "no credentials configured"}, nil
}

// credTypeForAccount returns the expected credential_type for an account
// based on its provider and auth mode.
func credTypeForAccount(acct *config.CloudAccount) string {
	switch acct.Provider {
	case "azure":
		return "azure_client_secret"
	case "gcp":
		return "gcp_service_account"
	default: // aws
		return "aws_access_keys"
	}
}

// listAccountServiceOverrides handles GET /api/accounts/:id/service-overrides.
func (h *Handler) listAccountServiceOverrides(ctx context.Context, req *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	overrides, err := h.config.ListAccountServiceOverrides(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	if overrides == nil {
		overrides = []config.AccountServiceOverride{}
	}

	return overrides, nil
}

// saveAccountServiceOverride handles PUT /api/accounts/:id/service-overrides/:provider/:service.
// providerServicePath contains the remaining path segment: "uuid/service-overrides/provider/service".
func (h *Handler) saveAccountServiceOverride(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, providerServicePath string) (any, error) {
	accountID, provider, service, err := parseServiceOverridePath(providerServicePath)
	if err != nil {
		return nil, NewClientError(400, err.Error())
	}

	if _, err := h.requireAdmin(ctx, httpReq); err != nil {
		return nil, err
	}

	var req AccountServiceOverrideRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	now := time.Now()
	existing, err := h.config.GetAccountServiceOverride(ctx, accountID, provider, service)
	if err != nil {
		return nil, fmt.Errorf("accounts: get existing override: %w", err)
	}

	override := buildServiceOverride(accountID, provider, service, req, existing, now)

	if err := h.config.SaveAccountServiceOverride(ctx, override); err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	return override, nil
}

// buildServiceOverride constructs an AccountServiceOverride from request and existing data.
func buildServiceOverride(accountID, provider, service string, req AccountServiceOverrideRequest, existing *config.AccountServiceOverride, now time.Time) *config.AccountServiceOverride {
	override := &config.AccountServiceOverride{
		AccountID: accountID,
		Provider:  provider,
		Service:   service,
		UpdatedAt: now,
	}

	if existing != nil {
		override.ID = existing.ID
		override.CreatedAt = existing.CreatedAt
	} else {
		override.ID = uuid.New().String()
		override.CreatedAt = now
	}

	applyServiceOverrideFields(override, req)

	return override
}

// applyServiceOverrideFields copies sparse request fields onto an override.
func applyServiceOverrideFields(o *config.AccountServiceOverride, req AccountServiceOverrideRequest) {
	applyOverrideScalars(o, req)
	applyOverrideSlices(o, req)
}

func applyOverrideScalars(o *config.AccountServiceOverride, req AccountServiceOverrideRequest) {
	if req.Enabled != nil {
		o.Enabled = req.Enabled
	}
	if req.Term != nil {
		o.Term = req.Term
	}
	if req.Payment != nil {
		o.Payment = req.Payment
	}
	if req.Coverage != nil {
		o.Coverage = req.Coverage
	}
	if req.RampSchedule != nil {
		o.RampSchedule = req.RampSchedule
	}
}

func applyOverrideSlices(o *config.AccountServiceOverride, req AccountServiceOverrideRequest) {
	if req.IncludeEngines != nil {
		o.IncludeEngines = req.IncludeEngines
	}
	if req.ExcludeEngines != nil {
		o.ExcludeEngines = req.ExcludeEngines
	}
	if req.IncludeRegions != nil {
		o.IncludeRegions = req.IncludeRegions
	}
	if req.ExcludeRegions != nil {
		o.ExcludeRegions = req.ExcludeRegions
	}
	if req.IncludeTypes != nil {
		o.IncludeTypes = req.IncludeTypes
	}
	if req.ExcludeTypes != nil {
		o.ExcludeTypes = req.ExcludeTypes
	}
}

// deleteAccountServiceOverride handles DELETE /api/accounts/:id/service-overrides/:provider/:service.
func (h *Handler) deleteAccountServiceOverride(ctx context.Context, req *events.LambdaFunctionURLRequest, providerServicePath string) (any, error) {
	accountID, provider, service, err := parseServiceOverridePath(providerServicePath)
	if err != nil {
		return nil, NewClientError(400, err.Error())
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	if err := h.config.DeleteAccountServiceOverride(ctx, accountID, provider, service); err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	return nil, nil
}

// setPlanAccounts handles PUT /api/plans/:id/accounts.
func (h *Handler) setPlanAccounts(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, httpReq); err != nil {
		return nil, err
	}

	var body struct {
		AccountIDs []string `json:"account_ids"`
	}
	if err := json.Unmarshal([]byte(httpReq.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	for _, aid := range body.AccountIDs {
		if err := validateUUID(aid); err != nil {
			return nil, NewClientError(400, fmt.Sprintf("invalid account_id %q: must be a valid UUID", aid))
		}
	}

	if err := h.config.SetPlanAccounts(ctx, id, body.AccountIDs); err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	return nil, nil
}

// listPlanAccounts handles GET /api/plans/:id/accounts.
func (h *Handler) listPlanAccounts(ctx context.Context, req *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	accounts, err := h.config.GetPlanAccounts(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	if accounts == nil {
		accounts = []config.CloudAccount{}
	}

	return accounts, nil
}

// discoverOrgAccounts handles POST /api/accounts/discover-org.
func (h *Handler) discoverOrgAccounts(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	return map[string]string{"message": "org discovery not yet implemented"}, nil
}

// parseServiceOverridePath parses "uuid/service-overrides/provider/service"
// and returns accountID, provider, service.
func parseServiceOverridePath(path string) (accountID, provider, service string, err error) {
	// Strip leading slash if present
	path = strings.TrimPrefix(path, "/")

	parts := strings.Split(path, "/")
	// Expect: [accountID, "service-overrides", provider, service]
	if len(parts) < 4 {
		return "", "", "", fmt.Errorf("invalid service override path: expected uuid/service-overrides/provider/service")
	}

	accountID = parts[0]
	if _, parseErr := uuid.Parse(accountID); parseErr != nil {
		return "", "", "", fmt.Errorf("invalid account ID in path")
	}

	if parts[1] != "service-overrides" {
		return "", "", "", fmt.Errorf("invalid service override path: missing service-overrides segment")
	}

	provider = parts[2]
	service = parts[3]

	if !validAccountProviders[provider] {
		return "", "", "", fmt.Errorf("invalid provider %q: must be one of aws, azure, gcp", provider)
	}
	if service == "" || !serviceNameRegex.MatchString(service) {
		return "", "", "", fmt.Errorf("invalid service name %q: must be 1-64 lowercase alphanumeric characters or hyphens", service)
	}

	return accountID, provider, service, nil
}
