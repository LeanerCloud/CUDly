package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"
)

// RegistrationRequest is the JSON body for POST /api/register.
type RegistrationRequest struct {
	Provider            string `json:"provider"`
	ExternalID          string `json:"external_id"`
	AccountName         string `json:"account_name"`
	ContactEmail        string `json:"contact_email"`
	Description         string `json:"description"`
	SourceProvider      string `json:"source_provider"`
	AWSRoleARN          string `json:"aws_role_arn"`
	AWSAuthMode         string `json:"aws_auth_mode"`
	AWSExternalID       string `json:"aws_external_id"`
	AzureSubscriptionID string `json:"azure_subscription_id"`
	AzureTenantID       string `json:"azure_tenant_id"`
	AzureClientID       string `json:"azure_client_id"`
	AzureAuthMode       string `json:"azure_auth_mode"`
	GCPProjectID        string `json:"gcp_project_id"`
	GCPClientEmail      string `json:"gcp_client_email"`
	GCPAuthMode         string `json:"gcp_auth_mode"`
	GCPWIFAudience      string `json:"gcp_wif_audience"`
	CredentialType      string `json:"credential_type"`
	CredentialPayload   string `json:"credential_payload"`
}

// RegistrationStatusResponse is the limited public response for GET /api/register/:token.
type RegistrationStatusResponse struct {
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}

// RejectRequest is the JSON body for POST /api/registrations/:id/reject.
type RejectRequest struct {
	Reason string `json:"reason"`
}

// submitRegistration handles POST /api/register.
func (h *Handler) submitRegistration(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if err := h.checkRateLimit(ctx, req, "register"); err != nil {
		return nil, err
	}

	var body RegistrationRequest
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	if err := validateRegistrationRequest(body); err != nil {
		return nil, err
	}

	token, err := generateReferenceToken()
	if err != nil {
		return nil, fmt.Errorf("registrations: generate token: %w", err)
	}

	reg := &config.AccountRegistration{
		ReferenceToken:      token,
		Status:              "pending",
		Provider:            body.Provider,
		ExternalID:          body.ExternalID,
		AccountName:         body.AccountName,
		ContactEmail:        body.ContactEmail,
		Description:         body.Description,
		SourceProvider:      body.SourceProvider,
		AWSRoleARN:          body.AWSRoleARN,
		AWSAuthMode:         body.AWSAuthMode,
		AWSExternalID:       body.AWSExternalID,
		AzureSubscriptionID: body.AzureSubscriptionID,
		AzureTenantID:       body.AzureTenantID,
		AzureClientID:       body.AzureClientID,
		AzureAuthMode:       body.AzureAuthMode,
		GCPProjectID:        body.GCPProjectID,
		GCPClientEmail:      body.GCPClientEmail,
		GCPAuthMode:         body.GCPAuthMode,
		GCPWIFAudience:      body.GCPWIFAudience,
		RegCredentialType:   body.CredentialType,
	}

	encrypted, encErr := h.encryptRegistrationCredential(body.CredentialPayload)
	if encErr != nil {
		return nil, fmt.Errorf("registrations: encrypt credential: %w", encErr)
	}
	reg.RegCredentialPayload = encrypted

	if err := h.config.CreateAccountRegistration(ctx, reg); err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			return nil, NewClientError(409, "a pending registration already exists for this account")
		}
		return nil, fmt.Errorf("registrations: %w", err)
	}

	// Notify admin (synchronous, errors logged but not propagated).
	if h.emailNotifier != nil {
		if notifyErr := h.emailNotifier.SendRegistrationReceivedNotification(context.Background(), email.RegistrationNotificationData{
			AccountName:  body.AccountName,
			Provider:     body.Provider,
			ExternalID:   body.ExternalID,
			ContactEmail: body.ContactEmail,
			DashboardURL: h.dashboardURL,
		}); notifyErr != nil {
			logging.Warnf("failed to send registration notification: %v", notifyErr)
		}
	}

	return map[string]string{
		"reference_token": token,
		"status":          "pending",
	}, nil
}

// getRegistrationStatus handles GET /api/register/:token.
func (h *Handler) getRegistrationStatus(ctx context.Context, token string) (any, error) {
	reg, err := h.config.GetAccountRegistrationByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("registrations: %w", err)
	}
	if reg == nil {
		return nil, NewClientError(404, "registration not found")
	}

	return RegistrationStatusResponse{
		Status:          reg.Status,
		CreatedAt:       reg.CreatedAt.Format(time.RFC3339),
		RejectionReason: reg.RejectionReason,
	}, nil
}

// listRegistrations handles GET /api/registrations.
func (h *Handler) listRegistrations(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	filter := buildRegistrationFilter(req.QueryStringParameters)

	regs, err := h.config.ListAccountRegistrations(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("registrations: %w", err)
	}
	if regs == nil {
		regs = []config.AccountRegistration{}
	}
	return regs, nil
}

// getRegistration handles GET /api/registrations/:id.
func (h *Handler) getRegistration(ctx context.Context, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	reg, err := h.config.GetAccountRegistration(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("registrations: %w", err)
	}
	if reg == nil {
		return nil, NewClientError(404, "registration not found")
	}
	return reg, nil
}

