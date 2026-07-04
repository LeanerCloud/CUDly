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
func (h *Handler) getConfig(ctx context.Context, req *events.LambdaFunctionURLRequest) (*ConfigResponse, error) {
	// Require view:config permission. Every other read handler in the package
	// pairs the route-level AuthUser gate with this explicit permission check;
	// config GET must be consistent (02-M4).
	if _, err := h.requirePermission(ctx, req, "view", "config"); err != nil {
		return nil, err
	}

	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, err
	}

	services, err := h.config.ListServiceConfigs(ctx)
	if err != nil {
		return nil, err
	}

	resp := &ConfigResponse{
		Global:      globalCfg,
		Services:    services,
		SourceCloud: sourceCloud(),
	}

	// SourceIdentity contains the host cloud account ID (AWS account number,
	// Azure tenant ID, etc.). Only expose it to admin sessions so that
	// non-admin users cannot extract the cloud identity of the CUDly host
	// account (issue #407).
	if _, adminErr := h.requireAdmin(ctx, req); adminErr == nil {
		resp.SourceIdentity = h.resolveSourceIdentity(ctx)
	}

	return resp, nil
}

func (h *Handler) updateConfig(ctx context.Context, req *events.LambdaFunctionURLRequest) (*StatusResponse, error) {
	// Require update:config permission
	if _, err := h.requirePermission(ctx, req, "update", "config"); err != nil {
		return nil, err
	}

	// Reject a malformed body before any DB work so a bad request fails fast
	// (and never opens a transaction). Keeps the nil-store fast-fail contract.
	if !json.Valid([]byte(req.Body)) {
		return nil, NewClientError(400, "invalid request body")
	}

	// Serialized read-modify-write: the store loads the stored config and
	// applies this closure under an advisory-locked transaction, then upserts
	// the result in the same tx. json.Unmarshal only assigns keys present in the
	// body, so any field ABSENT is preserved (present -> updated, absent ->
	// preserved) for EVERY field, with no per-field enumeration. Doing the read
	// and write in one locked tx prevents two concurrent partial PUTs (e.g. the
	// laddering kill-switch toggle and a Settings save) from reading the same
	// stale base and losing each other's update. The closure returns a
	// ClientError(400) on a bad body or validation failure, which the store
	// propagates unchanged; DB/transport errors surface as 500.
	cfg, err := h.config.UpdateGlobalConfigAtomic(ctx, func(existing *config.GlobalConfig) error {
		if uErr := json.Unmarshal([]byte(req.Body), existing); uErr != nil {
			return NewClientError(400, "invalid request body")
		}
		if vErr := existing.Validate(); vErr != nil {
			return NewClientError(400, fmt.Sprintf("validation error: %s", vErr))
		}
		return nil
	})
	if err != nil {
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

// checkCommitmentOptionCombo rejects saves that carry a (term, payment)
// combination we've dynamically confirmed the cloud doesn't sell. Returns
// nil when: no probe service is wired, the service hasn't persisted data
// yet (absent data → fall through to the frontend's hardcoded rules),
// the save isn't AWS, or the combo is valid. Errors from Validate are
// logged and swallowed (permissive) so a transient DB blip never blocks
// a settings save.
func (h *Handler) checkCommitmentOptionCombo(ctx context.Context, cfg config.ServiceConfig) error {
	if h.commitmentOpts == nil || cfg.Provider != "aws" || cfg.Term <= 0 || cfg.Payment == "" {
		return nil
	}
	ok, err := h.commitmentOpts.Validate(ctx, cfg.Provider, cfg.Service, cfg.Term, cfg.Payment)
	if err != nil {
		logging.Warnf("commitment-option validation error (allowing save): %v", err)
		return nil
	}
	if !ok {
		return NewClientError(400, fmt.Sprintf(
			"%s does not support %dyr %s commitments",
			cfg.Service, cfg.Term, cfg.Payment,
		))
	}
	return nil
}

// serviceConfigFilterKeys are the recommendation-filter JSON fields the
// Settings UI now edits. They are overlaid onto the existing row only when the
// request body actually contains the key, so a partial PUT from a non-UI
// client (or an older UI build) never zeroes a filter the operator set
// elsewhere. RampSchedule is intentionally absent: it is still set out-of-band
// and the UI does not own it.
var serviceConfigFilterKeys = []string{
	"include_engines", "exclude_engines",
	"include_regions", "exclude_regions",
	"include_types", "exclude_types",
	"min_count",
}

// mergeServiceConfig loads any existing service config and overlays the
// UI-editable fields from cfg onto it. The four always-present scalar fields
// (Enabled, Term, Payment, Coverage) are overlaid unconditionally; the
// recommendation-filter fields (include/exclude engines/regions/types,
// min_count) are overlaid only when the request body actually carried the
// corresponding key (per presentKeys), so a partial PUT cannot silently wipe a
// filter set out-of-band. body is the raw request JSON used to detect which
// filter keys are present.
//
// A "not found" error means no existing record — cfg is returned unchanged.
// Any other DB error is returned to prevent a partial write from clobbering
// previously configured filter fields.
func mergeServiceConfig(ctx context.Context, store config.StoreInterface, cfg config.ServiceConfig, body string) (config.ServiceConfig, error) {
	existing, err := store.GetServiceConfig(ctx, cfg.Provider, cfg.Service)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return cfg, nil // new record — no existing fields to preserve
		}
		return cfg, fmt.Errorf("failed to read existing service config before update: %w", err)
	}
	if existing == nil {
		return cfg, nil
	}

	existing.Enabled = cfg.Enabled
	existing.Term = cfg.Term
	existing.Payment = cfg.Payment
	existing.Coverage = cfg.Coverage

	present, perr := presentKeys(body, serviceConfigFilterKeys)
	if perr != nil {
		return cfg, perr
	}
	overlayPresentFilterFields(existing, &cfg, present)
	return *existing, nil
}

