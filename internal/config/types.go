// Package config provides configuration management for CUDly.
package config

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/ladder"
)

// GlobalConfig represents the global CUDly configuration.
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
	// provider's utilization metrics catch up. Keys are provider slugs
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

	// RecommendationsCacheStaleHours is the age (hours) at which the
	// recommendations cache is considered stale and a background refresh
	// fires automatically (stale-while-revalidate). 0 disables automatic
	// background refresh; the cron scheduler and the manual Refresh button
	// still work regardless. Valid range: 0–8760 (up to one year).
	// Default: 24.
	RecommendationsCacheStaleHours int `json:"recommendations_cache_stale_hours" db:"recommendations_cache_stale_hours"`

	// RecommendationsLookbackDays is the AWS Cost Explorer lookback window
	// (days) used when fetching fresh recommendations. Must be one of 7,
	// 30, or 60 -- the AWS Cost Explorer LookbackPeriodInDays enum.
	// GCP CUD Recommender has no equivalent lookback parameter (fixed
	// internally); this setting applies to AWS only.
	// Default: 7.
	RecommendationsLookbackDays int `json:"recommendations_lookback_days" db:"recommendations_lookback_days"`

	// PurchaseDelayHours is the Gmail-style pre-fire delay (issue #291 wave-2).
	// When > 0, approving a purchase defers the actual cloud SDK call by this
	// many hours. The user receives a "scheduled, revoke before X" email
	// immediately after approval and may cancel at $0 until the window closes.
	// 0 means immediate-execute (backward compat). Valid range: [0, 168].
	// Default: 48.
	PurchaseDelayHours int `json:"purchase_delay_hours" db:"purchase_delay_hours"`

	// LadderingEnabled is the global kill-switch for the commitment-laddering
	// feature (issue #1333 phase 3). When false (the default), no laddering
	// engine runs fire regardless of per-account LadderConfig.Enabled settings.
	// Set to true to allow per-account configs to activate individually.
	LadderingEnabled bool `json:"laddering_enabled" db:"laddering_enabled"`

	// LadderExecutionEnabled gates the write side of the ladder capability
	// (migration 000083). BOTH LadderingEnabled AND LadderExecutionEnabled
	// must be true for PurchaseLayer / ReshapeBuffer to be wired with real
	// AWS SDK clients. Default false: existing deployments that enable
	// laddering produce plans but never call AWS purchase APIs until an
	// operator explicitly opts in. Fail-loud: wireLadderWriteSide returns
	// a typed ErrLadderExecutionDisabled when this is false.
	LadderExecutionEnabled bool `json:"ladder_execution_enabled" db:"ladder_execution_enabled"`

	// OfferingClass controls the EC2 Reserved Instance offering class used
	// during purchase. Accepted values: "convertible" (default) and
	// "standard". Convertible RIs can be exchanged for a different
	// instance family/size/region/OS; Standard RIs are locked to the exact
	// instance type for the full term but are ~5% cheaper.
	// Unknown values are rejected at purchase time with an explicit error.
	OfferingClass string `json:"offering_class,omitempty" dynamodbav:"offering_class,omitempty"`
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
	// MinCount is the GUI/persisted equivalent of the CLI --min-count flag:
	// the minimum instance/node count a recommendation must carry to be
	// surfaced. Applied at read time by
	// scheduler.filterRecsByResolvedConfigs against the persisted
	// RecommendationRecord.Count. 0 (the default) disables the filter,
	// matching the CLI flag's 0-no-floor semantics.
	MinCount int `json:"min_count,omitempty" dynamodbav:"min_count,omitempty"`
}

// PurchasePlan represents a saved purchase plan for automated execution.
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
	// Unassigned is true when the plan has zero rows in plan_accounts.
	// This can happen for legacy plans created before target_accounts was
	// required (issue #743). Such plans are invisible when an account filter
	// is active because the normal JOIN excludes them; ListPurchasePlans
	// surfaces them alongside filtered results so operators can find and
	// re-scope them. The field is omitted (false) in the no-filter case
	// where all plans are returned unconditionally.
	Unassigned bool `json:"unassigned,omitempty" dynamodbav:"unassigned,omitempty"`
}

