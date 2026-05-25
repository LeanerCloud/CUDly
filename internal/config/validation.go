// Package config provides configuration management using PostgreSQL.
package config

import (
	"fmt"
	"net/mail"
	"strings"
)

// ValidProviders lists all supported cloud providers
var ValidProviders = []string{"aws", "azure", "gcp"}

// ValidPaymentOptions lists the AWS-canonical payment options. Kept for
// backwards compatibility; prefer ValidPaymentOptionsByProvider for
// provider-aware validation.
var ValidPaymentOptions = []string{"no-upfront", "partial-upfront", "all-upfront"}

// ValidPaymentOptionsByProvider maps each provider to the payment option
// tokens it accepts. The sets are tightened to the canonical tokens each
// provider actually models semantically; AWS-style aliases the service
// clients also accept (for legacy frontend compat) are deliberately not
// surfaced here — config-layer validation rejects them so input bugs aren't
// hidden behind silent alias coercion in the service-client switches.
// Callers that need to translate legacy/cross-provider tokens into the
// canonical form should use NormalizePaymentOption at the emission boundary
// (see internal/scheduler/scheduler.go:convertRecommendations).
//
// Canonical sets, verified against the per-service purchase/pricing switches:
//
//   - AWS  : {no-upfront, partial-upfront, all-upfront}
//     Three distinct billing tiers exposed by RI/SP offering APIs.
//
//   - Azure: {upfront, monthly}
//     Reservation purchases only model two billing plans. See:
//     providers/azure/services/compute/client.go:493-497
//     providers/azure/services/cache/client.go:375-379
//     providers/azure/services/cosmosdb/client.go:368-372
//     providers/azure/services/database/client.go:376-380
//     providers/azure/services/search/client.go:349-353
//     providers/azure/services/synapse/client.go:354-358
//     providers/azure/services/managedredis/client.go:345-349
//     (The savingsplans switch at providers/azure/services/savingsplans/
//     client.go:418-429 mirrors AWS's three-tier set, but Azure savings-plan
//     recommendations are not currently emitted — GetRecommendations returns
//     []; the canonical set follows the only path that emits today.)
//
//   - GCP  : {monthly}
//     GCP CUDs are inherently monthly-billed across the term — the GCP CUD
//     purchase API only takes a Plan (TWELVE_MONTH / THIRTY_SIX_MONTH), not a
//     payment-option discriminator (see
//     providers/gcp/services/computeengine/client.go:350-373 where
//     buildCommitmentRequests sets Plan from rec.Term and never reads
//     PaymentOption). The per-service pricing switches at
//     providers/gcp/services/computeengine/client.go:587-591,
//     providers/gcp/services/cloudsql/client.go:288-292,
//     providers/gcp/services/cloudstorage/client.go:297-301,
//     providers/gcp/services/memorystore/client.go:245-249 alias "upfront"
//     and "all-upfront" only for compatibility with downstream code that
//     expects a payment-option token; semantically GCP exposes one billing
//     plan and the validator surfaces that explicitly.
var ValidPaymentOptionsByProvider = map[string][]string{
	"aws":   {"no-upfront", "partial-upfront", "all-upfront"},
	"azure": {"upfront", "monthly"},
	"gcp":   {"monthly"},
}

// validPaymentOptionsUnion is the union of all provider payment option sets,
// used for global-config default validation where no provider context is
// available. Accepts any token that is valid for at least one provider.
var validPaymentOptionsUnion = func() []string {
	seen := map[string]bool{}
	var all []string
	for _, opts := range ValidPaymentOptionsByProvider {
		for _, o := range opts {
			if !seen[o] {
				seen[o] = true
				all = append(all, o)
			}
		}
	}
	return all
}()

// validPaymentOptionsFor returns the provider-canonical payment option slice
// for the given provider (lowercase). Returns nil when the provider is unknown.
func validPaymentOptionsFor(provider string) []string {
	return ValidPaymentOptionsByProvider[provider]
}

