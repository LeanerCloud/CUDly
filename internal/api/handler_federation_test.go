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
//
// Also pre-warms h.sourceID with a fully-populated identity matching the
// active source cloud so the validateSourceIdentity guard (see #41) is
// satisfied by default. Tests that specifically exercise the missing-env-var
// failure modes call mockSourceIdentity afterwards to override.
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
	h := NewHandler(HandlerConfig{ConfigStore: new(MockConfigStore), AuthService: mockAuth})
	mockSourceIdentity(h, defaultTestSourceIdentity())
	return h
}

// defaultTestSourceIdentity returns a fully-populated sourceIdentity matching
// the active CUDLY_SOURCE_CLOUD env var. Used by federationHandler to satisfy
// validateSourceIdentity and the AWS-cross-account fail-loud guard by default.
func defaultTestSourceIdentity() *sourceIdentity {
	switch sourceCloud() {
	case "aws":
		return &sourceIdentity{Provider: "aws", AccountID: "123456789012"}
	case "azure":
		return &sourceIdentity{
			Provider:       "azure",
			SubscriptionID: "00000000-0000-0000-0000-000000000001",
			TenantID:       "00000000-0000-0000-0000-000000000002",
			ClientID:       "00000000-0000-0000-0000-000000000003",
		}
	case "gcp":
		return &sourceIdentity{Provider: "gcp", ProjectID: "cudly-test-project"}
	}
	return &sourceIdentity{}
}

// mockSourceIdentity overrides the cached source identity on a Handler and
// trips the sync.Once so subsequent calls to resolveSourceIdentity return the
// supplied value. Same-package access — only intended for tests.
func mockSourceIdentity(h *Handler, id *sourceIdentity) {
	h.sourceID = id
	h.sourceIdentityOnce.Do(func() {})
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
	// aws-cross-account requires CUDly itself to be running on AWS — the new
	// validateFederationTargetSource guard (#42) rejects this combo otherwise.
	t.Setenv("CUDLY_SOURCE_CLOUD", "aws")
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
	// aws-cross-account requires CUDly itself to be running on AWS — the new
	// validateFederationTargetSource guard (#42) rejects this combo otherwise.
	t.Setenv("CUDLY_SOURCE_CLOUD", "aws")
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
	// aws-cross-account requires CUDly itself to be running on AWS — the new
	// validateFederationTargetSource guard (#42) rejects this combo otherwise.
	t.Setenv("CUDLY_SOURCE_CLOUD", "aws")
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
	t.Setenv("CUDLY_SOURCE_CLOUD", "aws")
	h := federationHandler()
	// Override the federationHandler default ({Provider:"aws", AccountID:"…"}) with
	// an empty AccountID to simulate the STS GetCallerIdentity failure path.
	mockSourceIdentity(h, &sourceIdentity{Provider: "aws", AccountID: ""})

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
	// Pre-warm the source identity to satisfy validateSourceIdentity (#41).
	mockSourceIdentity(h, defaultTestSourceIdentity())

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
	// Pre-warm the source identity to satisfy validateSourceIdentity (#41).
	mockSourceIdentity(h, defaultTestSourceIdentity())

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
			// aws-cross-account requires CUDLY_SOURCE_CLOUD=aws — the
			// validateFederationTargetSource guard (#42) rejects it otherwise.
			if tc.target == "aws" && tc.source == "aws" {
				t.Setenv("CUDLY_SOURCE_CLOUD", "aws")
			}
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
			// aws-cross-account requires CUDLY_SOURCE_CLOUD=aws — the
			// validateFederationTargetSource guard (#42) rejects it otherwise.
			if tc.target == "aws" && tc.source == "aws" {
				t.Setenv("CUDLY_SOURCE_CLOUD", "aws")
			}
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

// ---------------------------------------------------------------------------
// Pre-flight validation: source identity (issue #41) + target/source consistency (issue #42)
// ---------------------------------------------------------------------------

// TestGetFederationIaC_FailsLoudOnEmptyAzureSourceIdentity covers issue #41 for
// the Azure source-cloud path: when CUDly runs on Azure but AZURE_SUBSCRIPTION_ID
// or AZURE_TENANT_ID is missing, the bundle MUST fail with a 500-class error
// naming the missing env var instead of shipping a broken tfvars with empty
// client_id/tenant_id that fails at terraform apply.
func TestGetFederationIaC_FailsLoudOnEmptyAzureSourceIdentity(t *testing.T) {
	cases := []struct {
		name           string
		identity       *sourceIdentity
		wantInErrorMsg string
	}{
		{
			name: "missing-subscription-id",
			identity: &sourceIdentity{
				Provider:       "azure",
				SubscriptionID: "",
				TenantID:       "00000000-0000-0000-0000-000000000002",
				ClientID:       "00000000-0000-0000-0000-000000000003",
			},
			wantInErrorMsg: "AZURE_SUBSCRIPTION_ID",
		},
		{
			name: "missing-tenant-id",
			identity: &sourceIdentity{
				Provider:       "azure",
				SubscriptionID: "00000000-0000-0000-0000-000000000001",
				TenantID:       "",
				ClientID:       "00000000-0000-0000-0000-000000000003",
			},
			wantInErrorMsg: "AZURE_TENANT_ID",
		},
		{
			name: "all-populated-positive-control",
			identity: &sourceIdentity{
				Provider:       "azure",
				SubscriptionID: "00000000-0000-0000-0000-000000000001",
				TenantID:       "00000000-0000-0000-0000-000000000002",
				ClientID:       "00000000-0000-0000-0000-000000000003",
			},
			wantInErrorMsg: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUDLY_SOURCE_CLOUD", "azure")
			h := federationHandler()
			mockSourceIdentity(h, tc.identity)

			_, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
				"target": "azure", "source": "azure", "format": "cli",
			}))
			if tc.wantInErrorMsg == "" {
				require.NoError(t, err, "fully-populated identity must succeed")
				return
			}
			require.Error(t, err, "expected error when %s is empty", tc.wantInErrorMsg)
			// Operator misconfiguration → 500-class, not a client error.
			_, isClientErr := IsClientError(err)
			assert.False(t, isClientErr,
				"missing source-identity env var should produce a 500-class error, not a client error")
			assert.Contains(t, err.Error(), tc.wantInErrorMsg,
				"error must name the missing env var so operators know what to fix")
			assert.Contains(t, err.Error(), "federation iac")
		})
	}
}

