// Package config provides configuration management for CUDly.
package config

import (
	"time"
)

// GlobalConfig represents the global CUDly configuration
type GlobalConfig struct {
	EnabledProviders       []string `json:"enabled_providers" dynamodbav:"enabled_providers"`
	NotificationEmail      *string  `json:"notification_email,omitempty" dynamodbav:"notification_email,omitempty"`
	AutoCollect            bool     `json:"auto_collect"`
	CollectionSchedule     string   `json:"collection_schedule"`
	NotificationDaysBefore int      `json:"notification_days_before"`
	ApprovalRequired       bool     `json:"approval_required" dynamodbav:"approval_required"`
	DefaultTerm            int      `json:"default_term" dynamodbav:"default_term"`
	DefaultPayment         string   `json:"default_payment" dynamodbav:"default_payment"`
	DefaultCoverage        float64  `json:"default_coverage" dynamodbav:"default_coverage"`
	DefaultRampSchedule    string   `json:"default_ramp_schedule" dynamodbav:"default_ramp_schedule"`

	// GracePeriodDays is a per-provider window (in days) during which
	// just-purchased capacity is suppressed from the recommendations
	// list so users don't re-buy the same capacity while the cloud
	// provider's utilisation metrics catch up. Keys are provider slugs
	// ("aws", "azure", "gcp"). Missing keys default to DefaultGracePeriodDays
	// (7). An explicit 0 disables suppression for that provider. Use
	// GracePeriodFor to read a specific provider's effective value (it
	// applies the default + safety clamp).
	GracePeriodDays map[string]int `json:"grace_period_days,omitempty" dynamodbav:"grace_period_days,omitempty"`

	// RI Exchange automation settings
	RIExchangeEnabled              bool    `json:"ri_exchange_enabled" dynamodbav:"ri_exchange_enabled"`
	RIExchangeMode                 string  `json:"ri_exchange_mode" dynamodbav:"ri_exchange_mode"`
	RIExchangeUtilizationThreshold float64 `json:"ri_exchange_utilization_threshold" dynamodbav:"ri_exchange_utilization_threshold"`
	RIExchangeMaxPerExchangeUSD    float64 `json:"ri_exchange_max_per_exchange_usd" dynamodbav:"ri_exchange_max_per_exchange_usd"`
	RIExchangeMaxDailyUSD          float64 `json:"ri_exchange_max_daily_usd" dynamodbav:"ri_exchange_max_daily_usd"`
	RIExchangeLookbackDays         int     `json:"ri_exchange_lookback_days" dynamodbav:"ri_exchange_lookback_days"`
}

// DefaultGracePeriodDays is the fallback window used when a provider
// has no entry in GlobalConfig.GracePeriodDays. A week gives cloud
// providers enough time to reflect a fresh commitment in their
// utilisation metrics before we'd re-propose the same capacity.
const DefaultGracePeriodDays = 7

// MaxGracePeriodDays is the ceiling enforced at read time as a safety
// net. The UI clamps input to [0, 30]; the DB isn't constrained, so a
// rogue write through psql shouldn't be able to suppress recs for years.
const MaxGracePeriodDays = 90

// GracePeriodFor returns the effective grace-period window (in days)
// for the given provider slug ("aws", "azure", "gcp"). Returns the
// default when the provider has no explicit entry. Preserves an
// explicit 0 (which disables the feature for that provider). Clamps
// the result to [0, MaxGracePeriodDays] so a misconfigured DB row
// can't suppress recs indefinitely.
func (g *GlobalConfig) GracePeriodFor(provider string) int {
	if g == nil {
		return DefaultGracePeriodDays
	}
	days, ok := g.GracePeriodDays[provider]
	if !ok {
		return DefaultGracePeriodDays
	}
	if days < 0 {
		days = 0
	}
	if days > MaxGracePeriodDays {
		days = MaxGracePeriodDays
	}
	return days
}

