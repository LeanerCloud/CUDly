// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/internal/commitmentopts"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
)

// RateLimiter provides simple in-memory rate limiting for auth endpoints
// Note: For Lambda, this only works within a single warm instance.
// For production, use database-backed rate limiting.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*rateLimitEntry
}

type rateLimitEntry struct {
	count     int
	resetTime time.Time
}

// RateLimiterInterface defines the interface for rate limiting implementations
// This allows for both in-memory and database-backed rate limiters
type RateLimiterInterface interface {
	// Allow checks if a request should be allowed based on rate limits
	// Returns (allowed bool, error)
	Allow(ctx context.Context, key string, endpoint string) (bool, error)

	// AllowWithIP is a convenience method for IP-based rate limiting
	AllowWithIP(ctx context.Context, ip string, endpoint string) (bool, error)

	// AllowWithEmail is a convenience method for email-based rate limiting
	AllowWithEmail(ctx context.Context, email string, endpoint string) (bool, error)

	// AllowWithUser is a convenience method for user-based rate limiting
	AllowWithUser(ctx context.Context, userID string, endpoint string) (bool, error)
}

// HandlerConfig holds configuration for the API handler
type HandlerConfig struct {
	ConfigStore       config.StoreInterface
	CredentialStore   credentials.CredentialStore
	PurchaseManager   PurchaseManagerInterface
	Scheduler         SchedulerInterface
	AuthService       AuthServiceInterface
	APIKeySecretARN   string
	EnableDashboard   bool
	DashboardBucket   string
	CORSAllowedOrigin string // CORS allowed origin (default "*")
	RateLimiter       RateLimiterInterface
	EmailNotifier     email.SenderInterface // Optional: used to send purchase approval emails
	DashboardURL      string                // Base URL for approval/cancel links in emails
	// Analytics configuration (optional)
	AnalyticsClient    AnalyticsClientInterface
	AnalyticsCollector AnalyticsCollectorInterface
	// OIDCSigner is the cloud-agnostic signer that backs
	// /.well-known/openid-configuration and /.well-known/jwks.json.
	// Nil disables the OIDC issuer endpoints (they return 404).
	OIDCSigner oidc.Signer
	// OIDCIssuerURL is the canonical issuer URL the OIDC handlers
	// publish in the Discovery document. Must match what Azure AD
	// federated credentials are registered with.
	OIDCIssuerURL string
	// CommitmentOpts discovers which (term, payment) combinations each
	// AWS service actually sells and validates saves against that data.
	// Nil disables both the /api/commitment-options endpoint (returns
	// unavailable) and save-side validation in updateServiceConfig.
	CommitmentOpts CommitmentOptsInterface
	// EncryptionKeySource is the env var name that resolved the credential
	// encryption key (e.g. "CREDENTIAL_ENCRYPTION_KEY_SECRET_NAME"). Empty
	// when no credStore is configured. Used by the /health endpoint to
	// surface which key source is in use and detect dev-key state.
	EncryptionKeySource string
}

// CommitmentOptsInterface lets us swap the real *commitmentopts.Service for
// a stub in handler tests without pulling in the probe+store machinery.
type CommitmentOptsInterface interface {
	Get(ctx context.Context) (commitmentopts.Options, error)
	Validate(ctx context.Context, provider, service string, term int, payment string) (bool, error)
}

// AnalyticsClientInterface defines the interface for analytics queries.
//
// accountUUIDs / accountExternalIDsByProvider are the dual-column account
// filter: rows match when cloud_account_id = ANY(accountUUIDs) OR (provider = p
// AND account_id = ANY(accountExternalIDsByProvider[p])). Both nil/empty means
// "all accounts accessible to the caller" (scoping is enforced upstream in the
// handler). The handler resolves the requested account (a top-bar chip UUID)
// into both representations via resolveSingleAccountFilterIDs so rows that carry
// only the external account_id (cloud_account_id NULL) are still aggregated, and
// the external ids stay grouped by provider so a reused external number across
// providers cannot leak the wrong rows (issue #701/#498/#866).
type AnalyticsClientInterface interface {
	QueryHistory(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, start, end time.Time, interval string) ([]HistoryDataPoint, *HistorySummary, error)
	QueryBreakdown(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, start, end time.Time, dimension string) (map[string]BreakdownValue, error)
}

