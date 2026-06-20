// Package config provides configuration management for CUDly.
package config

import (
	"encoding/json"
	"strings"
	"time"
)

// GlobalConfig represents the global CUDly configuration.
type GlobalConfig struct {
	NotificationEmail              *string        `json:"notification_email,omitempty" dynamodbav:"notification_email,omitempty"`
	GracePeriodDays                map[string]int `json:"grace_period_days,omitempty" dynamodbav:"grace_period_days,omitempty"`
	DefaultPayment                 string         `json:"default_payment" dynamodbav:"default_payment"`
	CollectionSchedule             string         `json:"collection_schedule"`
	RIExchangeMode                 string         `json:"ri_exchange_mode" dynamodbav:"ri_exchange_mode"`
	DefaultRampSchedule            string         `json:"default_ramp_schedule" dynamodbav:"default_ramp_schedule"`
	EnabledProviders               []string       `json:"enabled_providers" dynamodbav:"enabled_providers"`
	RIExchangeMaxDailyUSD          float64        `json:"ri_exchange_max_daily_usd" dynamodbav:"ri_exchange_max_daily_usd"`
	DefaultCoverage                float64        `json:"default_coverage" dynamodbav:"default_coverage"`
	DefaultTerm                    int            `json:"default_term" dynamodbav:"default_term"`
	NotificationDaysBefore         int            `json:"notification_days_before"`
	RIExchangeUtilizationThreshold float64        `json:"ri_exchange_utilization_threshold" dynamodbav:"ri_exchange_utilization_threshold"`
	RIExchangeMaxPerExchangeUSD    float64        `json:"ri_exchange_max_per_exchange_usd" dynamodbav:"ri_exchange_max_per_exchange_usd"`
	RIExchangeLookbackDays         int            `json:"ri_exchange_lookback_days" dynamodbav:"ri_exchange_lookback_days"`
	RecommendationsCacheStaleHours int            `json:"recommendations_cache_stale_hours" db:"recommendations_cache_stale_hours"`
	RecommendationsLookbackDays    int            `json:"recommendations_lookback_days" db:"recommendations_lookback_days"`
	PurchaseDelayHours             int            `json:"purchase_delay_hours" db:"purchase_delay_hours"`
	ApprovalRequired               bool           `json:"approval_required" dynamodbav:"approval_required"`
	RIExchangeEnabled              bool           `json:"ri_exchange_enabled" dynamodbav:"ri_exchange_enabled"`
	AutoCollect                    bool           `json:"auto_collect"`
}

// DefaultGracePeriodDays is the fallback window used when a provider
// has no entry in GlobalConfig.GracePeriodDays. A week gives cloud
// providers enough time to reflect a fresh commitment in their
// utilization metrics before we'd re-propose the same capacity.
const DefaultGracePeriodDays = 7

// MaxGracePeriodDays is the ceiling enforced at read time as a safety
// net. The UI clamps input to [0, 30]; the DB isn't constrained, so a
// rogue write through psql shouldn't be able to suppress recs for years.
const MaxGracePeriodDays = 90

// DefaultRecommendationsCacheStaleHours is the default age (hours) after
// which the recommendations cache triggers a background refresh.
const DefaultRecommendationsCacheStaleHours = 24

// MaxRecommendationsCacheStaleHours is the maximum configurable stale
// threshold: one year. Values above this are rejected at validation time.
const MaxRecommendationsCacheStaleHours = 8760

// DefaultRecommendationsLookbackDays is the default AWS Cost Explorer
// lookback window when no explicit value is configured.
const DefaultRecommendationsLookbackDays = 7

// ValidRecommendationsLookbackDays lists the AWS Cost Explorer
// LookbackPeriodInDays enum values. Other values are rejected.
var ValidRecommendationsLookbackDays = []int{7, 30, 60}

// DefaultPurchaseDelayHours is the default Gmail-style pre-fire delay.
// 48 hours gives most users a working-day window to spot and cancel
// an approval they didn't intend.
const DefaultPurchaseDelayHours = 48

