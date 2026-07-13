// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"time"
)

// RateLimitConfig defines the rate limiting parameters for a specific endpoint/operation.
type RateLimitConfig struct {
	MaxAttempts int           // Maximum number of attempts allowed
	WindowSecs  int           // Time window in seconds
	Window      time.Duration // Computed time window (for convenience)
}

// NewRateLimitConfig creates a new RateLimitConfig.
func NewRateLimitConfig(maxAttempts, windowSecs int) RateLimitConfig {
	return RateLimitConfig{
		MaxAttempts: maxAttempts,
		WindowSecs:  windowSecs,
		Window:      time.Duration(windowSecs) * time.Second,
	}
}

// getDefaultRateLimits returns default rate limit configurations.
func getDefaultRateLimits() map[string]RateLimitConfig {
	return map[string]RateLimitConfig{
		"login":                 NewRateLimitConfig(5, 15*60),  // 5 attempts / 15 minutes / IP
		"forgot_password":       NewRateLimitConfig(10, 5*60),  // 10 attempts / 5 minutes / email
		"reset_password":        NewRateLimitConfig(10, 15*60), // 10 attempts / 15 minutes / IP
		"change_password":       NewRateLimitConfig(10, 15*60), // 10 attempts / 15 minutes / IP
		"api_general":           NewRateLimitConfig(300, 60),   // 300 requests / minute / IP
		"admin":                 NewRateLimitConfig(30, 60),    // 30 / minute / user
		"approve_cancel_public": NewRateLimitConfig(30, 60),    // 30 / minute / IP for public approve/cancel/reject
		// setup_admin should fire at most once per deployment. Issue #411 specifies
		// 5 attempts per 15 minutes so a cold-start burst cannot brute-force the
		// first-run endpoint before the DB-backed limiter is available.
		"setup_admin": NewRateLimitConfig(5, 15*60), // 5 attempts / 15 minutes / IP
		// register is a public, side-effect-heavy endpoint (DB write + email fan-out
		// per request). Issue #1016: without a dedicated bucket it fell through to
		// api_general (300/min), enabling bulk table-flooding and inbox-spam. Limit
		// to 5 per 15 minutes per IP, matching setup_admin severity.
		"register": NewRateLimitConfig(5, 15*60), // 5 attempts / 15 minutes / IP (#1016)
	}
}

// assertRateLimitKeysKnown panics at startup if any endpoint key passed to
// checkRateLimit is absent from the default rate-limit map. A missing key
// silently falls through to api_general (300/min), which is the root cause of
// issue #1016. Call this once during handler initialisation, passing every
// string literal that appears in checkRateLimit call sites.
func assertRateLimitKeysKnown(keys ...string) {
	limits := getDefaultRateLimits()
	for _, k := range keys {
		if _, ok := limits[k]; !ok {
			panic("checkRateLimit called with unknown endpoint key " + k +
				": add it to getDefaultRateLimits() (issue #1016)")
		}
	}
}
