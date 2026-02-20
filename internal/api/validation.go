// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/aws/aws-lambda-go/events"
)

// Security constants
const (
	// MaxRequestBodySize is the maximum allowed request body size (1MB)
	MaxRequestBodySize = 1 * 1024 * 1024
)

// Input validation helpers

// uuidRegex validates UUID format (used for path parameters)
var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validProviders are the allowed provider values
var validProviders = map[string]bool{
	"":      true, // empty is allowed (means all)
	"all":   true,
	"aws":   true,
	"azure": true,
	"gcp":   true,
}

// serviceNameRegex validates service names (alphanumeric, hyphens) - requires at least one character
var serviceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

// regionNameRegex validates AWS/Azure/GCP region names - requires at least one character
var regionNameRegex = regexp.MustCompile(`^[a-z0-9-]+$`)

// validateProvider checks if a provider value is valid
func validateProvider(provider string) error {
	if !validProviders[provider] {
		return fmt.Errorf("invalid provider: must be aws, azure, gcp, or all")
	}
	return nil
}

// validateServiceName checks if a service name is valid
func validateServiceName(service string) error {
	// Empty is allowed for queries (means all services)
	if service == "" {
		return nil
	}

	// Check length limits
	if len(service) > 64 {
		return fmt.Errorf("invalid service name: must be 1-64 characters")
	}

	// Check pattern (now requires at least one character due to + quantifier)
	if !serviceNameRegex.MatchString(service) {
		return fmt.Errorf("invalid service name: must contain only alphanumeric characters and hyphens")
	}
	return nil
}

// validateRegion checks if a region name is valid
func validateRegion(region string) error {
	// Empty is allowed for queries (means all regions)
	if region == "" {
		return nil
	}

	// Check length limits
	if len(region) > 64 {
		return fmt.Errorf("invalid region: must be 1-64 characters")
	}

	// Check pattern (now requires at least one character due to + quantifier)
	if !regionNameRegex.MatchString(region) {
		return fmt.Errorf("invalid region: must contain only lowercase alphanumeric characters and hyphens")
	}
	return nil
}

// validateServicePath checks for path traversal attacks in service paths
func validateServicePath(service string) error {
	// Reject path traversal attempts
	if strings.Contains(service, "..") {
		return fmt.Errorf("invalid service path: path traversal not allowed")
	}
	if strings.Contains(service, "//") {
		return fmt.Errorf("invalid service path: double slashes not allowed")
	}

	// Reject any non-alphanumeric except single slash and hyphen
	for _, r := range service {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '/' && r != '-' && r != '_' {
			return fmt.Errorf("invalid service path: contains invalid characters")
		}
	}

	// Ensure format is "provider/service" only
	parts := strings.Split(service, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid service path: must be in format provider/service")
	}

	return nil
}

// validateUUID checks if a string is a valid UUID
func validateUUID(id string) error {
	if !uuidRegex.MatchString(id) {
		return fmt.Errorf("invalid ID format: must be a valid UUID")
	}
	return nil
}

// validateContentType checks if the Content-Type header is acceptable for the request
func validateContentType(req *events.LambdaFunctionURLRequest) error {
	method := req.RequestContext.HTTP.Method
	// Only POST/PUT/PATCH with bodies need content-type validation
	if method != "POST" && method != "PUT" && method != "PATCH" {
		return nil
	}

	// If there's no body, no content-type required
	if req.Body == "" {
		return nil
	}

	contentType := req.Headers["content-type"]
	if contentType == "" {
		contentType = req.Headers["Content-Type"]
	}

	// Accept application/json or form data
	if contentType == "" {
		return fmt.Errorf("Content-Type header is required for requests with a body")
	}

	// Check for valid content types (allowing charset suffixes)
	validTypes := []string{"application/json", "application/x-www-form-urlencoded"}
	for _, vt := range validTypes {
		if strings.HasPrefix(contentType, vt) {
			return nil
		}
	}

	return fmt.Errorf("unsupported Content-Type: must be application/json")
}

// validateRequestBodySize checks if the request body is within allowed limits
func validateRequestBodySize(body string) error {
	if len(body) > MaxRequestBodySize {
		return fmt.Errorf("request body too large: maximum size is %d bytes", MaxRequestBodySize)
	}
	return nil
}

// decodeBase64Password decodes a base64-encoded password.
// Returns the decoded password or an error if decoding fails.
// If the input is empty, returns empty string with no error.
func decodeBase64Password(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid password encoding")
	}
	return string(decoded), nil
}