// TestGetFederationIaC_FailsLoudOnEmptyGCPSourceIdentity covers issue #41 for
// the GCP source-cloud path: when CUDly runs on GCP but GCP_PROJECT_ID is
// unset, the bundle MUST fail with a 500-class error naming the missing env
// var instead of shipping a broken tfvars with an empty project that fails at
// terraform apply.
func TestGetFederationIaC_FailsLoudOnEmptyGCPSourceIdentity(t *testing.T) {
	t.Setenv("CUDLY_SOURCE_CLOUD", "gcp")
	h := federationHandler()
	mockSourceIdentity(h, &sourceIdentity{Provider: "gcp", ProjectID: ""})

	_, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "gcp", "source": "gcp", "format": "cli",
	}))
	require.Error(t, err, "expected error when GCP_PROJECT_ID is empty")
	_, isClientErr := IsClientError(err)
	assert.False(t, isClientErr,
		"missing GCP_PROJECT_ID should produce a 500-class error, not a client error")
	assert.Contains(t, err.Error(), "GCP_PROJECT_ID",
		"error must name GCP_PROJECT_ID so operators know what to fix")
	assert.Contains(t, err.Error(), "federation iac")
}

// TestGetFederationIaC_RejectsImpossibleTargetSourceCombo covers issues #42 and #140:
// requesting a self-source bundle (target == source) from a CUDly not running on
// the matching cloud must return HTTP 400 — the rendered bundle needs CUDly's own
// cloud identity (account ID / subscription+tenant / project), which a deployment
// on a different cloud cannot supply.
func TestGetFederationIaC_RejectsImpossibleTargetSourceCombo(t *testing.T) {
	cases := []struct {
		name        string
		target      string
		source      string
		sourceCloud string
		wantStatus  int
		wantErrSub  string
	}{
		// aws-cross-account cases (original #42 coverage)
		{
			name:        "cudly-on-azure-rejects-aws-cross-account",
			target:      "aws",
			source:      "aws",
			sourceCloud: "azure",
			wantStatus:  400,
			wantErrSub:  "deployment is on azure",
		},
		{
			name:        "cudly-on-gcp-rejects-aws-cross-account",
			target:      "aws",
			source:      "aws",
			sourceCloud: "gcp",
			wantStatus:  400,
			wantErrSub:  "deployment is on gcp",
		},
		{
			name:        "cudly-on-aws-allows-aws-cross-account",
			target:      "aws",
			source:      "aws",
			sourceCloud: "aws",
			wantStatus:  0, // success
		},
		// azure-self-source cases (new #140 coverage)
		{
			name:        "cudly-on-aws-rejects-azure-self-source",
			target:      "azure",
			source:      "azure",
			sourceCloud: "aws",
			wantStatus:  400,
			wantErrSub:  "deployment is on aws",
		},
		{
			name:        "cudly-on-gcp-rejects-azure-self-source",
			target:      "azure",
			source:      "azure",
			sourceCloud: "gcp",
			wantStatus:  400,
			wantErrSub:  "deployment is on gcp",
		},
		// gcp-self-source cases (new #140 coverage)
		{
			name:        "cudly-on-aws-rejects-gcp-self-source",
			target:      "gcp",
			source:      "gcp",
			sourceCloud: "aws",
			wantStatus:  400,
			wantErrSub:  "deployment is on aws",
		},
		{
			name:        "cudly-on-azure-rejects-gcp-self-source",
			target:      "gcp",
			source:      "gcp",
			sourceCloud: "azure",
			wantStatus:  400,
			wantErrSub:  "deployment is on azure",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUDLY_SOURCE_CLOUD", tc.sourceCloud)
			h := federationHandler()

			_, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
				"target": tc.target, "source": tc.source, "format": "cli",
			}))
			if tc.wantStatus == 0 {
				require.NoError(t, err, "regression guard: matching-cloud self-source must be allowed")
				return
			}
			require.Error(t, err, "expected client error for impossible combo")
			ce, ok := IsClientError(err)
			require.True(t, ok, "must be a client error (400), got %T: %v", err, err)
			assert.Equal(t, tc.wantStatus, ce.code,
				"target/source consistency rejection must be a 400")
			assert.Contains(t, err.Error(), "self-source requires CUDly to be deployed on",
				"error must explain the constraint")
			assert.Contains(t, err.Error(), tc.wantErrSub,
				"error must name the actual deployment cloud")
		})
	}
}