// ServiceConfig represents per-service configuration
type ServiceConfig struct {
	Provider       string   `json:"provider" dynamodbav:"provider"`
	Service        string   `json:"service" dynamodbav:"service"`
	Enabled        bool     `json:"enabled" dynamodbav:"enabled"`
	Term           int      `json:"term" dynamodbav:"term"`
	Payment        string   `json:"payment" dynamodbav:"payment"`
	Coverage       float64  `json:"coverage" dynamodbav:"coverage"`
	RampSchedule   string   `json:"ramp_schedule" dynamodbav:"ramp_schedule"`
	IncludeEngines []string `json:"include_engines,omitempty" dynamodbav:"include_engines,omitempty"`
	ExcludeEngines []string `json:"exclude_engines,omitempty" dynamodbav:"exclude_engines,omitempty"`
	IncludeRegions []string `json:"include_regions,omitempty" dynamodbav:"include_regions,omitempty"`
	ExcludeRegions []string `json:"exclude_regions,omitempty" dynamodbav:"exclude_regions,omitempty"`
	IncludeTypes   []string `json:"include_types,omitempty" dynamodbav:"include_types,omitempty"`
	ExcludeTypes   []string `json:"exclude_types,omitempty" dynamodbav:"exclude_types,omitempty"`
}

// PurchasePlan represents a saved purchase plan for automated execution
type PurchasePlan struct {
	ID                     string                   `json:"id" dynamodbav:"id"`
	Name                   string                   `json:"name" dynamodbav:"name"`
	Enabled                bool                     `json:"enabled" dynamodbav:"enabled"`
	AutoPurchase           bool                     `json:"auto_purchase" dynamodbav:"auto_purchase"`
	NotificationDaysBefore int                      `json:"notification_days_before" dynamodbav:"notification_days_before"`
	Services               map[string]ServiceConfig `json:"services" dynamodbav:"services"`
	RampSchedule           RampSchedule             `json:"ramp_schedule" dynamodbav:"ramp_schedule"`
	CreatedAt              time.Time                `json:"created_at" dynamodbav:"created_at"`
	UpdatedAt              time.Time                `json:"updated_at" dynamodbav:"updated_at"`
	NextExecutionDate      *time.Time               `json:"next_execution_date,omitempty" dynamodbav:"next_execution_date,omitempty"`
	LastExecutionDate      *time.Time               `json:"last_execution_date,omitempty" dynamodbav:"last_execution_date,omitempty"`
	LastNotificationSent   *time.Time               `json:"last_notification_sent,omitempty" dynamodbav:"last_notification_sent,omitempty"`
}

// RampSchedule defines how purchases are spread over time
type RampSchedule struct {
	Type             string    `json:"type" dynamodbav:"type"` // immediate, weekly, monthly, custom
	PercentPerStep   float64   `json:"percent_per_step" dynamodbav:"percent_per_step"`
	StepIntervalDays int       `json:"step_interval_days" dynamodbav:"step_interval_days"`
	CurrentStep      int       `json:"current_step" dynamodbav:"current_step"`
	TotalSteps       int       `json:"total_steps" dynamodbav:"total_steps"`
	StartDate        time.Time `json:"start_date" dynamodbav:"start_date"`
}

// PresetRampSchedules provides common ramp-up configurations
var PresetRampSchedules = map[string]RampSchedule{
	"immediate": {
		Type:           "immediate",
		PercentPerStep: 100,
		TotalSteps:     1,
	},
	"weekly-25pct": {
		Type:             "weekly",
		PercentPerStep:   25,
		StepIntervalDays: 7,
		TotalSteps:       4,
	},
	"monthly-10pct": {
		Type:             "monthly",
		PercentPerStep:   10,
		StepIntervalDays: 30,
		TotalSteps:       10,
	},
}