// MaxPurchaseDelayHours is the ceiling for PurchaseDelayHours. One week
// is long enough for any reasonable review cycle; longer delays make the
// UX confusing and the scheduler overhead non-trivial.
const MaxPurchaseDelayHours = 168

// GetPurchaseDelay returns the pre-fire delay as a time.Duration. Nil
// receiver returns the default. Values outside [0, MaxPurchaseDelayHours]
// are clamped so a rogue DB write cannot break the scheduler.
func (g *GlobalConfig) GetPurchaseDelay() time.Duration {
	if g == nil {
		return time.Duration(DefaultPurchaseDelayHours) * time.Hour
	}
	h := g.PurchaseDelayHours
	if h < 0 {
		h = 0
	}
	if h > MaxPurchaseDelayHours {
		h = MaxPurchaseDelayHours
	}
	return time.Duration(h) * time.Hour
}

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

// ServiceConfig represents per-service configuration.
type ServiceConfig struct {
	Payment        string   `json:"payment" dynamodbav:"payment"`
	Service        string   `json:"service" dynamodbav:"service"`
	Provider       string   `json:"provider" dynamodbav:"provider"`
	RampSchedule   string   `json:"ramp_schedule" dynamodbav:"ramp_schedule"`
	IncludeRegions []string `json:"include_regions,omitempty" dynamodbav:"include_regions,omitempty"`
	IncludeEngines []string `json:"include_engines,omitempty" dynamodbav:"include_engines,omitempty"`
	ExcludeEngines []string `json:"exclude_engines,omitempty" dynamodbav:"exclude_engines,omitempty"`
	ExcludeRegions []string `json:"exclude_regions,omitempty" dynamodbav:"exclude_regions,omitempty"`
	IncludeTypes   []string `json:"include_types,omitempty" dynamodbav:"include_types,omitempty"`
	ExcludeTypes   []string `json:"exclude_types,omitempty" dynamodbav:"exclude_types,omitempty"`
	Coverage       float64  `json:"coverage" dynamodbav:"coverage"`
	Term           int      `json:"term" dynamodbav:"term"`
	MinCount       int      `json:"min_count,omitempty" dynamodbav:"min_count,omitempty"`
	Enabled        bool     `json:"enabled" dynamodbav:"enabled"`
}

// PurchasePlan represents a saved purchase plan for automated execution.
type PurchasePlan struct {
	CreatedAt              time.Time                `json:"created_at" dynamodbav:"created_at"`
	UpdatedAt              time.Time                `json:"updated_at" dynamodbav:"updated_at"`
	LastNotificationSent   *time.Time               `json:"last_notification_sent,omitempty" dynamodbav:"last_notification_sent,omitempty"`
	NextExecutionDate      *time.Time               `json:"next_execution_date,omitempty" dynamodbav:"next_execution_date,omitempty"`
	Services               map[string]ServiceConfig `json:"services" dynamodbav:"services"`
	LastExecutionDate      *time.Time               `json:"last_execution_date,omitempty" dynamodbav:"last_execution_date,omitempty"`
	Name                   string                   `json:"name" dynamodbav:"name"`
	ID                     string                   `json:"id" dynamodbav:"id"`
	RampSchedule           RampSchedule             `json:"ramp_schedule" dynamodbav:"ramp_schedule"`
	NotificationDaysBefore int                      `json:"notification_days_before" dynamodbav:"notification_days_before"`
	AutoPurchase           bool                     `json:"auto_purchase" dynamodbav:"auto_purchase"`
	Enabled                bool                     `json:"enabled" dynamodbav:"enabled"`
	Unassigned             bool                     `json:"unassigned,omitempty" dynamodbav:"unassigned,omitempty"`
}

