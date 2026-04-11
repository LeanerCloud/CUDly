package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixed UUIDs for federation test accounts.
const (
	fedAwsAccountID   = "11111111-1111-1111-1111-111111111111"
	fedGCPAccountID   = "22222222-2222-2222-2222-222222222222"
	fedAzureAccountID = "33333333-3333-3333-3333-333333333333"
	fedMissingUUID    = "00000000-0000-0000-0000-000000000000"
)

// awsAccount returns a minimal AWS CloudAccount for use in federation tests.
func awsAccount() *config.CloudAccount {
	return &config.CloudAccount{
		ID:         fedAwsAccountID,
		Name:       "prod-aws",
		ExternalID: "123456789012",
		Provider:   "aws",
	}
}

// gcpAccount returns a minimal GCP CloudAccount for use in federation tests.
func gcpAccount() *config.CloudAccount {
	return &config.CloudAccount{
		ID:           fedGCPAccountID,
		Name:         "prod-gcp",
		ExternalID:   "my-gcp-project",
		Provider:     "gcp",
		GCPProjectID: "my-gcp-project",
	}
}

// azureAccount returns a minimal Azure CloudAccount for use in federation tests.
func azureAccount() *config.CloudAccount {
	return &config.CloudAccount{
		ID:                  fedAzureAccountID,
		Name:                "prod-azure",
		ExternalID:          "sub-aabbccdd",
		Provider:            "azure",
		AzureSubscriptionID: "sub-aabbccdd",
		AzureTenantID:       "tenant-1234",
	}
}

func federationHandler(acct *config.CloudAccount) *Handler {
	store := new(MockConfigStore)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return acct, nil
	}
	return NewHandler(HandlerConfig{ConfigStore: store})
}

func federationReq(params map[string]string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{QueryStringParameters: params}
}

// ---------------------------------------------------------------------------
// singleFileSpec tests
// ---------------------------------------------------------------------------

func TestSingleFileSpec_AWSCrossAccount(t *testing.T) {
	tmpl, fname, ct, err := singleFileSpec("aws", "aws", "", "prod")
	require.NoError(t, err)
	assert.Contains(t, tmpl, "aws-cross-account.tfvars.tmpl")
	assert.Contains(t, fname, "aws-cross-account.tfvars")
	assert.Equal(t, "text/plain", ct)
}

func TestSingleFileSpec_AWSWIFCFParams(t *testing.T) {
	tmpl, fname, ct, err := singleFileSpec("aws", "azure", "cf-params", "prod")
	require.NoError(t, err)
	assert.Contains(t, tmpl, "aws-wif-cf-params.json.tmpl")
	assert.Contains(t, fname, "cf-params.json")
	assert.Equal(t, "application/json", ct)
}

func TestSingleFileSpec_AzureWIF(t *testing.T) {
	_, fname, _, err := singleFileSpec("azure", "aws", "", "prod")
	require.NoError(t, err)
	assert.Contains(t, fname, "azure-wif.tfvars")
}

func TestSingleFileSpec_GCPSAImpersonation(t *testing.T) {
	_, fname, _, err := singleFileSpec("gcp", "gcp", "", "prod")
	require.NoError(t, err)
	assert.Contains(t, fname, "gcp-sa-impersonation.tfvars")
}

func TestSingleFileSpec_GCPWif(t *testing.T) {
	_, fname, _, err := singleFileSpec("gcp", "aws", "", "prod")
	require.NoError(t, err)
	assert.Contains(t, fname, "gcp-wif.tfvars")
}

