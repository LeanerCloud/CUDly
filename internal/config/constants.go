// Package config provides configuration management functionality.
package config

import "time"

// Default configuration values
const (
	// DefaultListLimit is the default number of items returned in list operations
	DefaultListLimit = 100

	// DefaultExecutionTTLDays is how long execution records are kept
	DefaultExecutionTTLDays = 30

	// DefaultMaxRecommendationsInEmail is the max recommendations shown in email notifications
	DefaultMaxRecommendationsInEmail = 10

	// DefaultPasswordResetExpiry is how long password reset tokens are valid
	DefaultPasswordResetExpiry = 1 * time.Hour
)

// Validation constants
const (
	// MaxCoverage is the maximum allowed coverage percentage
	MaxCoverage = 100

	// MinCoverage is the minimum allowed coverage percentage
	MinCoverage = 0

	// MaxPlanNameLength is the maximum length for plan names
	MaxPlanNameLength = 100

	// MaxNotificationDaysBefore is the maximum days before purchase to send notification
	MaxNotificationDaysBefore = 30

	// MaxStepIntervalDays is the maximum interval between ramp steps
	MaxStepIntervalDays = 365

	// MaxTotalSteps is the maximum number of ramp steps
	MaxTotalSteps = 100
)

// Default values for new configurations
const (
	// DefaultCoveragePercent is the default coverage percentage for new configs
	DefaultCoveragePercent = 80

	// DefaultNotifyDaysBefore is the default days before purchase to send notification
	DefaultNotifyDaysBefore = 7
)

// Ramp schedule presets
const (
	// RampImmediate means all at once
	RampImmediate = "immediate"

	// RampWeekly25Pct means 25% per week for 4 weeks
	RampWeekly25Pct = "weekly-25pct"

	// RampMonthly10Pct means 10% per month for 10 months
	RampMonthly10Pct = "monthly-10pct"

	// Weekly step interval in days
	WeeklyStepIntervalDays = 7

	// Monthly step interval in days
	MonthlyStepIntervalDays = 30
)

// Time constants
const (
	// HoursPerDay is the number of hours in a day
	HoursPerDay = 24

	// MinHoursBetweenNotifications is the minimum hours between notification emails
	MinHoursBetweenNotifications = 24
)

// Token constants
const (
	// TokenByteLength is the length of generated tokens in bytes
	TokenByteLength = 32

	// MFATimeStep is the TOTP time step in seconds
	MFATimeStep = 30

	// MFADigits is the number of digits in MFA codes
	MFADigits = 6
)
