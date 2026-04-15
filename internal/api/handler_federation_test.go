package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func federationHandler() *Handler {
	return NewHandler(HandlerConfig{ConfigStore: new(MockConfigStore)})
}

func federationReq(params map[string]string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{QueryStringParameters: params}
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
