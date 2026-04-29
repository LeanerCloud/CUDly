package scheduledauth

import (
	"fmt"
	"strings"
)

// EnvSource is the minimal interface NewFromEnv depends on. The default
// implementation (envOS) reads from os.Getenv; tests inject a map.
type EnvSource interface {
	Get(string) string
}

// EnvMap is an EnvSource backed by a map. Useful in tests.
type EnvMap map[string]string

// Get returns the value for key, or "" if absent.
func (m EnvMap) Get(key string) string { return m[key] }

// Environment variable names. Centralised so tests and Terraform stay
// aligned with the Go code.
const (
	EnvAuthMode     = "SCHEDULED_TASK_AUTH_MODE"     // "oidc" | "bearer" | "disabled"
	EnvOIDCIssuer   = "SCHEDULED_TASK_OIDC_ISSUER"   // default GoogleIssuer
	EnvOIDCJWKSURL  = "SCHEDULED_TASK_OIDC_JWKS_URL" // default GoogleJWKSURL
	EnvOIDCAudience = "SCHEDULED_TASK_OIDC_AUDIENCE" // comma-separated
	EnvOIDCSubjects = "SCHEDULED_TASK_OIDC_SUBJECTS" // comma-separated; REQUIRED in oidc mode
	EnvBearerSecret = "SCHEDULED_TASK_SECRET"        // bearer mode only
)

// LoadConfig parses Config from env. Unset SCHEDULED_TASK_AUTH_MODE
// defaults to ModeDisabled (with a startup WARN logged inside New). Any
// other invalid combination returns an ErrConfigInvalid error so the
// server fails fast instead of silently downgrading to no-auth.
func LoadConfig(env EnvSource) (Config, error) {
	mode := strings.ToLower(strings.TrimSpace(env.Get(EnvAuthMode)))
	if mode == "" {
		mode = string(ModeDisabled)
	}

	cfg := Config{Mode: Mode(mode)}

	switch cfg.Mode {
	case ModeOIDC:
		cfg.Issuer = strings.TrimSpace(env.Get(EnvOIDCIssuer))
		cfg.JWKSURL = strings.TrimSpace(env.Get(EnvOIDCJWKSURL))
		cfg.Audiences = splitCSV(env.Get(EnvOIDCAudience))
		cfg.Subjects = splitCSV(env.Get(EnvOIDCSubjects))

	case ModeBearer:
		cfg.Bearer = env.Get(EnvBearerSecret)
		// Permit other env vars to be set without complaint — the
		// terraform module ships SCHEDULED_TASK_OIDC_* across all
		// platforms and they should be ignored on Azure.

	case ModeDisabled:
		// Nothing to parse.

	default:
		return Config{}, fmt.Errorf("%w: %s=%q (expected oidc, bearer, or disabled)",
			ErrConfigInvalid, EnvAuthMode, mode)
	}

	return cfg, nil
}

// splitCSV splits a comma-separated value into a trimmed list, dropping
// empty entries (so trailing commas / whitespace are tolerated).
func splitCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