// RampSchedule defines how purchases are spread over time.
type RampSchedule struct {
	StartDate        time.Time `json:"start_date" dynamodbav:"start_date"`
	Type             string    `json:"type" dynamodbav:"type"`
	PercentPerStep   float64   `json:"percent_per_step" dynamodbav:"percent_per_step"`
	StepIntervalDays int       `json:"step_interval_days" dynamodbav:"step_interval_days"`
	CurrentStep      int       `json:"current_step" dynamodbav:"current_step"`
	TotalSteps       int       `json:"total_steps" dynamodbav:"total_steps"`
}

// PresetRampSchedules provides common ramp-up configurations.
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

// GetCurrentCoverage calculates the current effective coverage based on ramp progress.
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

// GetNextPurchaseDate calculates when the next purchase step should occur.
func (r *RampSchedule) GetNextPurchaseDate() time.Time {
	if r.StartDate.IsZero() {
		return time.Now()
	}
	return r.StartDate.AddDate(0, 0, r.CurrentStep*r.StepIntervalDays)
}

// IsComplete returns true if all ramp steps are done.
func (r *RampSchedule) IsComplete() bool {
	return r.CurrentStep >= r.TotalSteps
}

// PurchaseExecution represents a single execution of a purchase plan.
type PurchaseExecution struct {
	ScheduledDate          time.Time              `json:"scheduled_date" dynamodbav:"scheduled_date"`
	ExecutedByUserID       *string                `json:"executed_by_user_id,omitempty" dynamodbav:"executed_by_user_id,omitempty"`
	PreApprovalSkipReason  *string                `json:"pre_approval_skip_reason,omitempty" dynamodbav:"pre_approval_skip_reason,omitempty"`
	ExecutedAt             *time.Time             `json:"executed_at,omitempty" dynamodbav:"executed_at,omitempty"`
	ScheduledExecutionAt   *time.Time             `json:"scheduled_execution_at,omitempty" dynamodbav:"scheduled_execution_at,omitempty"`
	NotificationSent       *time.Time             `json:"notification_sent,omitempty" dynamodbav:"notification_sent,omitempty"`
	CloudAccountID         *string                `json:"cloud_account_id,omitempty" dynamodbav:"cloud_account_id,omitempty"`
	ApprovalTokenExpiresAt *time.Time             `json:"approval_token_expires_at,omitempty" dynamodbav:"approval_token_expires_at,omitempty"`
	RetryExecutionID       *string                `json:"retry_execution_id,omitempty" dynamodbav:"retry_execution_id,omitempty"`
	CreatedByUserID        *string                `json:"created_by_user_id,omitempty" dynamodbav:"created_by_user_id,omitempty"`
	CompletedAt            *time.Time             `json:"completed_at,omitempty" dynamodbav:"completed_at,omitempty"`
	CancelledBy            *string                `json:"cancelled_by,omitempty" dynamodbav:"cancelled_by,omitempty"` //nolint:misspell // DB schema value 'cancelled_by' -- see migration 000035_add_execution_attribution.up.sql
	ApprovedBy             *string                `json:"approved_by,omitempty" dynamodbav:"approved_by,omitempty"`
	ApprovalToken          string                 `json:"approval_token,omitempty" dynamodbav:"approval_token,omitempty"`
	ExecutionID            string                 `json:"execution_id" dynamodbav:"execution_id"`
	Error                  string                 `json:"error,omitempty" dynamodbav:"error,omitempty"`
	IdempotencyKey         string                 `json:"idempotency_key,omitempty" dynamodbav:"idempotency_key,omitempty"`
	Status                 string                 `json:"status" dynamodbav:"status"`
	PlanID                 string                 `json:"plan_id" dynamodbav:"plan_id"`
	Source                 string                 `json:"source,omitempty" dynamodbav:"source,omitempty"`
	Recommendations        []RecommendationRecord `json:"recommendations" dynamodbav:"recommendations"`
	CapacityPercent        int                    `json:"capacity_percent,omitempty" dynamodbav:"capacity_percent,omitempty"`
	RetryAttemptN          int                    `json:"retry_attempt_n,omitempty" dynamodbav:"retry_attempt_n,omitempty"`
	StepNumber             int                    `json:"step_number" dynamodbav:"step_number"`
	TotalUpfrontCost       float64                `json:"total_upfront_cost" dynamodbav:"total_upfront_cost"`
	EstimatedSavings       float64                `json:"estimated_savings" dynamodbav:"estimated_savings"`
	TTL                    int64                  `json:"ttl,omitempty" dynamodbav:"ttl,omitempty"`
}