// AnalyticsCollectorInterface defines the interface for analytics collection
type AnalyticsCollectorInterface interface {
	Collect(ctx context.Context) error
}

// HistoryDataPoint represents aggregated historical data
type HistoryDataPoint struct {
	Timestamp         time.Time          `json:"timestamp"`
	TotalSavings      float64            `json:"total_savings"`
	TotalUpfront      float64            `json:"total_upfront"`
	PurchaseCount     int                `json:"purchase_count"`
	CumulativeSavings float64            `json:"cumulative_savings"`
	ByService         map[string]float64 `json:"by_service,omitempty"`
	ByProvider        map[string]float64 `json:"by_provider,omitempty"`
}

// HistorySummaryAnalytics contains aggregated statistics for analytics
type HistorySummaryAnalytics struct {
	TotalPeriodSavings      float64 `json:"total_period_savings"`
	TotalUpfrontSpent       float64 `json:"total_upfront_spent"`
	PurchaseCount           int     `json:"purchase_count"`
	AverageSavingsPerPeriod float64 `json:"average_savings_per_period"`
	PeakSavings             float64 `json:"peak_savings"`
}

// BreakdownValue represents savings breakdown by dimension
type BreakdownValue struct {
	TotalSavings  float64 `json:"total_savings"`
	TotalUpfront  float64 `json:"total_upfront"`
	PurchaseCount int     `json:"purchase_count"`
	Percentage    float64 `json:"percentage"`
}

// PurchaseManagerInterface defines purchase manager methods used by handler.
// `actor` is the session-authenticated user's email for per-user
// attribution via the auth-gated deep-link flow; pass "" for token-only
// paths (message workers, legacy callers) where attribution falls back to
// the notification email at render time.
type PurchaseManagerInterface interface {
	ApproveExecution(ctx context.Context, execID, token, actor string) error
	ApproveAndExecute(ctx context.Context, execID, actor string) error
	CancelExecution(ctx context.Context, execID, token, actor string) error
}

// SchedulerInterface defines scheduler methods used by handler
type SchedulerInterface interface {
	CollectRecommendations(ctx context.Context) (*scheduler.CollectResult, error)
	ListRecommendations(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error)
	// GetRecommendationByID fetches a single rec by its application-level id,
	// bypassing account-override filtering so deep-linked URLs to override-
	// hidden recs resolve. hiddenBy is non-nil when the rec would be dropped by
	// the override filter; callers render a "hidden" banner. Returns nil, nil,
	// nil when the rec is absent or fully suppressed.
	GetRecommendationByID(ctx context.Context, id string) (rec *config.RecommendationRecord, hiddenBy []string, err error)
}