// GetCurrentCoverage calculates the current effective coverage based on ramp progress
func (r *RampSchedule) GetCurrentCoverage(baseCoverage float64) float64 {
	if r.Type == "immediate" {
		return baseCoverage
	}
	completedPercent := float64(r.CurrentStep) * r.PercentPerStep
	if completedPercent > 100 {
		completedPercent = 100
	}
	return baseCoverage * completedPercent / 100
}

// GetNextPurchaseDate calculates when the next purchase step should occur
func (r *RampSchedule) GetNextPurchaseDate() time.Time {
	if r.StartDate.IsZero() {
		return time.Now()
	}
	return r.StartDate.AddDate(0, 0, r.CurrentStep*r.StepIntervalDays)
}

// IsComplete returns true if all ramp steps are done
func (r *RampSchedule) IsComplete() bool {
	return r.CurrentStep >= r.TotalSteps
}

// PurchaseExecution represents a single execution of a purchase plan
type PurchaseExecution struct {
	PlanID           string                 `json:"plan_id" dynamodbav:"plan_id"`
	ExecutionID      string                 `json:"execution_id" dynamodbav:"execution_id"`
	Status           string                 `json:"status" dynamodbav:"status"` // pending, notified, approved, cancelled, completed, failed
	StepNumber       int                    `json:"step_number" dynamodbav:"step_number"`
	ScheduledDate    time.Time              `json:"scheduled_date" dynamodbav:"scheduled_date"`
	NotificationSent *time.Time             `json:"notification_sent,omitempty" dynamodbav:"notification_sent,omitempty"`
	ApprovalToken    string                 `json:"approval_token,omitempty" dynamodbav:"approval_token,omitempty"`
	Recommendations  []RecommendationRecord `json:"recommendations" dynamodbav:"recommendations"`
	TotalUpfrontCost float64                `json:"total_upfront_cost" dynamodbav:"total_upfront_cost"`
	EstimatedSavings float64                `json:"estimated_savings" dynamodbav:"estimated_savings"`
	CompletedAt      *time.Time             `json:"completed_at,omitempty" dynamodbav:"completed_at,omitempty"`
	Error            string                 `json:"error,omitempty" dynamodbav:"error,omitempty"`
	TTL              int64                  `json:"ttl,omitempty" dynamodbav:"ttl,omitempty"`
	CloudAccountID   *string                `json:"cloud_account_id,omitempty" dynamodbav:"cloud_account_id,omitempty"`
	// Source identifies the CUDly surface that triggered this execution
	// ("cudly-cli" or "cudly-web"). Propagated into PurchaseOptions and
	// stamped as a tag/label onto every commitment this execution buys.
	Source string `json:"source,omitempty" dynamodbav:"source,omitempty"`
	// ApprovedBy / CancelledBy carry the email of the session-authenticated
	// user who acted on this execution via the auth-gated deep-link flow
	// (frontend /purchases/{action}/:id → login-if-needed → session-authed
	// endpoint). Nil on legacy token-only approve/cancel paths — the
	// handler / History UI falls back to the notification email as the
	// accountable party in that case. Nullable TEXT in Postgres.
	ApprovedBy  *string `json:"approved_by,omitempty" dynamodbav:"approved_by,omitempty"`
	CancelledBy *string `json:"cancelled_by,omitempty" dynamodbav:"cancelled_by,omitempty"`
	// CreatedByUserID is the UUID of the session-authenticated user who
	// triggered this execution (e.g. clicked Execute on the Recommendations
	// page or submitted the bulk-purchase modal). NULL on rows created
	// before the column was introduced (migration 000041) and on
	// scheduler-driven executions where there is no human creator. Used
	// by the session-authed cancel handler to enforce cancel:own_executions
	// — a non-admin may cancel only executions they themselves created.
	// NULL is treated as "not the current user".
	CreatedByUserID *string `json:"created_by_user_id,omitempty" dynamodbav:"created_by_user_id,omitempty"`
	// CapacityPercent records what fraction of the originally-recommended
	// counts the user chose when the bulk Purchase flow submitted this
	// execution (1..100). Audit-only: the Recommendations slice already
	// carries the scaled counts, so backend math is unaffected by this
	// field. Defaults to 100 for legacy and scheduler-driven executions.
	CapacityPercent int `json:"capacity_percent,omitempty" dynamodbav:"capacity_percent,omitempty"`
}