func TestSingleFileSpec_UnknownTarget(t *testing.T) {
	_, _, _, err := singleFileSpec("unknown", "aws", "", "prod")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// getFederationIaC handler tests
// ---------------------------------------------------------------------------

func TestGetFederationIaC_MissingParams(t *testing.T) {
	h := federationHandler(awsAccount())
	ctx := context.Background()

	_, err := h.getFederationIaC(ctx, federationReq(map[string]string{}))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a client error")
	assert.Equal(t, 400, ce.code)
}

func TestGetFederationIaC_MissingAccountID(t *testing.T) {
	h := federationHandler(awsAccount())
	ctx := context.Background()

	_, err := h.getFederationIaC(ctx, federationReq(map[string]string{"target": "aws", "source": "azure"}))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a client error")
	assert.Equal(t, 400, ce.code)
}

func TestGetFederationIaC_AccountNotFound(t *testing.T) {
	store := new(MockConfigStore)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return nil, nil
	}
	h := NewHandler(HandlerConfig{ConfigStore: store})
	ctx := context.Background()

	// fedMissingUUID is a valid UUID format but the store returns nil → 404.
	_, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "azure", "account_id": fedMissingUUID,
	}))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a client error")
	assert.Equal(t, 404, ce.code)
}

func TestGetFederationIaC_AWSCrossAccount_Tfvars(t *testing.T) {
	h := federationHandler(awsAccount())
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "aws", "account_id": fedAwsAccountID,
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "aws-cross-account.tfvars")
	assert.Empty(t, res.ContentEncoding)
	assert.Contains(t, res.Content, "role_name")
}

func TestGetFederationIaC_AWSWIF_Tfvars(t *testing.T) {
	h := federationHandler(awsAccount())
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "azure", "account_id": fedAwsAccountID,
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "aws-wif.tfvars")
	assert.Contains(t, res.Content, "oidc_issuer_url")
}

func TestGetFederationIaC_AWSWIF_CFParams(t *testing.T) {
	acct := awsAccount()
	acct.AzureTenantID = "tenant-abc"
	h := federationHandler(acct)
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "azure", "account_id": fedAwsAccountID, "format": "cf-params",
	}))
	require.NoError(t, err)
	assert.Contains(t, res.Filename, "cf-params.json")
	assert.Equal(t, "application/json", res.ContentType)
	var params []map[string]string
	require.NoError(t, json.Unmarshal([]byte(res.Content), &params))
}

func TestGetFederationIaC_Bundle_AWSCrossAccount(t *testing.T) {
	h := federationHandler(awsAccount())
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "aws", "account_id": fedAwsAccountID, "format": "bundle",
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
	// No CloudFormation files for aws→aws
	assert.False(t, names["cloudformation/template.yaml"], "aws→aws bundle must not contain CF template")
}

func TestGetFederationIaC_Bundle_AWSWif(t *testing.T) {
	h := federationHandler(awsAccount())
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "gcp", "account_id": fedAwsAccountID, "format": "bundle",
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
	h := federationHandler(gcpAccount())
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "gcp", "source": "gcp", "account_id": fedGCPAccountID, "format": "bundle",
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

func TestGetFederationIaC_UnknownTarget(t *testing.T) {
	h := federationHandler(awsAccount())
	ctx := context.Background()

	_, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "badcloud", "source": "aws", "account_id": fedAwsAccountID,
	}))
	require.Error(t, err)
}

func TestGetFederationIaC_InvalidSource(t *testing.T) {
	h := federationHandler(awsAccount())
	ctx := context.Background()

	_, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "badcloud", "account_id": fedAwsAccountID,
	}))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a client error")
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "source must be")
}

func TestGetFederationIaC_CFParams_ValidJSON(t *testing.T) {
	// Account with characters that would break text/template JSON rendering.
	acct := awsAccount()
	acct.Name = `test "account" with $pecial chars`
	acct.AzureTenantID = "tenant-abc"
	h := federationHandler(acct)
	ctx := context.Background()

	res, err := h.getFederationIaC(ctx, federationReq(map[string]string{
		"target": "aws", "source": "azure", "account_id": fedAwsAccountID, "format": "cf-params",
	}))
	require.NoError(t, err)
	assert.Equal(t, "application/json", res.ContentType)

	// Verify the output is valid JSON.
	var params []map[string]string
	require.NoError(t, json.Unmarshal([]byte(res.Content), &params), "CF params must be valid JSON even with special characters in account name")
	assert.GreaterOrEqual(t, len(params), 3, "should have at least 3 parameters")
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
