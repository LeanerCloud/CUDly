package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// federationHandler returns a Handler wired for federation IaC tests.
//
// Sets CUDLY_SOURCE_CLOUD=gcp when the env var is not already set, so that
// tests which do not specifically exercise the AWS-source fail-loud STS check
// are not broken by missing AWS credentials in the test environment. Tests
// that need CUDLY_SOURCE_CLOUD=aws must call t.Setenv("CUDLY_SOURCE_CLOUD","aws")
// BEFORE calling federationHandler (the check below will see the already-set
// value and skip the override).
func federationHandler() *Handler {
	if os.Getenv("CUDLY_SOURCE_CLOUD") == "" {
		_ = os.Setenv("CUDLY_SOURCE_CLOUD", "gcp")
	}
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "admin-token").Return(&Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}, nil)
	return NewHandler(HandlerConfig{ConfigStore: new(MockConfigStore), AuthService: mockAuth})
}

func federationReq(params map[string]string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers:               map[string]string{"Authorization": "Bearer admin-token"},
		QueryStringParameters: params,
	}
}

// federationReqWithDomain returns a request that also sets DomainName so the
// handler derives CUDlyAPIURL from it, which causes the {{if .CUDlyAPIURL}}
// sections in templates to be rendered. Use in tests that need to verify the
// registration block is present in the rendered output.
func federationReqWithDomain(params map[string]string) *events.LambdaFunctionURLRequest {
	req := federationReq(params)
	req.RequestContext.DomainName = "cudly.example.com"
	return req
}

// ---------------------------------------------------------------------------
// singleFileSpec tests
// ---------------------------------------------------------------------------

func TestSingleFileSpec_RejectsEmptyFormat(t *testing.T) {
	// The legacy tfvars-only (format="") path was removed. singleFileSpec must
	// now reject the empty format — the handler validates format upstream, but
	// the helper also defends the boundary.
	_, _, _, err := singleFileSpec("aws", "aws", "", "prod")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestSingleFileSpec_RejectsUnknownFormat(t *testing.T) {
	_, _, _, err := singleFileSpec("aws", "azure", "cf-params", "prod")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

// ---------------------------------------------------------------------------
// getFederationIaC handler tests (generic — no account_id)
// ---------------------------------------------------------------------------

func TestGetFederationIaC_MissingParams(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	_, err := h.getFederationIaC(ctx, federationReq(map[string]string{}))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a client error")
	assert.Equal(t, 400, ce.code)
}

// TestGetFederationIaC_RequiresAuth asserts an unauthenticated request is
// rejected before template rendering — the response would otherwise embed
// the CUDly host AWS account ID resolved via STS.
func TestGetFederationIaC_RequiresAuth(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"target": "aws", "source": "aws", "format": "cli",
		},
	}
	_, err := h.getFederationIaC(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a client error")
	assert.Equal(t, 401, ce.code)
}

func TestGetFederationIaC_RejectsEmptyFormat(t *testing.T) {
	// The legacy tfvars-only default was removed; an explicit format is required.
	h := federationHandler()
	ctx := context.Background()

	_, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "aws",
	}))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "format")
}

func TestGetFederationIaC_RejectsUnknownFormat(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	_, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "aws", "format": "tfvars",
	}))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestGetFederationIaC_AWSWIF_CFNZip(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "azure", "format": "cfn",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "aws-wif-cfn.zip")
	assert.Equal(t, "application/zip", res.ContentType)
	assert.Equal(t, "base64", res.ContentEncoding)

	zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)
	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	assert.True(t, names["cloudformation/template.yaml"], "cfn zip must contain CF template")
	assert.True(t, names["cloudformation/deploy-cfn.sh"], "cfn zip must contain deploy script")
	hasParams := false
	for n := range names {
		if strings.HasSuffix(n, "-cf-params.json") {
			hasParams = true
			break
		}
	}
	assert.True(t, hasParams, "cfn zip must contain a cf-params.json file")
}