// AuthServiceInterface defines auth service methods used by handler
// Note: This interface uses API-specific types that are converted from auth package types
type AuthServiceInterface interface {
	Login(ctx context.Context, req LoginRequest) (*LoginResponse, error)
	Logout(ctx context.Context, token string) error
	ValidateSession(ctx context.Context, token string) (*Session, error)
	ValidateCSRFToken(ctx context.Context, sessionToken, csrfToken string) error
	SetupAdmin(ctx context.Context, req SetupAdminRequest) (*LoginResponse, error)
	CheckAdminExists(ctx context.Context) (bool, error)
	RequestPasswordReset(ctx context.Context, email string) error
	ConfirmPasswordReset(ctx context.Context, req PasswordResetConfirm) error
	// ResetTokenStatus returns the runtime state of a reset token
	// without consuming it. Used by the GET /api/auth/reset-password/
	// status endpoint so the frontend can branch on expired / used
	// tokens before rendering the reset-password form (issues #460,
	// #461). state is one of "valid" | "expired" | "used"; flow is
	// "reset" | "invite".
	ResetTokenStatus(ctx context.Context, token string) (state string, flow string, err error)
	GetUser(ctx context.Context, userID string) (*User, error)
	UpdateUserProfile(ctx context.Context, userID string, email string, currentPassword string, newPassword string) error
	// User management - uses auth.API* types
	CreateUserAPI(ctx context.Context, req any) (any, error)
	UpdateUserAPI(ctx context.Context, actorUserID, userID string, req any) (any, error)
	DeleteUser(ctx context.Context, userID string) error
	ListUsersAPI(ctx context.Context) (any, error)
	ChangePasswordAPI(ctx context.Context, userID, currentPassword, newPassword string) error
	// MFA lifecycle (issue #497). All four require the user to be
	// already authenticated; setup + disable additionally require a
	// fresh password re-verify carried in the request body.
	MFASetupAPI(ctx context.Context, userID, password string) (secret, provisioningURI string, err error)
	MFAEnableAPI(ctx context.Context, userID, code string) (recoveryCodes []string, err error)
	MFADisableAPI(ctx context.Context, userID, password, codeOrRecovery string) error
	MFARegenerateRecoveryCodesAPI(ctx context.Context, userID, code string) (recoveryCodes []string, err error)
	// Group management - uses auth.API* types
	CreateGroupAPI(ctx context.Context, req any) (any, error)
	UpdateGroupAPI(ctx context.Context, groupID string, req any) (any, error)
	DeleteGroup(ctx context.Context, groupID string) error
	GetGroupAPI(ctx context.Context, groupID string) (any, error)
	ListGroupsAPI(ctx context.Context) (any, error)
	// Permission checking
	HasPermissionAPI(ctx context.Context, userID, action, resource string) (bool, error)
	// GetUserPermissionsAPI returns the effective permission set for a user
	// (union of all group permissions). Used by GET /api/auth/me/permissions.
	// Returns []auth.APIPermission converted to []PermissionEntry by the handler.
	GetUserPermissionsAPI(ctx context.Context, userID string) (any, error)
	// Account access - returns the union of allowed_accounts from all user groups (empty = all access)
	GetAllowedAccountsAPI(ctx context.Context, userID string) ([]string, error)
	// API Key management
	CreateAPIKeyAPI(ctx context.Context, userID string, req any) (any, error)
	ListUserAPIKeysAPI(ctx context.Context, userID string) (any, error)
	DeleteAPIKeyAPI(ctx context.Context, userID, keyID string) error
	RevokeAPIKeyAPI(ctx context.Context, userID, keyID string) error
	ValidateUserAPIKeyAPI(ctx context.Context, apiKey string) (any, any, error)
}

// Auth request/response types (to avoid import cycle with auth package)
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code,omitempty"`
}

type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt string    `json:"expires_at"`
	User      *UserInfo `json:"user"`
	CSRFToken string    `json:"csrf_token,omitempty"`
}

type UserInfo struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	Groups     []string `json:"groups,omitempty"`
	MFAEnabled bool     `json:"mfa_enabled"`
}

type SetupAdminRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type PasswordResetRequest struct {
	Email string `json:"email"`
}

type PasswordResetConfirm struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type Session struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

type User struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	Groups     []string `json:"groups,omitempty"`
	MFAEnabled bool     `json:"mfa_enabled"`
	CreatedAt  string   `json:"created_at,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

// CreateUserRequest represents a request to create a new user. Groups must be
// non-empty: authorization is group-membership-only (issue #907).
type CreateUserRequest struct {
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Groups   []string `json:"groups,omitempty"`
}

// UpdateUserRequest represents a request to update a user
type UpdateUserRequest struct {
	Email  string   `json:"email,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// Group represents a user group with permissions
type Group struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Description     string       `json:"description,omitempty"`
	Permissions     []Permission `json:"permissions"`
	AllowedAccounts []string     `json:"allowed_accounts,omitempty"`
	CreatedAt       string       `json:"created_at,omitempty"`
	UpdatedAt       string       `json:"updated_at,omitempty"`
}

// Permission represents an action that can be performed on a resource
type Permission struct {
	Action      string                `json:"action"`
	Resource    string                `json:"resource"`
	Constraints *PermissionConstraint `json:"constraints,omitempty"`
}

