package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/scorer"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

var validClouds = map[string]struct{}{
	"aws":   {},
	"azure": {},
	"gcp":   {},
}

// defaults returns a Config with all default values applied.
func defaults() Config {
	return Config{
		DryRun:            true,
		AutoApprove:       false,
		AuditLog:          "./cudly-audit.jsonl",
		EnabledClouds:     []string{"aws", "azure", "gcp"},
		IdempotencyWindow: 24 * time.Hour,
		Server: ServerConfig{
			Enabled:   false,
			Listen:    ":8080",
			APIKeyEnv: "CUDLY_API_KEY",
		},
		Azure: AzureConfig{Scope: "shared"},
	}
}

// Load returns a Config resolved from 4 layers:
//  1. defaults
//  2. YAML file (path arg, CUDLY_CONFIG env, or ./cudly.yaml)
//  3. environment variables
//  4. CLI flags (only explicitly-set flags via flags.Changed)
//
// An explicit path that does not exist is an error. A missing default ./cudly.yaml is not.
func Load(path string, flags *pflag.FlagSet) (Config, error) {
	cfg := defaults()

	// --- Layer 2: YAML file ---
	filePath, explicit, err := resolveFilePath(path)
	if err != nil {
		return Config{}, err
	}
	if filePath != "" {
		if err := applyYAML(&cfg, filePath, explicit); err != nil {
			return Config{}, err
		}
	}

	// --- Layer 3: Environment variables ---
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}

	// --- Layer 4: CLI flags ---
	if err := applyFlags(&cfg, flags); err != nil {
		return Config{}, err
	}

	// --- Validation ---
	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// resolveFilePath determines which config file to load.
// Returns (path, explicit, error). explicit=true means missing file is an error.
func resolveFilePath(argPath string) (string, bool, error) {
	if argPath != "" {
		return argPath, true, nil
	}
	if env := os.Getenv("CUDLY_CONFIG"); env != "" {
		return env, true, nil
	}
	return "./cudly.yaml", false, nil
}

// applyYAML reads the YAML file at path and merges it into cfg.
// If explicit is false and the file doesn't exist, it is silently ignored.
func applyYAML(cfg *Config, path string, explicit bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			return nil
		}
		return fmt.Errorf("config file not found: %s", path)
	}

	var yc yamlConfig
	if err := yaml.Unmarshal(data, &yc); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}

	if err := applyYAMLBase(cfg, yc); err != nil {
		return err
	}
	applyYAMLScorer(cfg, yc)
	applyYAMLCloud(cfg, yc)
	applyYAMLServer(cfg, yc)
	return nil
}

// applyYAMLBase merges top-level YAML fields into cfg.
func applyYAMLBase(cfg *Config, yc yamlConfig) error {
	if yc.DryRun != cfg.DryRun {
		cfg.DryRun = yc.DryRun
	}
	if yc.AutoApprove {
		cfg.AutoApprove = yc.AutoApprove
	}
	if yc.AuditLog != "" {
		cfg.AuditLog = yc.AuditLog
	}
	if len(yc.EnabledClouds) > 0 {
		cfg.EnabledClouds = yc.EnabledClouds
	}
	if yc.IdempotencyWindow != "" {
		d, err := time.ParseDuration(yc.IdempotencyWindow)
		if err != nil {
			return fmt.Errorf("idempotency_window: %w", err)
		}
		cfg.IdempotencyWindow = d
	}
	return nil
}

// applyYAMLScorer merges scorer YAML fields into cfg.
func applyYAMLScorer(cfg *Config, yc yamlConfig) {
	if yc.Scorer.MinSavingsPct != 0 {
		cfg.Scorer.MinSavingsPct = yc.Scorer.MinSavingsPct
	}
	if yc.Scorer.MaxBreakEvenMonths != 0 {
		cfg.Scorer.MaxBreakEvenMonths = yc.Scorer.MaxBreakEvenMonths
	}
	if yc.Scorer.MinCount != 0 {
		cfg.Scorer.MinCount = yc.Scorer.MinCount
	}
	if len(yc.Scorer.EnabledServices) > 0 {
		cfg.Scorer.EnabledServices = yc.Scorer.EnabledServices
	}
}