// NormalizePaymentOption maps a raw payment-option token onto the canonical
// token the given provider semantically models (see ValidPaymentOptionsByProvider).
// It exists so the recommendation-emission boundary (see
// internal/scheduler/scheduler.go:convertRecommendations) can defensively
// canonicalize any AWS-style token that a code path or a globally-default
// payment-option setting might stamp onto a non-AWS rec, before the rec is
// persisted and later validated against the provider-canonical set.
//
// Returns (canonical, true) when raw is already canonical for the provider
// or has an unambiguous canonical mapping. Returns ("", false) for unknown
// providers and (raw, false) for tokens that have no canonical mapping on
// the given provider (e.g. an Azure/GCP-style "upfront" on AWS) — callers
// can use ok=false to surface the unmapped token at the next validator
// boundary. Per-provider mapping:
//
//   - AWS  : passthrough (AWS already speaks the three-tier set).
//   - Azure: all-upfront → upfront, no-upfront → monthly,
//     partial-upfront → upfront (no semantic equivalent — coerce to the
//     all-upfront tier rather than drop the rec; caller may log).
//   - GCP  : all-upfront → monthly, no-upfront → monthly,
//     partial-upfront → monthly, upfront → monthly (GCP CUDs are
//     inherently monthly-billed — every non-monthly token collapses to the
//     one billing plan GCP actually models). The collapse from "upfront" to
//     "monthly" is what makes the existing
//     providers/gcp/services/computeengine/client.go:804 stamp safe: the
//     scheduler.convertRecommendations boundary coerces it once before
//     persistence so the rec carries the canonical token downstream.
//
// The partial-upfront coercion is deliberate on both Azure and GCP: dropping
// the rec would be a silent data loss for the user, while coercing to the
// closest billing model the provider offers preserves the rec. The caller is
// expected to log a warning when raw != canonical so an operator notices the
// upstream input bug.
//
// Empty raw passes through as ("", true) — callers that distinguish "unset"
// from "invalid" can check the returned bool only when raw is non-empty.
func NormalizePaymentOption(provider, raw string) (string, bool) {
	if _, known := ValidPaymentOptionsByProvider[provider]; !known {
		return "", false
	}
	if raw == "" {
		return "", true
	}
	// Already canonical for this provider: passthrough.
	for _, v := range ValidPaymentOptionsByProvider[provider] {
		if raw == v {
			return raw, true
		}
	}
	// Cross-provider tokens that have a canonical equivalent in the target
	// provider's billing model. Each provider's mapping is delegated to a
	// helper to keep cyclomatic complexity below the project limit.
	if canon, ok := crossProviderPaymentAlias(provider, raw); ok {
		return canon, true
	}
	// Anything else (including Azure/GCP-style tokens on AWS) is left as-is
	// and will surface as a validation error at the next boundary.
	return raw, false
}

// crossProviderPaymentAlias maps an AWS-style (or Azure-style on GCP) token
// onto the target provider's canonical token. Returns (canonical, true) when
// a mapping exists, ("", false) when none does. Split out of
// NormalizePaymentOption purely to keep that function under the project's
// gocyclo limit; the policy lives here.
func crossProviderPaymentAlias(provider, raw string) (string, bool) {
	switch provider {
	case "azure":
		// Azure reservations model both billing plans; coerce AWS-style tokens
		// to the Azure-canonical spelling. partial-upfront has no Azure
		// equivalent — coerce to the all-upfront tier so the rec survives
		// validation rather than dropping silently (caller WARN-logs).
		switch raw {
		case "all-upfront", "partial-upfront":
			return "upfront", true
		case "no-upfront":
			return "monthly", true
		}
	case "gcp":
		// GCP commitments are monthly-only — every non-monthly token (AWS-style
		// or the legacy "upfront" some GCP code paths still stamp) collapses
		// to "monthly", the one billing plan GCP semantically models.
		switch raw {
		case "all-upfront", "no-upfront", "partial-upfront", "upfront":
			return "monthly", true
		}
	}
	return "", false
}

// ValidRampScheduleTypes lists all supported ramp schedule types
var ValidRampScheduleTypes = []string{"immediate", "weekly", "monthly", "custom"}