// RampSchedule defines how purchases are spread over time.
type RampSchedule struct {
	Type             string    `json:"type" dynamodbav:"type"` // immediate, weekly, monthly, custom
	PercentPerStep   float64   `json:"percent_per_step" dynamodbav:"percent_per_step"`
	StepIntervalDays int       `json:"step_interval_days" dynamodbav:"step_interval_days"`
	CurrentStep      int       `json:"current_step" dynamodbav:"current_step"`
	TotalSteps       int       `json:"total_steps" dynamodbav:"total_steps"`
	StartDate        time.Time `json:"start_date" dynamodbav:"start_date"`
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
	PlanID           string                 `json:"plan_id" dynamodbav:"plan_id"`
	ExecutionID      string                 `json:"execution_id" dynamodbav:"execution_id"`
	Status           string                 `json:"status" dynamodbav:"status"` // pending, notified, approved, canceled, completed, failed
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
	// RetryExecutionID points from a *failed* execution to the new
	// execution created when the user clicked Retry (issue #47). Set
	// only on the original failed row; NULL on every other row including
	// the retry itself (the retry's own RetryAttemptN > 0 is the
	// "this is a retry" marker). Forms a forward-pointing chain:
	// failed_v1.retry_execution_id = failed_v2.execution_id, etc.
	// Migration 000042 self-FKs the column ON DELETE SET NULL so a
	// cleanup of a successor doesn't cascade-delete its predecessor.
	RetryExecutionID *string `json:"retry_execution_id,omitempty" dynamodbav:"retry_execution_id,omitempty"`
	// RetryAttemptN is the position of this execution in a retry chain.
	// 0 (default) on every fresh execution; 1 on the first retry of any
	// failed row; n+1 on the n+1-th retry. The handler reads the
	// predecessor's count and stamps n+1 atomically with the new
	// INSERT inside the retry transaction. The History UI uses this to
	// soft-block retries past a threshold so an obviously-stuck
	// configuration doesn't accumulate dozens of dead retry rows.
	// Migration 000042 added the column with default 0 so legacy rows
	// look exactly like fresh first-retry candidates.
	RetryAttemptN int `json:"retry_attempt_n,omitempty" dynamodbav:"retry_attempt_n,omitempty"`
	// CapacityPercent records what fraction of the originally-recommended
	// counts the user chose when the bulk Purchase flow submitted this
	// execution (1..100). Audit-only: the Recommendations slice already
	// carries the scaled counts, so backend math is unaffected by this
	// field. Defaults to 100 for legacy and scheduler-driven executions.
	CapacityPercent int `json:"capacity_percent,omitempty" dynamodbav:"capacity_percent,omitempty"`
	// ApprovalTokenExpiresAt is the UTC deadline after which the
	// ApprovalToken must be rejected by ApproveExecution and
	// loadCancelableExecution (issue #397). Set at execution creation to
	// ScheduledDate + ApprovalTokenTTL. NULL on rows created before
	// migration 000051 — legacy rows are treated as not-yet-expired
	// (backward-compatible: the TTL-checking gate only fires when the
	// field is non-nil). Migration 000051 adds the column; new rows
	// always carry a non-nil value.
	ApprovalTokenExpiresAt *time.Time `json:"approval_token_expires_at,omitempty" dynamodbav:"approval_token_expires_at,omitempty"`
	// ExecutedByUserID is the UUID of the session user who triggered a
	// direct-execute (issue #289, execute-any/execute-own). NULL on rows
	// that went through the normal approval flow. Non-null signals the
	// approval step was intentionally skipped by an authorized operator.
	// Migration 000058 adds the column.
	ExecutedByUserID *string `json:"executed_by_user_id,omitempty" dynamodbav:"executed_by_user_id,omitempty"`
	// ExecutedAt is the UTC timestamp when the direct-execute path fired.
	// NULL for rows on the normal approval flow. Migration 000058.
	ExecutedAt *time.Time `json:"executed_at,omitempty" dynamodbav:"executed_at,omitempty"`
	// PreApprovalSkipReason is a human-readable token describing why the
	// approval step was skipped. For direct-execute rows it is the literal
	// string "direct-execute permission". NULL on every normal-flow row.
	// Migration 000058.
	PreApprovalSkipReason *string `json:"pre_approval_skip_reason,omitempty" dynamodbav:"pre_approval_skip_reason,omitempty"`
	// IdempotencyKey is the stable lineage anchor the per-rec provider
	// idempotency token is derived from (issue #1012). Unlike ExecutionID
	// it is NOT regenerated on Retry or multi-account fan-out: it is
	// generated once at first creation, copied verbatim onto every Retry
	// successor, and combined with the account ID to seed each per-account
	// fan-out row. This makes DeriveIdempotencyToken reproduce the same
	// token across a strand-and-re-drive so the provider dedupes and the
	// commitment is never bought twice. Empty on rows created before
	// migration 000066 — the derivation falls back to ExecutionID for those
	// (identical to the pre-fix behavior for a single un-retried execution).
	IdempotencyKey string `json:"idempotency_key,omitempty" dynamodbav:"idempotency_key,omitempty"`
	// ScheduledExecutionAt is set by the Gmail-style pre-fire delay path
	// (issue #291 wave-2) when an approve defers the cloud SDK call. The
	// scheduler fires the actual SDK call when this timestamp is in the past.
	// NULL on every immediate-execute row. Migration 000065.
	ScheduledExecutionAt *time.Time `json:"scheduled_execution_at,omitempty" dynamodbav:"scheduled_execution_at,omitempty"`
}

