// Package iacfiles embeds federation IaC templates into the binary.
// Templates are rendered by handler_federation.go with per-account substitutions.
package iacfiles

import "embed"

// Templates holds all federation IaC template files.
//
//go:embed templates
var Templates embed.FS