// ValidCollectionSchedules lists all valid collection schedule values
var ValidCollectionSchedules = []string{"", "hourly", "daily", "weekly"}

// Validate validates the GlobalConfig
func (c *GlobalConfig) Validate() error {
	if err := c.validateProviders(); err != nil {
		return err
	}
	if err := c.validateNotificationEmail(); err != nil {
		return err
	}
	if err := validateGlobalTerm(c.DefaultTerm); err != nil {
		return err
	}
	if err := validatePaymentOption(c.DefaultPayment); err != nil {
		return err
	}
	if err := validateCoverage(c.DefaultCoverage); err != nil {
		return err
	}
	if !isValidCollectionSchedule(c.CollectionSchedule) {
		return fmt.Errorf("invalid collection_schedule: %q (valid: hourly, daily, weekly)", c.CollectionSchedule)
	}
	if c.NotificationDaysBefore < 0 || c.NotificationDaysBefore > MaxNotificationDaysBefore {
		return fmt.Errorf("notification_days_before must be between 0 and %d, got: %d", MaxNotificationDaysBefore, c.NotificationDaysBefore)
	}
	if err := c.validateGracePeriodDays(); err != nil {
		return err
	}
	return c.validateRecommendationsFields()
}

// validateRecommendationsFields validates the recommendation-cycle parameters
// added in #301 (cache-staleness threshold and AWS Cost Explorer lookback).
// Extracted to keep Validate's cyclomatic complexity under the project limit.
func (c *GlobalConfig) validateRecommendationsFields() error {
	if err := c.validateRecommendationsCacheStaleHours(); err != nil {
		return err
	}
	return c.validateRecommendationsLookbackDays()
}

// validateRecommendationsCacheStaleHours validates the stale-while-revalidate
// cache age threshold. 0 disables background refresh; negative values are
// rejected. Maximum is MaxRecommendationsCacheStaleHours (1 year).
func (c *GlobalConfig) validateRecommendationsCacheStaleHours() error {
	if c.RecommendationsCacheStaleHours < 0 || c.RecommendationsCacheStaleHours > MaxRecommendationsCacheStaleHours {
		return fmt.Errorf("recommendations_cache_stale_hours must be between 0 and %d, got: %d (0 = disable auto-refresh)", MaxRecommendationsCacheStaleHours, c.RecommendationsCacheStaleHours)
	}
	return nil
}

// validateRecommendationsLookbackDays validates the AWS Cost Explorer lookback
// window. Only the enum values {7, 30, 60} are accepted (LookbackPeriodInDays).
// A value of 0 means "unset / use the backend default" and is always accepted;
// this matches the zero-value of the Go int field when the DB row predates the
// column (the migration adds DEFAULT 7, but in-memory structs constructed
// without explicitly setting the field carry 0).
func (c *GlobalConfig) validateRecommendationsLookbackDays() error {
	if c.RecommendationsLookbackDays == 0 {
		// Unset → backend defaults to DefaultRecommendationsLookbackDays.
		return nil
	}
	for _, valid := range ValidRecommendationsLookbackDays {
		if c.RecommendationsLookbackDays == valid {
			return nil
		}
	}
	validStrs := make([]string, len(ValidRecommendationsLookbackDays))
	for i, v := range ValidRecommendationsLookbackDays {
		validStrs[i] = fmt.Sprintf("%d", v)
	}
	return fmt.Errorf("recommendations_lookback_days must be one of [%s], got: %d", strings.Join(validStrs, ", "), c.RecommendationsLookbackDays)
}

// validateGracePeriodDays validates the per-provider grace-period map.
// Keys must be known provider slugs; values must be in [0, MaxGracePeriodDays].
// A nil / empty map is always valid (falls back to the default).
func (c *GlobalConfig) validateGracePeriodDays() error {
	for provider, days := range c.GracePeriodDays {
		if !isValidProvider(provider) {
			return fmt.Errorf("grace_period_days: invalid provider key %q (valid: %s)", provider, strings.Join(ValidProviders, ", "))
		}
		if days < 0 || days > MaxGracePeriodDays {
			return fmt.Errorf("grace_period_days[%s] must be between 0 and %d, got: %d", provider, MaxGracePeriodDays, days)
		}
	}
	return nil
}