// ---------------------------------------------------------------------------
// Extended matrix: CLI renders, ARM renders, single-file shape (issue #316)
// ---------------------------------------------------------------------------

// canonicalPurchaseActions is a subset of the permission actions that every
// AWS-target CLI render must include. The full list is embedded in the
// template as a heredoc; we pin the most security-relevant entries so a
// template edit that accidentally drops a purchase permission is caught here.
var canonicalPurchaseActions = []string{
	"ec2:PurchaseReservedInstancesOffering",
	"rds:PurchaseReservedDBInstancesOffering",
	"savingsplans:CreateSavingsPlan",
}

// canonicalReadActions is a subset of the read/describe permissions that
// every AWS-target CLI render must include alongside the purchase actions.
var canonicalReadActions = []string{
	"ec2:DescribeReservedInstances",
	"rds:DescribeReservedDBInstances",
	"savingsplans:DescribeSavingsPlans",
}

// TestGetFederationIaC_CLIMatrix is a comprehensive table-driven test for
// all CLI target/source combinations. It verifies:
//   - ContentType is text/x-shellscript (single-file shape, no zip).
//   - ContentEncoding is empty (not base64-wrapped like zip formats).
//   - Output starts with the bash shebang.
//   - Filename matches the expected pattern.
//   - Archera opt-in is absent by default (default-off guarantee).
//   - For AWS targets: canonical purchase and read actions are present.
//   - Contact email from the session does not appear raw in the script
//     outside the CONTACT_EMAIL assignment (no PII leak into script body).
func TestGetFederationIaC_CLIMatrix(t *testing.T) {
	cases := []struct {
		name             string
		target           string
		source           string
		sourceCloud      string
		wantFileSuffix   string
		wantContent      []string
		wantNoContent    []string
		checkPermissions bool // true for AWS-target cases that embed IAM policy
	}{
		{
			name:             "aws-cross-account",
			target:           "aws",
			source:           "aws",
			sourceCloud:      "aws",
			wantFileSuffix:   "aws-cross-account-cli.sh",
			wantContent:      []string{"#!/usr/bin/env bash", "aws iam create-role", "SOURCE_ACCOUNT_ID"},
			wantNoContent:    []string{"archera", "Archera"},
			checkPermissions: true,
		},
		{
			name:             "aws-wif-from-azure",
			target:           "aws",
			source:           "azure",
			sourceCloud:      "gcp",
			wantFileSuffix:   "aws-wif-cli.sh",
			wantContent:      []string{"#!/usr/bin/env bash", "create-open-id-connect-provider", "login.microsoftonline.com"},
			wantNoContent:    []string{"archera", "Archera"},
			checkPermissions: true,
		},
		{
			name:             "aws-wif-from-gcp",
			target:           "aws",
			source:           "gcp",
			sourceCloud:      "gcp",
			wantFileSuffix:   "aws-wif-cli.sh",
			wantContent:      []string{"#!/usr/bin/env bash", "create-open-id-connect-provider"},
			wantNoContent:    []string{"archera", "Archera"},
			checkPermissions: true,
		},
		{
			name:             "azure-wif-from-aws",
			target:           "azure",
			source:           "aws",
			sourceCloud:      "gcp",
			wantFileSuffix:   "azure-wif-cli.sh",
			wantContent:      []string{"#!/usr/bin/env bash", "az ad app create", "Reservation Purchaser"},
			wantNoContent:    []string{"archera", "Archera"},
			checkPermissions: false,
		},
		{
			name:             "azure-wif-from-gcp",
			target:           "azure",
			source:           "gcp",
			sourceCloud:      "gcp",
			wantFileSuffix:   "azure-wif-cli.sh",
			wantContent:      []string{"#!/usr/bin/env bash", "az ad app create"},
			wantNoContent:    []string{"archera", "Archera"},
			checkPermissions: false,
		},
		{
			name:             "gcp-sa-impersonation",
			target:           "gcp",
			source:           "gcp",
			sourceCloud:      "gcp",
			wantFileSuffix:   "gcp-sa-impersonation-cli.sh",
			wantContent:      []string{"#!/usr/bin/env bash", "gcloud iam service-accounts", "SOURCE_SERVICE_ACCOUNT"},
			wantNoContent:    []string{"archera", "Archera"},
			checkPermissions: false,
		},
		{
			name:             "gcp-wif-from-aws",
			target:           "gcp",
			source:           "aws",
			sourceCloud:      "gcp",
			wantFileSuffix:   "gcp-wif-cli.sh",
			wantContent:      []string{"#!/usr/bin/env bash", "workload-identity-pools create", "providers create-oidc"},
			wantNoContent:    []string{"archera", "Archera", "create-aws"},
			checkPermissions: false,
		},
		{
			name:             "gcp-wif-from-azure",
			target:           "gcp",
			source:           "azure",
			sourceCloud:      "gcp",
			wantFileSuffix:   "gcp-wif-cli.sh",
			wantContent:      []string{"#!/usr/bin/env bash", "workload-identity-pools create", "providers create-oidc"},
			wantNoContent:    []string{"archera", "Archera", "create-aws"},
			checkPermissions: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUDLY_SOURCE_CLOUD", tc.sourceCloud)
			h := federationHandler()

			// Use federationReqWithDomain so CUDlyAPIURL is set and the
			// registration block renders — we need the full output to verify PII.
			res, err := h.getFederationIaC(context.Background(), federationReqWithDomain(map[string]string{
				"target": tc.target, "source": tc.source, "format": "cli",
			}))
			require.NoError(t, err)

			// Single-file shape: must NOT be base64-encoded (no zip wrapping).
			assert.Empty(t, res.ContentEncoding,
				"CLI render must be plain text, not base64-encoded")
			assert.Equal(t, "text/x-shellscript", res.ContentType,
				"CLI render must have text/x-shellscript content-type")
			assert.Contains(t, res.Filename, tc.wantFileSuffix,
				"CLI render filename must contain expected pattern")

			// Content assertions.
			for _, needle := range tc.wantContent {
				assert.Contains(t, res.Content, needle,
					"CLI render must contain %q", needle)
			}
			for _, needle := range tc.wantNoContent {
				assert.NotContains(t, res.Content, needle,
					"CLI render must NOT contain %q (Archera default-off)", needle)
			}

			// Permission set for AWS-target renders.
			if tc.checkPermissions {
				for _, action := range canonicalPurchaseActions {
					assert.Contains(t, res.Content, action,
						"AWS CLI render must include purchase action %q", action)
				}
				for _, action := range canonicalReadActions {
					assert.Contains(t, res.Content, action,
						"AWS CLI render must include read action %q", action)
				}
			}

			// PII guard: the session email must not appear raw in the script
			// body outside the CONTACT_EMAIL env-var assignment. The email
			// is legitimately present as the default value for CONTACT_EMAIL
			// (e.g. CONTACT_EMAIL="${CUDLY_CONTACT_EMAIL:-admin@example.com}"),
			// but it must not be repeated elsewhere (e.g. embedded in a URL,
			// a hardcoded JSON field, or a curl argument).
			//
			// Strategy: strip the CONTACT_EMAIL assignment line and verify
			// the email does not appear in the remaining content.
			sessionEmail := "admin@example.com"
			scriptWithoutAssignment := strings.ReplaceAll(res.Content,
				`CONTACT_EMAIL="${CUDLY_CONTACT_EMAIL:-`+sessionEmail+`}"`,
				"CONTACT_EMAIL_ASSIGNMENT_REMOVED")
			assert.NotContains(t, scriptWithoutAssignment, sessionEmail,
				"session email must not appear in CLI script body outside CONTACT_EMAIL assignment (PII leak)")
		})
	}
}

