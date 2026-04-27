package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"golang.org/x/oauth2"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/oidc"
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
	AWSAuthMode             string `json:"aws_auth_mode"`
	AWSRoleARN              string `json:"aws_role_arn"`
	AWSExternalID           string `json:"aws_external_id"`
	AWSBastionID            string `json:"aws_bastion_id"`
	AWSWebIdentityTokenFile string `json:"aws_web_identity_token_file"`
	AWSIsOrgRoot            bool   `json:"aws_is_org_root"`
	// Azure
	AzureSubscriptionID string `json:"azure_subscription_id"`
	AzureTenantID       string `json:"azure_tenant_id"`
	AzureClientID       string `json:"azure_client_id"`
	AzureAuthMode       string `json:"azure_auth_mode"`
	// GCP
	GCPProjectID   string `json:"gcp_project_id"`
	GCPClientEmail string `json:"gcp_client_email"`
	GCPAuthMode    string `json:"gcp_auth_mode"`
	GCPWIFAudience string `json:"gcp_wif_audience"` // Full WIF provider resource, secret-free path only.
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
	"aws_access_keys":              true,
	"azure_client_secret":          true,
	"gcp_service_account":          true,
	"gcp_workload_identity_config": true,
}

// listAccounts handles GET /api/accounts.
func (h *Handler) listAccounts(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "accounts")
	if err != nil {
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

	// Filter by allowed accounts if the user has restricted access.
	// An empty list or one containing "*" grants unrestricted access.
	// Otherwise each entry is matched against the account's ID or Name.
	allowedAccounts, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if !auth.IsUnrestrictedAccess(allowedAccounts) {
		filtered := accounts[:0]
		for _, acct := range accounts {
			if auth.MatchesAccount(allowedAccounts, acct.ID, acct.Name) {
				filtered = append(filtered, acct)
			}
		}
		accounts = filtered
	}

	// Mark the self-account (the account matching CUDly's own host identity)
	h.markSelfAccount(ctx, accounts)

	return accounts, nil
}

// markSelfAccount sets IsSelf=true on the account matching the source identity.
func (h *Handler) markSelfAccount(ctx context.Context, accounts []config.CloudAccount) {
	si := h.resolveSourceIdentity(ctx)
	if si == nil || si.ExternalID() == "" {
		return
	}
	for i := range accounts {
		if accounts[i].Provider == si.Provider && accounts[i].ExternalID == si.ExternalID() {
			accounts[i].IsSelf = true
		}
	}
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

// createSelfAccount handles POST /api/accounts/self.
// Auto-creates an account for CUDly's own host cloud using ambient credentials.
func (h *Handler) createSelfAccount(ctx context.Context, httpReq *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, httpReq, "create", "accounts"); err != nil {
		return nil, err
	}

	si := h.resolveSourceIdentity(ctx)
	if si == nil || si.ExternalID() == "" {
		return nil, NewClientError(400, "source identity not available — CUDly cannot detect its own cloud account")
	}

	req := buildSelfAccountRequest(si)
	if err := validateCloudAccountRequest(req); err != nil {
		return nil, err
	}

	now := time.Now()
	account := cloudAccountFromRequest(req)
	account.ID = uuid.New().String()
	account.CreatedAt = now
	account.UpdatedAt = now
	account.Enabled = true

	if err := h.config.CreateCloudAccount(ctx, account); err != nil {
		return nil, classifyStoreError(err, "self-account already exists")
	}

	account.IsSelf = true
	return account, nil
}

// isDuplicateKeyError matches Postgres unique-constraint violations bubbled
// up by internal/config. Tolerates both the human-readable "duplicate key"
// prefix and the raw SQLSTATE 23505 token.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "duplicate key") || strings.Contains(s, "23505")
}

// classifyStoreError wraps a config-store error so handler callers can
// surface a 409 with a friendly message when the root cause is a duplicate-
// key violation. Non-duplicate errors pass through as `accounts: <err>`
// (matching the prior wrap) so existing callers still get a 500.
func classifyStoreError(err error, msg409 string) error {
	if isDuplicateKeyError(err) {
		return NewClientError(409, msg409)
	}
	return fmt.Errorf("accounts: %w", err)
}