// PermissionConstraint limits where a permission applies
type PermissionConstraint struct {
	Accounts  []string `json:"accounts,omitempty"`
	Providers []string `json:"providers,omitempty"`
	Services  []string `json:"services,omitempty"`
	Regions   []string `json:"regions,omitempty"`
	MaxAmount float64  `json:"max_amount,omitempty"`
}

// CreateGroupRequest represents a request to create a new group
type CreateGroupRequest struct {
	Name            string       `json:"name"`
	Description     string       `json:"description,omitempty"`
	Permissions     []Permission `json:"permissions"`
	AllowedAccounts []string     `json:"allowed_accounts,omitempty"`
}

// UpdateGroupRequest represents a request to update a group
type UpdateGroupRequest struct {
	Name            string       `json:"name,omitempty"`
	Description     string       `json:"description,omitempty"`
	Permissions     []Permission `json:"permissions,omitempty"`
	AllowedAccounts []string     `json:"allowed_accounts,omitempty"`
}

// ChangePasswordRequest represents a request to change password
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ProfileUpdateRequest represents a profile update request
type ProfileUpdateRequest struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password,omitempty"`
}

// API Response types for type safety

// ConfigResponse holds the configuration response
type ConfigResponse struct {
	Global         *config.GlobalConfig   `json:"global"`
	Services       []config.ServiceConfig `json:"services"`
	SourceCloud    string                 `json:"source_cloud,omitempty"`
	SourceIdentity *sourceIdentity        `json:"source_identity,omitempty"`
}

// StatusResponse holds a simple status response
type StatusResponse struct {
	Status string `json:"status"`
}

// RecommendationsSummary holds aggregate statistics for recommendations
type RecommendationsSummary struct {
	TotalCount          int     `json:"total_count"`
	TotalMonthlySavings float64 `json:"total_monthly_savings"`
	TotalUpfrontCost    float64 `json:"total_upfront_cost"`
	AvgPaybackMonths    float64 `json:"avg_payback_months"`
}

// RecommendationsResponse holds the recommendations response
type RecommendationsResponse struct {
	Recommendations []config.RecommendationRecord `json:"recommendations"`
	Summary         RecommendationsSummary        `json:"summary"`
	Regions         []string                      `json:"regions"`
}

// UsagePoint is a single sample in the per-recommendation usage time
// series surfaced by GET /api/recommendations/:id/detail. The series is
// always ordered by Timestamp ascending. CPUPct/MemPct are 0..100.
//
// Empty in the current implementation: the collector pipeline does not
// yet persist time-series utilisation per recommendation. The endpoint
// returns the empty slice with a non-error status so the frontend can
// render a "Usage history not yet available" placeholder rather than a
// broken empty chart. See known_issues/28_recommendations_detail_endpoint.md
// for the full collector wiring follow-up.
type UsagePoint struct {
	Timestamp string  `json:"timestamp"`
	CPUPct    float64 `json:"cpu_pct"`
	MemPct    float64 `json:"mem_pct"`
}

// RecommendationDetailResponse is the per-id payload backing the
// Recommendations row-click drawer. Contract documented in issue #44.
//
// ConfidenceBucket is "low" | "medium" | "high" — server-side mirror of
// the client-side heuristic that previously lived in
// frontend/src/recommendations.ts:confidenceBucketFor. Centralising it
// server-side lets future provider-specific tuning happen in one place.
//
// ProvenanceNote is a short human-readable string naming the collector
// + the freshness window. Rendered verbatim in the drawer.
type RecommendationDetailResponse struct {
	ID               string       `json:"id"`
	UsageHistory     []UsagePoint `json:"usage_history"`
	ConfidenceBucket string       `json:"confidence_bucket"`
	ProvenanceNote   string       `json:"provenance_note"`
	// HiddenBy is non-nil when the rec is filtered out by an account-service
	// override (issue #214). Each element names one failing dimension:
	// "enabled=false", "engine", "region", or "resource_type". The frontend
	// renders a "hidden by your override" banner when this field is present.
	// Absent (null) means the rec is fully visible.
	HiddenBy []string `json:"hidden_by,omitempty"`
}

