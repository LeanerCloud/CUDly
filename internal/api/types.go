// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
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
	ConfigStore               config.StoreInterface
	CredentialStore           credentials.CredentialStore
	PurchaseManager           PurchaseManagerInterface
	Scheduler                 SchedulerInterface
	AuthService               AuthServiceInterface
	APIKeySecretARN           string
	AzureCredentialsSecretARN string
	GCPCredentialsSecretARN   string
	EnableDashboard           bool
	DashboardBucket           string
	CORSAllowedOrigin         string // CORS allowed origin (default "*")
	RateLimiter               RateLimiterInterface
	// Analytics configuration (optional)
	AnalyticsClient    AnalyticsClientInterface
	AnalyticsCollector AnalyticsCollectorInterface
}

// AnalyticsClientInterface defines the interface for analytics queries
type AnalyticsClientInterface interface {
	QueryHistory(ctx context.Context, accountID string, start, end time.Time, interval string) ([]HistoryDataPoint, *HistorySummary, error)
	QueryBreakdown(ctx context.Context, accountID string, start, end time.Time, dimension string) (map[string]BreakdownValue, error)
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

// PurchaseManagerInterface defines purchase manager methods used by handler
type PurchaseManagerInterface interface {
	ApproveExecution(ctx context.Context, execID, token string) error
	CancelExecution(ctx context.Context, execID, token string) error
}

// SchedulerInterface defines scheduler methods used by handler
type SchedulerInterface interface {
	CollectRecommendations(ctx context.Context) (*scheduler.CollectResult, error)
	GetRecommendations(ctx context.Context, params scheduler.RecommendationQueryParams) ([]config.RecommendationRecord, error)
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
	GetUser(ctx context.Context, userID string) (*User, error)
	UpdateUserProfile(ctx context.Context, userID string, email string, currentPassword string, newPassword string) error
	// User management - uses auth.API* types
	CreateUserAPI(ctx context.Context, req any) (any, error)
	UpdateUserAPI(ctx context.Context, userID string, req any) (any, error)
	DeleteUser(ctx context.Context, userID string) error
	ListUsersAPI(ctx context.Context) (any, error)
	ChangePasswordAPI(ctx context.Context, userID, currentPassword, newPassword string) error
	// Group management - uses auth.API* types
	CreateGroupAPI(ctx context.Context, req any) (any, error)
	UpdateGroupAPI(ctx context.Context, groupID string, req any) (any, error)
	DeleteGroup(ctx context.Context, groupID string) error
	GetGroupAPI(ctx context.Context, groupID string) (any, error)
	ListGroupsAPI(ctx context.Context) (any, error)
	// Permission checking
	HasPermissionAPI(ctx context.Context, userID, action, resource string) (bool, error)
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
	Role       string   `json:"role"`
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
	Role   string `json:"role"`
}

type User struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	Role       string   `json:"role"`
	Groups     []string `json:"groups,omitempty"`
	MFAEnabled bool     `json:"mfa_enabled"`
	CreatedAt  string   `json:"created_at,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

// CreateUserRequest represents a request to create a new user
type CreateUserRequest struct {
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Role     string   `json:"role"`
	Groups   []string `json:"groups,omitempty"`
}

// UpdateUserRequest represents a request to update a user
type UpdateUserRequest struct {
	Email  string   `json:"email,omitempty"`
	Role   string   `json:"role,omitempty"`
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
	Global      *config.GlobalConfig   `json:"global"`
	Services    []config.ServiceConfig `json:"services"`
	Credentials *CredentialsStatus     `json:"credentials,omitempty"`
}

// CredentialsStatus holds the status of cloud provider credentials
type CredentialsStatus struct {
	AzureConfigured bool `json:"azure_configured"`
	GCPConfigured   bool `json:"gcp_configured"`
}

// AzureCredentialsRequest holds Azure Service Principal credentials
type AzureCredentialsRequest struct {
	TenantID       string `json:"tenant_id"`
	ClientID       string `json:"client_id"`
	ClientSecret   string `json:"client_secret"`
	SubscriptionID string `json:"subscription_id"`
}

// GCPCredentialsRequest holds GCP Service Account credentials (JSON key file contents)
type GCPCredentialsRequest struct {
	Type                    string `json:"type"`
	ProjectID               string `json:"project_id"`
	PrivateKeyID            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientID                string `json:"client_id,omitempty"`
	AuthURI                 string `json:"auth_uri,omitempty"`
	TokenURI                string `json:"token_uri,omitempty"`
	AuthProviderX509CertURL string `json:"auth_provider_x509_cert_url,omitempty"`
	ClientX509CertURL       string `json:"client_x509_cert_url,omitempty"`
}

// StatusResponse holds a simple status response
type StatusResponse struct {
	Status string `json:"status"`
}

// RecommendationsResponse holds the recommendations response
type RecommendationsResponse struct {
	Recommendations []config.RecommendationRecord `json:"recommendations"`
	TotalSavings    float64                       `json:"total_savings"`
	Count           int                           `json:"count"`
}

// PlansResponse holds the purchase plans response
type PlansResponse struct {
	Plans []config.PurchasePlan `json:"plans"`
}

// CurrentUserResponse holds the current user response
type CurrentUserResponse struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	MFAEnabled bool   `json:"mfa_enabled"`
}

// AdminExistsResponse holds the admin exists check response
type AdminExistsResponse struct {
	AdminExists bool `json:"admin_exists"`
}

// EmptyServiceConfigResponse represents an empty service config
type EmptyServiceConfigResponse struct{}

// PublicInfoResponse holds public information about the CUDly instance
type PublicInfoResponse struct {
	Version         string `json:"version"`
	AdminExists     bool   `json:"admin_exists"`
	APIKeySecretURL string `json:"api_key_secret_url,omitempty"`
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

// UpcomingPurchaseResponse holds upcoming purchase data
type UpcomingPurchaseResponse struct {
	Purchases []UpcomingPurchase `json:"purchases"`
}

// UpcomingPurchase represents a scheduled purchase
type UpcomingPurchase struct {
	ExecutionID      string  `json:"execution_id"`
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

// HistorySummary provides aggregate statistics for purchase history
type HistorySummary struct {
	TotalPurchases      int     `json:"total_purchases"`
	TotalUpfront        float64 `json:"total_upfront"`
	TotalMonthlySavings float64 `json:"total_monthly_savings"`
	TotalAnnualSavings  float64 `json:"total_annual_savings"`
}