// TestGetFederationIaC_ARMMatrix is a comprehensive table-driven test for
// ARM renders across all valid source-cloud combinations. ARM is always a
// zip containing the azure-wif.arm.json template. The matrix verifies:
//   - The zip contains the expected files.
//   - The ARM JSON is structurally valid (schema, parameters, resources, outputs).
//   - The role assignment resource targets the Reservation Purchaser role.
//   - Archera opt-in is absent from all ARM artifacts (default-off guarantee).
//   - Deploy script contains the registration block.
func TestGetFederationIaC_ARMMatrix(t *testing.T) {
	armSources := []string{"aws", "gcp"} // azure-self-source requires sourceCloud=azure
	for _, source := range armSources {
		source := source
		t.Run("target=azure/source="+source, func(t *testing.T) {
			h := federationHandler()

			res, err := h.getFederationIaC(context.Background(), federationReqWithDomain(map[string]string{
				"target": "azure", "source": source, "format": "arm",
			}))
			require.NoError(t, err)

			// Zip shape.
			assert.Equal(t, "application/zip", res.ContentType)
			assert.Equal(t, "base64", res.ContentEncoding)
			assert.Contains(t, res.Filename, "azure-wif-arm.zip")

			files := unzipResponse(t, res)

			// Required files.
			require.Contains(t, files, "azure-wif.arm.json", "ARM zip must contain azure-wif.arm.json")
			require.Contains(t, files, "azure-wif-bicep-params.json", "ARM zip must contain params file")
			require.Contains(t, files, "deploy-azure.sh", "ARM zip must contain deploy script")
			require.Contains(t, files, "README.txt", "ARM zip must contain README")

			// ARM template structure.
			var armTemplate map[string]any
			require.NoError(t, json.Unmarshal(files["azure-wif.arm.json"], &armTemplate),
				"ARM template must be valid JSON")
			assert.Contains(t, armTemplate, "$schema", "ARM template must have $schema field")
			assert.Contains(t, armTemplate, "parameters", "ARM template must have parameters")
			assert.Contains(t, armTemplate, "resources", "ARM template must have resources")
			assert.Contains(t, armTemplate, "outputs", "ARM template must have outputs")

			// Schema must be an ARM subscription-deployment schema.
			schema, _ := armTemplate["$schema"].(string)
			assert.Contains(t, schema, "subscriptionDeploymentTemplate",
				"ARM template must use subscription-scope deployment schema")

			// Resources must contain a role assignment for Reservation Purchaser.
			resourcesRaw, _ := armTemplate["resources"].([]any)
			require.NotEmpty(t, resourcesRaw, "ARM template must have at least one resource")
			armJSON := string(files["azure-wif.arm.json"])
			assert.Contains(t, armJSON, "Microsoft.Authorization/roleAssignments",
				"ARM template must include role assignment resource")
			// f7b75c60 is the well-known Reservation Purchaser role definition ID.
			assert.Contains(t, armJSON, "f7b75c60",
				"ARM template must reference the Reservation Purchaser role definition ID")

			// Archera default-off: no Archera references in any ARM artifact.
			for name, content := range files {
				assert.NotContains(t, strings.ToLower(string(content)), "archera",
					"ARM artifact %q must not reference Archera (default-off guarantee)", name)
			}

			// Params file must be valid JSON with a parameters key.
			var params map[string]any
			require.NoError(t, json.Unmarshal(files["azure-wif-bicep-params.json"], &params),
				"ARM params file must be valid JSON")
			assert.Contains(t, params, "parameters", "ARM params file must have parameters key")

			// Deploy script must have the registration block (CUDlyAPIURL is set via domain).
			deployScript := string(files["deploy-azure.sh"])
			assert.Contains(t, deployScript, "/api/register",
				"deploy script must include registration curl call")
			assert.Contains(t, deployScript, `case "$HTTP_CODE"`,
				"deploy script must handle HTTP response codes")
		})
	}
}