// RecommendationRecord stores a recommendation with purchase status
type RecommendationRecord struct {
	ID             string  `json:"id" dynamodbav:"id"`
	Provider       string  `json:"provider" dynamodbav:"provider"`
	Service        string  `json:"service" dynamodbav:"service"`
	Region         string  `json:"region" dynamodbav:"region"`
	ResourceType   string  `json:"resource_type" dynamodbav:"resource_type"`
	Engine         string  `json:"engine,omitempty" dynamodbav:"engine,omitempty"`
	Count          int     `json:"count" dynamodbav:"count"`
	Term           int     `json:"term" dynamodbav:"term"`
	Payment        string  `json:"payment" dynamodbav:"payment"`
	UpfrontCost    float64 `json:"upfront_cost" dynamodbav:"upfront_cost"`
	MonthlyCost    float64 `json:"monthly_cost" dynamodbav:"monthly_cost"`
	Savings        float64 `json:"savings" dynamodbav:"savings"`
	Selected       bool    `json:"selected" dynamodbav:"selected"`
	Purchased      bool    `json:"purchased" dynamodbav:"purchased"`
	PurchaseID     string  `json:"purchase_id,omitempty" dynamodbav:"purchase_id,omitempty"`
	Error          string  `json:"error,omitempty" dynamodbav:"error,omitempty"`
	CloudAccountID *string `json:"cloud_account_id,omitempty" dynamodbav:"cloud_account_id,omitempty"`
	// SuppressedCount is the cumulative count already committed against
	// this recommendation's 6-tuple (account, provider, service, region,
	// resource_type, engine) within the active grace window. The
	// scheduler subtracts this from Count before returning the rec to
	// the frontend; a rec where SuppressedCount ≥ original count is
	// dropped entirely. Populated by the scheduler — zero on writes.
	SuppressedCount int `json:"suppressed_count,omitempty" dynamodbav:"suppressed_count,omitempty"`
	// SuppressionExpiresAt is the earliest expiry across all active
	// suppression rows contributing to this tuple. The frontend uses it
	// to render "Xd remaining" on the recently-purchased badge.
	SuppressionExpiresAt *time.Time `json:"suppression_expires_at,omitempty" dynamodbav:"suppression_expires_at,omitempty"`
	// PrimarySuppressionExecutionID identifies the execution whose
	// suppression contributed the most to this tuple (ties broken by
	// newest created_at). The frontend badge deep-links to Purchase
	// History filtered to this execution.
	PrimarySuppressionExecutionID *string `json:"primary_suppression_execution_id,omitempty" dynamodbav:"primary_suppression_execution_id,omitempty"`
}

