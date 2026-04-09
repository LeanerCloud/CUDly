package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFlags builds a pflag.FlagSet that mirrors the CLI flags relevant to Load().
func newFlags() *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.Bool("dry-run", true, "")
	fs.Bool("purchase", false, "")
	fs.Bool("yes", false, "")
	fs.String("audit-log", "./cudly-audit.jsonl", "")
	fs.String("profile", "", "")
	fs.Float64("min-savings-pct", 0, "")
	fs.Int("max-break-even-months", 0, "")
	fs.Int("min-count", 0, "")
	fs.String("idempotency-window", "24h", "")
	return fs
}

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "cudly-*.yaml")
	require.NoError(t, err)
	_, err = fmt.Fprint(f, content)
	require.NoError(t, err)
	f.Close()
	return f.Name()
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load("", newFlags())
	require.NoError(t, err)
	assert.True(t, cfg.DryRun)
	assert.False(t, cfg.AutoApprove)
	assert.Equal(t, "./cudly-audit.jsonl", cfg.AuditLog)
	assert.Equal(t, []string{"aws", "azure", "gcp"}, cfg.EnabledClouds)
	assert.Equal(t, 24*time.Hour, cfg.IdempotencyWindow)
	assert.False(t, cfg.Server.Enabled)
	assert.Equal(t, ":8080", cfg.Server.Listen)
	assert.Equal(t, "CUDLY_API_KEY", cfg.Server.APIKeyEnv)
}

func TestLoad_MissingDefaultFile_NoError(t *testing.T) {
	// No ./cudly.yaml in temp dir; should silently use defaults
	dir := t.TempDir()
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { os.Chdir(orig) })

	cfg, err := Load("", newFlags())
	require.NoError(t, err)
	assert.True(t, cfg.DryRun)
}

func TestLoad_ExplicitMissingFile_Error(t *testing.T) {
	_, err := Load("/nonexistent/path/cudly.yaml", newFlags())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config file not found")
}

func TestLoad_YAMLFile(t *testing.T) {
	path := writeYAML(t, `
scorer:
  min_savings_pct: 15
azure:
  subscription_id: "sub-123"
dry_run: false
`)
	cfg, err := Load(path, newFlags())
	require.NoError(t, err)
	assert.Equal(t, 15.0, cfg.Scorer.MinSavingsPct)
	assert.Equal(t, "sub-123", cfg.Azure.SubscriptionID)
	assert.False(t, cfg.DryRun)
}

func TestLoad_InvalidYAML_Error(t *testing.T) {
	path := writeYAML(t, "dry_run: [not a bool")
	_, err := Load(path, newFlags())
	require.Error(t, err)
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	path := writeYAML(t, "scorer:\n  min_savings_pct: 10\n")
	t.Setenv("CUDLY_MIN_SAVINGS_PCT", "20")
	cfg, err := Load(path, newFlags())
	require.NoError(t, err)
	assert.Equal(t, 20.0, cfg.Scorer.MinSavingsPct)
}

func TestLoad_EnvVars(t *testing.T) {
	t.Setenv("CUDLY_DRY_RUN", "false")
	t.Setenv("CUDLY_AUTO_APPROVE", "true")
	t.Setenv("CUDLY_AUDIT_LOG", "/tmp/my.jsonl")
	t.Setenv("CUDLY_CLOUDS", "aws,gcp")
	t.Setenv("CUDLY_MIN_SAVINGS_PCT", "5.5")
	t.Setenv("CUDLY_MAX_BREAK_EVEN_MONTHS", "18")
	t.Setenv("CUDLY_MIN_COUNT", "3")
	t.Setenv("CUDLY_AZURE_SUBSCRIPTION_ID", "az-sub")
	t.Setenv("CUDLY_GCP_ORG_ID", "org-123")
	t.Setenv("CUDLY_GCP_PROJECTS", "proj-a,proj-b")
	t.Setenv("CUDLY_IDEMPOTENCY_WINDOW", "48h")

	cfg, err := Load("", newFlags())
	require.NoError(t, err)
	assert.False(t, cfg.DryRun)
	assert.True(t, cfg.AutoApprove)
	assert.Equal(t, "/tmp/my.jsonl", cfg.AuditLog)
	assert.Equal(t, []string{"aws", "gcp"}, cfg.EnabledClouds)
	assert.Equal(t, 5.5, cfg.Scorer.MinSavingsPct)
	assert.Equal(t, 18, cfg.Scorer.MaxBreakEvenMonths)
	assert.Equal(t, 3, cfg.Scorer.MinCount)
	assert.Equal(t, "az-sub", cfg.Azure.SubscriptionID)
	assert.Equal(t, "org-123", cfg.GCP.OrgID)
	assert.Equal(t, []string{"proj-a", "proj-b"}, cfg.GCP.Projects)
	assert.Equal(t, 48*time.Hour, cfg.IdempotencyWindow)
}