func TestGetFederationIaC_CFN_AWSCrossAccount(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "aws", "format": "cfn",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "aws-cross-account-cfn.zip")
	assert.Equal(t, "application/zip", res.ContentType)
	assert.Equal(t, "base64", res.ContentEncoding)

	zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)
	names := make(map[string][]byte)
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(rc)
		rc.Close()
		names[f.Name] = buf.Bytes()
	}
	assert.Contains(t, names, "cloudformation/template.yaml")
	assert.Contains(t, names, "cloudformation/deploy-cfn.sh")
	// Cross-account template has SourceAccountID parameter, not OIDCIssuerURL.
	assert.Contains(t, string(names["cloudformation/template.yaml"]), "SourceAccountID")
	assert.Contains(t, string(names["cloudformation/deploy-cfn.sh"]), "SOURCE_ACCOUNT_ID")
	// Params JSON should reference SourceAccountID, not OIDC.
	var paramsContent []byte
	for n, b := range names {
		if strings.HasSuffix(n, "-cf-params.json") {
			paramsContent = b
		}
	}
	require.NotNil(t, paramsContent)
	var params []map[string]string
	require.NoError(t, json.Unmarshal(paramsContent, &params))
	paramKeys := make(map[string]bool)
	for _, p := range params {
		paramKeys[p["ParameterKey"]] = true
	}
	assert.True(t, paramKeys["SourceAccountID"])
	assert.True(t, paramKeys["ExternalID"])
	assert.False(t, paramKeys["OIDCIssuerURL"])
}

func TestGetFederationIaC_Bundle_AWSCrossAccount(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "aws", "format": "bundle",
	}))
	require.NoError(t, err)
	assert.Equal(t, "base64", res.ContentEncoding)
	assert.Equal(t, "application/zip", res.ContentType)
	assert.Contains(t, res.Filename, "aws-cross-account-bundle.zip")

	zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)
	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	assert.True(t, names["terraform/main.tf"], "zip must contain terraform/main.tf")
	assert.True(t, names["terraform/variables.tf"], "zip must contain terraform/variables.tf")
	assert.True(t, names["terraform/outputs.tf"], "zip must contain terraform/outputs.tf")
	assert.True(t, names["README.txt"], "zip must contain README.txt")
	assert.True(t, names["cloudformation/template.yaml"], "aws→aws bundle must include cross-account CF template")
	assert.True(t, names["cloudformation/deploy-cfn.sh"], "aws→aws bundle must include deploy script")
}

func TestGetFederationIaC_Bundle_AWSWif(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "gcp", "format": "bundle",
	}))
	require.NoError(t, err)
	assert.Equal(t, "base64", res.ContentEncoding)

	zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)
	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	assert.True(t, names["terraform/main.tf"])
	assert.True(t, names["cloudformation/template.yaml"], "aws WIF bundle must include CF template")
	assert.True(t, names["cloudformation/deploy-cfn.sh"], "aws WIF bundle must include deploy script")
}

func TestGetFederationIaC_Bundle_GCPSAImpersonation(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "gcp", "source": "gcp", "format": "bundle",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "gcp-sa-impersonation-bundle.zip")

	zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)
	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	assert.True(t, names["terraform/main.tf"])
	assert.False(t, names["cloudformation/template.yaml"], "gcp→gcp bundle must not include CF template")
}

func TestGetFederationIaC_InvalidSource(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	_, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "badcloud", "format": "cli",
	}))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a client error")
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "source must be")
}

func TestGetFederationIaC_CFNZip_ParamsValidJSON(t *testing.T) {
	h := federationHandler()
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "azure", "format": "cfn",
	}))
	require.NoError(t, err)

	zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)

	var paramsFile *zip.File
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "-cf-params.json") {
			paramsFile = f
			break
		}
	}
	require.NotNil(t, paramsFile, "cfn zip must contain a cf-params.json file")

	rc, err := paramsFile.Open()
	require.NoError(t, err)
	defer rc.Close()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(rc)
	require.NoError(t, err)

	var params []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &params), "CF params must be valid JSON")
	assert.GreaterOrEqual(t, len(params), 3, "should have at least 3 parameters")
}