func buildSelfAccountRequest(si *sourceIdentity) CloudAccountRequest {
	enabled := true
	req := CloudAccountRequest{
		Name:       "CUDly host",
		Provider:   si.Provider,
		ExternalID: si.ExternalID(),
		Enabled:    &enabled,
	}
	switch si.Provider {
	case "aws":
		req.AWSAuthMode = "role_arn"
	case "azure":
		req.AzureAuthMode = "managed_identity"
		req.AzureSubscriptionID = si.SubscriptionID
		req.AzureTenantID = si.TenantID
		req.AzureClientID = si.ClientID
	case "gcp":
		req.GCPAuthMode = "application_default"
		req.GCPProjectID = si.ProjectID
	}
	return req
}

// createAccount handles POST /api/accounts.
func (h *Handler) createAccount(ctx context.Context, httpReq *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, httpReq, "create", "accounts"); err != nil {
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
		return nil, classifyStoreError(err, fmt.Sprintf("an account with external ID %q already exists for %s", req.ExternalID, req.Provider))
	}

	return account, nil
}

var validAWSAuthModes = map[string]bool{
	"access_keys": true, "role_arn": true, "bastion": true, "workload_identity_federation": true,
}
var validAzureAuthModes = map[string]bool{
	"client_secret": true, "managed_identity": true, "workload_identity_federation": true,
}
var validGCPAuthModes = map[string]bool{
	"service_account": true, "application_default": true, "workload_identity_federation": true,
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

	if err := validateEmailFormat(req.ContactEmail); err != nil {
		return err
	}

	return validateAuthMode(req)
}

// validateAuthMode checks that the provider-specific auth mode is a known value.
func validateAuthMode(req CloudAccountRequest) error {
	switch req.Provider {
	case "aws":
		return validateAWSAuthMode(req)
	case "azure":
		if req.AzureAuthMode != "" && !validAzureAuthModes[req.AzureAuthMode] {
			return NewClientError(400, "invalid azure_auth_mode")
		}
	case "gcp":
		if req.GCPAuthMode != "" && !validGCPAuthModes[req.GCPAuthMode] {
			return NewClientError(400, "invalid gcp_auth_mode")
		}
	}
	return nil
}

// validateAWSAuthMode checks the AWS-specific auth-mode invariants:
// the mode is one of the known values (when set) and, for cross-account
// role_arn, the External ID satisfies the AWS sts:ExternalId rules
// (issue #128). The split from validateAuthMode keeps the parent
// function's cyclomatic complexity inside the project's gocyclo budget.
//
// Self-account onboarding uses role_arn with an empty role ARN to mean
// "use ambient Lambda/container credentials" (see awsAmbientCredResult).
// That path never calls sts:AssumeRole, so the ExternalId requirement
// doesn't apply — only enforce the validation when an actual cross-
// account role ARN is set.
func validateAWSAuthMode(req CloudAccountRequest) error {
	if req.AWSAuthMode != "" && !validAWSAuthModes[req.AWSAuthMode] {
		return NewClientError(400, "invalid aws_auth_mode")
	}
	if req.AWSAuthMode == "role_arn" && strings.TrimSpace(req.AWSRoleARN) != "" {
		return validateAWSExternalID(req.AWSExternalID)
	}
	return nil
}

// AWS sts:ExternalId per
// https://docs.aws.amazon.com/IAM/UserGuides/id_roles_create_for-user_externalid.html:
// 2..1224 chars, charset [A-Za-z0-9_+=,.@:/-]. CUDly tightens the lower
// bound to 16 because (a) every value the frontend generates is at least
// 32 chars (UUID or 16-byte hex), and (b) anything shorter is too weak
// to be useful as a confused-deputy guard. Validation runs on both
// create and update via validateCloudAccountRequest.
const (
	awsExternalIDMinLen = 16
	awsExternalIDMaxLen = 1224
)

// validateAWSExternalID enforces the issue #128 backend invariants:
//   - non-empty (defence-in-depth: the frontend always populates this,
//     but a hostile or buggy client posting "" would make AssumeRole
//     bypass the sts:ExternalId condition entirely if the customer's
//     trust policy lacks the StringEquals constraint).
//   - 16..1224 characters (lower bound is CUDly-internal, upper bound
//     is the AWS hard limit).
//   - charset restricted to AWS-accepted characters.
func validateAWSExternalID(s string) error {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return NewClientError(400, "aws_external_id is required for role_arn auth mode")
	}
	if len(trimmed) < awsExternalIDMinLen || len(trimmed) > awsExternalIDMaxLen {
		return NewClientError(400, fmt.Sprintf(
			"aws_external_id must be %d-%d characters (AWS sts:ExternalId limit)",
			awsExternalIDMinLen, awsExternalIDMaxLen,
		))
	}
	if !isValidAWSExternalIDCharset(trimmed) {
		return NewClientError(400,
			"aws_external_id contains characters AWS sts:ExternalId does not "+
				"accept (allowed: letters, digits, +=,.@:/-_)")
	}
	return nil
}