// validateProviders checks that all enabled providers are valid
func (c *GlobalConfig) validateProviders() error {
	for _, p := range c.EnabledProviders {
		if !isValidProvider(p) {
			return fmt.Errorf("invalid provider: %s (valid: %s)", p, strings.Join(ValidProviders, ", "))
		}
	}
	return nil
}

// validateNotificationEmail validates the notification email format if provided
func (c *GlobalConfig) validateNotificationEmail() error {
	if c.NotificationEmail != nil && *c.NotificationEmail != "" {
		if _, err := mail.ParseAddress(*c.NotificationEmail); err != nil {
			return fmt.Errorf("invalid notification email format: %s", *c.NotificationEmail)
		}
	}
	return nil
}

// validateTerm validates that the term is 1 or 3 years (or 0 for not set - service-level only)
func validateTerm(term int) error {
	if term != 0 && term != 1 && term != 3 {
		return fmt.Errorf("default term must be 1 or 3 years, got: %d", term)
	}
	return nil
}

// validateGlobalTerm validates that the term is 1 or 3 years (0 is not allowed for global config)
func validateGlobalTerm(term int) error {
	if term != 1 && term != 3 {
		return fmt.Errorf("default term must be 1 or 3 years, got: %d", term)
	}
	return nil
}

// validatePaymentOption validates that the payment option is in the union of
// all provider sets. Used by GlobalConfig.Validate where no provider context
// is available; any token valid for at least one provider is accepted.
func validatePaymentOption(payment string) error {
	if payment != "" && !isValidPaymentOption(payment) {
		return fmt.Errorf("invalid payment option: %s (valid: %s)", payment, strings.Join(validPaymentOptionsUnion, ", "))
	}
	return nil
}

// validateCoverage validates that coverage is within acceptable range
func validateCoverage(coverage float64) error {
	if coverage < MinCoverage || coverage > MaxCoverage {
		return fmt.Errorf("default coverage must be between %d and %d, got: %.2f", MinCoverage, MaxCoverage, coverage)
	}
	return nil
}

// Validate validates the ServiceConfig
func (c *ServiceConfig) Validate() error {
	if err := c.validateProvider(); err != nil {
		return err
	}
	if err := c.validateService(); err != nil {
		return err
	}
	if err := c.validateTerm(); err != nil {
		return err
	}
	if err := c.validatePayment(); err != nil {
		return err
	}
	return c.validateConfigCoverage()
}

func (c *ServiceConfig) validateProvider() error {
	if c.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if !isValidProvider(c.Provider) {
		return fmt.Errorf("invalid provider: %s (valid: %s)", c.Provider, strings.Join(ValidProviders, ", "))
	}
	return nil
}

func (c *ServiceConfig) validateService() error {
	if c.Service == "" {
		return fmt.Errorf("service is required")
	}
	return nil
}

func (c *ServiceConfig) validateTerm() error {
	if c.Term != 0 && c.Term != 1 && c.Term != 3 {
		return fmt.Errorf("term must be 1 or 3 years, got: %d", c.Term)
	}
	return nil
}

func (c *ServiceConfig) validatePayment() error {
	if c.Payment == "" {
		return nil
	}
	// Provider-canonical validation: each provider accepts only its own token
	// set. A cross-provider token (e.g. "all-upfront" on an Azure service) is
	// rejected even though that token is valid somewhere else.
	opts := validPaymentOptionsFor(c.Provider)
	if opts == nil {
		// Provider was already validated; unknown provider here is a bug.
		return fmt.Errorf("internal error: no payment options defined for provider %q", c.Provider)
	}
	for _, v := range opts {
		if c.Payment == v {
			return nil
		}
	}
	return fmt.Errorf("invalid payment option: %s (valid for %s: %s)", c.Payment, c.Provider, strings.Join(opts, ", "))
}

