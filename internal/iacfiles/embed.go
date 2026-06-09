// Package iacfiles embeds federation IaC templates into the binary.
// Templates are rendered by handler_federation.go with per-account substitutions.
//
// Security note: the templates use text/template, which performs no shell
// escaping. Fields such as ContactEmail, AccountName, CUDlyAPIURL, and
// AccountSlug originate partly from self-service registration payloads (user-
// controlled) and are interpolated directly into generated bash scripts. The
// renderer (internal/api/handler_federation.go) is responsible for validating
// and/or quoting these fields before calling template.Execute so that a value
// containing shell metacharacters (e.g. "; curl evil;") cannot inject commands
// into the script shown to the operator.
package iacfiles

import "embed"

// Templates holds all federation IaC template files.
//
//go:embed templates
var Templates embed.FS