// PlansResponse holds the purchase plans response
type PlansResponse struct {
	Plans []config.PurchasePlan `json:"plans"`
}

// CurrentUserResponse holds the current user response
type CurrentUserResponse struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	Groups     []string `json:"groups,omitempty"`
	MFAEnabled bool     `json:"mfa_enabled"`
}

// AdminExistsResponse holds the admin exists check response
type AdminExistsResponse struct {
	AdminExists bool `json:"admin_exists"`
}

// PermissionEntry is a single {action, resource} pair in the permissions
// response. Constraints are omitted from the wire shape for now; the
// frontend uses the pair for UX gating only.
type PermissionEntry struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
}

// UserPermissionsResponse is the response shape for GET /api/auth/me/permissions.
// Permissions is the effective set derived from the union of the user's groups.
// IsAdmin mirrors whether the effective set contains the {admin, *} wildcard.
type UserPermissionsResponse struct {
	Permissions []PermissionEntry `json:"permissions"`
	IsAdmin     bool              `json:"is_admin"`
}

// MFA enrollment + lifecycle DTOs (issue #497). Passwords carried by
// these requests are base64-encoded by the frontend (same convention
// as login / change-password / reset-password); the handler decodes
// before handing to the auth service.

// MFASetupRequest begins an MFA enrollment. Current password is
// required as defence-in-depth — a stolen session alone shouldn't
// be enough to swap a user's MFA secret.
type MFASetupRequest struct {
	Password string `json:"password"`
}

// MFASetupResponse returns the freshly-generated secret + the
// otpauth:// URI the frontend renders as a QR code. The secret is
// already persisted server-side as the pending secret; clients do
// not need to round-trip it back on enable.
type MFASetupResponse struct {
	Secret          string `json:"secret"`
	ProvisioningURI string `json:"provisioning_uri"`
}

// MFAEnableRequest finalizes an enrollment by proving the user
// loaded the secret into their authenticator (the supplied code is
// validated against the pending secret).
type MFAEnableRequest struct {
	Code string `json:"code"`
}

// MFAEnableResponse returns the plaintext recovery codes exactly
// once. Backend stores only bcrypt hashes.
type MFAEnableResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// MFADisableRequest turns off MFA. Requires the current password AND
// a fresh proof-of-possession (TOTP code or unused recovery code).
type MFADisableRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// MFARegenerateRequest replaces all stored recovery codes. Requires
// a fresh TOTP code (NOT a recovery code — see service for the
// rationale).
type MFARegenerateRequest struct {
	Code string `json:"code"`
}

// MFARegenerateResponse mirrors MFAEnableResponse — plaintext codes
// returned exactly once.
type MFARegenerateResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// EmptyServiceConfigResponse represents an empty service config
type EmptyServiceConfigResponse struct{}

// PublicInfoResponse holds public information about the CUDly instance
type PublicInfoResponse struct {
	Version                string `json:"version"`
	AdminExists            bool   `json:"admin_exists"`
	APIKeySecretURL        string `json:"api_key_secret_url,omitempty"`
	DeploymentAWSAccountID string `json:"deployment_aws_account_id,omitempty"`
}

// DashboardSummaryResponse holds the dashboard summary data
type DashboardSummaryResponse struct {
	PotentialMonthlySavings float64                   `json:"potential_monthly_savings"`
	TotalRecommendations    int                       `json:"total_recommendations"`
	ActiveCommitments       int                       `json:"active_commitments"`
	CommittedMonthly        float64                   `json:"committed_monthly"`
	CurrentCoverage         float64                   `json:"current_coverage"`
	TargetCoverage          float64                   `json:"target_coverage"`
	YTDSavings              float64                   `json:"ytd_savings"`
	ByService               map[string]ServiceSavings `json:"by_service"`
}

// ServiceSavings holds savings data for a service
type ServiceSavings struct {
	PotentialSavings float64 `json:"potential_savings"`
	CurrentSavings   float64 `json:"current_savings"`
}