func TestSingleFileSpec_CLI_AllScenarios(t *testing.T) {
	cases := []struct{ target, source, wantContains string }{
		{"aws", "aws", "aws-cross-account-cli.sh"},
		{"aws", "azure", "aws-wif-cli.sh"},
		{"aws", "gcp", "aws-wif-cli.sh"},
		{"azure", "aws", "azure-wif-cli.sh"},
		{"azure", "gcp", "azure-wif-cli.sh"},
		{"gcp", "gcp", "gcp-sa-impersonation-cli.sh"},
		{"gcp", "aws", "gcp-wif-cli.sh"},
		{"gcp", "azure", "gcp-wif-cli.sh"},
	}
	for _, tc := range cases {
		_, fname, ct, err := singleFileSpec(tc.target, tc.source, "cli", "prod")
		require.NoError(t, err, "target=%s source=%s", tc.target, tc.source)
		assert.Contains(t, fname, tc.wantContains)
		assert.Equal(t, "text/x-shellscript", ct)
	}
}

func TestGetFederationIaC_CLI_AWSCrossAccount(t *testing.T) {
	h := federationHandler()
	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "aws", "source": "aws", "format": "cli",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "aws-cross-account-cli.sh")
	assert.Equal(t, "text/x-shellscript", res.ContentType)
	assert.Contains(t, res.Content, "#!/usr/bin/env bash")
	assert.Contains(t, res.Content, "aws iam create-role")
	assert.Contains(t, res.Content, "SOURCE_ACCOUNT_ID")
}

func TestGetFederationIaC_CLI_AWSWIF(t *testing.T) {
	h := federationHandler()
	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "aws", "source": "azure", "format": "cli",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "aws-wif-cli.sh")
	assert.Contains(t, res.Content, "create-open-id-connect-provider")
	assert.Contains(t, res.Content, "login.microsoftonline.com")
}

func TestGetFederationIaC_CLI_Azure(t *testing.T) {
	h := federationHandler()
	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "azure", "source": "aws", "format": "cli",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "azure-wif-cli.sh")
	assert.Contains(t, res.Content, "az ad app create")
	assert.Contains(t, res.Content, "Reservation Purchaser")
}

func TestGetFederationIaC_CLI_GCPSAImpersonation(t *testing.T) {
	h := federationHandler()
	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "gcp", "source": "gcp", "format": "cli",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "gcp-sa-impersonation-cli.sh")
	assert.Contains(t, res.Content, "gcloud iam service-accounts")
	assert.Contains(t, res.Content, "SOURCE_SERVICE_ACCOUNT")
}

func TestGetFederationIaC_CLI_GCPWIF(t *testing.T) {
	h := federationHandler()
	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "gcp", "source": "aws", "format": "cli",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "gcp-wif-cli.sh")
	assert.Contains(t, res.Content, "workload-identity-pools create")
	// Secret-free redesign: the provider binds to CUDly's own OIDC
	// issuer, not a source-cloud AWS-STS provider.
	assert.Contains(t, res.Content, "providers create-oidc")
	assert.Contains(t, res.Content, "issuer-uri=\"${CUDLY_ISSUER_URL}\"")
	assert.NotContains(t, res.Content, "create-aws")
}

func TestShellEscape(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{`hello`, `hello`},
		{`he"llo`, `he\"llo`},
		{"he`llo", "he\\`llo"},
		{`$HOME`, `\$HOME`},
		{`back\slash`, `back\\slash`},
		{`$(rm -rf /)`, `\$(rm -rf /)`},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, shellEscape(tt.input), "input: %q", tt.input)
	}
}

// ---------------------------------------------------------------------------
// Azure Bicep / ARM tests
// ---------------------------------------------------------------------------