// PurchaseSuppression records the per-tuple grace window after a bulk
// purchase. See migration 000037 for the full SQL shape + lifecycle
// documentation. Written inside the same transaction as the execution
// insert; deleted inside the same transaction as a cancel/expire status
// transition.
type PurchaseSuppression struct {
	ID              string    `json:"id"`
	ExecutionID     string    `json:"execution_id"`
	AccountID       string    `json:"account_id"`
	Provider        string    `json:"provider"`
	Service         string    `json:"service"`
	Region          string    `json:"region"`
	ResourceType    string    `json:"resource_type"`
	Engine          string    `json:"engine"`
	SuppressedCount int       `json:"suppressed_count"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
}

// RecommendationFilter parameterises ListStoredRecommendations and the
// handler-facing scheduler.ListRecommendations wrapper. Zero-value fields
// mean "no filter"; non-empty AccountIDs restricts to the given IDs.
type RecommendationFilter struct {
	Provider   string   // "aws" / "azure" / "gcp" / "" (all)
	Service    string   // "" = all services
	Region     string   // "" = all regions
	AccountIDs []string // nil/empty = all accounts
	MinSavings float64  // 0 = no floor on monthly savings
}

// RecommendationsFreshness describes the cache staleness state surfaced to
// the frontend. LastCollectedAt is nil on a cold start.
// LastCollectionError is non-nil when the most recent collect attempt
// partially or fully failed.
type RecommendationsFreshness struct {
	LastCollectedAt     *time.Time `json:"last_collected_at"`
	LastCollectionError *string    `json:"last_collection_error"`
}

// SuccessfulCollect identifies a (provider, account) pair whose collection
// completed in the current cycle. UpsertRecommendations scopes the
// stale-row eviction DELETE to the union of these pairs so a partially-
// failed provider preserves the failed accounts' previous-cycle rows
// (the dashboard for those accounts isn't blanked out by transient
// cloud-API failures).
//
// CloudAccountID is nil for the AWS ambient-credentials path (no
// registered account); the eviction collapses nil to the zero UUID via
// the same generated-column rule that applies to inserts, so ambient
// rows are evicted independently of any registered-account rows under
// the same provider.
type SuccessfulCollect struct {
	Provider       string
	CloudAccountID *string
}

// RIUtilizationCacheEntry is a single cached Cost Explorer
// GetReservationUtilization result keyed by (region, lookback_days).
// Payload is the JSON encoding of the caller's utilization slice — kept
// opaque here so the config package stays free of AWS-provider types.
// TTL freshness is evaluated in the caller (api-layer cache wrapper)
// based on FetchedAt vs. a caller-supplied TTL.
type RIUtilizationCacheEntry struct {
	Region       string
	LookbackDays int
	Payload      []byte
	FetchedAt    time.Time
}

// PurchaseHistoryRecord is the response-layer representation for rows on the
// /api/history page. DB-backed rows always describe *completed* purchases; the
// handler additionally synthesises rows for pending executions so users can
// see (and cancel) in-flight approvals. Status is the discriminator — the DB
// layer never writes it (tag `dynamodbav:"-"` keeps it out of persistence),
// and the API layer populates it as "completed" or "pending" before returning.
type PurchaseHistoryRecord struct {
	AccountID        string    `json:"account_id" dynamodbav:"account_id"`
	PurchaseID       string    `json:"purchase_id" dynamodbav:"purchase_id"`
	Timestamp        time.Time `json:"timestamp" dynamodbav:"timestamp"`
	Provider         string    `json:"provider" dynamodbav:"provider"`
	Service          string    `json:"service" dynamodbav:"service"`
	Region           string    `json:"region" dynamodbav:"region"`
	ResourceType     string    `json:"resource_type" dynamodbav:"resource_type"`
	Count            int       `json:"count" dynamodbav:"count"`
	Term             int       `json:"term" dynamodbav:"term"`
	Payment          string    `json:"payment" dynamodbav:"payment"`
	UpfrontCost      float64   `json:"upfront_cost" dynamodbav:"upfront_cost"`
	MonthlyCost      float64   `json:"monthly_cost" dynamodbav:"monthly_cost"`
	EstimatedSavings float64   `json:"estimated_savings" dynamodbav:"estimated_savings"`
	PlanID           string    `json:"plan_id,omitempty" dynamodbav:"plan_id,omitempty"`
	PlanName         string    `json:"plan_name,omitempty" dynamodbav:"plan_name,omitempty"`
	RampStep         int       `json:"ramp_step,omitempty" dynamodbav:"ramp_step,omitempty"`
	CloudAccountID   *string   `json:"cloud_account_id,omitempty" dynamodbav:"cloud_account_id,omitempty"`
	Status           string    `json:"status,omitempty" dynamodbav:"-"`
	// Approver holds the email address the approval request was sent to (or
	// would have been, if SES failed). Set only on pending rows, so the
	// History UI can show "awaiting approval from <addr>" and the user knows
	// exactly whose inbox to check. Excluded from DB persistence.
	Approver string `json:"approver,omitempty" dynamodbav:"-"`
	Source   string `json:"source,omitempty" dynamodbav:"source,omitempty"`
	// StatusDescription carries a short human-readable explanation for non-
	// completed rows. For "failed", this is the stored Error message (e.g.
	// "send failed: Missing domain"). For "expired", a canned reminder that
	// the 7-day approval window elapsed. Empty on completed/pending rows —
	// those speak for themselves via Status alone.
	StatusDescription string `json:"status_description,omitempty" dynamodbav:"-"`
	// CreatedByUserID propagates the originating execution's
	// created_by_user_id so the History UI can decide whether to render
	// the inline Cancel button (issue #46): a non-admin user only sees
	// the button on their own pending rows. Set only for synthesised
	// pending/notified rows (executions); empty on completed history
	// rows (where the action has already completed). Excluded from DB
	// persistence.
	CreatedByUserID string `json:"created_by_user_id,omitempty" dynamodbav:"-"`
}

// RIExchangeRecord represents a record in the ri_exchange_history table
type RIExchangeRecord struct {
	ID                 string     `json:"id"`
	AccountID          string     `json:"account_id"`
	ExchangeID         string     `json:"exchange_id"`
	Region             string     `json:"region"`
	SourceRIIDs        []string   `json:"source_ri_ids"`
	SourceInstanceType string     `json:"source_instance_type"`
	SourceCount        int        `json:"source_count"`
	TargetOfferingID   string     `json:"target_offering_id"`
	TargetInstanceType string     `json:"target_instance_type"`
	TargetCount        int        `json:"target_count"`
	PaymentDue         string     `json:"payment_due"`
	Status             string     `json:"status"`
	ApprovalToken      string     `json:"approval_token,omitempty"`
	Error              string     `json:"error,omitempty"`
	Mode               string     `json:"mode"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	CloudAccountID     *string    `json:"cloud_account_id,omitempty"`
}