func TestLoad_InvalidEnvVar_Error(t *testing.T) {
	t.Setenv("CUDLY_MIN_SAVINGS_PCT", "abc")
	_, err := Load("", newFlags())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CUDLY_MIN_SAVINGS_PCT")
}

func TestLoad_InvalidDurationEnvVar_Error(t *testing.T) {
	t.Setenv("CUDLY_IDEMPOTENCY_WINDOW", "notaduration")
	_, err := Load("", newFlags())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CUDLY_IDEMPOTENCY_WINDOW")
}

func TestLoad_FlagOverridesEnvAndYAML(t *testing.T) {
	path := writeYAML(t, "scorer:\n  min_savings_pct: 10\n")
	t.Setenv("CUDLY_MIN_SAVINGS_PCT", "20")
	fs := newFlags()
	require.NoError(t, fs.Parse([]string{"--min-savings-pct", "0"}))
	cfg, err := Load(path, fs)
	require.NoError(t, err)
	assert.Equal(t, 0.0, cfg.Scorer.MinSavingsPct)
}

func TestLoad_DryRunAndPurchaseConflict(t *testing.T) {
	fs := newFlags()
	require.NoError(t, fs.Parse([]string{"--dry-run=true", "--purchase"}))
	_, err := Load("", fs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--dry-run")
	assert.Contains(t, err.Error(), "--purchase")
}

func TestLoad_PurchaseFlagSetsDryRunFalse(t *testing.T) {
	fs := newFlags()
	require.NoError(t, fs.Parse([]string{"--purchase"}))
	cfg, err := Load("", fs)
	require.NoError(t, err)
	assert.False(t, cfg.DryRun)
}

func TestLoad_UnknownCloud_Error(t *testing.T) {
	path := writeYAML(t, "enabled_clouds:\n  - oracle\n")
	_, err := Load(path, newFlags())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown cloud provider: oracle")
}

func TestLoad_NegativeThreshold_Error(t *testing.T) {
	path := writeYAML(t, "scorer:\n  min_savings_pct: -5\n")
	_, err := Load(path, newFlags())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "min_savings_pct must be ≥ 0")
}

func TestValidate_EmptyEnabledClouds_Error(t *testing.T) {
	// validate() is called by Load; test it directly since empty clouds
	// cannot be produced by env/YAML (empty overrides are ignored to protect defaults)
	err := validate(Config{EnabledClouds: []string{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one cloud")
}

func TestLoad_CUDLYCONFIGEnv(t *testing.T) {
	path := writeYAML(t, "scorer:\n  min_savings_pct: 7\n")
	t.Setenv("CUDLY_CONFIG", path)
	cfg, err := Load("", newFlags())
	require.NoError(t, err)
	assert.Equal(t, 7.0, cfg.Scorer.MinSavingsPct)
}

func TestLoad_ArgPathTakesPrecedenceOverCUDLYCONFIG(t *testing.T) {
	path1 := writeYAML(t, "scorer:\n  min_savings_pct: 5\n")
	path2 := writeYAML(t, "scorer:\n  min_savings_pct: 99\n")
	t.Setenv("CUDLY_CONFIG", path2)
	cfg, err := Load(path1, newFlags())
	require.NoError(t, err)
	assert.Equal(t, 5.0, cfg.Scorer.MinSavingsPct)
}

func TestLoad_YAMLRoundtrip(t *testing.T) {
	content := `dry_run: false
auto_approve: true
audit_log: /tmp/audit.jsonl
enabled_clouds:
  - aws
  - gcp
scorer:
  min_savings_pct: 10
  max_break_even_months: 24
  min_count: 2
aws:
  profile: my-profile
azure:
  subscription_id: sub-xyz
  scope: single
gcp:
  org_id: org-1
  projects:
    - proj-a
  regions:
    - us-central1
`
	path := filepath.Join(t.TempDir(), "cudly.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	cfg, err := Load(path, newFlags())
	require.NoError(t, err)
	assert.False(t, cfg.DryRun)
	assert.True(t, cfg.AutoApprove)
	assert.Equal(t, "/tmp/audit.jsonl", cfg.AuditLog)
	assert.Equal(t, []string{"aws", "gcp"}, cfg.EnabledClouds)
	assert.Equal(t, 10.0, cfg.Scorer.MinSavingsPct)
	assert.Equal(t, 24, cfg.Scorer.MaxBreakEvenMonths)
	assert.Equal(t, 2, cfg.Scorer.MinCount)
	assert.Equal(t, "my-profile", cfg.AWS.Profile)
	assert.Equal(t, "sub-xyz", cfg.Azure.SubscriptionID)
	assert.Equal(t, "single", cfg.Azure.Scope)
	assert.Equal(t, "org-1", cfg.GCP.OrgID)
	assert.Equal(t, []string{"proj-a"}, cfg.GCP.Projects)
	assert.Equal(t, []string{"us-central1"}, cfg.GCP.Regions)
}