// IsCancelable reports whether an execution may still be canceled. Only the
// pre-purchase states ("pending"/"notified"/"scheduled") qualify: once a row
// reaches "approved" or "running" the AWS commitment is being or has been
// created, so canceling would leave the DB and the cloud out of sync;
// "canceled", "completed", "failed", "expired", and "paused" are likewise
// non-cancelable. The "scheduled" state is cancellable because the cloud SDK
// has not been called yet (issue #291 wave-2).
// Both cancel paths (purchase.Manager.CancelExecution on the email-token flow
// and the session-authed cancelPurchaseViaSession) call this single predicate
// so the policy can never drift between them (issue #645).
func (e *PurchaseExecution) IsCancelable() bool {
	return e.Status == "pending" || e.Status == "notified" || e.Status == "scheduled"
}

// RecommendationRecord stores a recommendation with purchase status.
type RecommendationRecord struct {
	MonthlyCost                   *float64        `json:"monthly_cost" dynamodbav:"monthly_cost"`
	MemoryGB                      *float64        `json:"memory_gb,omitempty" dynamodbav:"-"`
	VCPU                          *int            `json:"vcpu,omitempty" dynamodbav:"-"`
	PrimarySuppressionExecutionID *string         `json:"primary_suppression_execution_id,omitempty" dynamodbav:"primary_suppression_execution_id,omitempty"`
	SuppressionExpiresAt          *time.Time      `json:"suppression_expires_at,omitempty" dynamodbav:"suppression_expires_at,omitempty"`
	CloudAccountID                *string         `json:"cloud_account_id,omitempty" dynamodbav:"cloud_account_id,omitempty"`
	SavingsPercentage             *float64        `json:"savings_percentage" dynamodbav:"savings_percentage,omitempty"`
	OnDemandCost                  *float64        `json:"on_demand_cost,omitempty" dynamodbav:"on_demand_cost,omitempty"`
	PurchaseID                    string          `json:"purchase_id,omitempty" dynamodbav:"purchase_id,omitempty"`
	Error                         string          `json:"error,omitempty" dynamodbav:"error,omitempty"`
	Payment                       string          `json:"payment" dynamodbav:"payment"`
	Provider                      string          `json:"provider" dynamodbav:"provider"`
	Service                       string          `json:"service" dynamodbav:"service"`
	Region                        string          `json:"region" dynamodbav:"region"`
	ResourceType                  string          `json:"resource_type" dynamodbav:"resource_type"`
	Engine                        string          `json:"engine,omitempty" dynamodbav:"engine,omitempty"`
	ID                            string          `json:"id" dynamodbav:"id"`
	UsageHistory                  []float64       `json:"usage_history,omitempty" dynamodbav:"usage_history,omitempty"`
	Details                       json.RawMessage `json:"details,omitempty" dynamodbav:"-"`
	SuppressedCount               int             `json:"suppressed_count,omitempty" dynamodbav:"suppressed_count,omitempty"`
	Term                          int             `json:"term" dynamodbav:"term"`
	Count                         int             `json:"count" dynamodbav:"count"`
	Savings                       float64         `json:"savings" dynamodbav:"savings"`
	RecommendedCount              int             `json:"recommended_count,omitempty" dynamodbav:"recommended_count,omitempty"`
	UpfrontCost                   float64         `json:"upfront_cost" dynamodbav:"upfront_cost"`
	Purchased                     bool            `json:"purchased" dynamodbav:"purchased"`
	Selected                      bool            `json:"selected" dynamodbav:"selected"`
}