// ConfigSetting represents a configuration setting for the defaults system
type ConfigSetting struct {
	Key         string    `json:"key"`
	Value       any       `json:"value"`
	Type        string    `json:"type"` // int, float, bool, string, json
	Category    string    `json:"category"`
	Description string    `json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CloudAccount represents a single managed cloud account/subscription/project.
type CloudAccount struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ContactEmail string `json:"contact_email,omitempty"`
	Enabled      bool   `json:"enabled"`
	Provider     string `json:"provider"`
	ExternalID   string `json:"external_id"`

	// AWS-specific
	AWSAuthMode             string `json:"aws_auth_mode,omitempty"`
	AWSRoleARN              string `json:"aws_role_arn,omitempty"`
	AWSExternalID           string `json:"aws_external_id,omitempty"`
	AWSBastionID            string `json:"aws_bastion_id,omitempty"`
	AWSWebIdentityTokenFile string `json:"aws_web_identity_token_file,omitempty"`
	AWSIsOrgRoot            bool   `json:"aws_is_org_root,omitempty"`

	// Azure-specific
	AzureSubscriptionID string `json:"azure_subscription_id,omitempty"`
	AzureTenantID       string `json:"azure_tenant_id,omitempty"`
	AzureClientID       string `json:"azure_client_id,omitempty"`
	AzureAuthMode       string `json:"azure_auth_mode,omitempty"`

	// GCP-specific
	GCPProjectID   string `json:"gcp_project_id,omitempty"`
	GCPClientEmail string `json:"gcp_client_email,omitempty"`
	GCPAuthMode    string `json:"gcp_auth_mode,omitempty"`
	// GCPWIFAudience is the full Workload Identity Pool provider
	// resource used as the STS audience when exchanging a CUDly
	// KMS-signed JWT for a GCP access token. Only set for accounts
	// using the secret-free workload_identity_federation path.
	// Shape: //iam.googleapis.com/projects/<number>/locations/global/workloadIdentityPools/<pool>/providers/<provider>
	GCPWIFAudience string `json:"gcp_wif_audience,omitempty"`

	// Derived (not stored in DB)
	CredentialsConfigured bool   `json:"credentials_configured"`
	BastionAccountName    string `json:"bastion_account_name,omitempty"`
	IsSelf                bool   `json:"is_self,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedBy string    `json:"created_by,omitempty"`
}