func (c *ServiceConfig) validateConfigCoverage() error {
	if c.Coverage < MinCoverage || c.Coverage > MaxCoverage {
		return fmt.Errorf("coverage must be between %d and %d, got: %.2f", MinCoverage, MaxCoverage, c.Coverage)
	}
	return nil
}

// Validate validates the PurchasePlan
func (p *PurchasePlan) Validate() error {
	// Name is required
	if p.Name == "" {
		return fmt.Errorf("plan name is required")
	}
	if len(p.Name) > MaxPlanNameLength {
		return fmt.Errorf("plan name is too long (max %d characters)", MaxPlanNameLength)
	}

	// Validate notification days
	if p.NotificationDaysBefore < 0 || p.NotificationDaysBefore > MaxNotificationDaysBefore {
		return fmt.Errorf("notification days must be between 0 and %d, got: %d", MaxNotificationDaysBefore, p.NotificationDaysBefore)
	}

	// Validate ramp schedule
	if err := p.RampSchedule.Validate(); err != nil {
		return fmt.Errorf("invalid ramp schedule: %w", err)
	}

	// Enabled plans must have at least one service; disabled/draft plans may not yet
	if p.Enabled && len(p.Services) == 0 {
		return fmt.Errorf("plan must have at least one service")
	}
	for key, svc := range p.Services {
		if err := svc.Validate(); err != nil {
			return fmt.Errorf("invalid service config '%s': %w", key, err)
		}
	}

	return nil
}

// Validate validates the RampSchedule
func (r *RampSchedule) Validate() error {
	if r.Type != "" && !isValidRampScheduleType(r.Type) {
		return fmt.Errorf("invalid ramp schedule type: %s (valid: %s)", r.Type, strings.Join(ValidRampScheduleTypes, ", "))
	}
	if err := r.validatePercentPerStep(); err != nil {
		return err
	}
	if r.StepIntervalDays < 0 || r.StepIntervalDays > MaxStepIntervalDays {
		return fmt.Errorf("step interval must be between 0 and %d days, got: %d", MaxStepIntervalDays, r.StepIntervalDays)
	}
	if r.CurrentStep < 0 {
		return fmt.Errorf("current step cannot be negative")
	}
	if r.TotalSteps < 0 || r.TotalSteps > MaxTotalSteps {
		return fmt.Errorf("total steps must be between 0 and %d, got: %d", MaxTotalSteps, r.TotalSteps)
	}
	return nil
}

// validatePercentPerStep checks PercentPerStep bounds based on ramp type.
// Step-based types require > 0; "immediate" ignores it; unset type allows 0.
func (r *RampSchedule) validatePercentPerStep() error {
	switch r.Type {
	case "immediate":
		// Immediate mode purchases all at once — PercentPerStep is irrelevant.
		return nil
	case "":
		if r.PercentPerStep < MinCoverage || r.PercentPerStep > MaxCoverage {
			return fmt.Errorf("percent per step must be between %d and %d, got: %.2f", MinCoverage, MaxCoverage, r.PercentPerStep)
		}
	default:
		if r.PercentPerStep <= MinCoverage || r.PercentPerStep > MaxCoverage {
			return fmt.Errorf("percent per step must be between %d and %d for ramp schedule, got: %.2f", MinCoverage+1, MaxCoverage, r.PercentPerStep)
		}
	}
	return nil
}

// Helper functions

func isValidProvider(p string) bool {
	for _, valid := range ValidProviders {
		if p == valid {
			return true
		}
	}
	return false
}

// isValidPaymentOption reports whether p is a valid payment option for any
// provider. It checks against the union of all provider payment option sets.
func isValidPaymentOption(p string) bool {
	for _, valid := range validPaymentOptionsUnion {
		if p == valid {
			return true
		}
	}
	return false
}

func isValidCollectionSchedule(s string) bool {
	for _, valid := range ValidCollectionSchedules {
		if s == valid {
			return true
		}
	}
	return false
}

func isValidRampScheduleType(t string) bool {
	for _, valid := range ValidRampScheduleTypes {
		if t == valid {
			return true
		}
	}
	return false
}
