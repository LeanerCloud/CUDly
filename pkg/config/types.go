// Package config provides 4-layer configuration loading for the CUDly CLI:
// defaults → YAML file → environment variables → CLI flags.
package config

import (
	"time"

	"github.com/LeanerCloud/CUDly/pkg/scorer"
)

// Config is the fully resolved CLI configuration (all layers merged).
type Config struct {
	Azure             AzureConfig
	AuditLog          string
	ConfigFile        string
	AWS               AWSConfig
	GCP               GCPConfig
	Server            ServerConfig
	EnabledClouds     []string
	Scorer            scorer.Config
	IdempotencyWindow time.Duration
	DryRun            bool
	AutoApprove       bool
}

// AWSConfig holds AWS-specific settings.
type AWSConfig struct {
	Profile string // AWS CLI profile name (maps to existing --profile flag)
}

// AzureConfig holds Azure-specific settings.
type AzureConfig struct {
	SubscriptionID string // Required when Azure is enabled
	Scope          string // "shared" or "single"; default: "shared"
}

// GCPConfig holds GCP-specific settings.
type GCPConfig struct {
	Projects []string // Explicit project list. Empty = auto-discover via OrgID.
	OrgID    string   // GCP org ID for auto-discovery
	Regions  []string // Empty = all regions
}

// ServerConfig holds HTTP API server settings.
type ServerConfig struct {
	Listen    string
	APIKeyEnv string
	Enabled   bool
}

// yamlConfig mirrors Config for YAML unmarshalling (snake_case keys).
type yamlConfig struct {
	Azure             yamlAzure  `yaml:"azure"`
	AuditLog          string     `yaml:"audit_log"`
	IdempotencyWindow string     `yaml:"idempotency_window"`
	AWS               yamlAWS    `yaml:"aws"`
	GCP               yamlGCP    `yaml:"gcp"`
	Server            yamlServer `yaml:"server"`
	EnabledClouds     []string   `yaml:"enabled_clouds"`
	Scorer            yamlScorer `yaml:"scorer"`
	DryRun            bool       `yaml:"dry_run"`
	AutoApprove       bool       `yaml:"auto_approve"`
}

type yamlScorer struct {
	EnabledServices    []string `yaml:"enabled_services"`
	MinSavingsPct      float64  `yaml:"min_savings_pct"`
	MaxBreakEvenMonths int      `yaml:"max_break_even_months"`
	MinCount           int      `yaml:"min_count"`
}

type yamlAWS struct {
	Profile string `yaml:"profile"`
}

type yamlAzure struct {
	SubscriptionID string `yaml:"subscription_id"`
	Scope          string `yaml:"scope"`
}

type yamlGCP struct {
	Projects []string `yaml:"projects"`
	OrgID    string   `yaml:"org_id"`
	Regions  []string `yaml:"regions"`
}

type yamlServer struct {
	Listen    string `yaml:"listen"`
	APIKeyEnv string `yaml:"api_key_env"`
	Enabled   bool   `yaml:"enabled"`
}