func unzipResponse(t *testing.T, res *FederationIaCResponse) map[string][]byte {
	t.Helper()
	require.Equal(t, "application/zip", res.ContentType)
	require.Equal(t, "base64", res.ContentEncoding)
	zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)
	files := make(map[string][]byte)
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(rc)
		rc.Close()
		files[f.Name] = buf.Bytes()
	}
	return files
}

func TestGetFederationIaC_AzureBicep(t *testing.T) {
	h := federationHandler()
	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "azure", "source": "aws", "format": "bicep",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "azure-wif-bicep.zip")

	files := unzipResponse(t, res)
	require.Contains(t, files, "azure-wif.bicep")
	require.Contains(t, files, "azure-wif-bicep-params.json")
	require.Contains(t, files, "deploy-azure.sh")
	require.Contains(t, files, "README.txt")

	assert.Contains(t, string(files["azure-wif.bicep"]), "targetScope = 'subscription'")
	assert.Contains(t, string(files["azure-wif.bicep"]), "Microsoft.Authorization/roleAssignments")
	assert.Contains(t, string(files["deploy-azure.sh"]), "az deployment sub create")

	var params map[string]any
	require.NoError(t, json.Unmarshal(files["azure-wif-bicep-params.json"], &params),
		"params file must be valid JSON")
	assert.Contains(t, params, "parameters")
}

func TestGetFederationIaC_AzureARM(t *testing.T) {
	h := federationHandler()
	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "azure", "source": "gcp", "format": "arm",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "azure-wif-arm.zip")

	files := unzipResponse(t, res)
	require.Contains(t, files, "azure-wif.arm.json")
	require.Contains(t, files, "azure-wif-bicep-params.json")
	require.Contains(t, files, "deploy-azure.sh")

	var armTemplate map[string]any
	require.NoError(t, json.Unmarshal(files["azure-wif.arm.json"], &armTemplate),
		"ARM template must be valid JSON")
	assert.Contains(t, armTemplate, "parameters")
	assert.Contains(t, armTemplate, "resources")
}

func TestGetFederationIaC_Bicep_RejectsNonAzure(t *testing.T) {
	h := federationHandler()
	_, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "aws", "source": "azure", "format": "bicep",
	}))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "target=azure")
}

// ---------------------------------------------------------------------------
// Zero-touch registration: ContactEmail pre-fill + fail-loud STS tests
// ---------------------------------------------------------------------------

// TestGetFederationIaC_FailsLoudOnEmptySourceAccountID verifies that when
// CUDLY_SOURCE_CLOUD=aws and STS GetCallerIdentity fails (returns ""), the
// handler returns a non-nil error instead of shipping a broken bundle with an
// empty source_account_id.
func TestGetFederationIaC_FailsLoudOnEmptySourceAccountID(t *testing.T) {
	// sourceCloud() returns "aws" by default (and we explicitly set it here for
	// clarity). resolveAWSAccountID returns "" because there are no real AWS
	// credentials in the test environment, which triggers the fail-loud path.
	t.Setenv("CUDLY_SOURCE_CLOUD", "aws")
	h := federationHandler()

	_, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "aws", "source": "aws", "format": "cli",
	}))
	require.Error(t, err, "expected error when SourceAccountID is empty")
	// Must NOT be a client error (400/401/403) — it's a server-side misconfiguration.
	_, isClientErr := IsClientError(err)
	assert.False(t, isClientErr, "STS failure should produce a 500-class error, not a client error")
	assert.Contains(t, err.Error(), "federation iac")
}

// TestGetFederationIaC_PrefillContactEmailFromSession verifies that the bundle
// is rendered with contact_email equal to the authenticated user's email.
func TestGetFederationIaC_PrefillContactEmailFromSession(t *testing.T) {
	t.Setenv("CUDLY_SOURCE_CLOUD", "gcp")
	h := federationHandler()

	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "aws", "source": "gcp", "format": "bundle",
	}))
	require.NoError(t, err)

	zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)

	var tfvarsContent string
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".tfvars") {
			rc, err := f.Open()
			require.NoError(t, err)
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rc)
			rc.Close()
			tfvarsContent = buf.String()
			break
		}
	}
	require.NotEmpty(t, tfvarsContent, "bundle must contain a .tfvars file")
	assert.Contains(t, tfvarsContent, `contact_email = "admin@example.com"`,
		"contact_email must be pre-filled from Session.Email")
}