// CloudAccountFilter for ListCloudAccounts queries.
type CloudAccountFilter struct {
	Provider  *string
	Enabled   *bool
	Search    string  // substring match on name or external_id
	BastionID *string // return accounts whose aws_bastion_id = *BastionID
}

// AccountServiceOverride is a sparse per-account override on top of the global ServiceConfig.
// Nil pointer fields inherit the global value.
type AccountServiceOverride struct {
	ID             string    `json:"id"`
	AccountID      string    `json:"account_id"`
	Provider       string    `json:"provider"`
	Service        string    `json:"service"`
	Enabled        *bool     `json:"enabled,omitempty"`
	Term           *int      `json:"term,omitempty"`
	Payment        *string   `json:"payment,omitempty"`
	Coverage       *float64  `json:"coverage,omitempty"`
	RampSchedule   *string   `json:"ramp_schedule,omitempty"`
	IncludeEngines []string  `json:"include_engines,omitempty"`
	ExcludeEngines []string  `json:"exclude_engines,omitempty"`
	IncludeRegions []string  `json:"include_regions,omitempty"`
	ExcludeRegions []string  `json:"exclude_regions,omitempty"`
	IncludeTypes   []string  `json:"include_types,omitempty"`
	ExcludeTypes   []string  `json:"exclude_types,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AccountRegistration represents a self-service registration request from a
// target account owner. Submitted via POST /api/register during Terraform apply
// of the federation IaC, then approved or rejected by a CUDly admin.
type AccountRegistration struct {
	ID                   string     `json:"id"`
	ReferenceToken       string     `json:"reference_token"`
	Status               string     `json:"status"` // pending, approved, rejected
	Provider             string     `json:"provider"`
	ExternalID           string     `json:"external_id"`
	AccountName          string     `json:"account_name"`
	ContactEmail         string     `json:"contact_email"`
	Description          string     `json:"description,omitempty"`
	SourceProvider       string     `json:"source_provider,omitempty"`
	AWSRoleARN           string     `json:"aws_role_arn,omitempty"`
	AWSAuthMode          string     `json:"aws_auth_mode,omitempty"`
	AWSExternalID        string     `json:"aws_external_id,omitempty"`
	AzureSubscriptionID  string     `json:"azure_subscription_id,omitempty"`
	AzureTenantID        string     `json:"azure_tenant_id,omitempty"`
	AzureClientID        string     `json:"azure_client_id,omitempty"`
	AzureAuthMode        string     `json:"azure_auth_mode,omitempty"`
	GCPProjectID         string     `json:"gcp_project_id,omitempty"`
	GCPClientEmail       string     `json:"gcp_client_email,omitempty"`
	GCPAuthMode          string     `json:"gcp_auth_mode,omitempty"`
	GCPWIFAudience       string     `json:"gcp_wif_audience,omitempty"` // Full WIF provider resource; only set for federated path.
	RegCredentialType    string     `json:"reg_credential_type,omitempty"`
	RegCredentialPayload string     `json:"-"`                         // never returned in API responses (encrypted at rest)
	HasCredentials       bool       `json:"has_credentials,omitempty"` // derived: true when reg_credential_type is set
	RejectionReason      string     `json:"rejection_reason,omitempty"`
	CloudAccountID       *string    `json:"cloud_account_id,omitempty"`
	ReviewedBy           *string    `json:"reviewed_by,omitempty"`
	ReviewedAt           *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// AccountRegistrationFilter for ListAccountRegistrations queries.
type AccountRegistrationFilter struct {
	Status   *string
	Provider *string
	Search   string
}