// PurchaseSuppression records the per-tuple grace window after a bulk
// purchase. See migration 000037 for the full SQL shape + lifecycle
// documentation. Written inside the same transaction as the execution
// insert; deleted inside the same transaction as a cancel/expire status
// transition.
type PurchaseSuppression struct {
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
	ID              string    `json:"id"`
	ExecutionID     string    `json:"execution_id"`
	AccountID       string    `json:"account_id"`
	Provider        string    `json:"provider"`
	Service         string    `json:"service"`
	Region          string    `json:"region"`
	ResourceType    string    `json:"resource_type"`
	Engine          string    `json:"engine"`
	SuppressedCount int       `json:"suppressed_count"`
}

// RecommendationFilter parameterises ListStoredRecommendations and the
// handler-facing scheduler.ListRecommendations wrapper. Zero-value fields
// mean "no filter"; non-empty AccountIDs restricts to the given IDs.
//
// MinSavingsUSD is a dollar floor: only recommendations whose monthly savings
// are >= MinSavingsUSD are returned. 0 means no floor.
//
// MinSavingsPct is a percentage floor (0–100): only recommendations whose
// effective savings percentage (savings/on-demand*100) meets or exceeds this
// threshold are returned. 0 means no floor. Applied in-process after the DB
// query rather than in SQL (avoids a computed column). These two filters are
// independent and can be combined.
type RecommendationFilter struct {
	Provider      string
	Service       string
	Region        string
	ID            string
	AccountIDs    []string
	MinSavingsUSD float64
	MinSavingsPct float64
}

// PurchasePlanFilter parameterises ListPurchasePlans. Zero-value means "no
// filter" (all plans are returned). Non-empty AccountIDs restricts the result
// to plans that reference at least one of the given account IDs via the
// plan_accounts join table.
type PurchasePlanFilter struct {
	AccountIDs []string // nil/empty = all plans
}

// RecommendationsFreshness describes the cache staleness state surfaced to
// the frontend. LastCollectedAt is nil on a cold start.
// LastCollectionError is non-nil when the most recent collect attempt
// partially or fully failed.
// LastCollectionStartedAt is non-nil while an async collect is in flight;
// the scheduler clears it on completion (success or failure). A value older
// than 5 minutes means the scheduler crashed mid-run — the refresh handler
// treats it as stale and allows a new collection.
type RecommendationsFreshness struct {
	LastCollectedAt         *time.Time `json:"last_collected_at"`
	LastCollectionError     *string    `json:"last_collection_error"`
	LastCollectionStartedAt *time.Time `json:"last_collection_started_at"`
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
	CloudAccountID *string
	Provider       string
}

// RIUtilizationCacheEntry is a single cached Cost Explorer
// GetReservationUtilization result keyed by (region, lookback_days).
// Payload is the JSON encoding of the caller's utilization slice — kept
// opaque here so the config package stays free of AWS-provider types.
// TTL freshness is evaluated in the caller (api-layer cache wrapper)
// based on FetchedAt vs. a caller-supplied TTL.
type RIUtilizationCacheEntry struct {
	FetchedAt    time.Time
	Region       string
	Payload      []byte
	LookbackDays int
}

// PurchaseHistoryFilter is the filter set consumed by
// StoreInterface.GetPurchaseHistoryFiltered. Each field is optional; a
// zero-valued filter selects all rows (same plan-shape as
// GetAllPurchaseHistory). See the implementation docstring for the per-field
// semantics and the dual-column account predicate.
type PurchaseHistoryFilter struct {
	ExternalIDsByProvider map[string][]string
	Start                 *time.Time
	End                   *time.Time
	Provider              string
	AccountIDs            []string
	Limit                 int
}