// getPendingRegistration fetches a registration by ID and verifies it is pending.
func (h *Handler) getPendingRegistration(ctx context.Context, id string) (*config.AccountRegistration, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}
	reg, err := h.config.GetAccountRegistration(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("registrations: %w", err)
	}
	if reg == nil {
		return nil, NewClientError(404, "registration not found")
	}
	if reg.Status != "pending" {
		return nil, NewClientError(409, fmt.Sprintf("registration is already %s", reg.Status))
	}
	return reg, nil
}

// setReviewMetadata populates reviewer info from the current admin session.
func (h *Handler) setReviewMetadata(ctx context.Context, reg *config.AccountRegistration, httpReq *events.LambdaFunctionURLRequest) {
	reviewedAt := time.Now()
	reg.ReviewedAt = &reviewedAt
	session, _ := h.requireAdmin(ctx, httpReq)
	if session != nil {
		reg.ReviewedBy = &session.UserID
	}
}

// storeRegistrationCredentials decrypts and stores credentials from the registration
// record into the account_credentials table. Errors are logged, not propagated.
func (h *Handler) storeRegistrationCredentials(ctx context.Context, reg *config.AccountRegistration, accountID string) {
	if reg.RegCredentialPayload == "" || reg.RegCredentialType == "" || h.credStore == nil {
		return
	}
	plaintext, err := h.credStore.DecryptPayload(reg.RegCredentialPayload)
	if err != nil {
		logging.Warnf("registration %s: failed to decrypt credential payload: %v", reg.ID, err)
		return
	}
	if err := h.credStore.SaveCredential(ctx, accountID, reg.RegCredentialType, plaintext); err != nil {
		logging.Warnf("registration %s: failed to store credential: %v", reg.ID, err)
	}
}

// encryptRegistrationCredential encrypts the credential payload for storage in the
// registration table. Returns empty string if no credential is provided.
func (h *Handler) encryptRegistrationCredential(payload string) (string, error) {
	if payload == "" || h.credStore == nil {
		return "", nil
	}
	return h.credStore.EncryptPayload([]byte(payload))
}

// notifyRegistrant sends an email about an approval or rejection.
// Errors are logged but not propagated (matching sendPurchaseApprovalEmail pattern).
func (h *Handler) notifyRegistrant(reg *config.AccountRegistration, data email.RegistrationDecisionData) {
	if h.emailNotifier == nil || reg.ContactEmail == "" {
		return
	}
	if err := h.emailNotifier.SendRegistrationDecisionNotification(context.Background(), reg.ContactEmail, data); err != nil {
		logging.Warnf("failed to send registration decision notification: %v", err)
	}
}

// approveRegistration handles POST /api/registrations/:id/approve.
// The body is a CloudAccountRequest (the same format as POST /api/accounts),
// pre-populated from registration data by the frontend and optionally edited.
func (h *Handler) approveRegistration(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, id string) (any, error) {
	reg, err := h.getPendingRegistration(ctx, id)
	if err != nil {
		return nil, err
	}

	var acctReq CloudAccountRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &acctReq); err != nil {
		return nil, NewClientError(400, "invalid account request body")
	}
	if err := validateCloudAccountRequest(acctReq); err != nil {
		return nil, err
	}

	// Atomically transition to "approved" first — prevents double-approval.
	reg.Status = "approved"
	h.setReviewMetadata(ctx, reg, httpReq)
	if err := h.config.TransitionRegistrationStatus(ctx, reg, "pending"); err != nil {
		if errors.Is(err, config.ErrRegistrationConflict) {
			return nil, NewClientError(409, "registration was already processed by another request")
		}
		return nil, fmt.Errorf("registrations: transition: %w", err)
	}

	// Create the cloud account (only one request reaches here).
	now := time.Now()
	account := cloudAccountFromRequest(acctReq)
	account.ID = uuid.New().String()
	// Auto-enable when the operator either embedded a credential in
	// the registration (legacy key-based flow) OR when the account
	// uses an auth mode that doesn't require any stored credential
	// at all — ambient (instance profile, managed identity, ADC) or
	// KMS-backed federated. Without this, every federated-path
	// approval leaves the account disabled and the operator has to
	// follow up with a manual PUT enabled=true.
	account.Enabled = reg.RegCredentialPayload != "" || accountHasCredentialFreePath(account)
	account.CreatedAt = now
	account.UpdatedAt = now

	if err := h.config.CreateCloudAccount(ctx, account); err != nil {
		return nil, fmt.Errorf("registrations: create account: %w", err)
	}

	// Auto-store credentials if the registration included them.
	h.storeRegistrationCredentials(ctx, reg, account.ID)

	// Link the cloud account and wipe the credential payload from the registration record.
	reg.CloudAccountID = &account.ID
	reg.RegCredentialPayload = "" // Don't keep encrypted credentials longer than needed.
	if err := h.config.UpdateAccountRegistration(ctx, reg); err != nil {
		logging.Warnf("registration %s approved but failed to link cloud_account_id: %v", reg.ID, err)
	}

	h.notifyRegistrant(reg, email.RegistrationDecisionData{
		AccountName: reg.AccountName, Provider: reg.Provider,
		ExternalID: reg.ExternalID, Decision: "approved",
	})

	return account, nil
}

