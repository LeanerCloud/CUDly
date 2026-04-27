// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"encoding/base64"
	"fmt"
	"net/mail"
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

// serviceNameRegex validates service override names: lowercase alphanumeric + hyphens, 1-64 chars.
// Uppercase is rejected to prevent stored-XSS via mixed-case surprises and to keep names consistent.
var serviceNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// regionNameRegex validates AWS/Azure/GCP region names - requires at least one character
var regionNameRegex = regexp.MustCompile(`^[a-z0-9-]+$`)

// validateProvider checks if a provider value is valid
func validateProvider(provider string) error {
	if !validProviders[provider] {
		return NewClientError(400, "invalid provider: must be aws, azure, gcp, or all")
	}
	return nil
}

// validateEmailFormat returns a 400 error when email is non-empty but does not
// parse as an RFC 5322 address. Empty strings are accepted (contact email is
// optional on cloud accounts).
func validateEmailFormat(email string) error {
	if email == "" {
		return nil
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return NewClientError(400, "contact_email is not a valid email address")
	}
	return nil
}

// credentialPayloadSchema defines the per-credential-type required keys and
// optional keys. Any payload key outside the union of required+optional is
// rejected. Required keys must be non-empty strings. The schemas mirror what
// the credentials resolver actually consumes — see internal/credentials/resolver.go
// (awsAccessKeyPayload, the azure_client_secret + azure WIF + GCP loaders).
type credentialPayloadSchema struct {
	required []string
	optional []string
}

var credentialPayloadSchemas = map[string]credentialPayloadSchema{
	"aws_access_keys":     {required: []string{"access_key_id", "secret_access_key"}},
	"azure_client_secret": {required: []string{"client_secret"}},
}

// gcpServiceAccountKeys are the fields a Google service-account JSON file is
// expected to carry. Subset that we strictly require, plus the rest as optional.
var gcpServiceAccountRequired = []string{"type", "project_id", "private_key", "client_email"}
var gcpServiceAccountOptional = []string{
	"private_key_id", "client_id", "auth_uri", "token_uri",
	"auth_provider_x509_cert_url", "client_x509_cert_url", "universe_domain",
}

// gcpWIFConfigRequired/Optional follow the GCP "external_account" credential
// JSON format documented at https://google.aip.dev/auth/4117.
var gcpWIFConfigRequired = []string{"type", "audience", "subject_token_type", "token_url", "credential_source"}
var gcpWIFConfigOptional = []string{
	"service_account_impersonation_url", "service_account_impersonation",
	"workforce_pool_user_project", "quota_project_id", "universe_domain",
}

// maxCredentialPayloadDepth caps the JSON nesting allowed inside a
// CredentialsRequest.Payload. Two is enough for every supported credential type
// (top-level keys → simple values, or one nested object for credential_source).
const maxCredentialPayloadDepth = 2

// validateCredentialPayload enforces shape per declared credential_type. It
// rejects payloads with missing required keys, unknown extra keys, non-string
// required values, or excessive nesting depth. Caller has already verified the
// credentialType is in validCredentialTypes.
func validateCredentialPayload(credentialType string, payload map[string]interface{}) error {
	if len(payload) == 0 {
		return NewClientError(400, "credentials payload must not be empty")
	}
	if depth := payloadDepth(payload, 1); depth > maxCredentialPayloadDepth {
		return NewClientError(400, fmt.Sprintf("credentials payload nests too deeply (max %d levels)", maxCredentialPayloadDepth))
	}

	switch credentialType {
	case "aws_access_keys", "azure_client_secret":
		schema := credentialPayloadSchemas[credentialType]
		return validateFlatPayload(credentialType, payload, schema.required, schema.optional)
	case "gcp_service_account":
		if err := validateFlatPayload(credentialType, payload, gcpServiceAccountRequired, gcpServiceAccountOptional); err != nil {
			return err
		}
		if t, _ := payload["type"].(string); t != "service_account" {
			return NewClientError(400, "gcp_service_account payload must have type=\"service_account\"")
		}
		return nil
	case "gcp_workload_identity_config":
		if err := validateGCPWIFPayload(payload); err != nil {
			return err
		}
		if t, _ := payload["type"].(string); t != "external_account" {
			return NewClientError(400, "gcp_workload_identity_config payload must have type=\"external_account\"")
		}
		return nil
	}
	// validCredentialTypes is the gate; if a new type slips past it without a
	// schema entry here, that is a programming error worth surfacing.
	return NewClientError(400, fmt.Sprintf("no payload schema defined for credential_type %q", credentialType))
}