// AzureRevocationWindowDays is the length of the Azure reservation free-cancel
// window: a reservation can be returned for a full refund within this many days
// of purchase (issue #290). It is the single source of truth for the window,
// referenced both at purchase-write time (to stamp
// PurchaseHistoryRecord.RevocationWindowClosesAt) and by the revoke endpoint's
// window check, so the two never drift.
const AzureRevocationWindowDays = 7

// RevocationWindowClosesAtFor returns the timestamp at which the in-app revoke
// button should stop being offered for a purchase of the given provider made at
// purchaseTime, or nil when the provider has no in-app free-cancel window.
//
// Only Azure has a direct-API free-cancel window in Phase 1. AWS EC2 RIs have a
// 24h window but no direct cancel API (revocation goes through an AWS Support
// case, out of Phase-1 scope), and GCP commitments have no free-cancel window
// at all, so both return nil and the History UI hides the button.
func RevocationWindowClosesAtFor(provider string, purchaseTime time.Time) *time.Time {
	if strings.EqualFold(provider, "azure") {
		closesAt := purchaseTime.AddDate(0, 0, AzureRevocationWindowDays)
		return &closesAt
	}
	return nil
}

// PurchaseHistoryRecord is the response-layer representation for rows on the
// /api/history page. DB-backed rows always describe *completed* purchases; the
// handler additionally synthesizes rows for pending executions so users can
// see (and cancel) in-flight approvals. Status is the discriminator — the DB
// layer never writes it (tag `dynamodbav:"-"` keeps it out of persistence),
// and the API layer populates it as "completed" or "pending" before returning.
type PurchaseHistoryRecord struct {
	Timestamp                time.Time  `json:"timestamp" dynamodbav:"timestamp"`
	MonthlyCost              *float64   `json:"monthly_cost" dynamodbav:"monthly_cost"`
	CalcRefundAmount         *float64   `json:"calc_refund_amount,omitempty" dynamodbav:"calc_refund_amount,omitempty"`
	RevokedAt                *time.Time `json:"revoked_at,omitempty" dynamodbav:"revoked_at,omitempty"`
	RevocationWindowClosesAt *time.Time `json:"revocation_window_closes_at,omitempty" dynamodbav:"revocation_window_closes_at,omitempty"`
	CloudAccountID           *string    `json:"cloud_account_id,omitempty" dynamodbav:"cloud_account_id,omitempty"`
	OpsHint                  string     `json:"ops_hint,omitempty" dynamodbav:"-"`
	Source                   string     `json:"source,omitempty" dynamodbav:"source,omitempty"`
	CalcRefundCurrency       string     `json:"calc_refund_currency,omitempty" dynamodbav:"calc_refund_currency,omitempty"`
	Payment                  string     `json:"payment" dynamodbav:"payment"`
	PurchaseID               string     `json:"purchase_id" dynamodbav:"purchase_id"`
	ResourceType             string     `json:"resource_type" dynamodbav:"resource_type"`
	SupportCaseID            string     `json:"support_case_id,omitempty" dynamodbav:"support_case_id,omitempty"`
	PlanID                   string     `json:"plan_id,omitempty" dynamodbav:"plan_id,omitempty"`
	PlanName                 string     `json:"plan_name,omitempty" dynamodbav:"plan_name,omitempty"`
	RevokedVia               string     `json:"revoked_via,omitempty" dynamodbav:"revoked_via,omitempty"`
	Region                   string     `json:"region" dynamodbav:"region"`
	Status                   string     `json:"status,omitempty" dynamodbav:"-"`
	Approver                 string     `json:"approver,omitempty" dynamodbav:"-"`
	Provider                 string     `json:"provider" dynamodbav:"provider"`
	StatusDescription        string     `json:"status_description,omitempty" dynamodbav:"-"`
	CreatedByUserID          string     `json:"created_by_user_id,omitempty" dynamodbav:"-"`
	RetryExecutionID         string     `json:"retry_execution_id,omitempty" dynamodbav:"-"`
	Service                  string     `json:"service" dynamodbav:"service"`
	AccountID                string     `json:"account_id" dynamodbav:"account_id"`
	CreatedByUserEmail       string     `json:"created_by_user_email,omitempty" dynamodbav:"-"`
	RetryAttemptN            int        `json:"retry_attempt_n,omitempty" dynamodbav:"-"`
	Count                    int        `json:"count" dynamodbav:"count"`
	RampStep                 int        `json:"ramp_step,omitempty" dynamodbav:"ramp_step,omitempty"`
	EstimatedSavings         float64    `json:"estimated_savings" dynamodbav:"estimated_savings"`
	UpfrontCost              float64    `json:"upfront_cost" dynamodbav:"upfront_cost"`
	Term                     int        `json:"term" dynamodbav:"term"`
	IsAuditGap               bool       `json:"is_audit_gap,omitempty" dynamodbav:"-"`
	RevocationInFlight       bool       `json:"revocation_in_flight,omitempty" dynamodbav:"revocation_in_flight,omitempty"`
}