// isValidAWSExternalIDCharset matches AWS's documented sts:ExternalId regex
// `[\w+=,.@:/-]` — letters, digits, underscore, and the seven punctuation
// marks listed. Implemented byte-wise (single-byte ASCII only) instead of
// regexp to keep this allocation-free on the hot path.
func isValidAWSExternalIDCharset(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9':
			continue
		}
		switch c {
		case '_', '+', '=', ',', '.', '@', ':', '/', '-':
			continue
		}
		return false
	}
	return true
}

// cloudAccountFromRequest maps a CloudAccountRequest to a config.CloudAccount.
func cloudAccountFromRequest(req CloudAccountRequest) *config.CloudAccount {
	a := &config.CloudAccount{
		Name:                    req.Name,
		Description:             req.Description,
		ContactEmail:            req.ContactEmail,
		Provider:                req.Provider,
		ExternalID:              req.ExternalID,
		AWSAuthMode:             req.AWSAuthMode,
		AWSRoleARN:              req.AWSRoleARN,
		AWSExternalID:           req.AWSExternalID,
		AWSBastionID:            req.AWSBastionID,
		AWSWebIdentityTokenFile: req.AWSWebIdentityTokenFile,
		AWSIsOrgRoot:            req.AWSIsOrgRoot,
		AzureSubscriptionID:     req.AzureSubscriptionID,
		AzureTenantID:           req.AzureTenantID,
		AzureClientID:           req.AzureClientID,
		AzureAuthMode:           req.AzureAuthMode,
		GCPProjectID:            req.GCPProjectID,
		GCPClientEmail:          req.GCPClientEmail,
		GCPAuthMode:             req.GCPAuthMode,
		GCPWIFAudience:          req.GCPWIFAudience,
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

	session, err := h.requirePermission(ctx, req, "view", "accounts")
	if err != nil {
		return nil, err
	}

	account, err := h.requireAccountAccess(ctx, session, id)
	if err != nil {
		return nil, err
	}

	return account, nil
}

// updateAccount handles PUT /api/accounts/:id.
func (h *Handler) updateAccount(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, httpReq, "update", "accounts")
	if err != nil {
		return nil, err
	}

	var req CloudAccountRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	if err := validateCloudAccountRequest(req); err != nil {
		return nil, err
	}

	existing, err := h.requireAccountAccess(ctx, session, id)
	if err != nil {
		return nil, err
	}

	account := cloudAccountFromRequest(req)
	account.ID = id
	account.CreatedAt = existing.CreatedAt
	account.CreatedBy = existing.CreatedBy
	account.UpdatedAt = time.Now()

	if err := h.config.UpdateCloudAccount(ctx, account); err != nil {
		return nil, classifyStoreError(err, fmt.Sprintf("an account with external ID %q already exists for %s", req.ExternalID, req.Provider))
	}

	return account, nil
}

// deleteAccount handles DELETE /api/accounts/:id.
func (h *Handler) deleteAccount(ctx context.Context, req *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, req, "delete", "accounts")
	if err != nil {
		return nil, err
	}

	// Verify the user can access this account AND that it exists. Returns 404
	// for both "doesn't exist" and "out of scope" to avoid existence leakage.
	if _, err := h.requireAccountAccess(ctx, session, id); err != nil {
		return nil, err
	}

	if err := h.config.DeleteCloudAccount(ctx, id); err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	return nil, nil
}

// parseAndValidateCredentialsRequest performs all input validation that must
// run before we touch the database: UUID format, permission, body parse,
// credential_type allowlist, and per-type payload schema. Pulled out of
// saveAccountCredentials to keep that function under the cyclomatic limit.
// Returns the parsed request and the session so the caller can scope the
// subsequent account lookup against allowed_accounts.
func (h *Handler) parseAndValidateCredentialsRequest(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, id string) (*CredentialsRequest, *Session, error) {
	if err := validateUUID(id); err != nil {
		return nil, nil, err
	}
	session, err := h.requirePermission(ctx, httpReq, "update", "accounts")
	if err != nil {
		return nil, nil, err
	}
	var req CredentialsRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, nil, NewClientError(400, "invalid request body")
	}
	if !validCredentialTypes[req.CredentialType] {
		return nil, nil, NewClientError(400, "credential_type must be one of: aws_access_keys, azure_client_secret, gcp_service_account, gcp_workload_identity_config")
	}
	if err := validateCredentialPayload(req.CredentialType, req.Payload); err != nil {
		return nil, nil, err
	}
	return &req, session, nil
}