// TestGetFederationIaC_SingleFileRenderShape verifies that single-file
// (non-zip) renders always produce a plain-text response with no
// ContentEncoding — the caller can write the Content bytes directly to
// a file without any decoding step. This is the invariant that
// distinguishes CLI from zip formats (bundle/cfn/bicep/arm).
func TestGetFederationIaC_SingleFileRenderShape(t *testing.T) {
	// Every format that singleFileSpec accepts must produce a plain-text
	// response. Currently only "cli" qualifies.
	h := federationHandler()

	res, err := h.getFederationIaC(context.Background(), federationReq(map[string]string{
		"target": "aws", "source": "gcp", "format": "cli",
	}))
	require.NoError(t, err)

	// Single-file invariants.
	assert.Empty(t, res.ContentEncoding,
		"single-file render must have no ContentEncoding (caller writes bytes directly)")
	assert.Equal(t, "text/x-shellscript", res.ContentType,
		"CLI single-file render must be text/x-shellscript")
	assert.NotEmpty(t, res.Content,
		"single-file render must have non-empty Content")
	assert.NotEmpty(t, res.Filename,
		"single-file render must have a non-empty Filename")

	// Content must be readable as UTF-8 text (no binary content).
	assert.True(t, strings.HasPrefix(res.Content, "#!/usr/bin/env bash"),
		"CLI single-file render must start with bash shebang")
}

