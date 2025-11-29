module github.com/LeanerCloud/CUDly/pkg

go 1.22

toolchain go1.24.4

// This module contains cloud-agnostic types and provider interfaces
// No cloud-specific dependencies should be added here

require github.com/stretchr/testify v1.11.1

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