// saveAccountCredentials handles POST /api/accounts/:id/credentials.
func (h *Handler) saveAccountCredentials(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, id string) (any, error) {
	req, session, err := h.parseAndValidateCredentialsRequest(ctx, httpReq, id)
	if err != nil {
		return nil, err
	}

	// Scope the account to the user's allowed_accounts AND verify existence.
	// Must precede the credStore-nil check so missing/out-of-scope accounts
	// return 404 rather than a 500 about credential store configuration.
	// Returns errNotFound for both cases to avoid existence disclosure.
	if _, err := h.requireAccountAccess(ctx, session, id); err != nil {
		return nil, err
	}

	if h.credStore == nil {
		return nil, fmt.Errorf("accounts: credential store not configured")
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

// ambientCredResult returns the test result for auth modes that don't require a stored
// credential (role ARN, managed identity, application default). The second return value
// is true when the check was handled and the caller should return immediately.
func ambientCredResult(acct *config.CloudAccount) (AccountTestResult, bool) {
	switch acct.Provider {
	case "aws":
		return awsAmbientCredResult(acct)
	case "azure":
		if acct.AzureAuthMode == "managed_identity" {
			return AccountTestResult{OK: true, Message: "managed identity configured (no stored credential required)"}, true
		}
	case "gcp":
		if acct.GCPAuthMode == "application_default" {
			return AccountTestResult{OK: true, Message: "application default credentials configured (no stored credential required)"}, true
		}
	}
	return AccountTestResult{}, false
}

func awsAmbientCredResult(acct *config.CloudAccount) (AccountTestResult, bool) {
	switch acct.AWSAuthMode {
	case "workload_identity_federation":
		if acct.AWSRoleARN == "" {
			return AccountTestResult{OK: false, Message: "aws_role_arn is required but not set"}, true
		}
		return AccountTestResult{OK: true, Message: "web identity federation configured (no stored credential required)"}, true
	case "role_arn", "bastion":
		if acct.AWSRoleARN == "" {
			// Self-account: role_arn mode with no role ARN means "use ambient
			// Lambda execution role credentials" — the account is CUDly's own
			// host and doesn't need cross-account role assumption.
			return AccountTestResult{OK: true, Message: "ambient credentials (CUDly host account)"}, true
		}
		return AccountTestResult{OK: true, Message: "role assumption configured (no stored credential required)"}, true
	}
	return AccountTestResult{}, false
}

// testAccountCredentials handles POST /api/accounts/:id/test.
func (h *Handler) testAccountCredentials(ctx context.Context, req *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}
	session, err := h.requirePermission(ctx, req, "view", "accounts")
	if err != nil {
		return nil, err
	}
	acct, err := h.requireAccountAccess(ctx, session, id)
	if err != nil {
		return nil, err
	}
	if res, ok := ambientCredResult(acct); ok {
		return res, nil
	}
	if res, ok := h.azureFederatedCredResult(ctx, acct); ok {
		return res, nil
	}
	if res, ok := h.gcpFederatedCredResult(ctx, acct); ok {
		return res, nil
	}
	res, err := h.checkCredentialPresence(ctx, acct)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// gcpFederatedCredResult exercises the secret-free GCP federated
// credential path end-to-end. Guards on provider/mode/signer/issuer/
// audience and delegates the actual token exchange to
// runGCPFederatedTokenExchange so this top-level branch stays under
// gocyclo's 10-branch threshold. Returns (result, true) when this
// path applies; (_, false) means the caller should fall back to the
// presence check.
func (h *Handler) gcpFederatedCredResult(ctx context.Context, acct *config.CloudAccount) (AccountTestResult, bool) {
	if !h.canUseGCPFederated(ctx, acct) {
		return AccountTestResult{}, false
	}
	ts, err := credentials.BuildGCPFederatedCredential(ctx, h.signer, oidc.IssuerURL(), acct.GCPWIFAudience, acct.GCPClientEmail)
	if err != nil {
		return AccountTestResult{OK: false, Message: fmt.Sprintf("gcp federated credential build failed: %v", err)}, true
	}
	return runGCPFederatedTokenExchange(ctx, ts), true
}

// canUseGCPFederated returns true when all preconditions for the
// federated GCP test path are met. Factored out of
// gcpFederatedCredResult to keep that function's cyclomatic
// complexity below gocyclo's threshold.
func (h *Handler) canUseGCPFederated(ctx context.Context, acct *config.CloudAccount) bool {
	if acct.Provider != "gcp" || acct.GCPAuthMode != "workload_identity_federation" {
		return false
	}
	if h.signer == nil || acct.GCPWIFAudience == "" {
		return false
	}
	if oidc.IssuerURL() == "" {
		return false
	}
	// If a legacy stored WIF JSON is present, defer to the presence
	// check so legacy accounts keep reporting the same shape.
	if h.credStore != nil {
		if has, _ := h.credStore.HasCredential(ctx, acct.ID, credentials.CredTypeGCPWIFConfig); has {
			return false
		}
	}
	return true
}

// runGCPFederatedTokenExchange calls Token() on the federated token
// source with a per-attempt 15-second deadline (oauth2.TokenSource.Token()
// is not context-aware, so we plumb the timeout through a goroutine +
// select). A successful non-empty token proves the KMS → GCP STS →
// SA-impersonation chain is healthy.
//
// Retries on IAM propagation errors: freshly-bound WIF principals and
// iam.workloadIdentityUser grants on the impersonated service account
// take up to ~30s to become visible to GCP's token exchange. During
// that window iamcredentials.generateAccessToken returns
// IAM_PERMISSION_DENIED / "The caller does not have permission" even
// though the policy is correct. We retry up to 3 times with a 10s
// pause, but only when the error actually looks like that race.
// Other failure modes (KMS unauthorized, JWT rejected, wrong
// audience) fail fast on the first attempt.
func runGCPFederatedTokenExchange(ctx context.Context, ts oauth2.TokenSource) AccountTestResult {
	const maxAttempts = 3
	const retryDelay = 10 * time.Second

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		res, err, retriable := gcpTokenExchangeAttempt(ctx, ts)
		if err == nil {
			return res
		}
		lastErr = err
		if !retriable {
			return AccountTestResult{OK: false, Message: fmt.Sprintf("gcp token exchange failed: %v", err)}
		}
		if attempt == maxAttempts {
			break
		}
		select {
		case <-time.After(retryDelay):
		case <-ctx.Done():
			return AccountTestResult{OK: false, Message: fmt.Sprintf("gcp token exchange aborted while waiting for IAM propagation: %v", ctx.Err())}
		}
	}
	return AccountTestResult{OK: false, Message: fmt.Sprintf("gcp token exchange failed after %d attempts (IAM propagation): %v", maxAttempts, lastErr)}
}

// gcpTokenExchangeAttempt runs one Token() call with a 15s deadline.
// Returns (result, nil, _) on success, (_, err, true) on a retriable
// IAM propagation error, (_, err, false) on any other failure.
func gcpTokenExchangeAttempt(ctx context.Context, ts oauth2.TokenSource) (AccountTestResult, error, bool) {
	tokCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tokenChan := make(chan tokenResult, 1)
	go func() {
		tok, err := ts.Token()
		tokenChan <- tokenResult{tok: tok, err: err}
	}()
	select {
	case r := <-tokenChan:
		if r.err != nil {
			return AccountTestResult{}, r.err, isGCPIAMPropagationError(r.err)
		}
		if r.tok == nil || r.tok.AccessToken == "" {
			return AccountTestResult{OK: false, Message: "gcp token exchange returned empty token"}, nil, false
		}
		return AccountTestResult{OK: true, Message: "federated credential validated (KMS-signed JWT accepted by GCP STS, SA impersonation succeeded)"}, nil, false
	case <-tokCtx.Done():
		return AccountTestResult{}, fmt.Errorf("timed out after 15s"), true
	}
}

// isGCPIAMPropagationError reports whether err looks like the
// IAM-binding-not-yet-visible race that follows a fresh WIF provider
// or roles/iam.workloadIdentityUser grant. Matched by substring
// because the underlying error is wrapped through oauth2 →
// externalaccount → iamcredentials, and the leaf type isn't exported.
func isGCPIAMPropagationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "IAM_PERMISSION_DENIED") ||
		strings.Contains(msg, "iam.serviceAccounts.getAccessToken") ||
		strings.Contains(msg, "The caller does not have permission") ||
		strings.Contains(msg, "Permission 'iam.serviceAccounts.getAccessToken' denied")
}

