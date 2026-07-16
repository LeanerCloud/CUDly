// Package config provides configuration management functionality.
package config

import "time"

// Default configuration values.
const (
	// DefaultListLimit is the default number of items returned in list operations.
	DefaultListLimit = 100

	// MaxListLimit is the maximum number of items allowed in a single list request.
	MaxListLimit = 1000

	// DefaultExecutionTTLDays is how long execution records are kept.
	DefaultExecutionTTLDays = 30

	// DefaultMaxRecommendationsInEmail is the max recommendations shown in email notifications.
	DefaultMaxRecommendationsInEmail = 10

	// DefaultPasswordResetExpiry is how long password reset tokens are valid.
	DefaultPasswordResetExpiry = 1 * time.Hour
)

// Validation constants.
const (
	// MaxCoverage is the maximum allowed coverage percentage.
	MaxCoverage = 100

	// MinCoverage is the minimum allowed coverage percentage.
	MinCoverage = 0

	// MaxPlanNameLength is the maximum length for plan names.
	MaxPlanNameLength = 100

	// MaxNotificationDaysBefore is the maximum days before purchase to send notification.
	MaxNotificationDaysBefore = 30

	// MaxStepIntervalDays is the maximum interval between ramp steps.
	MaxStepIntervalDays = 365

	// MaxTotalSteps is the maximum number of ramp steps.
	MaxTotalSteps = 100

	// MaxServiceMinCount caps the per-service min-count recommendation
	// filter. Mirrors the CLI's MaxReasonableInstances ceiling so a typo
	// (e.g. a stray trailing zero) can't silently suppress every
	// recommendation. 0 disables the filter; values above this are rejected
	// at validation time.
	MaxServiceMinCount = 10000
)

// Default values for new configurations.
const (
	// DefaultCoveragePercent is the default coverage percentage for new configs.
	DefaultCoveragePercent = 80

	// DefaultNotifyDaysBefore is the default days before purchase to send notification.
	DefaultNotifyDaysBefore = 7
)

// Ramp schedule presets.
const (
	// RampImmediate means all at once.
	RampImmediate = "immediate"

	// RampWeekly25Pct means 25% per week for 4 weeks.
	RampWeekly25Pct = "weekly-25pct" // #nosec G101 -- schedule constant; gosec misidentifies "pct" suffix as a credential pattern

	// RampMonthly10Pct means 10% per month for 10 months.
	RampMonthly10Pct = "monthly-10pct"

	// Weekly step interval in days.
	WeeklyStepIntervalDays = 7

	// Monthly step interval in days.
	MonthlyStepIntervalDays = 30
)

// Time constants.
const (
	// HoursPerDay is the number of hours in a day.
	HoursPerDay = 24

	// MinHoursBetweenNotifications is the minimum hours between notification emails.
	MinHoursBetweenNotifications = 24
)

// Token constants.
const (
	// TokenByteLength is the length of generated tokens in bytes.
	TokenByteLength = 32

	// MFATimeStep is the TOTP time step in seconds.
	MFATimeStep = 30

	// MFADigits is the number of digits in MFA codes.
	MFADigits = 6
)

// ApprovalTokenTTL is the lifetime of a purchase approval token (issue #397).
// Tokens older than this are rejected by ApproveExecution and
// loadCancelableExecution. 7 days gives approvers a full business week to act
// without the window being infinite. Mirror the RI exchange model which uses a
// 6-hour TTL; purchase approvals are higher-stakes so a longer window is
// appropriate but must still be bounded.
const ApprovalTokenTTL = 7 * 24 * time.Hour

// RevocationWindow is the time window after a purchase completes during which
// the buyer may request revocation (issue #291). 24 hours matches the AWS RI/SP
// support-case window advertised in the post-execution email. It is the single
// source of truth for both the fresh revocation token's expiry (minted in
// purchase.mintRevocationToken) and the enforcement check in api.validateRevokeToken:
// the two MUST stay equal or a token could expire before the window closes and
// silently block a valid revoke.
const RevocationWindow = 24 * time.Hour
