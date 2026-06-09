package iac

import "embed"

// Modules embeds the federation IaC module files for use by the CUDly backend
// when building zip bundles. Hidden files and directories (e.g., .terraform/)
// are excluded automatically by go:embed.
//
//go:embed federation
var Modules embed.FS