// applyYAMLCloud merges cloud-specific YAML fields into cfg.
func applyYAMLCloud(cfg *Config, yc yamlConfig) {
	if yc.AWS.Profile != "" {
		cfg.AWS.Profile = yc.AWS.Profile
	}
	if yc.Azure.SubscriptionID != "" {
		cfg.Azure.SubscriptionID = yc.Azure.SubscriptionID
	}
	if yc.Azure.Scope != "" {
		cfg.Azure.Scope = yc.Azure.Scope
	}
	if len(yc.GCP.Projects) > 0 {
		cfg.GCP.Projects = yc.GCP.Projects
	}
	if yc.GCP.OrgID != "" {
		cfg.GCP.OrgID = yc.GCP.OrgID
	}
	if len(yc.GCP.Regions) > 0 {
		cfg.GCP.Regions = yc.GCP.Regions
	}
}

// applyYAMLServer merges server YAML fields into cfg.
func applyYAMLServer(cfg *Config, yc yamlConfig) {
	if yc.Server.Enabled {
		cfg.Server.Enabled = yc.Server.Enabled
	}
	if yc.Server.Listen != "" {
		cfg.Server.Listen = yc.Server.Listen
	}
	if yc.Server.APIKeyEnv != "" {
		cfg.Server.APIKeyEnv = yc.Server.APIKeyEnv
	}
}

// applyEnv reads CUDLY_* environment variables and merges them into cfg.
func applyEnv(cfg *Config) error {
	if err := applyEnvCore(cfg); err != nil {
		return err
	}
	if err := applyEnvDurations(cfg); err != nil {
		return err
	}
	if err := applyEnvScorer(cfg); err != nil {
		return err
	}
	applyEnvCloud(cfg)
	return nil
}

// applyEnvCore merges core CUDLY_* env vars (bools + strings) into cfg.
func applyEnvCore(cfg *Config) error {
	if v := os.Getenv("CUDLY_DRY_RUN"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CUDLY_DRY_RUN: %w", err)
		}
		cfg.DryRun = b
	}
	if v := os.Getenv("CUDLY_AUTO_APPROVE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CUDLY_AUTO_APPROVE: %w", err)
		}
		cfg.AutoApprove = b
	}
	if v := os.Getenv("CUDLY_AUDIT_LOG"); v != "" {
		cfg.AuditLog = v
	}
	if v := os.Getenv("CUDLY_CLOUDS"); v != "" {
		cfg.EnabledClouds = splitComma(v)
	}
	return nil
}

// applyEnvDurations merges duration CUDLY_* env vars into cfg.
func applyEnvDurations(cfg *Config) error {
	if v := os.Getenv("CUDLY_IDEMPOTENCY_WINDOW"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("CUDLY_IDEMPOTENCY_WINDOW: %w", err)
		}
		cfg.IdempotencyWindow = d
	}
	return nil
}

// applyEnvScorer merges scorer CUDLY_* env vars into cfg.
func applyEnvScorer(cfg *Config) error {
	if v := os.Getenv("CUDLY_MIN_SAVINGS_PCT"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("CUDLY_MIN_SAVINGS_PCT: invalid value %q: %w", v, err)
		}
		cfg.Scorer.MinSavingsPct = f
	}
	if v := os.Getenv("CUDLY_MAX_BREAK_EVEN_MONTHS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CUDLY_MAX_BREAK_EVEN_MONTHS: %w", err)
		}
		cfg.Scorer.MaxBreakEvenMonths = n
	}
	if v := os.Getenv("CUDLY_MIN_COUNT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CUDLY_MIN_COUNT: %w", err)
		}
		cfg.Scorer.MinCount = n
	}
	return nil
}