// validateFlatPayload checks that every required key is present as a non-empty
// string and that no key falls outside required+optional.
func validateFlatPayload(credentialType string, payload map[string]interface{}, required, optional []string) error {
	allowed := buildAllowedSet(required, optional)
	if err := rejectUnknownKeys(credentialType, payload, allowed); err != nil {
		return err
	}
	for _, key := range required {
		v, ok := payload[key]
		if !ok {
			return NewClientError(400, fmt.Sprintf("credentials payload for %s missing required key %q", credentialType, key))
		}
		s, isStr := v.(string)
		if !isStr || strings.TrimSpace(s) == "" {
			return NewClientError(400, fmt.Sprintf("credentials payload key %q must be a non-empty string", key))
		}
	}
	return nil
}

// validateGCPWIFPayload checks that the WIF config payload has every required
// key, allows optional GCP fields, and does not contain unknown keys. The
// `credential_source` value may be a nested object; everything else must be a
// non-empty string.
func validateGCPWIFPayload(payload map[string]interface{}) error {
	allowed := buildAllowedSet(gcpWIFConfigRequired, gcpWIFConfigOptional)
	if err := rejectUnknownKeys("gcp_workload_identity_config", payload, allowed); err != nil {
		return err
	}
	for _, key := range gcpWIFConfigRequired {
		if err := validateGCPWIFRequiredKey(payload, key); err != nil {
			return err
		}
	}
	return nil
}

// validateGCPWIFRequiredKey checks one required GCP WIF key, treating
// "credential_source" specially as a nested object.
func validateGCPWIFRequiredKey(payload map[string]interface{}, key string) error {
	v, ok := payload[key]
	if !ok {
		return NewClientError(400, fmt.Sprintf("credentials payload for gcp_workload_identity_config missing required key %q", key))
	}
	if key == "credential_source" {
		if _, isMap := v.(map[string]interface{}); !isMap {
			return NewClientError(400, "credentials payload key \"credential_source\" must be an object")
		}
		return nil
	}
	s, isStr := v.(string)
	if !isStr || strings.TrimSpace(s) == "" {
		return NewClientError(400, fmt.Sprintf("credentials payload key %q must be a non-empty string", key))
	}
	return nil
}

// buildAllowedSet collapses required + optional key lists into a lookup set.
func buildAllowedSet(required, optional []string) map[string]bool {
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, k := range required {
		allowed[k] = true
	}
	for _, k := range optional {
		allowed[k] = true
	}
	return allowed
}

// rejectUnknownKeys returns 400 when any payload key is outside the allowed set.
func rejectUnknownKeys(credentialType string, payload map[string]interface{}, allowed map[string]bool) error {
	for key := range payload {
		if !allowed[key] {
			return NewClientError(400, fmt.Sprintf("credentials payload for %s contains unknown key %q", credentialType, key))
		}
	}
	return nil
}

// payloadDepth returns the maximum nesting depth of m, counting the top-level
// map as depth 1. A nested map adds 1; non-map values do not.
func payloadDepth(m map[string]interface{}, current int) int {
	max := current
	for _, v := range m {
		if nested, ok := v.(map[string]interface{}); ok {
			if d := payloadDepth(nested, current+1); d > max {
				max = d
			}
		}
	}
	return max
}