// InventoryCommitment is one row in the per-commitment Inventory &
// Coverage view (issue #340 deferred sub-task — "Active commitments").
// Aggregated from PurchaseHistoryRecord rows that are still within
// their term; the inventory endpoint filters out expired commitments
// before responding.
//
// ID is `{account_id}:{purchase_id}` so the row is uniquely identifiable
// in the JSON payload without a DB schema change — purchase_id alone
// is unique within an account but not globally across the table.
//
// Status is always `"active"` today (the handler drops expired rows).
// The field stays in the response shape so a future "expiring soon"
// sub-state has a slot without a breaking API change.
type InventoryCommitment struct {
	ID            string    `json:"id"`
	Provider      string    `json:"provider"`
	AccountID     string    `json:"account_id"`
	AccountName   string    `json:"account_name,omitempty"`
	Service       string    `json:"service"`
	ResourceType  string    `json:"resource_type,omitempty"`
	Region        string    `json:"region"`
	Count         int       `json:"count"`
	TermYears     int       `json:"term_years"`
	PaymentOption string    `json:"payment_option,omitempty"`
	StartDate     time.Time `json:"start_date"`
	EndDate       time.Time `json:"end_date"`
	UpfrontCost   float64   `json:"upfront_cost"`
	// MonthlyCost is nil when the source purchase_history row has a NULL
	// monthly_cost (provider did not return a monthly breakdown). The
	// frontend renders "—" for nil and "$X.XX" when non-nil.
	MonthlyCost      *float64 `json:"monthly_cost"`
	EstimatedSavings float64  `json:"estimated_savings"`
	Status           string   `json:"status"`
}

// InventoryCommitmentsResponse is the envelope returned by
// GET /api/inventory/commitments. Commitments is always a slice — never
// nil — so the frontend can rely on `resp.commitments.length` without
// a null check.
type InventoryCommitmentsResponse struct {
	Commitments []InventoryCommitment `json:"commitments"`
}

// CoverageServiceRow is one service row within a provider's coverage
// section. CoveredMonthly is the sum of active-commitment MonthlyCost
// values for the (provider, service) pair. OnDemandMonthly is the sum
// of recommendation Savings values — i.e. the portion of on-demand
// spend that is NOT yet committed. CoveragePct is nil when both sums
// are zero (no usage detected), not 0, to preserve the "absent"
// semantic per feedback_nullable_not_zero.
type CoverageServiceRow struct {
	Service         string   `json:"service"`
	CoveredMonthly  float64  `json:"covered_monthly"`
	OnDemandMonthly float64  `json:"on_demand_monthly"`
	CoveragePct     *float64 `json:"coverage_pct"`
}

// ProviderCoverageSection is the per-provider block returned by
// GET /api/inventory/coverage. Services is nil (not []) when the
// provider has no usage data, which the frontend uses to distinguish
// "no usage detected" from "usage exists but all services are 0%".
// OverallCoveragePct follows the same null-vs-zero contract as
// CoverageServiceRow.CoveragePct.
type ProviderCoverageSection struct {
	Provider           string               `json:"provider"`
	Services           []CoverageServiceRow `json:"services"`
	OverallCoveragePct *float64             `json:"overall_coverage_pct"`
}

// CoverageBreakdownResponse is the envelope returned by
// GET /api/inventory/coverage.
type CoverageBreakdownResponse struct {
	Providers []ProviderCoverageSection `json:"providers"`
}

// UpcomingPurchaseResponse holds upcoming purchase data
type UpcomingPurchaseResponse struct {
	Purchases []UpcomingPurchase `json:"purchases"`
}