// RIExchangeRecord represents a record in the ri_exchange_history table.
type RIExchangeRecord struct {
	UpdatedAt          time.Time  `json:"updated_at"`
	CreatedAt          time.Time  `json:"created_at"`
	CreatedByUserID    *string    `json:"created_by_user_id,omitempty"`
	CloudAccountID     *string    `json:"cloud_account_id,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	ApprovedBy         *string    `json:"approved_by,omitempty"`
	SourceInstanceType string     `json:"source_instance_type"`
	Mode               string     `json:"mode"`
	AccountID          string     `json:"account_id"`
	PaymentDue         string     `json:"payment_due"`
	Status             string     `json:"status"`
	ApprovalToken      string     `json:"approval_token,omitempty"`
	Error              string     `json:"error,omitempty"`
	TargetInstanceType string     `json:"target_instance_type"`
	TargetOfferingID   string     `json:"target_offering_id"`
	ExchangeID         string     `json:"exchange_id"`
	ID                 string     `json:"id"`
	Region             string     `json:"region"`
	SourceRIIDs        []string   `json:"source_ri_ids"`
	SourceCount        int        `json:"source_count"`
	TargetCount        int        `json:"target_count"`
}

// Setting represents a configuration setting for the defaults system.
type Setting struct {
	UpdatedAt   time.Time `json:"updated_at"`
	Value       any       `json:"value"`
	Key         string    `json:"key"`
	Type        string    `json:"type"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
}

// CloudAccount represents a single managed cloud account/subscription/project.
type CloudAccount struct {
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
	GCPClientEmail          string    `json:"gcp_client_email,omitempty"`
	AzureSubscriptionID     string    `json:"azure_subscription_id,omitempty"`
	CreatedBy               string    `json:"created_by,omitempty"`
	Provider                string    `json:"provider"`
	ExternalID              string    `json:"external_id"`
	AWSAuthMode             string    `json:"aws_auth_mode,omitempty"`
	AWSRoleARN              string    `json:"aws_role_arn,omitempty"`
	AWSExternalID           string    `json:"aws_external_id,omitempty"`
	AWSBastionID            string    `json:"aws_bastion_id,omitempty"`
	AWSWebIdentityTokenFile string    `json:"aws_web_identity_token_file,omitempty"`
	Name                    string    `json:"name"`
	AzureClientID           string    `json:"azure_client_id,omitempty"`
	ContactEmail            string    `json:"contact_email,omitempty"`
	AzureAuthMode           string    `json:"azure_auth_mode,omitempty"`
	AzureTenantID           string    `json:"azure_tenant_id,omitempty"`
	GCPProjectID            string    `json:"gcp_project_id,omitempty"`
	ID                      string    `json:"id"`
	GCPAuthMode             string    `json:"gcp_auth_mode,omitempty"`
	GCPWIFAudience          string    `json:"gcp_wif_audience,omitempty"`
	Description             string    `json:"description,omitempty"`
	BastionAccountName      string    `json:"bastion_account_name,omitempty"`
	IsSelf                  bool      `json:"is_self,omitempty"`
	CredentialsConfigured   bool      `json:"credentials_configured"`
	AWSIsOrgRoot            bool      `json:"aws_is_org_root,omitempty"`
	Enabled                 bool      `json:"enabled"`
}