// validateServiceName checks if a service name is valid
func validateServiceName(service string) error {
	// Empty is allowed for queries (means all services)
	if service == "" {
		return nil
	}

	// Check length limits
	if len(service) > 64 {
		return NewClientError(400, "invalid service name: must be 1-64 characters")
	}

	// Check pattern (now requires at least one character due to + quantifier)
	if !serviceNameRegex.MatchString(service) {
		return NewClientError(400, "invalid service name: must contain only alphanumeric characters and hyphens")
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
		return NewClientError(400, "invalid region: must be 1-64 characters")
	}

	// Check pattern (now requires at least one character due to + quantifier)
	if !regionNameRegex.MatchString(region) {
		return NewClientError(400, "invalid region: must contain only lowercase alphanumeric characters and hyphens")
	}
	return nil
}

// validateServicePath checks for path traversal attacks in service paths
func validateServicePath(service string) error {
	// Reject path traversal attempts
	if strings.Contains(service, "..") {
		return NewClientError(400, "invalid service path: path traversal not allowed")
	}
	if strings.Contains(service, "//") {
		return NewClientError(400, "invalid service path: double slashes not allowed")
	}

	// Reject any non-alphanumeric except single slash and hyphen
	for _, r := range service {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '/' && r != '-' && r != '_' {
			return NewClientError(400, "invalid service path: contains invalid characters")
		}
	}

	// Ensure format is "provider/service" only
	parts := strings.Split(service, "/")
	if len(parts) != 2 {
		return NewClientError(400, "invalid service path: must be in format provider/service")
	}

	return nil
}

// validateUUID checks if a string is a valid UUID
func validateUUID(id string) error {
	if !uuidRegex.MatchString(id) {
		return NewClientError(400, "invalid ID format: must be a valid UUID")
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
		return NewClientError(400, "Content-Type header is required for requests with a body")
	}

	// Check for valid content types (allowing charset suffixes)
	validTypes := []string{"application/json", "application/x-www-form-urlencoded"}
	for _, vt := range validTypes {
		if strings.HasPrefix(contentType, vt) {
			return nil
		}
	}

	return NewClientError(400, "unsupported Content-Type: must be application/json")
}

// validateRequestBodySize checks if the request body is within allowed limits
func validateRequestBodySize(body string) error {
	if len(body) > MaxRequestBodySize {
		return NewClientError(400, fmt.Sprintf("request body too large: maximum size is %d bytes", MaxRequestBodySize))
	}
	return nil
}

// MaxAccountIDsPerRequest caps the number of account IDs accepted in a
// single comma-separated `account_ids` query parameter. Each accepted ID
// fans out into per-account DB queries / cloud API calls downstream, so
// an unbounded list is an amplification vector — a single request with
// thousands of IDs can exhaust connection pools or hit cloud-API rate
// limits. 200 is generous for legitimate usage (typical operators have
// far fewer onboarded accounts) while keeping the worst-case work bounded.
const MaxAccountIDsPerRequest = 200

// parseAccountIDs splits a comma-separated account_ids query parameter into a slice.
// Empty entries are removed. Returns nil when the input is empty.
// Returns an error if any value is not a valid UUID, or if the list
// exceeds MaxAccountIDsPerRequest entries.
func parseAccountIDs(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	// Cap the parse step itself: count non-empty entries before allocating
	// the result slice. We reject early so a megabyte-long string of empty
	// commas can't pin an Lambda CPU on Split allocations.
	nonEmpty := 0
	for _, id := range parts {
		if strings.TrimSpace(id) != "" {
			nonEmpty++
		}
	}
	if nonEmpty > MaxAccountIDsPerRequest {
		return nil, NewClientError(400, fmt.Sprintf("too many account IDs: max %d per request", MaxAccountIDsPerRequest))
	}
	ids := make([]string, 0, nonEmpty)
	for _, id := range parts {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if err := validateUUID(trimmed); err != nil {
			return nil, fmt.Errorf("invalid account_ids: %q is not a valid UUID", trimmed)
		}
		ids = append(ids, trimmed)
	}
	return ids, nil
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
		return "", NewClientError(400, "invalid password encoding")
	}
	return string(decoded), nil
}
