// Package config provides configuration management using PostgreSQL.
package config

import (
	"fmt"
	"net/mail"
	"strings"
)

// ValidProviders lists all supported cloud providers
var ValidProviders = []string{"aws", "azure", "gcp"}

// ValidPaymentOptions lists all supported payment options
var ValidPaymentOptions = []string{"no-upfront", "partial-upfront", "all-upfront"}

// ValidRampScheduleTypes lists all supported ramp schedule types
var ValidRampScheduleTypes = []string{"immediate", "weekly", "monthly", "custom"}

// Validate validates the GlobalConfig
func (c *GlobalConfig) Validate() error {
	if err := c.validateProviders(); err != nil {
		return err
	}
	if err := c.validateNotificationEmail(); err != nil {
		return err
	}
	if err := validateTerm(c.DefaultTerm); err != nil {
		return err
	}
	if err := validatePaymentOption(c.DefaultPayment); err != nil {
		return err
	}
	return validateCoverage(c.DefaultCoverage)
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

// validateTerm validates that the term is 1 or 3 years (or 0 for not set)
func validateTerm(term int) error {
	if term != 0 && term != 1 && term != 3 {
		return fmt.Errorf("default term must be 1 or 3 years, got: %d", term)
	}
	return nil
}

// validatePaymentOption validates that the payment option is valid if set
func validatePaymentOption(payment string) error {
	if payment != "" && !isValidPaymentOption(payment) {
		return fmt.Errorf("invalid payment option: %s (valid: %s)", payment, strings.Join(ValidPaymentOptions, ", "))
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
	if c.Payment != "" && !isValidPaymentOption(c.Payment) {
		return fmt.Errorf("invalid payment option: %s (valid: %s)", c.Payment, strings.Join(ValidPaymentOptions, ", "))
	}
	return nil
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

	// Validate each service config
	if len(p.Services) == 0 {
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
	// Type is required if any ramp settings are provided
	if r.Type != "" && !isValidRampScheduleType(r.Type) {
		return fmt.Errorf("invalid ramp schedule type: %s (valid: %s)", r.Type, strings.Join(ValidRampScheduleTypes, ", "))
	}

	// Validate percent per step
	if r.Type != "" {
		if r.PercentPerStep <= MinCoverage || r.PercentPerStep > MaxCoverage {
			return fmt.Errorf("percent per step must be between %d and %d for ramp schedule, got: %.2f", MinCoverage+1, MaxCoverage, r.PercentPerStep)
		}
	} else {
		if r.PercentPerStep < MinCoverage || r.PercentPerStep > MaxCoverage {
			return fmt.Errorf("percent per step must be between %d and %d, got: %.2f", MinCoverage, MaxCoverage, r.PercentPerStep)
		}
	}

	// Validate step interval
	if r.StepIntervalDays < 0 || r.StepIntervalDays > MaxStepIntervalDays {
		return fmt.Errorf("step interval must be between 0 and %d days, got: %d", MaxStepIntervalDays, r.StepIntervalDays)
	}

	// Validate current step
	if r.CurrentStep < 0 {
		return fmt.Errorf("current step cannot be negative")
	}

	// Validate total steps
	if r.TotalSteps < 0 || r.TotalSteps > MaxTotalSteps {
		return fmt.Errorf("total steps must be between 0 and %d, got: %d", MaxTotalSteps, r.TotalSteps)
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

func isValidPaymentOption(p string) bool {
	for _, valid := range ValidPaymentOptions {
		if p == valid {
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