// TestGetFederationIaC_CLIArcheraDefaultOff is a dedicated regression test
// for the Archera default-off guarantee across all CLI formats. Even if a
// future template change adds Archera opt-in logic, the default render
// (no special request params) must not include any Archera references.
func TestGetFederationIaC_CLIArcheraDefaultOff(t *testing.T) {
	cliCombos := []struct{ target, source, sourceCloud string }{
		{"aws", "aws", "aws"},
		{"aws", "gcp", "gcp"},
		{"azure", "aws", "gcp"},
		{"gcp", "gcp", "gcp"},
		{"gcp", "aws", "gcp"},
	}

	for _, tc := range cliCombos {
		tc := tc
		t.Run(tc.target+"/"+tc.source, func(t *testing.T) {
			t.Setenv("CUDLY_SOURCE_CLOUD", tc.sourceCloud)
			h := federationHandler()

			res, err := h.getFederationIaC(context.Background(), federationReqWithDomain(map[string]string{
				"target": tc.target, "source": tc.source, "format": "cli",
			}))
			require.NoError(t, err)

			lowerContent := strings.ToLower(res.Content)
			assert.NotContains(t, lowerContent, "archera",
				"CLI render for target=%s/source=%s must not reference Archera by default",
				tc.target, tc.source)
		})
	}
}

