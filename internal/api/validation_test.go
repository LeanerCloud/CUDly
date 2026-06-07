package api

import (
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRegion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		region    string
		wantError bool
	}{
		{"empty region is valid", "", false},
		{"valid AWS region", "us-east-1", false},
		{"valid AWS region with numbers", "eu-west-2", false},
		{"valid GCP region", "us-central1", false},
		{"valid Azure region", "eastus", false},
		{"region with only letters", "useast", false},
		{"invalid region with uppercase", "US-EAST-1", true},
		{"invalid region with underscore", "us_east_1", true},
		{"invalid region with special chars", "us-east-1!", true},
		{"invalid region with spaces", "us east 1", true},
		{"region too long", strings.Repeat("a", 65), true},
		{"region at max length", strings.Repeat("a", 64), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRegion(tt.region)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		provider  string
		wantError bool
	}{
		{"empty provider is valid", "", false},
		{"aws is valid", "aws", false},
		{"azure is valid", "azure", false},
		{"gcp is valid", "gcp", false},
		{"all is valid", "all", false},
		{"invalid provider", "invalid", true},
		{"uppercase AWS is invalid", "AWS", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProvider(tt.provider)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateServiceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		serviceName string
		wantError   bool
	}{
		{"empty service is valid", "", false},
		{"valid service name", "rds", false},
		{"valid service with hyphen", "elastic-cache", false},
		{"valid service with numbers", "ec2", false},
		// Issue #22 follow-up: the four per-plan-type SP slugs all match
		// the regex. Locked in here so a future regex tightening can't
		// silently break SP saves at the API layer.
		{"valid SP slug compute", "savings-plans-compute", false},
		{"valid SP slug ec2instance", "savings-plans-ec2instance", false},
		{"valid SP slug sagemaker", "savings-plans-sagemaker", false},
		{"valid SP slug database", "savings-plans-database", false},
		{"uppercase service is invalid", "RDS", true},
		{"invalid with underscore", "elastic_cache", true},
		{"invalid with special chars", "rds!", true},
		{"invalid with spaces", "rds aurora", true},
		{"service too long", strings.Repeat("a", 65), true},
		{"service at max length", strings.Repeat("a", 64), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServiceName(tt.serviceName)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateServicePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		path      string
		wantError bool
	}{
		{"valid path", "aws/rds", false},
		{"valid path with hyphen", "aws/elastic-cache", false},
		{"valid path with underscore", "aws/rds_aurora", false},
		{"path traversal attack", "aws/../etc/passwd", true},
		{"double slash", "aws//rds", true},
		{"no slash", "awsrds", true},
		{"too many slashes", "aws/rds/aurora", true},
		{"special characters", "aws/rds!", true},
		{"leading slash", "/aws/rds", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServicePath(tt.path)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateUUID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		uuid      string
		wantError bool
	}{
		{"valid UUID", "12345678-1234-1234-1234-123456789abc", false},
		{"valid UUID uppercase", "12345678-1234-1234-1234-123456789ABC", false},
		{"valid UUID mixed case", "12345678-1234-1234-1234-123456789AbC", false},
		{"invalid - no hyphens", "123456781234123412341234567890ab", true},
		{"invalid - wrong length", "12345678-1234-1234-1234-12345678", true},
		{"invalid - extra chars", "12345678-1234-1234-1234-123456789abcd", true},
		{"invalid - non-hex", "12345678-1234-1234-1234-123456789xyz", true},
		{"empty string", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUUID(tt.uuid)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateContentType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		method    string
		body      string
		headers   map[string]string
		wantError bool
	}{
		{"GET request without body", "GET", "", nil, false},
		{"POST with json content type", "POST", `{"key": "value"}`, map[string]string{"Content-Type": "application/json"}, false},
		{"POST with json and charset", "POST", `{"key": "value"}`, map[string]string{"Content-Type": "application/json; charset=utf-8"}, false},
		{"PUT with json content type", "PUT", `{"key": "value"}`, map[string]string{"content-type": "application/json"}, false},
		{"POST with form content type", "POST", "key=value", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, false},
		{"POST without body is ok", "POST", "", nil, false},
		{"POST with body but no content type", "POST", `{"key": "value"}`, nil, true},
		{"POST with unsupported content type", "POST", `{"key": "value"}`, map[string]string{"Content-Type": "text/plain"}, true},
		{"DELETE without body", "DELETE", "", nil, false},
		{"PATCH with json", "PATCH", `{"key": "value"}`, map[string]string{"Content-Type": "application/json"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &events.LambdaFunctionURLRequest{
				Body:    tt.body,
				Headers: tt.headers,
				RequestContext: events.LambdaFunctionURLRequestContext{
					HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
						Method: tt.method,
					},
				},
			}
			err := validateContentType(req)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCredentialPayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		credentialType string
		payload        map[string]interface{}
		wantErrSubstr  string
	}{
		// aws_access_keys
		{"aws happy", "aws_access_keys",
			map[string]interface{}{"access_key_id": "AKIA", "secret_access_key": "sk"},
			""},
		{"aws missing required", "aws_access_keys",
			map[string]interface{}{"access_key_id": "AKIA"},
			"missing required key \"secret_access_key\""},
		{"aws extra key", "aws_access_keys",
			map[string]interface{}{"access_key_id": "AKIA", "secret_access_key": "sk", "session_token": "tok"},
			"unknown key \"session_token\""},
		{"aws empty value", "aws_access_keys",
			map[string]interface{}{"access_key_id": "", "secret_access_key": "sk"},
			"must be a non-empty string"},
		{"aws non-string value", "aws_access_keys",
			map[string]interface{}{"access_key_id": true, "secret_access_key": "sk"},
			"must be a non-empty string"},

		// azure_client_secret
		{"azure secret happy", "azure_client_secret",
			map[string]interface{}{"client_secret": "abc123"}, ""},
		{"azure secret unknown key", "azure_client_secret",
			map[string]interface{}{"some_other": "abc"}, "unknown key \"some_other\""},

		// gcp_service_account
		{"gcp svc happy", "gcp_service_account",
			map[string]interface{}{
				"type": "service_account", "project_id": "p", "private_key": "k", "client_email": "e@p.iam",
				"private_key_id": "id", "client_id": "cid",
			}, ""},
		{"gcp svc wrong type", "gcp_service_account",
			map[string]interface{}{
				"type": "external_account", "project_id": "p", "private_key": "k", "client_email": "e@p.iam",
			}, "type=\"service_account\""},
		{"gcp svc missing project_id", "gcp_service_account",
			map[string]interface{}{"type": "service_account", "private_key": "k", "client_email": "e@p.iam"},
			"missing required key \"project_id\""},

		// gcp_workload_identity_config
		{"gcp wif happy", "gcp_workload_identity_config",
			map[string]interface{}{
				"type": "external_account", "audience": "//iam...", "subject_token_type": "urn:...",
				"token_url":         "https://sts.googleapis.com/v1/token",
				"credential_source": map[string]interface{}{"environment_id": "aws1"},
			}, ""},
		{"gcp wif missing audience", "gcp_workload_identity_config",
			map[string]interface{}{
				"type": "external_account", "subject_token_type": "urn:...",
				"token_url":         "https://sts.googleapis.com/v1/token",
				"credential_source": map[string]interface{}{"environment_id": "aws1"},
			}, "missing required key \"audience\""},
		{"gcp wif credential_source not object", "gcp_workload_identity_config",
			map[string]interface{}{
				"type": "external_account", "audience": "x", "subject_token_type": "y",
				"token_url": "https://sts.googleapis.com/v1/token", "credential_source": "string",
			}, "credential_source\" must be an object"},
		{"gcp wif wrong type", "gcp_workload_identity_config",
			map[string]interface{}{
				"type": "service_account", "audience": "x", "subject_token_type": "y",
				"token_url":         "https://sts.googleapis.com/v1/token",
				"credential_source": map[string]interface{}{"k": "v"},
			}, "type=\"external_account\""},

		// generic
		{"empty payload", "aws_access_keys", map[string]interface{}{}, "must not be empty"},
		{"nesting too deep", "gcp_workload_identity_config",
			map[string]interface{}{
				"type": "external_account", "audience": "x", "subject_token_type": "y",
				"token_url": "https://sts.googleapis.com/v1/token",
				"credential_source": map[string]interface{}{
					"deeper": map[string]interface{}{"x": "y"},
				},
			}, "nests too deeply"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCredentialPayload(tt.credentialType, tt.payload)
			if tt.wantErrSubstr == "" {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			if err != nil {
				assert.Contains(t, err.Error(), tt.wantErrSubstr)
			}
		})
	}
}

func TestValidateRequestBodySize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		bodySize  int
		wantError bool
	}{
		{"empty body", 0, false},
		{"small body", 100, false},
		{"body at limit", MaxRequestBodySize, false},
		{"body over limit", MaxRequestBodySize + 1, true},
		{"large body over limit", MaxRequestBodySize * 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Repeat("a", tt.bodySize)
			err := validateRequestBodySize(body)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateGCPClientEmail covers issue #405: gcp_client_email must match the
// GCP service-account email format so it cannot be used to inject path segments
// into the SA impersonation URL.
func TestValidateGCPClientEmail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		email     string
		wantError bool
	}{
		{"empty is allowed", "", false},
		{"valid SA email", "my-sa@my-project.iam.gserviceaccount.com", false},
		{"valid SA email with dots in name", "sa.name-123@proj.iam.gserviceaccount.com", false},
		{"path traversal injection", "foo@bar.com/../../v1/projects/-/serviceAccounts/attacker@evil.com", true},
		{"wrong domain", "sa@my-project.iam.googleapis.com", true},
		{"missing @ sign", "my-project.iam.gserviceaccount.com", true},
		{"arbitrary email", "user@example.com", true},
		{"space in value", "sa @proj.iam.gserviceaccount.com", true},
		{"newline injection", "sa@proj.iam.gserviceaccount.com\nX-Injected: header", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGCPClientEmail(tt.email)
			if tt.wantError {
				assert.Error(t, err)
				ce, ok := IsClientError(err)
				if assert.True(t, ok, "expected ClientError") {
					assert.Equal(t, 400, ce.code)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateAWSRoleARN covers issue #413: aws_role_arn must be a valid IAM
// role ARN so malformed strings are caught at the API boundary.
func TestValidateAWSRoleARN(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		arn       string
		wantError bool
	}{
		{"empty is allowed (ambient creds)", "", false},
		{"valid commercial ARN", "arn:aws:iam::123456789012:role/MyRole", false},
		{"valid govcloud ARN", "arn:aws-us-gov:iam::123456789012:role/MyRole", false},
		{"valid china ARN", "arn:aws-cn:iam::123456789012:role/MyRole", false},
		{"valid ARN with path", "arn:aws:iam::123456789012:role/path/to/MyRole", false},
		{"valid ARN with IAM-allowed special chars", "arn:aws:iam::123456789012:role/My+Role=v1,name.test@example_role-A", false},
		{"missing colons (common typo)", "arn:aws:iam:123456789012:role/Foo", true},
		{"non-ARN string", "not-an-arn", true},
		{"wrong service (sts)", "arn:aws:sts::123456789012:role/Foo", true},
		{"account ID too short", "arn:aws:iam::12345:role/Foo", true},
		{"unknown partition", "arn:aws-eu:iam::123456789012:role/Foo", true},
		// The strict character class rejects anything outside the IAM-permitted
		// set so attackers cannot smuggle whitespace, newlines, or HTML metachars
		// past validation into downstream consumers.
		{"whitespace in role name", "arn:aws:iam::123456789012:role/My Role", true},
		{"newline in role name", "arn:aws:iam::123456789012:role/My\nRole", true},
		{"angle-bracket injection", "arn:aws:iam::123456789012:role/<script>", true},
		{"trailing slash (empty role name)", "arn:aws:iam::123456789012:role/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAWSRoleARN(tt.arn)
			if tt.wantError {
				assert.Error(t, err)
				ce, ok := IsClientError(err)
				if assert.True(t, ok, "expected ClientError") {
					assert.Equal(t, 400, ce.code)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateEmailFormat covers issue #868: validateEmailFormat must reject TLD-less
// addresses like "user@host" that RFC 5322 accepts but that sign-up also rejects.
// The profile-update and account-create paths now call the same validator, so this
// table locks in parity across all three entry points.
func TestValidateEmailFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		email     string
		wantError bool
	}{
		// Happy paths
		{"empty is allowed (optional field)", "", false},
		{"typical address", "user@example.com", false},
		{"dotted local part", "user.name@example.com", false},
		{"plus tag", "user+tag@sub.example.com", false},
		{"minimum valid (short TLD)", "u@a.b", false},
		{"subdomain", "admin@mail.corp.example.com", false},

		// Issue #868 cases — TLD-less addresses that were previously accepted
		{"no TLD (bare host)", "user@host", true},
		{"trailing dot on domain", "user@host.", true},
		{"dot before host (no host part)", "user@.com", true},

		// Other invalid shapes
		{"empty string is OK (see first row), but just @", "@", true},
		{"no local part", "@host.com", true},
		{"space in local part", "user @host.com", true},
		{"space in domain", "user@host .com", true},
		{"no at-sign", "notanemail", true},
		{"double at-sign", "a@@b.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEmailFormat(tt.email)
			if tt.wantError {
				assert.Error(t, err)
				ce, ok := IsClientError(err)
				if assert.True(t, ok, "expected ClientError") {
					assert.Equal(t, 400, ce.code)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateAWSWebIdentityTokenFile covers issue #403: aws_web_identity_token_file
// must be restricted to known-safe mount prefixes to prevent arbitrary host file reads.
func TestValidateAWSWebIdentityTokenFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		path      string
		wantError bool
	}{
		{"empty is allowed (env var fallback)", "", false},
		{"EKS token file", "/var/run/secrets/eks.amazonaws.com/serviceaccount/token", false},
		{"k8s service-account token", "/var/run/secrets/kubernetes.io/serviceaccount/token", false},
		{"arbitrary host path", "/proc/self/environ", true},
		{"etc passwd", "/etc/passwd", true},
		{"docker secret", "/run/secrets/my-secret", true},
		{"env file", "/var/run/.env", true},
		{"traversal into allowed prefix", "/var/run/secrets/eks.amazonaws.com/serviceaccount/../../etc/passwd", true}, // path traversal blocked even when prefix matches
		{"relative path", "secrets/token", true},
		{"kubernetes prefix without trailing slash content", "/var/run/secrets/kubernetes.io/serviceaccount/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAWSWebIdentityTokenFile(tt.path)
			if tt.wantError {
				assert.Error(t, err)
				ce, ok := IsClientError(err)
				if assert.True(t, ok, "expected ClientError") {
					assert.Equal(t, 400, ce.code)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestParseMinSavingsParam is the regression test for issue #1089.
// It asserts that the savings-floor parsing helper correctly handles absent,
// zero, numeric, and invalid inputs, and that the "usd" and "pct" paths
// produce independent parsing results (preventing unit conflation at parse time).
func TestParseMinSavingsParam(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		paramName string
		wantVal   float64
		wantError bool
	}{
		// Absent / zero inputs produce a zero floor (no filter).
		{"empty string", "", "min_savings_usd", 0, false},
		{"zero string", "0", "min_savings_usd", 0, false},
		{"whitespace only", "   ", "min_savings_usd", 0, false},

		// Valid positive values.
		{"positive integer", "30", "min_savings_usd", 30, false},
		{"positive float", "12.5", "min_savings_usd", 12.5, false},
		{"large integer", "10000", "min_savings_usd", 10000, false},

		// Percentage param uses the same parsing path.
		{"pct integer", "20", "min_savings_pct", 20, false},
		{"pct float", "33.3", "min_savings_pct", 33.3, false},

		// Invalid inputs.
		{"non-numeric word", "thirty", "min_savings_usd", 0, true},
		{"negative value", "-5", "min_savings_usd", 0, true},
		{"mixed string", "30abc", "min_savings_usd", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			val, err := parseMinSavingsParam(tc.raw, tc.paramName)
			if tc.wantError {
				require.Error(t, err)
				ce, ok := IsClientError(err)
				require.True(t, ok, "expected ClientError, got %T: %v", err, err)
				assert.Equal(t, 400, ce.code)
			} else {
				require.NoError(t, err)
				assert.InDelta(t, tc.wantVal, val, 0.0001)
			}
		})
	}
}