// UpcomingPurchase represents one upcoming planned purchase — a pending
// purchase_executions row whose scheduled_date hasn't fired yet, joined
// to its parent PurchasePlan for display.
//
// The dashboard's Cancel button targets ExecutionID via
// DELETE /api/purchases/planned/{id} (api.deletePlannedPurchase) so the
// operator removes just THIS scheduled instance and leaves the plan
// template intact — the next scheduler tick re-creates the next instance
// for the plan. PlanID is exposed as context (e.g. for linking to the
// plan's settings) and is NOT what destructive action endpoints should
// target. PR #207 + #213 history: an earlier iteration routed Cancel to
// api.deletePlan(planID) which deleted the entire plan; that was too
// aggressive — operators usually want "skip this scheduled run", not
// "nuke the recurring template".
type UpcomingPurchase struct {
	ExecutionID      string  `json:"execution_id"`
	PlanID           string  `json:"plan_id"`
	PlanName         string  `json:"plan_name"`
	ScheduledDate    string  `json:"scheduled_date"`
	Provider         string  `json:"provider"`
	Service          string  `json:"service"`
	StepNumber       int     `json:"step_number"`
	TotalSteps       int     `json:"total_steps"`
	EstimatedSavings float64 `json:"estimated_savings"`
}

// PlannedPurchasesResponse holds the list of planned purchases
type PlannedPurchasesResponse struct {
	Purchases []PlannedPurchase `json:"purchases"`
}

// PlannedPurchase represents a scheduled purchase from a plan
type PlannedPurchase struct {
	ID               string  `json:"id"`
	PlanID           string  `json:"plan_id"`
	PlanName         string  `json:"plan_name"`
	ScheduledDate    string  `json:"scheduled_date"`
	Provider         string  `json:"provider"`
	Service          string  `json:"service"`
	ResourceType     string  `json:"resource_type"`
	Region           string  `json:"region"`
	Count            int     `json:"count"`
	Term             int     `json:"term"`
	Payment          string  `json:"payment"`
	EstimatedSavings float64 `json:"estimated_savings"`
	UpfrontCost      float64 `json:"upfront_cost"`
	Status           string  `json:"status"`
	StepNumber       int     `json:"step_number"`
	TotalSteps       int     `json:"total_steps"`
}

// PlanRequest represents the API request format for creating/updating plans
// The frontend sends ramp_schedule as a string, which we convert to the proper struct
type PlanRequest struct {
	Name                   string `json:"name"`
	Description            string `json:"description,omitempty"`
	Enabled                bool   `json:"enabled"`
	AutoPurchase           bool   `json:"auto_purchase"`
	NotificationDaysBefore int    `json:"notification_days_before"`
	// Frontend sends these as top-level fields
	Provider       string `json:"provider,omitempty"`
	Service        string `json:"service,omitempty"`
	Term           int    `json:"term,omitempty"`
	Payment        string `json:"payment,omitempty"`
	TargetCoverage int    `json:"target_coverage,omitempty"`
	// Ramp schedule as string from frontend (immediate, weekly-25pct, monthly-10pct, custom)
	RampSchedule       string `json:"ramp_schedule,omitempty"`
	CustomStepPercent  int    `json:"custom_step_percent,omitempty"`
	CustomIntervalDays int    `json:"custom_interval_days,omitempty"`

	// TargetAccounts is the list of cloud_account UUIDs the plan will purchase
	// for. Required (non-empty) on POST /plans — a plan with no rows in
	// plan_accounts is a "universal plan", which the design no longer allows:
	// every plan must be tied to at least one explicit account. The handler
	// inserts the plan_accounts rows immediately after CreatePurchasePlan so
	// the two writes are observed together by downstream consumers.
	TargetAccounts []string `json:"target_accounts,omitempty"`
}

// toPurchasePlan converts a PlanRequest to a config.PurchasePlan
func (r *PlanRequest) toPurchasePlan() *config.PurchasePlan {
	now := time.Now()
	plan := &config.PurchasePlan{
		Name:                   r.Name,
		Enabled:                r.Enabled,
		AutoPurchase:           r.AutoPurchase,
		NotificationDaysBefore: r.NotificationDaysBefore,
		CreatedAt:              now,
		UpdatedAt:              now,
	}

	plan.RampSchedule = r.buildRampSchedule(now)
	plan.Services = r.buildServiceConfig()
	plan.NextExecutionDate = r.calculateNextExecutionDate(now, plan.RampSchedule)

	return plan
}

