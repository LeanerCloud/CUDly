package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"golang.org/x/oauth2"

	"github.com/LeanerCloud/CUDly/internal/accounts"
	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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

// AccountSummary is the minimal-disclosure projection of a cloud account used
// by the global topbar filter and the create-plan-from-commitment target
// prefill (issues #949, #951). It deliberately omits every credential/config
// field (role ARNs, subscription IDs, client emails, auth modes, bastion IDs,
// the self-account marker) so the endpoint can be gated on a low read verb that
// Standard / Read-Only users already hold, without leaking sensitive account
// configuration. The full CloudAccount object stays behind GET /api/accounts
// (view:accounts, admin-grade).
type AccountSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ExternalID string `json:"external_id"`
	Provider   string `json:"provider"`
}

// listAccountsMinimal handles GET /api/accounts/list.
//
// Returns the minimal AccountSummary projection scoped by the session's
// allowed_accounts list. Gated on view:recommendations (held by both the
// Standard Users and Read-Only Users groups) rather than view:accounts, so the
// account dropdown in the global filter and the plan-target prefill work for
// non-admin users without exposing the credential/config metadata carried by
// the full GET /api/accounts response. See issues #949 / #951.
func (h *Handler) listAccountsMinimal(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "recommendations")
	if err != nil {
		return nil, err
	}

	filter := buildAccountFilter(req.QueryStringParameters)

	accounts, err := h.config.ListCloudAccounts(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}

	allowedAccounts, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	unrestricted := auth.IsUnrestrictedAccess(allowedAccounts)

	// Build the minimal projection in place, applying allowed_accounts scoping
	// during the copy so a restricted user only ever sees their entitled rows.
	summaries := make([]AccountSummary, 0, len(accounts))
	for i := range accounts {
		acct := &accounts[i]
		if !unrestricted && !auth.MatchesAccount(allowedAccounts, acct.ID, acct.Name) {
			continue
		}
		summaries = append(summaries, AccountSummary{
			ID:         acct.ID,
			Name:       acct.Name,
			ExternalID: acct.ExternalID,
			Provider:   acct.Provider,
		})
	}

	return summaries, nil
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
		return NewClientError(400, "invalid contact_email format")
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
		if err := validateGCPClientEmail(req.GCPClientEmail); err != nil {
			return err
		}
	}
	return nil
}

