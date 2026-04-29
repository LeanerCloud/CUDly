// Package scheduledauth provides authentication for the /api/scheduled/*
// endpoints invoked by Cloud Scheduler / Logic Apps. Two modes are
// supported:
//
//   - oidc:   Google-issued OIDC ID tokens (RS256, JWKS-validated). Used
//     by GCP Cloud Run and GKE deployments where Cloud Scheduler signs an
//     OIDC token with a service-account audience. Subject pinning to the
//     scheduler service account email(s) is REQUIRED — the audience alone
//     is not sufficient if other tenants in the same GCP org could mint
//     tokens with the same `aud` value.
//
//   - bearer: shared-secret HMAC-style bearer (constant-time compare).
//     Used by Azure Container Apps + Logic Apps where the workflow
//     pulls the secret from Key Vault at invocation time.
//
// See plan: ~/.claude/projects/.../plans/issue-161-oidc-validator.md
package scheduledauth

import "errors"

// ErrConfigInvalid signals a fatal configuration parse error. Callers are
// expected to fail-fast on this — the deploy is misconfigured and the
// scheduled endpoint would silently accept or reject everything.
var ErrConfigInvalid = errors.New("scheduledauth: invalid configuration")

// ErrUnauthorized is the validation failure returned by Validate. The
// detailed reason is wrapped via fmt.Errorf so it gets logged but not
// returned to the client (which only sees 401).
var ErrUnauthorized = errors.New("scheduledauth: unauthorized")