// StatusCanceled is the canonical US-spelling status value new code writes.
const StatusCanceled = "canceled"

// LegacyStatusCanceled is the British-spelling status value old code writes
// during the expand-contract rename (migration 000089). It is constructed by
// concatenation rather than a single literal so the US-locale misspell linter
// does not flag it -- this lets the dual-spelling read paths reference the
// legacy value without a //nolint:misspell directive. The CONTRACT migration
// (#1278) normalizes all rows to StatusCanceled once old code is gone, after
// which every reference to this constant can be deleted.
const LegacyStatusCanceled = "cancel" + "led"

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
	ID           string `json:"id" dynamodbav:"id"`
	Provider     string `json:"provider" dynamodbav:"provider"`
	Service      string `json:"service" dynamodbav:"service"`
	Region       string `json:"region" dynamodbav:"region"`
	ResourceType string `json:"resource_type" dynamodbav:"resource_type"`
	Engine       string `json:"engine,omitempty" dynamodbav:"engine,omitempty"`
	// Details preserves the full common.ServiceDetails payload from the
	// source common.Recommendation so the purchase path can reconstruct
	// the correct typed *Details pointer at execute time (issue #453).
	// Stored as raw JSON because RecommendationRecord lives in the
	// config package, which must NOT import pkg/common (the dependency
	// graph is config <- common in callers, never the reverse). The
	// scheduler populates this at collection time via
	// common.MarshalServiceDetails; the purchase manager reads it via
	// common.DecodeServiceDetailsFor when it builds the
	// common.Recommendation handed to the cloud service client.
	//
	// Empty for rows persisted before #453 — DecodeServiceDetailsFor
	// returns a zero-valued typed pointer in that case so the cloud
	// client's findOfferingID type-assertion still succeeds (the
	// service-side buildOfferingFilters substitutes default
	// Platform / Tenancy / Scope / AZConfig values). New rows always
	// carry the full Details, so non-default platforms (Windows EC2,
	// Postgres RDS, etc.) round-trip correctly.
	Details json.RawMessage `json:"details,omitempty" dynamodbav:"-"`
	Count   int             `json:"count" dynamodbav:"count"`
	// RecommendedCount is the pre-scaling count the collector originally
	// recommended, before the bulk-purchase Capacity % slider scaled it down.
	// The web execute path stamps it so the backend can verify the
	// client-supplied capacity_percent against the scaled Count
	// (floor(RecommendedCount*pct/100) must equal Count) rather than trusting
	// a decorative audit field that could silently disagree (#647). Optional:
	// 0 / absent means "not supplied" (legacy callers, scheduler/CLI rows,
	// retry replays) and the consistency check is skipped for that rec.
	RecommendedCount int     `json:"recommended_count,omitempty" dynamodbav:"recommended_count,omitempty"`
	Term             int     `json:"term" dynamodbav:"term"`
	Payment          string  `json:"payment" dynamodbav:"payment"`
	UpfrontCost      float64 `json:"upfront_cost" dynamodbav:"upfront_cost"`
	// MonthlyCost is nil when the provider API did not return a monthly
	// recurring breakdown (rendered as "—" in the UI, not "$0").
	// Backward-compatible with DynamoDB: existing items with a numeric 0
	// attribute unmarshal as a pointer to 0.0; absent attributes unmarshal
	// as nil. No migration needed.
	MonthlyCost *float64 `json:"monthly_cost" dynamodbav:"monthly_cost"`
	Savings     float64  `json:"savings" dynamodbav:"savings"`
	// OnDemandCost is the canonical on-demand monthly baseline for the
	// recommended commitment, sourced directly from the cloud provider
	// (Azure `CostWithNoReservedInstances`, AWS Cost Explorer
	// `EstimatedMonthlyOnDemandCost`). Persisted via the recommendations
	// row's JSONB `payload` column — no DDL needed.
	//
	// nil means the provider API did not return a baseline; the frontend
	// falls back to reconstructing on-demand from `monthly_cost + savings
	// + amortized_upfront`. When non-nil, the frontend prefers the raw
	// value over reconstruction so anomalies in the reconstructed
	// denominator (e.g. Azure all-upfront recs where monthly_cost=$0
	// collapses the denominator) don't inflate the displayed effective
	// savings %. See #274.
	OnDemandCost *float64 `json:"on_demand_cost,omitempty" dynamodbav:"on_demand_cost,omitempty"`
	// SavingsPercentage is the provider-authoritative effective savings %
	// reported directly by the cloud provider (AWS Cost Explorer
	// `EstimatedMonthlySavingsPercentage`, Azure / GCP converters' computed
	// SavingsPercentage). It is the same figure the CLI/reporter prints
	// verbatim (internal/reporter/reporter.go); persisting it lets the GUI
	// show the identical number instead of re-deriving it client-side from
	// savings / on-demand. Persisted via the recommendations row's JSONB
	// `payload` column; no DDL needed.
	//
	// nil means the provider did not report a percentage; the frontend then
	// falls back to the client-side reconstruction (effectiveSavingsPct).
	// When non-nil, the frontend prefers this value so the displayed % cannot
	// drift from the provider's authoritative number and AWS recs missing
	// on_demand_cost still render a real % rather than an em-dash (see #323).
	SavingsPercentage *float64 `json:"savings_percentage" dynamodbav:"savings_percentage,omitempty"`
	Selected          bool     `json:"selected" dynamodbav:"selected"`
	Purchased         bool     `json:"purchased" dynamodbav:"purchased"`
	PurchaseID        string   `json:"purchase_id,omitempty" dynamodbav:"purchase_id,omitempty"`
	Error             string   `json:"error,omitempty" dynamodbav:"error,omitempty"`
	CloudAccountID    *string  `json:"cloud_account_id,omitempty" dynamodbav:"cloud_account_id,omitempty"`
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
	// UsageHistory is a short time-series of daily RI-coverage percentages
	// (0-100) for the last N days of the lookback window, ordered from
	// oldest to newest. nil means the collector did not populate it (e.g.
	// provider not yet wired); an empty non-nil slice means the collector
	// ran but returned no daily data. The frontend renders nil as "—" and
	// a non-empty slice as a thumbnail sparkline. Stored inside the
	// recommendations JSONB payload — no DDL change needed (closes #239
	// Part 1 for AWS).
	UsageHistory []float64 `json:"usage_history,omitempty" dynamodbav:"usage_history,omitempty"`
	// VCPU and MemoryGB surface the compute size of the recommended
	// instance type so the frontend's Capacity column can render
	// "<vcpu> vCPU / <memory> GB" without parsing the opaque Details blob
	// (#219). They are NOT persisted: the canonical source is the typed
	// ComputeDetails nested inside Details (config must stay free of
	// pkg/common imports). The api layer decodes Details via
	// common.DecodeServiceDetailsFor in buildRecommendationsResponse and
	// stamps these top-level fields on the way out, so the API JSON carries
	// them at the top level where the frontend already reads them.
	//
	// Pointers (not plain int/float64) so "absent / non-compute / unknown
	// size" serializes as omitted rather than a misleading 0: the frontend
	// renders absent as "—", and a literal 0 would otherwise look like a
	// real "0 vCPU / 0 GB" capacity. dynamodbav:"-" because they are
	// derived-on-read, never stored.
	VCPU     *int     `json:"vcpu,omitempty" dynamodbav:"-"`
	MemoryGB *float64 `json:"memory_gb,omitempty" dynamodbav:"-"`
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
	Provider      string   // "aws" / "azure" / "gcp" / "" (all)
	Service       string   // "" = all services
	Region        string   // "" = all regions
	AccountIDs    []string // nil/empty = all accounts
	MinSavingsUSD float64  // 0 = no floor on monthly savings dollar amount
	MinSavingsPct float64  // 0 = no floor on savings percentage (0–100 scale)
	ID            string   // "" = all ids; non-empty = exact match on the id column
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