// TestGetFederationIaC_TfvarsAutoLoadedByTerraform pins that the bundle's
// tfvars file ships with the .auto.tfvars suffix so customers can run
// `terraform init && terraform apply` with no -var-file= flag — Terraform
// auto-loads any file matching that pattern from the working directory.
// Regression guard against accidentally reverting to the plain .tfvars
// shape, which would silently re-introduce the manual flag requirement.
func TestGetFederationIaC_TfvarsAutoLoadedByTerraform(t *testing.T) {
	t.Setenv("CUDLY_SOURCE_CLOUD", "gcp")
	h := federationHandler()

	cases := []struct {
		name           string
		target, source string
	}{
		{"aws-cross-account", "aws", "aws"},
		{"aws-wif", "aws", "gcp"},
		{"azure-wif", "azure", "gcp"},
		{"gcp-sa-impersonation", "gcp", "gcp"},
		{"gcp-wif", "gcp", "aws"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// aws-cross-account requires sourceCloud()=="aws" for the STS resolution
			// to succeed; every other case can run under the gcp default above.
			if tc.target == "aws" && tc.source == "aws" {
				t.Setenv("CUDLY_SOURCE_CLOUD", "aws")
				t.Skip("aws-cross-account needs real STS resolution; covered by TestGetFederationIaC_FailsLoudOnEmptySourceAccountID")
			}
			res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
				"target": tc.target, "source": tc.source, "format": "bundle",
			}))
			require.NoError(t, err)

			zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
			require.NoError(t, err)
			zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
			require.NoError(t, err)

			var foundAutoTfvars bool
			for _, f := range zr.File {
				if strings.HasSuffix(f.Name, ".auto.tfvars") {
					foundAutoTfvars = true
					assert.False(t, strings.HasSuffix(f.Name, ".tfvars") && !strings.HasSuffix(f.Name, ".auto.tfvars"),
						"tfvars file %s must use .auto.tfvars (not plain .tfvars) for Terraform auto-loading", f.Name)
					break
				}
			}
			assert.True(t, foundAutoTfvars, "bundle must contain a *.auto.tfvars file for Terraform auto-loading")
		})
	}
}

// TestGetFederationIaC_PreservesPlusInSessionEmail verifies that a + in the
// email address is not mangled by shell-escaping when rendering CLI scripts.
func TestGetFederationIaC_PreservesPlusInSessionEmail(t *testing.T) {
	t.Setenv("CUDLY_SOURCE_CLOUD", "gcp")
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "admin-token").Return(&Session{
		UserID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		Email:  "user+tag@example.com",
		Role:   "admin",
	}, nil)
	h := NewHandler(HandlerConfig{ConfigStore: new(MockConfigStore), AuthService: mockAuth})

	// Use federationReqWithDomain so the {{if .CUDlyAPIURL}} block renders and
	// the CONTACT_EMAIL line (with the pre-filled email) appears in the output.
	res, err := h.getFederationIaC(context.Background(), federationReqWithDomain(map[string]string{
		"target": "gcp", "source": "aws", "format": "cli",
	}))
	require.NoError(t, err)
	// '+' has no special meaning in double-quoted bash strings — it must be preserved.
	assert.Contains(t, res.Content, "user+tag@example.com",
		"+ in email must be preserved in CLI script")
}

