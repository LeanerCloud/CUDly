package iacfiles

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

// testTemplateData mirrors the fields that CLI templates read. It's a local
// fixture rather than a dependency on internal/api's federationIaCData so the
// test stays in this package (which has no other test coverage).
type testTemplateData struct {
	AccountName         string
	AccountExternalID   string
	AccountSlug         string
	Source              string
	OIDCIssuerURL       string
	OIDCIssuerHost      string
	OIDCAudience        string
	SubscriptionID      string
	TenantID            string
	ProjectID           string
	ServiceAccountEmail string
	OIDCIssuerURI       string
	CUDlyAPIURL         string
	SourceAccountID     string
}

func renderCLITemplate(t *testing.T, path string, data testTemplateData) string {
	t.Helper()
	raw, err := Templates.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	tmpl, err := template.New(path).Parse(string(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute %s: %v", path, err)
	}
	return buf.String()
}

// baseData matches what buildGenericIaCData produces for a self-service download
// (account-specific fields empty, CUDlyAPIURL set so the auto-register block renders).
func baseData() testTemplateData {
	return testTemplateData{
		AccountSlug:     "target",
		Source:          "aws",
		CUDlyAPIURL:     "https://cudly.example.com",
		SourceAccountID: "123456789012",
	}
}

func TestCLITemplatesAutoRegister(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		mustContain []string
		mustNot     []string
	}{
		{
			name: "aws-cross-account",
			path: "templates/aws-cross-account-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"aws_auth_mode": "role_arn"`,
				`"aws_external_id": "${EXTERNAL_ID}"`,
				`"aws_role_arn": "${ROLE_ARN}"`,
				`"external_id": "${TARGET_ACCOUNT_ID}"`,
				`TARGET_ACCOUNT_ID=$(aws sts get-caller-identity`,
				`ACCOUNT_NAME="${CUDLY_ACCOUNT_NAME:-AWS ${TARGET_ACCOUNT_ID}}"`,
				`read -r -d '' PAYLOAD <<JSON || true`,
				`case "$HTTP_CODE" in`,
			},
			mustNot: []string{
				"/api/registrations",
				`"account_id":`,
				`"auth_mode":`,
			},
		},
		{
			name: "aws-wif",
			path: "templates/aws-wif-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"aws_auth_mode": "workload_identity_federation"`,
				`"aws_role_arn": "${ROLE_ARN}"`,
				`"external_id": "${TARGET_ACCOUNT_ID}"`,
				`TARGET_ACCOUNT_ID=$(aws sts get-caller-identity`,
			},
			mustNot: []string{
				"/api/registrations",
				`"account_id":`,
				// WIF flow has no STS external_id
				`aws_external_id`,
			},
		},
		{
			name: "azure-wif",
			path: "templates/azure-wif-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"azure_auth_mode": "workload_identity_federation"`,
				`"azure_subscription_id": "${SUBSCRIPTION_ID}"`,
				`"azure_tenant_id": "${TENANT_ID}"`,
				`"azure_client_id": "${APP_ID}"`,
				`"external_id": "${SUBSCRIPTION_ID}"`,
				`ACCOUNT_NAME="${CUDLY_ACCOUNT_NAME:-Azure ${SUBSCRIPTION_ID}}"`,
				// Secret-free redesign: must use federated identity credential, not a cert upload.
				"az ad app federated-credential create",
				`"issuer": "${CUDLY_ISSUER_URL}"`,
				`"subject": "${CUDLY_FEDERATED_SUBJECT}"`,
				`"audiences": ["${CUDLY_FEDERATED_AUDIENCE}"]`,
				// Issuer env var must default to the CUDly base URL + /oidc
				// so Azure AD appending /.well-known/openid-configuration
				// resolves to the discovery endpoint on the CUDly deployment.
				`CUDLY_ISSUER_URL="${CUDLY_ISSUER_URL:-https://cudly.example.com/oidc}"`,
			},
			mustNot: []string{
				"/api/registrations",
				`"auth_mode":`,
				`"tenant_id":`,
				`"client_id":`,
				// Never the cert-based path.
				"az ad app credential reset",
				"CERTIFICATE_PEM_PATH",
				"azure_wif_private_key",
			},
		},
		{
			name: "gcp-wif",
			path: "templates/gcp-wif-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"gcp_auth_mode": "workload_identity_federation"`,
				`"gcp_project_id": "${PROJECT_ID}"`,
				`"gcp_client_email": "${SERVICE_ACCOUNT_EMAIL}"`,
				`"gcp_wif_audience": "${WIF_AUDIENCE}"`,
				`"external_id": "${PROJECT_ID}"`,
				`ACCOUNT_NAME="${CUDLY_ACCOUNT_NAME:-GCP ${PROJECT_ID}}"`,
				// Secret-free redesign: issuer is CUDly's own OIDC
				// deployment, subject is the fixed cudly-controller.
				`CUDLY_ISSUER_URL="${CUDLY_ISSUER_URL:-https://cudly.example.com/oidc}"`,
				`--issuer-uri="${CUDLY_ISSUER_URL}"`,
				`--attribute-mapping="google.subject=assertion.sub"`,
				`--attribute-condition="assertion.sub == '${CUDLY_FEDERATED_SUBJECT}'"`,
				`principal://iam.googleapis.com/${POOL_NAME}/subject/${CUDLY_FEDERATED_SUBJECT}`,
				`WIF_AUDIENCE="//iam.googleapis.com/${POOL_NAME}/providers/${PROVIDER_ID}"`,
			},
			mustNot: []string{
				"/api/registrations",
				`"service_account_email":`,
				// The old AWS-STS-ARN provider is gone.
				"create-aws",
				"attribute.aws_role",
				"SOURCE_AWS_ACCOUNT_ID",
				// No longer asking the operator to generate a creds
				// config JSON file.
				"create-cred-config",
				"gcp_workload_identity_config",
			},
		},
		{
			name: "gcp-sa-impersonation",
			path: "templates/gcp-sa-impersonation-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"gcp_auth_mode": "application_default"`,
				`"gcp_project_id": "${PROJECT_ID}"`,
				`"gcp_client_email": "${SERVICE_ACCOUNT_EMAIL}"`,
				`ACCOUNT_NAME="${CUDLY_ACCOUNT_NAME:-GCP ${PROJECT_ID}}"`,
			},
			mustNot: []string{
				"/api/registrations",
				`"service_account_email":`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := renderCLITemplate(t, tc.path, baseData())
			for _, needle := range tc.mustContain {
				if !strings.Contains(rendered, needle) {
					t.Errorf("%s: rendered script missing %q", tc.path, needle)
				}
			}
			for _, needle := range tc.mustNot {
				if strings.Contains(rendered, needle) {
					t.Errorf("%s: rendered script still contains stale %q", tc.path, needle)
				}
			}
		})
	}
}

// TestCLITemplatesOmitRegisterBlock proves the auto-register section is gated
// on CUDlyAPIURL — when it's empty, the block disappears entirely.
func TestCLITemplatesOmitRegisterBlock(t *testing.T) {
	data := baseData()
	data.CUDlyAPIURL = ""

	paths := []string{
		"templates/aws-cross-account-cli.sh.tmpl",
		"templates/aws-wif-cli.sh.tmpl",
		"templates/azure-wif-cli.sh.tmpl",
		"templates/gcp-wif-cli.sh.tmpl",
		"templates/gcp-sa-impersonation-cli.sh.tmpl",
	}
	for _, p := range paths {
		rendered := renderCLITemplate(t, p, data)
		if strings.Contains(rendered, "/api/register") {
			t.Errorf("%s: auto-register block should be gone when CUDlyAPIURL is empty", p)
		}
	}
}