// PurchaseHistoryFilter is the filter set consumed by
// StoreInterface.GetPurchaseHistoryFiltered. Each field is optional; a
// zero-valued filter selects all rows (same plan-shape as
// GetAllPurchaseHistory). See the implementation docstring for the per-field
// semantics and the dual-column account predicate.
type PurchaseHistoryFilter struct {
	// Provider matches purchase_history.provider exactly. Empty skips the clause.
	Provider string
	// AccountIDs matches purchase_history.cloud_account_id (the cloud_accounts
	// UUID FK) with ANY($). Empty/nil skips this half of the account predicate.
	AccountIDs []string
	// ExternalIDsByProvider matches purchase_history.account_id (the
	// cloud-provider external account number) scoped per provider. The caller
	// resolves AccountIDs to their (provider, external_id) pairs and groups the
	// external ids by provider, so the predicate matches each external id only
	// against rows of its own provider:
	//
	//	(provider = $p AND account_id = ANY($extsForP))
	//
	// This keeps the (provider, external_id) pairing intact so a filter for
	// aws/123 never pulls azure/123 rows that reuse the same external number.
	// The "" provider key means "provider unknown" (legacy raw external number)
	// and matches account_id without a provider gate. Empty/nil skips this half
	// of the account predicate.
	ExternalIDsByProvider map[string][]string
	// Start/End bound purchase_history.timestamp. nil for both skips the clause;
	// nil for either leaves that side open (caller owns any range cap, see
	// api.MaxHistoryDateRangeDays).
	Start *time.Time
	End   *time.Time
	// Limit caps the row count; clamped to [1, MaxListLimit] with a
	// DefaultListLimit fallback when <= 0.
	Limit int
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
	AccountID    string    `json:"account_id" dynamodbav:"account_id"`
	PurchaseID   string    `json:"purchase_id" dynamodbav:"purchase_id"`
	Timestamp    time.Time `json:"timestamp" dynamodbav:"timestamp"`
	Provider     string    `json:"provider" dynamodbav:"provider"`
	Service      string    `json:"service" dynamodbav:"service"`
	Region       string    `json:"region" dynamodbav:"region"`
	ResourceType string    `json:"resource_type" dynamodbav:"resource_type"`
	Count        int       `json:"count" dynamodbav:"count"`
	Term         int       `json:"term" dynamodbav:"term"`
	Payment      string    `json:"payment" dynamodbav:"payment"`
	UpfrontCost  float64   `json:"upfront_cost" dynamodbav:"upfront_cost"`
	// MonthlyCost is nil when the provider API did not return a monthly
	// recurring breakdown for this commitment (e.g. Azure all-upfront where
	// no recurring charge exists at the commitment layer). GCP commitments
	// are monthly-billed in this repo, so they always populate MonthlyCost.
	// The frontend renders "—" for nil, "$X.XX" when populated. Aggregations
	// must skip nil entries rather than treating them as $0 to avoid
	// distorting totals. Migration 000063 dropped the NOT NULL constraint so
	// new rows can carry NULL; existing rows with 0.0 are preserved as-is
	// (those are real zeros from AWS all-upfront commitments).
	MonthlyCost      *float64 `json:"monthly_cost" dynamodbav:"monthly_cost"`
	EstimatedSavings float64  `json:"estimated_savings" dynamodbav:"estimated_savings"`
	PlanID           string   `json:"plan_id,omitempty" dynamodbav:"plan_id,omitempty"`
	PlanName         string   `json:"plan_name,omitempty" dynamodbav:"plan_name,omitempty"`
	RampStep         int      `json:"ramp_step,omitempty" dynamodbav:"ramp_step,omitempty"`
	CloudAccountID   *string  `json:"cloud_account_id,omitempty" dynamodbav:"cloud_account_id,omitempty"`
	Status           string   `json:"status,omitempty" dynamodbav:"-"`
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
	// the button on their own pending rows. Set only for synthesized
	// pending/notified rows (executions); empty on completed history
	// rows (where the action has already completed). Excluded from DB
	// persistence.
	CreatedByUserID string `json:"created_by_user_id,omitempty" dynamodbav:"-"`
	// RetryExecutionID propagates the originating execution's pointer to
	// its successor when the user retried it (issue #47). Set only on
	// the *original* failed row that has been retried; the History UI
	// renders an inline "Retried as #abc" link to the successor row when
	// this is non-empty. Excluded from DB persistence (synthesized from
	// purchase_executions).
	RetryExecutionID string `json:"retry_execution_id,omitempty" dynamodbav:"-"`
	// RetryAttemptN propagates the originating execution's retry-chain
	// position so the History UI can render "↻ Retry of #xyz" inline
	// links on retry rows (n > 0) and gate the Retry button against the
	// soft-block threshold (n >= 5). Excluded from DB persistence.
	RetryAttemptN int `json:"retry_attempt_n,omitempty" dynamodbav:"-"`
	// OpsHint is a short operator-actionable message rendered inline in
	// place of the Retry button when the failure reason on the row
	// matches a known-persistent-misconfiguration pattern (e.g.
	// "FROM_EMAIL not configured" → "Set FROM_EMAIL tfvar then retry").
	// Set only on `failed` rows whose Error matches the persistent map;
	// empty otherwise. Excluded from DB persistence (computed at read
	// time so updates to the persistent-failure map land instantly).
	OpsHint string `json:"ops_hint,omitempty" dynamodbav:"-"`
	// IsAuditGap marks a synthesized "completed" row whose purchase_history
	// write failed after a successful purchase (issue #621). Such a row is
	// reconstructed from the execution so the purchase stays visible, but its
	// execution-level dollars are excluded from the committed totals: a
	// partially-saved multi-rec execution can have BOTH some real
	// purchase_history rows AND this synthesized row, so adding the full
	// execution total would double-count the recs that did save. The dollars
	// are surfaced via the individual purchase_history rows that succeeded;
	// this row is the audit flag, not a money source. Real purchase_history
	// rows loaded from the DB always leave this false. Excluded from DB
	// persistence (set only at read time on synthesized rows).
	IsAuditGap bool `json:"is_audit_gap,omitempty" dynamodbav:"-"`
	// CreatedByUserEmail is the email address of the user who created the
	// underlying execution, resolved from CreatedByUserID via the auth
	// service. Populated only on synthesized execution rows (pending,
	// notified, failed, expired, canceled) when a valid user ID is
	// present; empty for scheduler-driven executions, legacy NULL-creator
	// rows, and completed purchase_history rows. Excluded from DB
	// persistence (resolved at read time). The UI renders this in the
	// Approval Queue "Created by" column instead of the raw UUID.
	CreatedByUserEmail string `json:"created_by_user_email,omitempty" dynamodbav:"-"`

	// --- Revocation window fields (issue #290) ---
	//
	// RevocationWindowClosesAt is set when the purchase_history row is
	// written: Timestamp + the provider-specific free-cancel window
	// (Azure: 7 days). NULL for AWS EC2 (no direct cancel API) and GCP
	// (no free-cancel window). Persisted in purchase_history.
	RevocationWindowClosesAt *time.Time `json:"revocation_window_closes_at,omitempty" dynamodbav:"revocation_window_closes_at,omitempty"`
	// RevokedAt is set by the revoke endpoint when the provider API
	// confirmed the cancellation / refund. Persisted.
	RevokedAt *time.Time `json:"revoked_at,omitempty" dynamodbav:"revoked_at,omitempty"`
	// RevokedVia identifies how the revocation was completed: "direct-api"
	// (provider returned 2xx) or "support-case" (AWS Support case filed).
	// Persisted.
	RevokedVia string `json:"revoked_via,omitempty" dynamodbav:"revoked_via,omitempty"`
	// SupportCaseID is non-empty when RevokedVia == "support-case".
	// Persisted.
	SupportCaseID string `json:"support_case_id,omitempty" dynamodbav:"support_case_id,omitempty"`

	// --- Refund-quote audit fields (issue #290 Finding #4, migration 000071) ---
	//
	// CalcRefundAmount is the amount Azure quoted at CalculateRefund time, captured
	// for audit and TOCTOU-divergence detection in the two-step revoke confirm flow.
	// NULL for revocations that predate this feature or where Azure returned no amount.
	CalcRefundAmount *float64 `json:"calc_refund_amount,omitempty" dynamodbav:"calc_refund_amount,omitempty"`
	// CalcRefundCurrency is the ISO-4217 currency code from the CalculateRefund quote
	// (e.g. "USD"). NULL when CalcRefundAmount is NULL.
	CalcRefundCurrency string `json:"calc_refund_currency,omitempty" dynamodbav:"calc_refund_currency,omitempty"`

	// --- Partial-success reconciliation (issue #290 Finding #6, migration 000072) ---
	//
	// RevocationInFlight is set to true immediately before the Azure Return API call
	// and cleared (set to false) by a successful MarkPurchaseRevoked. When all DB
	// retries fail, the flag stays true so the finalize_revocations scheduled sweep
	// can detect and retry the MarkPurchaseRevoked write without re-calling Azure
	// (preventing a duplicate-refund error).
	RevocationInFlight bool `json:"revocation_in_flight,omitempty" dynamodbav:"revocation_in_flight,omitempty"`

	// OfferingClass records whether this commitment is a 'standard' or
	// 'convertible' RI. NULL on pre-migration rows. The Sell-on-Marketplace
	// button renders only when this equals "standard" (issue #292).
	// Persisted in purchase_history via migration 000087.
	OfferingClass string `json:"offering_class,omitempty" dynamodbav:"offering_class,omitempty"`
	// ListingID is the AWS ReservedInstancesListingId returned by
	// CreateReservedInstancesListing. Empty when the RI has not been
	// listed. Persisted in purchase_history via migration 000087.
	ListingID string `json:"listing_id,omitempty" dynamodbav:"listing_id,omitempty"`
	// ListingState mirrors the AWS marketplace listing state (see the
	// ListingState* constants). Empty when not listed. Persisted in
	// purchase_history via migration 000087.
	ListingState string `json:"listing_state,omitempty" dynamodbav:"listing_state,omitempty"`
}