// TestGetFederationIaC_NoSessionEmail_ShipsBundleWithEmptyContact verifies the
// defensive edge case: if Session.Email is empty (e.g. admin API key path where
// Email is not set), the bundle downloads successfully with contact_email=""
// and the customer's deploy will fail with a clear HTTP 400 at registration time.
func TestGetFederationIaC_NoSessionEmail_ShipsBundleWithEmptyContact(t *testing.T) {
	t.Setenv("CUDLY_SOURCE_CLOUD", "gcp")
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "admin-token").Return(&Session{
		UserID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Email:  "", // empty — admin API key path
		Role:   "admin",
	}, nil)
	h := NewHandler(HandlerConfig{ConfigStore: new(MockConfigStore), AuthService: mockAuth})

	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "gcp", "source": "aws", "format": "bundle",
	}))
	// Bundle must still download — the 400 happens at registration time, not here.
	require.NoError(t, err)
	assert.Equal(t, "base64", res.ContentEncoding)

	zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)
	var tfvarsContent string
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".tfvars") {
			rc, err := f.Open()
			require.NoError(t, err)
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rc)
			rc.Close()
			tfvarsContent = buf.String()
			break
		}
	}
	require.NotEmpty(t, tfvarsContent)
	assert.Contains(t, tfvarsContent, `contact_email = ""`,
		"empty email must render as empty string in tfvars")
}

// ---------------------------------------------------------------------------
// Per-format render assertions
// ---------------------------------------------------------------------------

// TestRenderedCLIShellScript_RegistrationAlwaysRuns verifies that all CLI
// shell templates no longer gate registration on CUDLY_CONTACT_EMAIL being set,
// and instead use CONTACT_EMAIL with a pre-filled default.
func TestRenderedCLIShellScript_RegistrationAlwaysRuns(t *testing.T) {
	cases := []struct{ target, source string }{
		{"aws", "aws"},
		{"aws", "gcp"},
		{"azure", "aws"},
		{"gcp", "gcp"},
		{"gcp", "aws"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.target+"/"+tc.source, func(t *testing.T) {
			h := federationHandler()
			// Use federationReqWithDomain so CUDlyAPIURL is populated and the
			// {{if .CUDlyAPIURL}} registration block is rendered.
			res, err := h.getFederationIaC(context.Background(), federationReqWithDomain(map[string]string{
				"target": tc.target, "source": tc.source, "format": "cli",
			}))
			require.NoError(t, err)

			// Old gate must be gone.
			assert.NotContains(t, res.Content, `if [[ -n "${CUDLY_CONTACT_EMAIL:-}"`,
				"old CUDLY_CONTACT_EMAIL gate must be removed")
			// New env-override-with-default pattern must be present.
			assert.Contains(t, res.Content, `CONTACT_EMAIL="${CUDLY_CONTACT_EMAIL:-`,
				"CONTACT_EMAIL must use env-override-with-default pattern")
			// Error handling must fail loud.
			assert.Contains(t, res.Content, "exit 1",
				"non-2xx/409 registration response must exit 1")
			assert.NotContains(t, res.Content, "WARNING: CUDly registration",
				"WARNING must be replaced with ERROR+exit")
		})
	}
}

// TestRenderedCFNDeployScript_HasRegistrationBlock verifies both CFN deploy
// templates include a curl POST to /api/register after the stack deploy.
func TestRenderedCFNDeployScript_HasRegistrationBlock(t *testing.T) {
	cases := []struct {
		target, source string
		format         string
	}{
		{"aws", "gcp", "cfn"}, // WIF
		{"aws", "aws", "cfn"}, // cross-account
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.target+"/"+tc.source, func(t *testing.T) {
			h := federationHandler()
			// Use federationReqWithDomain so CUDlyAPIURL is populated and the
			// {{if .CUDlyAPIURL}} registration block is rendered in the deploy script.
			res, err := h.getFederationIaC(context.Background(), federationReqWithDomain(map[string]string{
				"target": tc.target, "source": tc.source, "format": tc.format,
			}))
			require.NoError(t, err)

			zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
			require.NoError(t, err)
			zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
			require.NoError(t, err)

			var deployScript string
			for _, f := range zr.File {
				if f.Name == "cloudformation/deploy-cfn.sh" {
					rc, err := f.Open()
					require.NoError(t, err)
					var buf bytes.Buffer
					_, _ = buf.ReadFrom(rc)
					rc.Close()
					deployScript = buf.String()
					break
				}
			}
			require.NotEmpty(t, deployScript, "cfn zip must contain deploy-cfn.sh")
			assert.Contains(t, deployScript, "/api/register",
				"deploy script must include registration curl call")
			assert.Contains(t, deployScript, `case "$HTTP_CODE"`,
				"deploy script must handle registration HTTP response codes")
		})
	}
}