// presentKeys returns the subset of keys that appear as top-level fields in the
// JSON body. A malformed body is reported as a client error so the caller
// returns 400 rather than silently overlaying nothing.
func presentKeys(body string, keys []string) (map[string]bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	present := make(map[string]bool, len(keys))
	for _, k := range keys {
		if _, ok := raw[k]; ok {
			present[k] = true
		}
	}
	return present, nil
}

// overlayPresentFilterFields copies each filter field from src onto dst only
// when its JSON key was present in the request body. Split out of
// mergeServiceConfig to keep that function under the cyclomatic-complexity
// gate.
func overlayPresentFilterFields(dst, src *config.ServiceConfig, present map[string]bool) {
	if present["include_engines"] {
		dst.IncludeEngines = src.IncludeEngines
	}
	if present["exclude_engines"] {
		dst.ExcludeEngines = src.ExcludeEngines
	}
	if present["include_regions"] {
		dst.IncludeRegions = src.IncludeRegions
	}
	if present["exclude_regions"] {
		dst.ExcludeRegions = src.ExcludeRegions
	}
	if present["include_types"] {
		dst.IncludeTypes = src.IncludeTypes
	}
	if present["exclude_types"] {
		dst.ExcludeTypes = src.ExcludeTypes
	}
	if present["min_count"] {
		dst.MinCount = src.MinCount
	}
}

func (h *Handler) getServiceConfig(ctx context.Context, req *events.LambdaFunctionURLRequest, service string) (any, error) {
	// Require view:config permission. Consistent with getConfig (02-M4).
	if _, err := h.requirePermission(ctx, req, "view", "config"); err != nil {
		return nil, err
	}

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
	// Require update:config permission
	if _, err := h.requirePermission(ctx, req, "update", "config"); err != nil {
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

	// Merge: overlay the scalar UI fields unconditionally and the
	// recommendation-filter fields only when the body carried them, so a
	// partial PUT never zeroes a filter (ramp_schedule, or a filter set
	// out-of-band) the request didn't mean to touch.
	merged, mergeErr := mergeServiceConfig(ctx, h.config, cfg, req.Body)
	if mergeErr != nil {
		return nil, mergeErr
	}
	cfg = merged

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return nil, NewClientError(400, fmt.Sprintf("validation error: %s", err))
	}

	if err := h.checkCommitmentOptionCombo(ctx, cfg); err != nil {
		return nil, err
	}

	if err := h.config.SaveServiceConfig(ctx, &cfg); err != nil {
		return nil, err
	}

	return &StatusResponse{Status: "updated"}, nil
}