// AWS EC2 ReservedInstancesListing status values, mirroring the
// ec2types.ListingStatus enum. Stored verbatim in purchase_history.listing_state
// so the strings must match AWS exactly. ListingStatePending is additionally
// written by the marketplace-list handler as a transient claim that guards
// against concurrent listing creation (issue #292); the other three come
// straight from AWS.
const (
	ListingStateActive    = "active"
	ListingStatePending   = "pending"
	ListingStateCancelled = "cancelled" //nolint:misspell // AWS ListingStatus enum literal, not prose
	ListingStateClosed    = "closed"
)

// RIExchangeRecord represents a record in the ri_exchange_history table.
type RIExchangeRecord struct {
	ID                 string   `json:"id"`
	AccountID          string   `json:"account_id"`
	ExchangeID         string   `json:"exchange_id"`
	Region             string   `json:"region"`
	SourceRIIDs        []string `json:"source_ri_ids"`
	SourceInstanceType string   `json:"source_instance_type"`
	SourceCount        int      `json:"source_count"`
	TargetOfferingID   string   `json:"target_offering_id"`
	TargetInstanceType string   `json:"target_instance_type"`
	TargetCount        int      `json:"target_count"`
	PaymentDue         string   `json:"payment_due"`
	Status             string   `json:"status"`
	ApprovalToken      string   `json:"approval_token,omitempty"`
	Error              string   `json:"error,omitempty"`
	Mode               string   `json:"mode"`
	// CreatedByUserID is the UUID of the session user who submitted the exchange
	// (populated for dashboard-initiated exchanges; nil for automated or legacy
	// email-link-initiated ones). Exposed to the frontend so the Approve button
	// can apply the approve-own ownership check client-side.
	CreatedByUserID *string `json:"created_by_user_id,omitempty"`
	// ApprovedBy carries the email of the session user who approved the exchange
	// via the dashboard Approve button (issue #300). Nil for token-authed approvals.
	ApprovedBy *string `json:"approved_by,omitempty"`
	// LadderRunID links this exchange record to the ladder run that created it
	// (cudly-ladder engine). Nil for standalone ri_exchange_reshape task records.
	// The database column ri_exchange_history.ladder_run_id was added in migration
	// 000080 and is the authoritative source for origin scoping in
	// CancelPendingExchangesByOrigin.
	//
	// KNOWN/ACCEPTABLE: the FK is ON DELETE SET NULL (migration 000080), so
	// deleting a ladder_runs row nulls this column and reclassifies the record
	// as standalone. A still-pending reshape then becomes standalone-cancellable
	// (the standalone-origin sweep would cancel it). This is acceptable: a
	// deleted run has no owner to approve its pendings, so canceling them on the
	// next standalone sweep is the safe outcome, not a leak.
	LadderRunID    *string    `json:"ladder_run_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CloudAccountID *string    `json:"cloud_account_id,omitempty"`
}

// ConfigSetting represents a configuration setting for the defaults system.
type ConfigSetting struct { //nolint:revive // exported: doc comment style intentional
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

// ==========================================
// COMMITMENT LADDERING
// ==========================================

// Ladder default constants. These alias the canonical values in pkg/ladder so
// that internal/config callers can reference them by name without importing
// pkg/ladder directly. The string-typed mode/cadence values are also sourced
// from pkg/ladder to guarantee the DB and the engine always use the same tokens.

// DefaultLadderTargetCoverage is the default commitment coverage target (%).
// Aliases ladder.DefaultTargetCoveragePct so only one source of truth exists.
const DefaultLadderTargetCoverage = ladder.DefaultTargetCoveragePct

// DefaultLadderBufferFraction is the default fraction of the base allocation
// reserved in short-term / convertible buffer commitments.
const DefaultLadderBufferFraction = ladder.DefaultBufferFraction

// DefaultLadderBaselinePercentile is the default usage percentile used to
// anchor the base commitment layer.
const DefaultLadderBaselinePercentile = ladder.DefaultBaselinePercentile

// DefaultLadderLookbackDays is the default historical window (days) used to
// compute the usage baseline.
const DefaultLadderLookbackDays = ladder.DefaultLookbackDays

// DefaultLadderBufferUtilThreshold is the default buffer-layer utilization %
// below which the engine emits a reshape recommendation.
const DefaultLadderBufferUtilThreshold = ladder.DefaultBufferUtilizationThresholdPct

// DefaultLadderMaxActionsPerRun is the default cap on the number of
// PlannedActions the engine may execute per run.
const DefaultLadderMaxActionsPerRun = 10

// MaxLadderActionsPerRun is the ceiling enforced at validation time. A value
// above this is almost certainly a misconfiguration and should fail loud rather
// than fan out unbounded actions.
const MaxLadderActionsPerRun = 50

// LadderConfigDB is the DB-persistence mirror of pkg/ladder.LadderConfig.
// It stores one per-account, per-provider ladder configuration row and is
// used by the store layer (GetLadderConfig / UpsertLadderConfig) and the API
// handler. Mode and Cadence are plain strings whose valid values are defined
// by pkg/ladder (ModeEmailApproval, ModeAutoApprove, CadenceDaily,
// CadenceWeekly). Validation calls pkg/ladder's Parse* functions so the
// internal/config package never redefines those constants.
//
// MaxHourlyCommitPerRun is a pointer because nil means "no cap" (distinct from
// 0, which would cap all spending). All numeric money fields follow the project
// rule: absent = nil/pointer, never 0.
// Field order is optimized for govet fieldalignment (pointer-containing fields
// grouped first to shrink the GC pointer-scan range, then scalars, bool last).
// It intentionally does not follow the logical/SQL column order; see the
// scanLadderConfig / Upsert SQL for the wire order.
type LadderConfigDB struct {
	UpdatedAt time.Time `json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
	// MaxHourlyCommitPerRun caps the total hourly commitment delta a single run
	// may purchase. nil means no cap.
	MaxHourlyCommitPerRun      *float64        `json:"max_hourly_commit_per_run,omitempty"`
	CloudAccountID             string          `json:"cloud_account_id"`
	Provider                   string          `json:"provider"`
	Mode                       string          `json:"mode"`    // ladder.ModeEmailApproval | ladder.ModeAutoApprove
	Cadence                    string          `json:"cadence"` // ladder.CadenceDaily | ladder.CadenceWeekly
	ID                         string          `json:"id"`
	RampSchedule               json.RawMessage `json:"ramp_schedule"`
	BufferUtilizationThreshold float64         `json:"buffer_utilization_threshold"`
	LookbackDays               int             `json:"lookback_days"`
	MaxActionsPerRun           int             `json:"max_actions_per_run"`
	// BaselinePercentile is the statistical percentile used to anchor the base
	// commitment layer. Must be in (0, 50].
	BaselinePercentile float64 `json:"baseline_percentile"`
	BufferFraction     float64 `json:"buffer_fraction"`
	TargetCoverage     float64 `json:"target_coverage"`
	Enabled            bool    `json:"enabled"`
}