// tokenResult bundles the return of oauth2.TokenSource.Token() for
// plumbing through a select for timeout handling, since oauth2's
// TokenSource interface does not take a context.
type tokenResult struct {
	tok *oauth2.Token
	err error
}

// azureFederatedCredResult exercises the secret-free Azure federated
// credential path end-to-end for accounts in workload_identity_federation
// mode whose CUDly deployment has an OIDC signer + issuer configured.
// It mints a client_assertion JWT via the KMS signer, exchanges it at
// Azure AD's token endpoint, and reports the result. Returns
// (result, true) when this path applies; (_, false) means the caller
// should fall back to the presence check (client_secret mode only).
func (h *Handler) azureFederatedCredResult(ctx context.Context, acct *config.CloudAccount) (AccountTestResult, bool) {
	if acct.Provider != "azure" || acct.AzureAuthMode != "workload_identity_federation" {
		return AccountTestResult{}, false
	}
	if h.signer == nil {
		return AccountTestResult{}, false
	}
	issuer := oidc.IssuerURL()
	if issuer == "" {
		return AccountTestResult{}, false
	}

	cred, err := credentials.BuildAzureFederatedCredential(h.signer, issuer, acct.AzureTenantID, acct.AzureClientID)
	if err != nil {
		return AccountTestResult{OK: false, Message: fmt.Sprintf("azure federated credential build failed: %v", err)}, true
	}

	// Actually exchange the JWT for an access token. This is the only
	// path that proves the whole chain — KMS sign → Azure AD federated
	// credential validation → access token — is healthy.
	tokCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tok, err := cred.GetToken(tokCtx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return AccountTestResult{OK: false, Message: fmt.Sprintf("azure token exchange failed: %v", err)}, true
	}
	if tok.Token == "" {
		return AccountTestResult{OK: false, Message: "azure token exchange returned empty token"}, true
	}
	return AccountTestResult{OK: true, Message: "federated credential validated (KMS-signed JWT accepted by Azure AD)"}, true
}

