package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
)

// ==========================================
// LADDER CONFIG HANDLERS
// ==========================================

// getLadderConfigs returns all per-account ladder configurations.
// Requires view:config permission.
func (h *Handler) getLadderConfigs(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "config"); err != nil {
		return nil, err
	}

	configs, err := h.config.GetLadderConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list ladder configs: %w", err)
	}

	return map[string]any{"configs": configs}, nil
}

// upsertLadderConfig inserts or updates the per-account ladder configuration
// for the given (cloud_account_id, provider) pair extracted from the request
// body. Requires update:config permission.
//
// Auth dispatch mirrors handler_ri_exchange.go (session RBAC only; no token
// path exists for config writes). requirePermission denies the request when
// the auth component is nil (returns an error, never a session), so the
// handler fails closed; the exact status is a 500-class error in that case
// rather than a 403, but no unauthenticated write ever reaches the store.
func (h *Handler) upsertLadderConfig(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "update", "config"); err != nil {
		return nil, err
	}

	var cfg config.LadderConfigDB
	if err := json.Unmarshal([]byte(req.Body), &cfg); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	if cfg.CloudAccountID == "" {
		return nil, NewClientError(400, "cloud_account_id is required")
	}
	if cfg.Provider == "" {
		return nil, NewClientError(400, "provider is required")
	}

	// Presence map to distinguish an OMITTED field from an explicitly-supplied
	// value, so defaults apply only to genuinely-absent keys (see
	// applyLadderConfigNumericDefaults).
	var present map[string]json.RawMessage
	if err := json.Unmarshal([]byte(req.Body), &present); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	applyLadderConfigNumericDefaults(&cfg, present)

	if err := cfg.Validate(); err != nil {
		return nil, NewClientError(400, fmt.Sprintf("validation error: %s", err))
	}

	result, err := h.config.UpsertLadderConfig(ctx, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert ladder config: %w", err)
	}

	return result, nil
}

// applyLadderConfigNumericDefaults fills a numeric field with its default ONLY
// when the field's key is genuinely absent from the request body (tracked via
// present). When a key IS supplied, the caller's value is left untouched so it
// flows to Validate(), which rejects any out-of-range value -- including an
// explicit 0 on the five fields whose valid range excludes 0 -- with a 400. A
// bare `== 0` check would instead silently rewrite an explicit out-of-range 0
// to the default, the forbidden silent-fallback pattern on a money-adjacent
// config path (feedback_no_silent_fallbacks). buffer_fraction's valid range is
// [0, 1), so an explicit 0 there is a legitimate "no buffer" choice and is
// likewise passed through untouched.
func applyLadderConfigNumericDefaults(cfg *config.LadderConfigDB, present map[string]json.RawMessage) {
	if _, ok := present["target_coverage"]; !ok {
		cfg.TargetCoverage = config.DefaultLadderTargetCoverage
	}
	if _, ok := present["buffer_fraction"]; !ok {
		cfg.BufferFraction = config.DefaultLadderBufferFraction
	}
	if _, ok := present["baseline_percentile"]; !ok {
		cfg.BaselinePercentile = config.DefaultLadderBaselinePercentile
	}
	if _, ok := present["lookback_days"]; !ok {
		cfg.LookbackDays = config.DefaultLadderLookbackDays
	}
	if _, ok := present["buffer_utilization_threshold"]; !ok {
		cfg.BufferUtilizationThreshold = config.DefaultLadderBufferUtilThreshold
	}
	if _, ok := present["max_actions_per_run"]; !ok {
		cfg.MaxActionsPerRun = config.DefaultLadderMaxActionsPerRun
	}
}