// CloudAccountFilter for ListCloudAccounts queries.
type CloudAccountFilter struct {
	Provider  *string
	Enabled   *bool
	BastionID *string
	Search    string
}

// AccountServiceOverride is a sparse per-account override on top of the global ServiceConfig.
// Nil pointer fields inherit the global value.
type AccountServiceOverride struct {
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	RampSchedule   *string   `json:"ramp_schedule,omitempty"`
	Enabled        *bool     `json:"enabled,omitempty"`
	Term           *int      `json:"term,omitempty"`
	Payment        *string   `json:"payment,omitempty"`
	Coverage       *float64  `json:"coverage,omitempty"`
	Provider       string    `json:"provider"`
	ID             string    `json:"id"`
	Service        string    `json:"service"`
	AccountID      string    `json:"account_id"`
	IncludeEngines []string  `json:"include_engines,omitempty"`
	ExcludeRegions []string  `json:"exclude_regions,omitempty"`
	IncludeTypes   []string  `json:"include_types,omitempty"`
	ExcludeTypes   []string  `json:"exclude_types,omitempty"`
	IncludeRegions []string  `json:"include_regions,omitempty"`
	ExcludeEngines []string  `json:"exclude_engines,omitempty"`
}

// AccountRegistration represents a self-service registration request from a
// target account owner. Submitted via POST /api/register during Terraform apply
// of the federation IaC, then approved or rejected by a CUDly admin.
type AccountRegistration struct {
	UpdatedAt            time.Time  `json:"updated_at"`
	CreatedAt            time.Time  `json:"created_at"`
	CloudAccountID       *string    `json:"cloud_account_id,omitempty"`
	ReviewedAt           *time.Time `json:"reviewed_at,omitempty"`
	ReviewedBy           *string    `json:"reviewed_by,omitempty"`
	AzureClientID        string     `json:"azure_client_id,omitempty"`
	GCPProjectID         string     `json:"gcp_project_id,omitempty"`
	Description          string     `json:"description,omitempty"`
	SourceProvider       string     `json:"source_provider,omitempty"`
	AWSRoleARN           string     `json:"aws_role_arn,omitempty"`
	AWSAuthMode          string     `json:"aws_auth_mode,omitempty"`
	AWSExternalID        string     `json:"aws_external_id,omitempty"`
	AzureSubscriptionID  string     `json:"azure_subscription_id,omitempty"`
	AzureTenantID        string     `json:"azure_tenant_id,omitempty"`
	ID                   string     `json:"id"`
	AzureAuthMode        string     `json:"azure_auth_mode,omitempty"`
	ContactEmail         string     `json:"contact_email"`
	GCPClientEmail       string     `json:"gcp_client_email,omitempty"`
	GCPAuthMode          string     `json:"gcp_auth_mode,omitempty"`
	GCPWIFAudience       string     `json:"gcp_wif_audience,omitempty"`
	RegCredentialType    string     `json:"reg_credential_type,omitempty"`
	RegCredentialPayload string     `json:"-"`
	ReferenceToken       string     `json:"reference_token"`
	RejectionReason      string     `json:"rejection_reason,omitempty"`
	AccountName          string     `json:"account_name"`
	ExternalID           string     `json:"external_id"`
	Provider             string     `json:"provider"`
	Status               string     `json:"status"`
	HasCredentials       bool       `json:"has_credentials,omitempty"`
}

// AccountRegistrationFilter for ListAccountRegistrations queries.
type AccountRegistrationFilter struct {
	Status   *string
	Provider *string
	Search   string
}
