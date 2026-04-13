// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

// sourceCloud returns the cloud where CUDly is currently running, read from
// the CUDLY_SOURCE_CLOUD env var (set by Terraform). Falls back to "aws".
func sourceCloud() string {
	if v := os.Getenv("CUDLY_SOURCE_CLOUD"); v != "" {
		return v
	}
	return "aws"
}

// Configuration handlers
func (h *Handler) getConfig(ctx context.Context) (*ConfigResponse, error) {
	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, err
	}

	services, err := h.config.ListServiceConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Check credentials status
	credStatus := h.getCredentialsStatus(ctx)

	return &ConfigResponse{
		Global:      globalCfg,
		Services:    services,
		Credentials: credStatus,
		SourceCloud: sourceCloud(),
	}, nil
}

func (h *Handler) updateConfig(ctx context.Context, req *events.LambdaFunctionURLRequest) (*StatusResponse, error) {
	// Require admin access for updating configuration
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	var cfg config.GlobalConfig
	if err := json.Unmarshal([]byte(req.Body), &cfg); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return nil, NewClientError(400, fmt.Sprintf("validation error: %s", err))
	}

	if err := h.config.SaveGlobalConfig(ctx, &cfg); err != nil {
		return nil, err
	}

	// Propagate global defaults to all service configurations
	services, err := h.config.ListServiceConfigs(ctx)
	if err != nil {
		// Log but don't fail - global config was saved
		logging.Warnf("Failed to list service configs for propagation: %v", err)
	} else {
		for _, svc := range services {
			svc.Term = cfg.DefaultTerm
			svc.Payment = cfg.DefaultPayment
			svc.Coverage = cfg.DefaultCoverage
			svc.RampSchedule = cfg.DefaultRampSchedule
			if err := h.config.SaveServiceConfig(ctx, &svc); err != nil {
				logging.Warnf("Failed to update service config %s/%s: %v", svc.Provider, svc.Service, err)
			}
		}
	}

	return &StatusResponse{Status: "updated"}, nil
}

// mergeServiceConfig loads any existing service config and overlays the four
// UI-editable fields (Enabled, Term, Payment, Coverage) from cfg onto it, so
// that filter fields set outside the UI (RampSchedule, IncludeEngines, etc.)
// are not zeroed on every settings save.
//
// A "not found" error means no existing record — cfg is returned unchanged.
// Any other DB error is returned to prevent a partial write from clobbering
// previously configured filter fields.
func mergeServiceConfig(ctx context.Context, store config.StoreInterface, cfg config.ServiceConfig) (config.ServiceConfig, error) {
	existing, err := store.GetServiceConfig(ctx, cfg.Provider, cfg.Service)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return cfg, nil // new record — no existing fields to preserve
		}
		return cfg, fmt.Errorf("failed to read existing service config before update: %w", err)
	}
	if existing != nil {
		existing.Enabled = cfg.Enabled
		existing.Term = cfg.Term
		existing.Payment = cfg.Payment
		existing.Coverage = cfg.Coverage
		return *existing, nil
	}
	return cfg, nil
}

func (h *Handler) getServiceConfig(ctx context.Context, service string) (any, error) {
	// Validate for path traversal attacks
	if err := validateServicePath(service); err != nil {
		return nil, err
	}

	parts := strings.SplitN(service, "/", 2)
	if len(parts) != 2 {
		return nil, NewClientError(400, "invalid service format, expected: provider/service")
	}

	// Validate provider
	if err := validateProvider(parts[0]); err != nil {
		return nil, err
	}

	cfg, err := h.config.GetServiceConfig(ctx, parts[0], parts[1])
	if err != nil {
		return nil, err
	}

	if cfg == nil {
		return &EmptyServiceConfigResponse{}, nil
	}

	return cfg, nil
}

func (h *Handler) updateServiceConfig(ctx context.Context, req *events.LambdaFunctionURLRequest, service string) (*StatusResponse, error) {
	// Require admin access for updating service configuration
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	// Validate for path traversal attacks
	if err := validateServicePath(service); err != nil {
		return nil, err
	}

	var cfg config.ServiceConfig
	if err := json.Unmarshal([]byte(req.Body), &cfg); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	parts := strings.SplitN(service, "/", 2)
	if len(parts) == 2 {
		cfg.Provider = parts[0]
		cfg.Service = parts[1]
	}

	// Merge: preserve existing filter fields, overlay the 4 UI-editable fields.
	// The frontend only sends enabled/term/payment/coverage; a full UPSERT would
	// zero out ramp_schedule, include_engines, etc. that were set elsewhere.
	merged, mergeErr := mergeServiceConfig(ctx, h.config, cfg)
	if mergeErr != nil {
		return nil, mergeErr
	}
	cfg = merged

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return nil, NewClientError(400, fmt.Sprintf("validation error: %s", err))
	}

	if err := h.config.SaveServiceConfig(ctx, &cfg); err != nil {
		return nil, err
	}

	return &StatusResponse{Status: "updated"}, nil
}