// TestBuildAzureTemplateZip_IncludesRenderedDeployScript verifies that the
// bicep zip bundle contains a rendered deploy-azure.sh with:
//   - the original az deployment sub create invocation,
//   - the registration block (curl /api/register, HTTP-code case statement,
//     CONTACT_EMAIL pre-fill), and
//   - executable Unix mode (0755).
func TestBuildAzureTemplateZip_IncludesRenderedDeployScript(t *testing.T) {
	for _, format := range []string{"bicep", "arm"} {
		format := format
		t.Run(format, func(t *testing.T) {
			h := federationHandler()
			res, err := h.getFederationIaC(context.Background(), federationReqWithDomain(map[string]string{
				"target": "azure", "source": "aws", "format": format,
			}))
			require.NoError(t, err)

			zipBytes, err := base64.StdEncoding.DecodeString(res.Content)
			require.NoError(t, err)
			zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
			require.NoError(t, err)

			var deployFile *zip.File
			for _, f := range zr.File {
				if f.Name == "deploy-azure.sh" {
					deployFile = f
					break
				}
			}
			require.NotNil(t, deployFile, "zip must contain deploy-azure.sh")

			// Verify executable mode.
			assert.Equal(t, fs.FileMode(0755), deployFile.Mode(),
				"deploy-azure.sh must be marked executable (0755)")

			rc, err := deployFile.Open()
			require.NoError(t, err)
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rc)
			rc.Close()
			script := buf.String()

			// Original deployment commands must be present.
			assert.Contains(t, script, "az deployment sub create",
				"deploy script must invoke az deployment sub create")

			// Registration block must be rendered (CUDlyAPIURL is set via federationReqWithDomain).
			assert.Contains(t, script, "/api/register",
				"deploy script must include registration curl call")
			assert.Contains(t, script, `case "$HTTP_CODE"`,
				"deploy script must handle HTTP response codes")
			assert.Contains(t, script, "200|201",
				"deploy script must handle success codes")
			assert.Contains(t, script, "409",
				"deploy script must handle 409 already-pending")
			assert.Contains(t, script, `CONTACT_EMAIL="${CUDLY_CONTACT_EMAIL:-`,
				"deploy script must pre-fill CONTACT_EMAIL with env-override support")
		})
	}
}

// TestRenderedTerraformRegistrationTF_GateOnURLOnly reads the static
// registration.tf files and asserts that do_register only checks cudly_api_url,
// not contact_email.
func TestRenderedTerraformRegistrationTF_GateOnURLOnly(t *testing.T) {
	dirs := []string{
		"../../iac/federation/aws-cross-account/terraform/registration.tf",
		"../../iac/federation/aws-target/terraform/registration.tf",
		"../../iac/federation/azure-target/terraform/registration.tf",
		"../../iac/federation/gcp-target/terraform/registration.tf",
		"../../iac/federation/gcp-sa-impersonation/terraform/registration.tf",
	}
	for _, path := range dirs {
		path := path
		t.Run(path, func(t *testing.T) {
			content, err := os.ReadFile(path)
			require.NoError(t, err, "registration.tf must exist at %s", path)
			src := string(content)

			// The file uses `do_register      =` (extra whitespace); check for
			// the key substrings rather than the exact spacing.
			assert.Contains(t, src, `do_register`,
				"registration.tf must define do_register local")
			assert.Contains(t, src, `var.cudly_api_url != ""`,
				"do_register must gate on cudly_api_url being non-empty")
			assert.NotContains(t, src, `&& var.contact_email != ""`,
				"do_register must NOT gate on contact_email")
		})
	}
}