// applyEnvCloud merges cloud-specific CUDLY_* env vars into cfg.
func applyEnvCloud(cfg *Config) {
	if v := os.Getenv("CUDLY_AZURE_SUBSCRIPTION_ID"); v != "" {
		cfg.Azure.SubscriptionID = v
	}
	if v := os.Getenv("CUDLY_GCP_ORG_ID"); v != "" {
		cfg.GCP.OrgID = v
	}
	if v := os.Getenv("CUDLY_GCP_PROJECTS"); v != "" {
		cfg.GCP.Projects = splitComma(v)
	}
}

// applyFlags reads explicitly-set CLI flags and merges them into cfg.
func applyFlags(cfg *Config, flags *pflag.FlagSet) error {
	if flags == nil {
		return nil
	}
	if err := applyFlagsDryRun(cfg, flags); err != nil {
		return err
	}
	if err := applyFlagsScorer(cfg, flags); err != nil {
		return err
	}
	return applyFlagsOther(cfg, flags)
}

// applyFlagsDryRun handles the --dry-run / --purchase conflict and sets DryRun.
func applyFlagsDryRun(cfg *Config, flags *pflag.FlagSet) error {
	dryRunChanged := flags.Changed("dry-run")
	purchaseChanged := flags.Changed("purchase")

	if dryRunChanged && purchaseChanged {
		return fmt.Errorf("cannot specify both --dry-run and --purchase simultaneously")
	}
	if dryRunChanged {
		v, err := flags.GetBool("dry-run")
		if err != nil {
			return fmt.Errorf("--dry-run: %w", err)
		}
		cfg.DryRun = v
	}
	if purchaseChanged {
		cfg.DryRun = false
	}
	return nil
}

// applyFlagsScorer merges scorer-related CLI flags into cfg.
func applyFlagsScorer(cfg *Config, flags *pflag.FlagSet) error {
	if flags.Changed("min-savings-pct") {
		v, err := flags.GetFloat64("min-savings-pct")
		if err != nil {
			return fmt.Errorf("--min-savings-pct: %w", err)
		}
		cfg.Scorer.MinSavingsPct = v
	}
	if flags.Changed("max-break-even-months") {
		v, err := flags.GetInt("max-break-even-months")
		if err != nil {
			return fmt.Errorf("--max-break-even-months: %w", err)
		}
		cfg.Scorer.MaxBreakEvenMonths = v
	}
	if flags.Changed("min-count") {
		v, err := flags.GetInt("min-count")
		if err != nil {
			return fmt.Errorf("--min-count: %w", err)
		}
		cfg.Scorer.MinCount = v
	}
	return nil
}

// applyFlagsOther merges remaining CLI flags into cfg.
func applyFlagsOther(cfg *Config, flags *pflag.FlagSet) error {
	if flags.Changed("yes") {
		v, _ := flags.GetBool("yes")
		cfg.AutoApprove = v
	}
	if flags.Changed("audit-log") {
		v, _ := flags.GetString("audit-log")
		cfg.AuditLog = v
	}
	if flags.Changed("profile") {
		v, _ := flags.GetString("profile")
		cfg.AWS.Profile = v
	}
	if flags.Changed("idempotency-window") {
		v, _ := flags.GetString("idempotency-window")
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("--idempotency-window: %w", err)
		}
		cfg.IdempotencyWindow = d
	}
	return nil
}

// validate checks the resolved config for semantic errors.
func validate(cfg Config) error {
	for _, cloud := range cfg.EnabledClouds {
		if _, ok := validClouds[strings.ToLower(cloud)]; !ok {
			return fmt.Errorf("unknown cloud provider: %s", cloud)
		}
	}
	if len(cfg.EnabledClouds) == 0 {
		return fmt.Errorf("at least one cloud must be enabled")
	}
	if cfg.Scorer.MinSavingsPct < 0 {
		return fmt.Errorf("min_savings_pct must be ≥ 0")
	}
	return nil
}

// splitComma splits a comma-separated string and trims whitespace from each element.
func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
}

// DefaultScorerConfig returns a scorer.Config that can be embedded in a config.Config.
// Exposed so callers can construct a default Config without importing the scorer package directly.
func DefaultScorerConfig() scorer.Config {
	return scorer.Config{}
}
