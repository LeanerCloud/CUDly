// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"encoding/base64"
	"fmt"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/aws/aws-lambda-go/events"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// Security constants.
const (
	// MaxRequestBodySize is the maximum allowed request body size (1MB).
	MaxRequestBodySize = 1 * 1024 * 1024
)

// Input validation helpers

// uuidRegex validates UUID format (used for path parameters).
var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// gcpClientEmailRegex matches a GCP service-account email.
// Pattern: <name>@<project>.iam.gserviceaccount.com
// The service-account name component may contain alphanumerics, dots, hyphens,
// and underscores. The project ID component may contain alphanumerics, dots, and
// hyphens. Rejecting any value that does not match prevents URL path injection in
// BuildGCPFederatedCredential (issue #405).
var gcpClientEmailRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+@[a-zA-Z0-9.-]+\.iam\.gserviceaccount\.com$`)

// awsRoleARNRegex matches a valid IAM role ARN across commercial, China, and GovCloud
// partitions. The role-name portion is restricted to the IAM-permitted character
// set ([A-Za-z0-9+=,.@_-]) for both the optional path segments and the final
// name. Limiting the trailing portion prevents whitespace, newlines, or other
// control characters from sneaking past validation and reaching
// stscreds.NewAssumeRoleProvider (issue #413).
var awsRoleARNRegex = regexp.MustCompile(`^arn:(aws|aws-cn|aws-us-gov):iam::[0-9]{12}:role/(?:[A-Za-z0-9+=,.@_-]+/)*[A-Za-z0-9+=,.@_-]+$`)

// awsWebIdentityTokenFilePrefixes lists the only path prefixes accepted for
// aws_web_identity_token_file. Restricting to known EKS/Kubernetes mount points
// prevents arbitrary host-file reads when the credential resolver reads the file
// and sends its content to AWS STS (issue #403).
var awsWebIdentityTokenFilePrefixes = []string{
	"/var/run/secrets/eks.amazonaws.com/serviceaccount/",
	"/var/run/secrets/kubernetes.io/serviceaccount/",
}

// validProviders are the allowed provider values.
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

// regionNameRegex validates AWS/Azure/GCP region names - requires at least one character.
var regionNameRegex = regexp.MustCompile(`^[a-z0-9-]+$`)

// validateGCPClientEmail returns a 400 error when gcp_client_email is non-empty
// but does not match the GCP service-account email format. An empty value is
// allowed (field is optional when auth mode does not require impersonation).
func validateGCPClientEmail(email string) error {
	if email == "" {
		return nil
	}
	if !gcpClientEmailRegex.MatchString(email) {
		return NewClientError(400, "gcp_client_email must be a valid GCP service-account email "+
			"(format: name@project.iam.gserviceaccount.com)")
	}
	return nil
}

// validateAWSRoleARN returns a 400 error when aws_role_arn is non-empty but
// does not match the IAM role ARN format for any AWS partition. An empty value
// is allowed (self-account onboarding uses an empty role ARN to mean "ambient
// credentials").
func validateAWSRoleARN(arn string) error {
	if arn == "" {
		return nil
	}
	if !awsRoleARNRegex.MatchString(arn) {
		return NewClientError(400, "aws_role_arn must be a valid IAM role ARN "+
			"(format: arn:aws:iam::<12-digit-account-id>:role/<name>)")
	}
	return nil
}

// validateAWSWebIdentityTokenFile returns a 400 error when the path is non-empty
// but does not start with one of the allowed prefixes, or contains path traversal
// sequences. An empty value is allowed (the resolver falls back to the
// AWS_WEB_IDENTITY_TOKEN_FILE environment variable).
func validateAWSWebIdentityTokenFile(path string) error {
	if path == "" {
		return nil
	}
	// Block path traversal regardless of prefix match.
	if strings.Contains(path, "..") {
		return NewClientError(400, "aws_web_identity_token_file must not contain path traversal sequences")
	}
	for _, prefix := range awsWebIdentityTokenFilePrefixes {
		if strings.HasPrefix(path, prefix) {
			return nil
		}
	}
	return NewClientError(400, "aws_web_identity_token_file must start with an allowed prefix "+
		"(/var/run/secrets/eks.amazonaws.com/serviceaccount/ or "+
		"/var/run/secrets/kubernetes.io/serviceaccount/)")
}

// validateProvider checks if a provider value is valid.
func validateProvider(provider string) error {
	if !validProviders[provider] {
		return NewClientError(400, "invalid provider: must be aws, azure, gcp, or all")
	}
	return nil
}

// validateEmailFormat returns a 400 error when email is non-empty but does not
// parse as an RFC 5322 address or lacks a TLD (e.g. name@hostonly). Empty
// strings are accepted (contact email is optional on cloud accounts).
func validateEmailFormat(email string) error {
	if email == "" {
		return nil
	}
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return NewClientError(400, "invalid email format")
	}
	// mail.ParseAddress is RFC 5322-compliant and accepts single-label domains
	// like "name@host" that have no TLD. Reject them here so the profile-update
	// path applies the same constraint as sign-up. The address portion always
	// contains exactly one "@" after a successful parse.
	at := strings.LastIndex(addr.Address, "@")
	if at < 0 || !strings.Contains(addr.Address[at+1:], ".") {
		return NewClientError(400, "invalid email format")
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

// payloadTypeMatches returns true when payload["type"] is a string equal to
// want. Returns false when the field is absent, wrong type, or wrong value.
// Extracted to avoid a `||` operator in validateCredentialPayload that would
// push the function over the cyclomatic complexity gate.
func payloadTypeMatches(payload map[string]interface{}, want string) bool {
	t, ok := payload["type"].(string)
	return ok && t == want
}

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
		if !payloadTypeMatches(payload, "service_account") {
			return NewClientError(400, "gcp_service_account payload must have type=\"service_account\"")
		}
		return nil
	case "gcp_workload_identity_config":
		if err := validateGCPWIFPayload(payload); err != nil {
			return err
		}
		if !payloadTypeMatches(payload, "external_account") {
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
	max := current //nolint:gocritic // builtinShadow: local var name; does not mask builtin in use
	for _, v := range m {
		if nested, ok := v.(map[string]interface{}); ok {
			if d := payloadDepth(nested, current+1); d > max {
				max = d
			}
		}
	}
	return max
}

// validateServiceName checks if a service name is valid.
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

// validateRegion checks if a region name is valid.
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

// validateServicePath checks for path traversal attacks in service paths.
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

// validateUUID checks if a string is a valid UUID.
func validateUUID(id string) error {
	if !uuidRegex.MatchString(id) {
		return NewClientError(400, "invalid ID format: must be a valid UUID")
	}
	return nil
}

// validUUIDPtrOrNil returns p when *p is a valid UUID, or nil otherwise.
// Used to convert reviewer/creator strings (which may be "admin-api-key"
// for API-key sessions — not a real UUID) to a nullable FK-safe actor pointer
// before stamping transitioned_by on state-transition rows.
func validUUIDPtrOrNil(p *string) *string {
	if p == nil || validateUUID(*p) != nil {
		return nil
	}
	return p
}

// validateContentType checks if the Content-Type header is acceptable for the request.
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

// validateRequestBodySize checks if the request body is within allowed limits.
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

// purchasePaymentSet returns the provider-canonical set of accepted payment
// option tokens for the given (already-lowercased) provider. It derives the
// set from config.ValidPaymentOptionsByProvider so that this boundary and the
// plan-validator share a single source of truth and cannot drift (issue #717).
// Returns nil when provider is unknown.
func purchasePaymentSet(provider string) map[string]bool {
	opts, ok := config.ValidPaymentOptionsByProvider[provider]
	if !ok {
		return nil
	}
	set := make(map[string]bool, len(opts))
	for _, o := range opts {
		set[o] = true
	}
	return set
}

// purchaseTermWhitelist maps a concrete provider to the set of commitment
// terms (in years) that provider accepts. All three clouds offer 1- and
// 3-year reservations. A rec carrying e.g. Term:7 is rejected before
// execution rather than failing opaquely deep in the provider call.
var purchaseTermWhitelist = map[string]map[int]bool{
	"aws":   {1: true, 3: true},
	"azure": {1: true, 3: true},
	"gcp":   {1: true, 3: true},
}

// validatePurchaseRecommendation validates a single client-supplied
// recommendation before it reaches the cloud purchase SDK. Unlike the
// query-time validateProvider (which permits ""/"all"), the execute path
// requires a concrete provider because each rec triggers a real provider
// call. idx is the rec's position in the request slice, surfaced in the
// error so the caller can point at the offending row. Closes issues #643,
// #717.
//
// Payment-option handling follows the same two-step approach as the
// plan-validator in internal/config/validation.go:
//  1. NormalizePaymentOption coerces any legacy AWS-style token onto the
//     provider-canonical spelling (e.g. "all-upfront" on Azure -> "upfront").
//  2. The provider-canonical set (from config.ValidPaymentOptionsByProvider)
//     rejects anything that has no canonical mapping, with an error that
//     names the provider and lists the accepted tokens.
func validatePurchaseRecommendation(rec *config.RecommendationRecord, idx int) error {
	provider := strings.ToLower(strings.TrimSpace(rec.Provider))
	payments := purchasePaymentSet(provider)
	if payments == nil {
		return NewClientError(400, fmt.Sprintf("recommendation %d has invalid provider %q: must be one of aws, azure, gcp", idx, rec.Provider))
	}
	rec.Provider = provider
	rec.Service = strings.TrimSpace(rec.Service)
	if rec.Service == "" {
		return NewClientError(400, fmt.Sprintf("recommendation %d is missing a service", idx))
	}
	if rec.Count <= 0 {
		return NewClientError(400, fmt.Sprintf("recommendation %d has non-positive count: %d", idx, rec.Count))
	}
	if !purchaseTermWhitelist[provider][rec.Term] {
		return NewClientError(400, fmt.Sprintf("recommendation %d has invalid term %d for provider %s: must be 1 or 3", idx, rec.Term, provider))
	}
	payment := strings.ToLower(strings.TrimSpace(rec.Payment))
	// Coerce any legacy/cross-provider alias before the whitelist check so
	// that callers using old AWS-style tokens are transparently redirected to
	// the canonical token for the target provider.
	if normalized, ok := config.NormalizePaymentOption(provider, payment); ok {
		payment = normalized
	}
	if !payments[payment] {
		return NewClientError(400, fmt.Sprintf(
			"invalid payment option for %s service: %q (valid for %s: %s)",
			provider, rec.Payment, provider,
			strings.Join(config.ValidPaymentOptionsByProvider[provider], ", "),
		))
	}
	rec.Payment = payment
	return nil
}

// validateCapacityConsistency cross-checks the client-supplied capacity_percent
// against the scaled rec counts so the audit record can't claim a capacity that
// disagrees with what was actually purchased (#647). The frontend scales each
// rec as floor(RecommendedCount * pct / 100); this recomputes that and rejects
// any rec where the scaled Count doesn't match. Recs that don't carry a
// RecommendedCount (0 / absent: legacy callers, single-rec full-capacity
// purchases, retry replays) are skipped — the field is opt-in, so its absence
// means "no claim to verify" rather than a failure. capacityPercent is the
// already-defaulted/bounded value (1..100) from validateExecutePurchaseRequest.
func validateCapacityConsistency(recs []config.RecommendationRecord, capacityPercent int) error {
	for i := range recs {
		rec := recs[i]
		if rec.RecommendedCount <= 0 {
			continue
		}
		expected := rec.RecommendedCount * capacityPercent / 100
		if expected != rec.Count {
			return NewClientError(400, fmt.Sprintf(
				"recommendation %d: count %d is inconsistent with capacity_percent %d%% of recommended_count %d (expected %d)",
				i, rec.Count, capacityPercent, rec.RecommendedCount, expected))
		}
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
		return "", NewClientError(400, "invalid password encoding")
	}
	return string(decoded), nil
}

// parseMinSavingsParam parses a numeric savings-floor query parameter.
// Returns (0, nil) when the parameter is absent or empty (no floor).
// Returns 400 when the value is present but not a valid non-negative
// integer or float. Fractional values are allowed (e.g. "12.5") since
// savings floors can be sub-dollar amounts.
//
// paramName is included in the error message so callers can distinguish
// min_savings_usd vs min_savings_pct errors in client logs.
func parseMinSavingsParam(raw, paramName string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, NewClientError(400, fmt.Sprintf("%s must be a non-negative number", paramName))
	}
	if v < 0 {
		return 0, NewClientError(400, fmt.Sprintf("%s must be non-negative", paramName))
	}
	return v, nil
}