// TestValidateFederationTargetSource unit-tests the guard directly, covering
// all self-source rejection and allow cases (issues #42 and #140).
func TestValidateFederationTargetSource(t *testing.T) {
	cases := []struct {
		name        string
		target      string
		source      string
		sourceCloud string
		wantErr     bool
		wantCode    int
		wantSub     string
	}{
		// Self-source combos on the correct cloud: allowed
		{name: "aws-self-source-on-aws", target: "aws", source: "aws", sourceCloud: "aws", wantErr: false},
		{name: "azure-self-source-on-azure", target: "azure", source: "azure", sourceCloud: "azure", wantErr: false},
		{name: "gcp-self-source-on-gcp", target: "gcp", source: "gcp", sourceCloud: "gcp", wantErr: false},
		// Self-source on mismatched cloud: rejected
		{name: "aws-self-source-on-azure", target: "aws", source: "aws", sourceCloud: "azure", wantErr: true, wantCode: 400, wantSub: "deployment is on azure"},
		{name: "aws-self-source-on-gcp", target: "aws", source: "aws", sourceCloud: "gcp", wantErr: true, wantCode: 400, wantSub: "deployment is on gcp"},
		{name: "azure-self-source-on-aws", target: "azure", source: "azure", sourceCloud: "aws", wantErr: true, wantCode: 400, wantSub: "deployment is on aws"},
		{name: "azure-self-source-on-gcp", target: "azure", source: "azure", sourceCloud: "gcp", wantErr: true, wantCode: 400, wantSub: "deployment is on gcp"},
		{name: "gcp-self-source-on-aws", target: "gcp", source: "gcp", sourceCloud: "aws", wantErr: true, wantCode: 400, wantSub: "deployment is on aws"},
		{name: "gcp-self-source-on-azure", target: "gcp", source: "gcp", sourceCloud: "azure", wantErr: true, wantCode: 400, wantSub: "deployment is on azure"},
		// WIF combos (target != source): always allowed regardless of deployment cloud
		{name: "azure-wif-from-aws", target: "azure", source: "aws", sourceCloud: "aws", wantErr: false},
		{name: "azure-wif-from-gcp", target: "azure", source: "gcp", sourceCloud: "gcp", wantErr: false},
		{name: "gcp-wif-from-aws", target: "gcp", source: "aws", sourceCloud: "aws", wantErr: false},
		{name: "gcp-wif-from-azure", target: "gcp", source: "azure", sourceCloud: "azure", wantErr: false},
		{name: "aws-wif-from-azure", target: "aws", source: "azure", sourceCloud: "azure", wantErr: false},
		{name: "aws-wif-from-gcp", target: "aws", source: "gcp", sourceCloud: "gcp", wantErr: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUDLY_SOURCE_CLOUD", tc.sourceCloud)
			err := validateFederationTargetSource(tc.target, tc.source)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			ce, ok := IsClientError(err)
			require.True(t, ok, "must be ClientError, got %T: %v", err, err)
			assert.Equal(t, tc.wantCode, ce.code)
			assert.Contains(t, err.Error(), tc.wantSub)
		})
	}
}