// buildRampSchedule builds the ramp schedule from request parameters
func (r *PlanRequest) buildRampSchedule(now time.Time) config.RampSchedule {
	if preset, ok := config.PresetRampSchedules[r.RampSchedule]; ok {
		preset.StartDate = now
		return preset
	}

	if r.RampSchedule == "custom" {
		return r.buildCustomRampSchedule(now)
	}

	// Default to immediate
	schedule := config.PresetRampSchedules["immediate"]
	schedule.StartDate = now
	return schedule
}

// buildCustomRampSchedule builds a custom ramp schedule with validated parameters
func (r *PlanRequest) buildCustomRampSchedule(now time.Time) config.RampSchedule {
	stepPercent := float64(r.CustomStepPercent)
	if stepPercent <= 0 {
		stepPercent = 20
	}

	intervalDays := r.CustomIntervalDays
	if intervalDays <= 0 {
		intervalDays = 7
	}

	totalSteps := int(100 / stepPercent)
	if totalSteps < 1 {
		totalSteps = 1
	}

	return config.RampSchedule{
		Type:             "custom",
		PercentPerStep:   stepPercent,
		StepIntervalDays: intervalDays,
		TotalSteps:       totalSteps,
		StartDate:        now,
	}
}

// buildServiceConfig creates service configuration from request fields
func (r *PlanRequest) buildServiceConfig() map[string]config.ServiceConfig {
	if r.Provider == "" || r.Service == "" {
		return nil
	}

	term := r.Term
	if term == 0 {
		term = 3
	}

	payment := r.Payment
	if payment == "" {
		payment = "no-upfront"
	}

	coverage := float64(r.TargetCoverage)
	if coverage == 0 {
		coverage = 80
	}

	return map[string]config.ServiceConfig{
		r.Provider + "/" + r.Service: {
			Provider: r.Provider,
			Service:  r.Service,
			Enabled:  true,
			Term:     term,
			Payment:  payment,
			Coverage: coverage,
		},
	}
}

// calculateNextExecutionDate determines the next execution date based on ramp schedule
func (r *PlanRequest) calculateNextExecutionDate(now time.Time, schedule config.RampSchedule) *time.Time {
	var nextDate time.Time

	if schedule.Type == "immediate" {
		nextDate = now.AddDate(0, 0, 1) // Schedule for tomorrow
	} else if schedule.StepIntervalDays > 0 {
		nextDate = now.AddDate(0, 0, schedule.StepIntervalDays)
	} else {
		return nil
	}

	return &nextDate
}

// CreatePlannedPurchasesRequest represents a request to create planned purchases
type CreatePlannedPurchasesRequest struct {
	Count     int    `json:"count"`
	StartDate string `json:"start_date"`
}

// CreatePlannedPurchasesResponse represents the response after creating planned purchases
type CreatePlannedPurchasesResponse struct {
	Created int `json:"created"`
}

// HistoryResponse represents the response from the history API
type HistoryResponse struct {
	Summary   HistorySummary                 `json:"summary"`
	Purchases []config.PurchaseHistoryRecord `json:"purchases"`
}

// HistorySummary provides aggregate statistics for purchase history.
// TotalPurchases is the total count of rows (completed + all non-completed
// states); the per-state counters break it down so the UI can render
// meaningful totals. Dollar totals count completed rows only: pending,
// in-progress, failed, expired, and cancelled rows are all excluded because
// no money was committed for any of those states.
type HistorySummary struct {
	TotalPurchases int `json:"total_purchases"`
	TotalCompleted int `json:"total_completed"`
	TotalPending   int `json:"total_pending"`
	// TotalInProgress counts executions that have been approved but whose
	// synchronous purchase has not finalised (status approved/running/paused).
	// Tracked separately from pending and excluded from the dollar totals so an
	// interrupted approval (issue #621) stays visible without inflating
	// committed spend/savings.
	TotalInProgress     int     `json:"total_in_progress"`
	TotalFailed         int     `json:"total_failed"`
	TotalExpired        int     `json:"total_expired"`
	TotalUpfront        float64 `json:"total_upfront"`
	TotalMonthlySavings float64 `json:"total_monthly_savings"`
	TotalAnnualSavings  float64 `json:"total_annual_savings"`
}