// validateAWSAuthMode checks the AWS-specific auth-mode invariants:
// the mode is one of the known values (when set) and, for any auth
// mode that calls sts:AssumeRole into a customer role, the External ID
// satisfies the AWS sts:ExternalId rules (issue #128, extended to
// bastion mode by #129). The split from validateAuthMode keeps the
// parent function's cyclomatic complexity inside the project's gocyclo
// budget.
//
// Self-account onboarding uses role_arn with an empty role ARN to mean
// "use ambient Lambda/container credentials" (see awsAmbientCredResult).
// That path never calls sts:AssumeRole, so the ExternalId requirement
// doesn't apply — only enforce the validation when an actual cross-
// account role ARN is set. The same exemption is applied to bastion
// mode for parity (an empty role ARN in bastion mode is rejected
// elsewhere by the credential resolver, but the validator stays
// defensive).
//
// workload_identity_federation does NOT use ExternalID — OIDC verifies
// identity via the token subject claim (see resolveWebIdentityProvider
// in internal/credentials/resolver.go), and stscreds.WebIdentityRoleOptions
// has no ExternalID field. access_keys doesn't assume a role at all.
func validateAWSAuthMode(req CloudAccountRequest) error {
	if req.AWSAuthMode != "" && !validAWSAuthModes[req.AWSAuthMode] {
		return NewClientError(400, "invalid aws_auth_mode")
	}
	if err := validateAWSRoleARN(req.AWSRoleARN); err != nil {
		return err
	}
	if err := validateAWSWebIdentityTokenFile(req.AWSWebIdentityTokenFile); err != nil {
		return err
	}
	requiresExternalID := (req.AWSAuthMode == "role_arn" || req.AWSAuthMode == "bastion") &&
		strings.TrimSpace(req.AWSRoleARN) != ""
	if requiresExternalID {
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
		return NewClientError(400, "aws_external_id is required for role_arn and bastion auth modes")
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
//
// Preflights the delete against pending/notified purchase_executions
// (issue #606). Migration 000053 also enforces ON DELETE RESTRICT at the
// DB level so a race that slips past the preflight still can't orphan a
// pending execution — but the preflight gives the frontend a structured
// 409 (with the count + execution_ids) so it can offer a Cancel-All-Then-
// Delete affordance instead of surfacing a raw FK-violation 500.
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

	// Preflight: refuse the delete if pending/notified executions still
	// reference this account. The frontend uses the pending_count + the
	// pending_execution_ids list to drive the Cancel-All-Then-Delete UX.
	pendingCount, err := h.config.CountPendingExecutionsForAccount(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	if pendingCount > 0 {
		execIDs, listErr := h.config.ListPendingExecutionIDsForAccount(ctx, id)
		if listErr != nil {
			// Count succeeded but list failed — still surface the 409 so the
			// operator gets a clear "cancel them first" message instead of
			// the raw FK error from the eventual DB delete. The list payload
			// is omitted; the frontend falls back to a generic message.
			return nil, NewClientErrorWithDetails(409,
				fmt.Sprintf("cannot delete account: %d pending purchase(s) must be cancelled first", pendingCount),
				map[string]any{
					"pending_count": pendingCount,
					"reason":        "pending_executions",
				})
		}
		return nil, NewClientErrorWithDetails(409,
			fmt.Sprintf("cannot delete account: %d pending purchase(s) must be cancelled first", pendingCount),
			map[string]any{
				"pending_count":         pendingCount,
				"pending_execution_ids": execIDs,
				"reason":                "pending_executions",
			})
	}

	if err := h.config.DeleteCloudAccount(ctx, id); err != nil {
		// Race: the preflight count was 0 but a pending execution row was
		// inserted concurrently before we issued the DELETE. Migration 000053
		// enforces ON DELETE RESTRICT, so Postgres raises SQLSTATE 23503
		// (foreign_key_violation). Map this to the same structured 409 the
		// preflight branch returns so the frontend's Cancel-All-Then-Delete
		// affordance still kicks in — minus the count/ids, which we can't
		// supply without re-querying (and the operator can just retry).
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return nil, NewClientErrorWithDetails(409,
				"cannot delete account: pending purchase(s) must be cancelled first",
				map[string]any{
					"reason": "pending_executions",
				})
		}
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
// This operation performs live outbound cloud API calls / credential probing,
// which is a write-class side effect. Gate on update:accounts (not view:accounts)
// so the handler is self-protecting if the route Auth level is ever relaxed (02-M5).
func (h *Handler) testAccountCredentials(ctx context.Context, req *events.LambdaFunctionURLRequest, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}
	session, err := h.requirePermission(ctx, req, "update", "accounts")
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
		has, err := h.credStore.HasCredential(ctx, acct.ID, credentials.CredTypeGCPWIFConfig)
		if err != nil {
			logging.Warnf("hasCredential check failed for account %s: %v", acct.ID, err)
		}
		if has {
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

	// Defence-in-depth: reject invalid (term, payment) combos before persisting.
	// checkCommitmentOptionCombo is permissive when commitmentOpts is nil or
	// probe data is absent (ErrNoData) — the frontend's hardcoded rules are the
	// primary gate in those cases.
	if err := h.checkCommitmentOptionCombo(ctx, config.ServiceConfig{
		Provider: override.Provider,
		Service:  override.Service,
		Term:     derefInt(override.Term),
		Payment:  derefString(override.Payment),
	}); err != nil {
		return nil, err
	}

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

// derefInt dereferences an *int pointer, returning 0 for nil.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// derefString dereferences a *string pointer, returning "" for nil.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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

// validatePlanAccountProviders enforces the issue-#209 / spec E-4 rule
// that every account assigned to a plan must have its provider match
// one of the plan's derived providers. Returns:
//   - 404 ClientError when the plan does not exist
//   - 404 ClientError when an account_id does not exist (referencing
//     the offending ID)
//   - 400 ClientError listing every provider mismatch in one message
//     (so clients fix everything in one round-trip rather than
//     resubmitting to discover the next)
//   - nil when all accounts match (or when the plan has no parseable
//     services — defensive skip; production plans always carry at least
//     one service, frontend enforces this)
//
// Pulled out of setPlanAccounts to keep that function under the gocyclo
// budget (limit 10). No business logic lives here that isn't otherwise
// described in setPlanAccounts' doc comment.
func (h *Handler) validatePlanAccountProviders(ctx context.Context, planID string, accountIDs []string) error {
	plan, err := h.getPlanForAccountProviderValidation(ctx, planID)
	if err != nil {
		return err
	}

	expected := config.DerivePlanProviders(plan)
	if len(expected) == 0 {
		return nil
	}

	type mismatch struct {
		ID       string
		Name     string
		Provider string
	}
	var mismatches []mismatch
	for _, aid := range accountIDs {
		acct, getErr := h.config.GetCloudAccount(ctx, aid)
		if getErr != nil {
			// Do NOT wrap getErr with the raw account UUID: a non-ClientError
			// here propagates to the router's `logging.Errorf("API error: %v")`,
			// which would leak the account UUID and the raw DB error string
			// into logs (issue #944(b): log account counts, never raw IDs).
			// Map to a 404 if the store reports not-found, else a structured
			// 500 that logs only the derived providers + account count.
			return mapCreatePlanStorageError(getErr,
				"account not found", "failed to validate plan accounts",
				"validatePlanAccountProviders: GetCloudAccount failed (providers=%v accounts=%d)",
				expected, len(accountIDs))
		}
		if acct == nil {
			return NewClientError(404, fmt.Sprintf("account not found: %s", aid))
		}
		if !slices.Contains(expected, acct.Provider) {
			mismatches = append(mismatches, mismatch{ID: aid, Name: acct.Name, Provider: acct.Provider})
		}
	}
	if len(mismatches) == 0 {
		return nil
	}

	parts := make([]string, len(mismatches))
	for i, m := range mismatches {
		parts[i] = fmt.Sprintf("account %q has provider=%q, expected one of %v",
			m.Name, m.Provider, expected)
	}
	return NewClientError(400, "plan provider mismatch: "+strings.Join(parts, "; "))
}

func (h *Handler) getPlanForAccountProviderValidation(ctx context.Context, planID string) (*config.PurchasePlan, error) {
	plan, err := h.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		// Do NOT wrap err with the raw plan UUID: a non-ClientError propagates
		// to the router's logging.Errorf("API error: %v"), leaking the raw DB
		// error string (issue #965). Map to 404 on ErrNotFound; else log a
		// structured 500 without the DB error or the plan UUID.
		return nil, mapCreatePlanStorageError(err,
			"plan not found", "failed to validate plan",
			"getPlanForAccountProviderValidation: GetPurchasePlan failed")
	}
	if plan == nil {
		return nil, NewClientError(404, fmt.Sprintf("plan not found: %s", planID))
	}
	return plan, nil
}

// setPlanAccounts handles PUT /api/plans/:id/accounts.
//
// Per issue #209 / spec acceptance criterion E-4, every account assigned
// to a plan must have its provider match one of the plan's derived
// providers (extracted from plan.Services keys). Mismatches return 400
// listing every offender; the assignment is rejected atomically (no
// partial writes).
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

	// Reject empty account_ids: a plan must remain tied to at least one
	// cloud_account row. Allowing the PUT to clear all rows would recreate
	// the universal-plan bug class (purchase_plans row with no matching
	// plan_accounts row) that createPlan now refuses at insert time.
	if len(body.AccountIDs) == 0 {
		return nil, NewClientError(400, "account_ids is required: a plan must be tied to at least one account")
	}

	for _, aid := range body.AccountIDs {
		if err := validateUUID(aid); err != nil {
			return nil, NewClientError(400, fmt.Sprintf("invalid account_id %q: must be a valid UUID", aid))
		}
	}

	// Provider-match validation (issue #209). Extracted to keep
	// setPlanAccounts under the gocyclo budget (10).
	if err := h.validatePlanAccountProviders(ctx, id, body.AccountIDs); err != nil {
		return nil, err
	}

	if err := h.config.SetPlanAccounts(ctx, id, body.AccountIDs); err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, NewClientError(404, err.Error())
		}
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

// DiscoverOrgRequest is the request body for POST /api/accounts/discover-org.
// AccountID is the UUID of the org-root cloud account whose stored credentials
// will be used to call AWS Organizations.
type DiscoverOrgRequest struct {
	AccountID string `json:"account_id"`
}

// DiscoverOrgResult is the response shape for POST /api/accounts/discover-org.
// Discovered is the total number of member accounts the AWS Organizations API
// returned; Created is the number of new cloud_accounts rows persisted; Skipped
// is the number that already existed (matched by provider+external_id).
type DiscoverOrgResult struct {
	Discovered int `json:"discovered"`
	Created    int `json:"created"`
	Skipped    int `json:"skipped"`
}

// discoverOrgAccounts handles POST /api/accounts/discover-org. The endpoint
// (1) validates the named cloud account is an AWS org root, (2) resolves its
// stored credentials, (3) calls AWS Organizations ListAccounts via the
// credentials, (4) deduplicates against existing aws cloud_accounts rows by
// external_id, and (5) persists the new ones with enabled=false, an empty
// aws_auth_mode, and aws_bastion_id pointing at the org root, so an operator
// must review/approve each discovered account and explicitly choose the
// bastion auth mode plus role ARN before it'll be picked up by the scheduler.
// See issue #208 for the spec; see specs/multi-account-execution/
// acceptance.md F-1..F-3 for the acceptance criteria.
func (h *Handler) discoverOrgAccounts(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	// Admin-only: org discovery can create N cloud_accounts rows in one call
	// and may bring unfamiliar accounts into the roster. Even though those
	// rows boot disabled, the elevated privilege makes admin-scope the right
	// gate (CR pass 1 on PR #212).
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	root, err := h.parseDiscoverOrgRoot(ctx, req.Body)
	if err != nil {
		return nil, err
	}

	cfg, err := h.buildOrgRootAWSConfig(ctx, root)
	if err != nil {
		return nil, err
	}

	disco, err := h.runOrgDiscovery(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if disco == nil {
		return DiscoverOrgResult{}, nil
	}

	return h.persistDiscoveredMembers(ctx, root, disco.Accounts)
}

// parseDiscoverOrgRoot decodes the request body, loads the named account, and
// validates it's a usable AWS org root. Returns ClientErrors for the four
// failure modes the spec enumerates (bad JSON / bad UUID / not-aws / not-root)
// + 404 for missing-account. Pulled out of discoverOrgAccounts to keep that
// function under the gocyclo budget.
func (h *Handler) parseDiscoverOrgRoot(ctx context.Context, rawBody string) (*config.CloudAccount, error) {
	var body DiscoverOrgRequest
	if err := json.Unmarshal([]byte(rawBody), &body); err != nil {
		return nil, NewClientError(400, "invalid JSON body")
	}
	if err := validateUUID(body.AccountID); err != nil {
		return nil, err
	}

	root, err := h.config.GetCloudAccount(ctx, body.AccountID)
	if err != nil {
		return nil, fmt.Errorf("accounts: get cloud account: %w", err)
	}
	if root == nil {
		return nil, NewClientError(404, "cloud account not found")
	}
	if root.Provider != "aws" {
		return nil, NewClientError(400, "discover-org requires an aws account")
	}
	if !root.AWSIsOrgRoot {
		return nil, NewClientError(400, "account is not configured as an org root (set aws_is_org_root=true)")
	}
	return root, nil
}

// buildOrgRootAWSConfig resolves the org-root account's stored credentials and
// returns an aws.Config configured to call AWS APIs as that account.
//
// Returns the resolver's error wrapped as a regular Go error (which the
// handler-default mapping surfaces as 500) — NOT a 400 ClientError — because
// ResolveAWSCredentialProvider mixes definite client-side validation
// failures (e.g., missing aws_role_arn for role_arn mode) with transient
// server-side failures (credential store unavailable, network errors during
// access-key load). Without a sentinel/typed error in the credentials
// package today, blanket-400 was misleading; 5xx is the safer default and
// retries are possible. Refining this to a proper 400/500 split is tracked
// in the credentials-package error-type cleanup (out of scope for this PR;
// see CR pass 1 on #212).
func (h *Handler) buildOrgRootAWSConfig(ctx context.Context, root *config.CloudAccount) (aws.Config, error) {
	baseCfg, err := h.getBaseAWSConfig(ctx)
	if err != nil {
		return aws.Config{}, fmt.Errorf("accounts: load base aws config: %w", err)
	}
	stsClient := sts.NewFromConfig(baseCfg)
	credProvider, err := credentials.ResolveAWSCredentialProvider(ctx, root, h.credStore, stsClient)
	if err != nil {
		return aws.Config{}, fmt.Errorf("accounts: resolve credentials for org root %s: %w", root.ID, err)
	}
	cfg := baseCfg.Copy()
	cfg.Credentials = credProvider
	return cfg, nil
}

// runOrgDiscovery dispatches to the configured discovery function — the
// injectable seam Handler.discoverOrgFn for tests, falling back to the real
// accounts.DiscoverOrgAccounts in production.
func (h *Handler) runOrgDiscovery(ctx context.Context, cfg aws.Config) (*accounts.OrgDiscoveryResult, error) {
	discoverFn := h.discoverOrgFn
	if discoverFn == nil {
		discoverFn = accounts.DiscoverOrgAccounts
	}
	disco, err := discoverFn(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("accounts: org discovery failed: %w", err)
	}
	return disco, nil
}

// persistDiscoveredMembers dedupes the discovered list against existing aws
// rows and persists each new one with the spec-mandated defaults
// (enabled=false, aws_auth_mode="", aws_bastion_id=root.ID). Returns the
// {discovered, created, skipped} summary.
func (h *Handler) persistDiscoveredMembers(ctx context.Context, root *config.CloudAccount, members []config.CloudAccount) (DiscoverOrgResult, error) {
	awsProvider := "aws"
	existing, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{Provider: &awsProvider})
	if err != nil {
		return DiscoverOrgResult{}, fmt.Errorf("accounts: list existing aws accounts: %w", err)
	}
	knownExternal := make(map[string]struct{}, len(existing))
	for i := range existing {
		knownExternal[existing[i].ExternalID] = struct{}{}
	}

	result := DiscoverOrgResult{Discovered: len(members)}
	now := time.Now()
	for i := range members {
		member := members[i]
		if _, found := knownExternal[member.ExternalID]; found {
			result.Skipped++
			continue
		}
		// Defaults the spec mandates: persist disabled (operator review gate)
		// + bastion-id pre-filled with this org root's ID. AWSAuthMode is
		// LEFT EMPTY on purpose — pre-setting it to "bastion" while
		// AWSRoleARN is empty would cause awsAmbientCredResult to falsely
		// classify the row as having valid ambient creds (the role_arn /
		// bastion + empty-AWSRoleARN branch returns OK with the "ambient
		// host" message, which is wrong for a discovered member account).
		// The operator's review step must set both AWSAuthMode="bastion"
		// AND a non-empty AWSRoleARN before flipping enabled=true; the
		// scheduler silently skips disabled rows in the meantime, and the
		// empty AWSAuthMode we persist here also fails
		// ResolveAWSCredentialProvider's switch with a clear
		// "unsupported aws_auth_mode" error if the row is enabled
		// prematurely. (CR pass 1 on PR #212.)
		member.Enabled = false
		member.ID = uuid.New().String()
		member.CreatedAt = now
		member.UpdatedAt = now
		member.AWSAuthMode = ""
		member.AWSBastionID = root.ID
		if err := h.config.CreateCloudAccount(ctx, &member); err != nil {
			if isDuplicateKeyError(err) {
				knownExternal[member.ExternalID] = struct{}{}
				result.Skipped++
				continue
			}
			return DiscoverOrgResult{}, fmt.Errorf("accounts: persist discovered %s: %w", member.ExternalID, err)
		}
		knownExternal[member.ExternalID] = struct{}{}
		result.Created++
	}
	return result, nil
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
