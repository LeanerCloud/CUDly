// Package config provides 4-layer configuration loading for the CUDly CLI:
// defaults → YAML file → environment variables → CLI flags.
package config

import (
	"time"

	"github.com/LeanerCloud/CUDly/pkg/scorer"
)

// Config is the fully resolved CLI configuration (all layers merged).
type Config struct {
	DryRun            bool          // default: true — no purchases without explicit opt-out
	AutoApprove       bool          // default: false
	AuditLog          string        // default: "./cudly-audit.jsonl"
	EnabledClouds     []string      // default: ["aws","azure","gcp"]
	IdempotencyWindow time.Duration // default: 24h
	ConfigFile        string
	Scorer            scorer.Config
	AWS               AWSConfig
	Azure             AzureConfig
	GCP               GCPConfig
	Server            ServerConfig
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
	Enabled   bool   // default: false
	Listen    string // default: ":8080"
	APIKeyEnv string // env var name holding the API key; default: "CUDLY_API_KEY"
}

// yamlConfig mirrors Config for YAML unmarshalling (snake_case keys).
type yamlConfig struct {
	DryRun            bool       `yaml:"dry_run"`
	AutoApprove       bool       `yaml:"auto_approve"`
	AuditLog          string     `yaml:"audit_log"`
	EnabledClouds     []string   `yaml:"enabled_clouds"`
	IdempotencyWindow string     `yaml:"idempotency_window"` // parsed as duration string
	Scorer            yamlScorer `yaml:"scorer"`
	AWS               yamlAWS    `yaml:"aws"`
	Azure             yamlAzure  `yaml:"azure"`
	GCP               yamlGCP    `yaml:"gcp"`
	Server            yamlServer `yaml:"server"`
}

type yamlScorer struct {
	MinSavingsPct      float64  `yaml:"min_savings_pct"`
	MaxBreakEvenMonths int      `yaml:"max_break_even_months"`
	MinCount           int      `yaml:"min_count"`
	EnabledServices    []string `yaml:"enabled_services"`
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
	Enabled   bool   `yaml:"enabled"`
	Listen    string `yaml:"listen"`
	APIKeyEnv string `yaml:"api_key_env"`
}