// LadderRunDB mirrors the ladder_runs table (migration 000080).
// Monetary snapshot columns are *float64 (nullable, NEVER 0-coerced:
// NULL means "not computed", not "$0"). Field order minimizes GC
// pointer-scan range: explicit pointer fields come before scalars.
type LadderRunDB struct {
	// Nullable monetary snapshot: nil means "not computed", never $0.
	BaselineUSDHr *float64 `json:"baseline_usd_hr,omitempty"`
	TargetUSDHr   *float64 `json:"target_usd_hr,omitempty"`
	ExistingUSDHr *float64 `json:"existing_usd_hr,omitempty"`
	GapUSDHr      *float64 `json:"gap_usd_hr,omitempty"`

	// Nullable FK and optional text fields (all pointer types).
	ConfigID               *string    `json:"config_id,omitempty"`
	CompletedAt            *time.Time `json:"completed_at,omitempty"`
	ApprovalTokenHash      *string    `json:"approval_token_hash,omitempty"`
	ApprovalTokenExpiresAt *time.Time `json:"approval_token_expires_at,omitempty"`
	ApprovedBy             *string    `json:"approved_by,omitempty"`
	CancelledBy            *string    `json:"cancelled_by,omitempty"`
	FireAt                 *time.Time `json:"fire_at,omitempty"`
	// Mode and Cadence are nullable in the DB (populated from LadderConfigDB
	// at run creation time; nil only for legacy / partially-failed rows).
	Mode    *string `json:"mode,omitempty"`
	Cadence *string `json:"cadence,omitempty"`

	// Plan JSON blob (JSONB, NOT NULL DEFAULT '{}').
	Plan json.RawMessage `json:"plan"`

	// Non-nullable accumulator totals (initialised to 0, not measurements;
	// zero is a meaningful value for these counters unlike the monetary snapshot).
	TotalHourlyCommit float64 `json:"total_hourly_commit"`
	TotalUpfrontCost  float64 `json:"total_upfront_cost"`
	EstimatedSavings  float64 `json:"estimated_savings"`

	// Required string / enum fields.
	ID     string           `json:"id"`
	Status ladder.RunStatus `json:"status"`

	// Required timestamps.
	StartedAt time.Time `json:"started_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LadderTrancheDB mirrors the ladder_tranches table (migration 000081).
// One row per ramp step per allocation; persisted as status=scheduled
// audit rows only in PR-2 (no firing sweep wired yet). Field order
// minimizes GC pointer-scan range.
type LadderTrancheDB struct {
	// Nullable FK pointers.
	ConfigID    *string `json:"config_id,omitempty"`
	RunID       *string `json:"run_id,omitempty"`
	ExecutionID *string `json:"execution_id,omitempty"` // references purchase_executions.execution_id

	// Required fields.
	ID            string               `json:"id"`
	LayerType     ladder.LayerType     `json:"layer_type"`
	Term          ladder.Term          `json:"term"`
	PaymentOption ladder.PaymentOption `json:"payment_option"`
	Status        ladder.TrancheStatus `json:"status"`

	// Monetary and timing.
	AmountUSDHr   float64   `json:"amount_usd_hr"`
	ScheduledDate time.Time `json:"scheduled_date"`
	CreatedAt     time.Time `json:"created_at"`
}