// checkCredentialPresence checks whether the expected credential is stored for an account.
func (h *Handler) checkCredentialPresence(ctx context.Context, acct *config.CloudAccount) (AccountTestResult, error) {
	if h.credStore != nil {
		credType := credTypeForAccount(acct)
		// Empty credType means this auth mode isn't backed by a stored
		// credential — e.g. Azure workload_identity_federation on a
		// deployment where the OIDC signer wasn't wired (so
		// azureFederatedCredResult returned ok=false and we fell
		// through to here). Report an operator-facing message instead
		// of querying for "no  credential stored".
		if credType == "" {
			return AccountTestResult{OK: false, Message: "this account's auth mode is not backed by a stored credential — check the deployment's OIDC issuer wiring"}, nil
		}
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
// based on its provider and auth mode. Returns "" for auth modes that
// aren't backed by a stored credential (e.g. Azure
// workload_identity_federation, which uses the deployment's OIDC signer
// at request time and stores nothing per-account).
func credTypeForAccount(acct *config.CloudAccount) string {
	switch acct.Provider {
	case "azure":
		if acct.AzureAuthMode == "workload_identity_federation" {
			return ""
		}
		return "azure_client_secret"
	case "gcp":
		if acct.GCPAuthMode == "workload_identity_federation" {
			return "gcp_workload_identity_config"
		}
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

	session, err := h.requirePermission(ctx, req, "view", "accounts")
	if err != nil {
		return nil, err
	}

	if _, err := h.requireAccountAccess(ctx, session, id); err != nil {
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

	session, err := h.requirePermission(ctx, httpReq, "update", "accounts")
	if err != nil {
		return nil, err
	}

	if _, err := h.requireAccountAccess(ctx, session, accountID); err != nil {
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

	session, err := h.requirePermission(ctx, req, "delete", "accounts")
	if err != nil {
		return nil, err
	}

	if _, err := h.requireAccountAccess(ctx, session, accountID); err != nil {
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

	if _, err := h.requirePermission(ctx, httpReq, "update", "plans"); err != nil {
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

	if _, err := h.requirePermission(ctx, req, "view", "plans"); err != nil {
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
	if _, err := h.requirePermission(ctx, req, "create", "accounts"); err != nil {
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
	if len(parts) != 4 {
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