// accountHasCredentialFreePath returns true when the account's auth
// mode resolves a credential without any stored secret — either via
// ambient instance credentials (role assumption, managed identity,
// Application Default Credentials) or via CUDly's KMS-backed OIDC
// federated path. These accounts should auto-enable on approval
// because there's no follow-up "upload the PEM/JSON" step.
//
// Cert-based legacy Azure WIF is NOT included here — it needs a
// stored azure_wif_private_key blob that the operator uploads
// separately after approval, so those accounts keep the old
// opt-in-via-manual-PUT behaviour.
func accountHasCredentialFreePath(acct *config.CloudAccount) bool {
	switch acct.Provider {
	case "aws":
		// role_arn flows via STS AssumeRole with ambient CUDly creds;
		// workload_identity_federation mints its own token file.
		return acct.AWSAuthMode == "role_arn" || acct.AWSAuthMode == "workload_identity_federation"
	case "azure":
		// managed_identity: ambient. workload_identity_federation:
		// federated via the KMS-backed path when no PEM is stored —
		// the /test handler falls back to the cert path when one is
		// present, so auto-enabling the account either way is fine.
		return acct.AzureAuthMode == "managed_identity" || acct.AzureAuthMode == "workload_identity_federation"
	case "gcp":
		// application_default: ambient (Cloud Run / GKE). WIF:
		// federated when gcp_wif_audience is set (the CLI template
		// always sets it for the new flow).
		return acct.GCPAuthMode == "application_default" ||
			(acct.GCPAuthMode == "workload_identity_federation" && acct.GCPWIFAudience != "")
	}
	return false
}

// rejectRegistration handles POST /api/registrations/:id/reject.
func (h *Handler) rejectRegistration(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, id string) (any, error) {
	reg, err := h.getPendingRegistration(ctx, id)
	if err != nil {
		return nil, err
	}

	var body RejectRequest
	if httpReq.Body != "" {
		if err := json.Unmarshal([]byte(httpReq.Body), &body); err != nil {
			return nil, NewClientError(400, "invalid request body")
		}
	}

	reg.Status = "rejected"
	reg.RejectionReason = body.Reason
	h.setReviewMetadata(ctx, reg, httpReq)
	if err := h.config.TransitionRegistrationStatus(ctx, reg, "pending"); err != nil {
		if errors.Is(err, config.ErrRegistrationConflict) {
			return nil, NewClientError(409, "registration was already processed by another request")
		}
		return nil, fmt.Errorf("registrations: transition: %w", err)
	}

	h.notifyRegistrant(reg, email.RegistrationDecisionData{
		AccountName: reg.AccountName, Provider: reg.Provider,
		ExternalID: reg.ExternalID, Decision: "rejected",
		RejectionReason: body.Reason,
	})

	// Return a filtered view — don't expose reference_token to admin.
	return map[string]any{
		"id":               reg.ID,
		"status":           reg.Status,
		"provider":         reg.Provider,
		"external_id":      reg.ExternalID,
		"account_name":     reg.AccountName,
		"rejection_reason": reg.RejectionReason,
	}, nil
}

// deleteRegistration handles DELETE /api/registrations/:id.
func (h *Handler) deleteRegistration(ctx context.Context, id string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}
	reg, err := h.config.GetAccountRegistration(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("registrations: %w", err)
	}
	if reg == nil {
		return nil, NewClientError(404, "registration not found")
	}
	if err := h.config.DeleteAccountRegistration(ctx, id); err != nil {
		return nil, fmt.Errorf("registrations: delete: %w", err)
	}
	return map[string]string{"status": "deleted"}, nil
}

// validateRegistrationRequest checks required fields for a registration submission.
func validateRegistrationRequest(req RegistrationRequest) error {
	if !validAccountProviders[req.Provider] {
		return NewClientError(400, "provider must be one of: aws, azure, gcp")
	}
	if req.ExternalID == "" {
		return NewClientError(400, "external_id is required")
	}
	if req.AccountName == "" {
		return NewClientError(400, "account_name is required")
	}
	if req.ContactEmail == "" {
		return NewClientError(400, "contact_email is required")
	}
	if !strings.Contains(req.ContactEmail, "@") || len(req.ContactEmail) < 5 {
		return NewClientError(400, "contact_email must be a valid email address")
	}
	return nil
}

func buildRegistrationFilter(params map[string]string) config.AccountRegistrationFilter {
	var filter config.AccountRegistrationFilter
	if s, ok := params["status"]; ok && s != "" {
		filter.Status = &s
	}
	if p, ok := params["provider"]; ok && p != "" {
		filter.Provider = &p
	}
	if s, ok := params["search"]; ok {
		filter.Search = s
	}
	return filter
}

func generateReferenceToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
