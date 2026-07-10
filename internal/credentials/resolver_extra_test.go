package credentials

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubOIDCSigner is a minimal oidc.Signer whose methods must never be
// reached in the tests that use it — it only exists so GCPResolveOptions
// looks federated-capable.
type stubOIDCSigner struct{}

func (stubOIDCSigner) Sign(context.Context, []byte) ([]byte, error) {
	return nil, assert.AnError
}
func (stubOIDCSigner) PublicKey(context.Context) (crypto.PublicKey, error) {
	return nil, assert.AnError
}
func (stubOIDCSigner) KeyID(context.Context) (string, error) {
	return "", assert.AnError
}

var _ oidc.Signer = stubOIDCSigner{}

// ---------------------------------------------------------------------------
// resolveBastionProvider
// ---------------------------------------------------------------------------

func TestResolveBastionProvider_NoBastion(t *testing.T) {
	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "bastion",
		// AWSBastionID deliberately left empty
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aws_bastion_id is required")
}

func TestResolveBastionProvider_WithBastionAndRoleARN(t *testing.T) {
	account := &config.CloudAccount{
		ID:           "acct1",
		AWSAuthMode:  "bastion",
		AWSBastionID: "bastion-acct",
		AWSRoleARN:   "arn:aws:iam::123456789012:role/CUDly",
	}
	provider, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestResolveBastionProvider_NoRoleARN(t *testing.T) {
	// bastion mode (legacy fallback) delegates to resolveRoleARNProvider with nil
	// ambient; an empty ARN now returns the ambient-creds error instead of the
	// old "is required" message.
	account := &config.CloudAccount{
		ID:           "acct1",
		AWSAuthMode:  "bastion",
		AWSBastionID: "bastion-acct",
		AWSRoleARN:   "",
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aws_role_arn is empty and no ambient credentials available")
}

// ---------------------------------------------------------------------------
// resolveWebIdentityProvider
// ---------------------------------------------------------------------------

func TestResolveWebIdentityProvider_NoRoleARN(t *testing.T) {
	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "workload_identity_federation",
		AWSRoleARN:  "",
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aws_role_arn required")
}

func TestResolveWebIdentityProvider_NoTokenFile(t *testing.T) {
	// Unset env var to ensure we get the "token file required" error.
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")

	account := &config.CloudAccount{
		ID:                      "acct1",
		AWSAuthMode:             "workload_identity_federation",
		AWSRoleARN:              "arn:aws:iam::123456789012:role/CUDly",
		AWSWebIdentityTokenFile: "",
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aws_web_identity_token_file required")
}

func TestResolveWebIdentityProvider_TokenFileFromEnv(t *testing.T) {
	f, err := os.CreateTemp("", "wif-token-*.txt")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	f.WriteString("dummy-oidc-token")
	f.Close()

	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", f.Name())

	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "workload_identity_federation",
		AWSRoleARN:  "arn:aws:iam::123456789012:role/CUDly",
	}
	provider, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestResolveWebIdentityProvider_TokenFileFromAccount(t *testing.T) {
	f, err := os.CreateTemp("", "wif-token-*.txt")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	f.WriteString("dummy-oidc-token")
	f.Close()

	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")

	account := &config.CloudAccount{
		ID:                      "acct1",
		AWSAuthMode:             "workload_identity_federation",
		AWSRoleARN:              "arn:aws:iam::123456789012:role/CUDly",
		AWSWebIdentityTokenFile: f.Name(),
	}
	provider, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

// ---------------------------------------------------------------------------
// ResolveAzureTokenCredential
// ---------------------------------------------------------------------------

func TestResolveAzureTokenCredential_NilStore_ClientSecret(t *testing.T) {
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "client_secret",
		AzureTenantID: "tenant1",
		AzureClientID: "client1",
	}
	_, err := ResolveAzureTokenCredential(context.Background(), account, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credential store required")
}

func TestResolveAzureTokenCredential_MissingTenantOrClient(t *testing.T) {
	store := newMockStore()
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "client_secret",
		AzureTenantID: "",
		AzureClientID: "",
	}
	_, err := ResolveAzureTokenCredential(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "azure_tenant_id and azure_client_id required")
}

func TestResolveAzureTokenCredential_ClientSecret_NoStoredCred(t *testing.T) {
	store := newMockStore() // no credentials stored
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "client_secret",
		AzureTenantID: "tenant1",
		AzureClientID: "client1",
	}
	_, err := ResolveAzureTokenCredential(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no client secret stored")
}

func TestResolveAzureTokenCredential_ClientSecret_ValidCred(t *testing.T) {
	store := newMockStore()
	payload, _ := json.Marshal(map[string]string{"client_secret": "test-secret"})
	store.data["acct1/azure_client_secret"] = payload

	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "client_secret",
		// Use valid UUID-format GUIDs that azidentity accepts.
		AzureTenantID: "00000000-0000-0000-0000-000000000001",
		AzureClientID: "00000000-0000-0000-0000-000000000002",
	}
	cred, err := ResolveAzureTokenCredential(context.Background(), account, store)
	require.NoError(t, err)
	assert.NotNil(t, cred)
}

func TestResolveAzureTokenCredential_WIF_NilStore(t *testing.T) {
	// Azure WIF no longer loads anything from the credential store, so a
	// nil store is no longer an error by itself. What still errors is
	// the absence of an OIDC signer (zero-opts caller) — same code path
	// as TestResolveAzureTokenCredential_WIF_NoSigner, just demonstrated
	// with store=nil.
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "workload_identity_federation",
	}
	_, err := ResolveAzureTokenCredential(context.Background(), account, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires a wired OIDC signer")
}

func TestResolveAzureTokenCredential_WIF_NoSigner(t *testing.T) {
	// With the legacy cert path removed, Azure WIF mode can only be
	// resolved via the KMS-backed OIDC signer. A zero-opts caller
	// (ResolveAzureTokenCredential) now errors cleanly instead of
	// falling back to a cert-based assertion.
	store := newMockStore()
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "workload_identity_federation",
	}
	_, err := ResolveAzureTokenCredential(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires a wired OIDC signer")
}

// ---------------------------------------------------------------------------
// ResolveGCPTokenSource
// ---------------------------------------------------------------------------

func TestResolveGCPTokenSource_ApplicationDefault_ReturnsNil(t *testing.T) {
	account := &config.CloudAccount{
		ID:          "acct1",
		GCPAuthMode: "application_default",
	}
	src, err := ResolveGCPTokenSource(context.Background(), account, nil)
	require.NoError(t, err)
	assert.Nil(t, src)
}

func TestResolveGCPTokenSource_NilStore(t *testing.T) {
	account := &config.CloudAccount{
		ID:          "acct1",
		GCPAuthMode: "service_account_key",
	}
	_, err := ResolveGCPTokenSource(context.Background(), account, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credential store required")
}

func TestResolveGCPTokenSource_NoStoredCredentials(t *testing.T) {
	store := newMockStore()
	account := &config.CloudAccount{
		ID:          "acct1",
		GCPAuthMode: "service_account_key",
	}
	_, err := ResolveGCPTokenSource(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no gcp credentials stored")
}

func TestResolveGCPTokenSource_WIF_NoStoredCredentials(t *testing.T) {
	store := newMockStore()
	account := &config.CloudAccount{
		ID:          "acct1",
		GCPAuthMode: "workload_identity_federation",
	}
	_, err := ResolveGCPTokenSource(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no gcp credentials stored")
}

// TestResolveGCPTokenSource_WIF_StoreErrorFailsLoud pins the fail-loud
// contract on the WIF peek: a LoadRaw failure must propagate, NOT be
// treated as "no stored credential" — otherwise a store outage would
// silently flip a stored-JSON WIF account onto the federated path (the
// options here are deliberately federated-capable so the pre-fix code
// would have taken that path and returned no error).
func TestResolveGCPTokenSource_WIF_StoreErrorFailsLoud(t *testing.T) {
	store := newMockStore()
	store.err = assert.AnError

	account := &config.CloudAccount{
		ID:             "acct1",
		GCPAuthMode:    "workload_identity_federation",
		GCPWIFAudience: "//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/p/providers/x",
		GCPClientEmail: "sa@proj.iam.gserviceaccount.com",
	}
	opts := GCPResolveOptions{
		Signer:    stubOIDCSigner{},
		IssuerURL: "https://cudly.example.com/oidc",
	}

	_, err := ResolveGCPTokenSourceWithOpts(context.Background(), account, store, opts)
	require.Error(t, err, "store failure must fail loud, not fall through to the federated path")
	assert.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "LoadRaw WIF config")
}

func TestResolveGCPTokenSource_InvalidJSON(t *testing.T) {
	store := newMockStore()
	store.data["acct1/gcp_service_account"] = []byte("not valid json")

	account := &config.CloudAccount{
		ID:          "acct1",
		GCPAuthMode: "service_account_key",
	}
	_, err := ResolveGCPTokenSource(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse gcp credentials")
}

// ---------------------------------------------------------------------------
// Additional coverage for already-partially-covered functions
// ---------------------------------------------------------------------------

func TestResolveGCPCredentials_LoadError(t *testing.T) {
	store := newMockStore()
	store.err = assert.AnError

	account := &config.CloudAccount{ID: "acct1"}
	_, err := ResolveGCPCredentials(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "load gcp service account")
}

func TestResolveGCPCredentials_NotStored(t *testing.T) {
	store := newMockStore()
	account := &config.CloudAccount{ID: "acct1"}
	_, err := ResolveGCPCredentials(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no service account stored")
}

func TestResolveAzureCredentials_LoadError(t *testing.T) {
	store := newMockStore()
	store.err = assert.AnError

	account := &config.CloudAccount{ID: "acct1"}
	_, err := ResolveAzureCredentials(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "load azure secret")
}

func TestResolveAzureCredentials_InvalidJSON(t *testing.T) {
	store := newMockStore()
	store.data["acct1/azure_client_secret"] = []byte("not json")

	account := &config.CloudAccount{ID: "acct1"}
	_, err := ResolveAzureCredentials(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse azure secret")
}

func TestResolveAccessKeyProvider_LoadError(t *testing.T) {
	store := newMockStore()
	store.err = assert.AnError

	account := &config.CloudAccount{ID: "acct1", AWSAuthMode: "access_keys"}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, store, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "load access keys")
}

func TestResolveAccessKeyProvider_InvalidJSON(t *testing.T) {
	store := newMockStore()
	store.data["acct1/aws_access_keys"] = []byte("not json")

	account := &config.CloudAccount{ID: "acct1", AWSAuthMode: "access_keys"}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, store, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse access key payload")
}

func TestResolveRoleARNProvider_LongID_WithExternalID(t *testing.T) {
	// Exercise the branch where account.ID > 8 chars (sessionSuffix truncated)
	// and AWSExternalID is set.
	account := &config.CloudAccount{
		ID:            "a-very-long-account-id",
		AWSAuthMode:   "role_arn",
		AWSRoleARN:    "arn:aws:iam::123456789012:role/CUDly",
		AWSExternalID: "ext-id-123",
	}
	provider, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestResolveWebIdentityProvider_LongID_WithTokenFile(t *testing.T) {
	// Exercises resolveWebIdentityProvider with a long account ID (truncation branch)
	// and with the token file path set in the account struct.
	f, err := os.CreateTemp("", "wif-token-*.txt")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	f.WriteString("dummy-oidc-token")
	f.Close()

	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")

	account := &config.CloudAccount{
		ID:                      "a-very-long-account-id",
		AWSAuthMode:             "workload_identity_federation",
		AWSRoleARN:              "arn:aws:iam::123456789012:role/CUDly",
		AWSWebIdentityTokenFile: f.Name(),
	}
	provider, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestResolveAzureTokenCredential_ManagedIdentity(t *testing.T) {
	// managed_identity mode creates a ManagedIdentityCredential; no store needed.
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "managed_identity",
	}
	cred, err := ResolveAzureTokenCredential(context.Background(), account, nil)
	require.NoError(t, err)
	assert.NotNil(t, cred)
}

func TestResolveGCPTokenSource_WIF_WithStoredConfig(t *testing.T) {
	// Exercises the WIF branch where CredTypeGCPWIFConfig is used as the key.
	// The JSON is intentionally invalid so google.CredentialsFromJSON fails,
	// but we cover the credType selection branch.
	store := newMockStore()
	store.data["acct1/gcp_workload_identity_config"] = []byte("not valid json")

	account := &config.CloudAccount{
		ID:          "acct1",
		GCPAuthMode: "workload_identity_federation",
	}
	_, err := ResolveGCPTokenSource(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse gcp credentials")
}

func TestResolveGCPTokenSource_ServiceAccountKey_ValidJSON(t *testing.T) {
	// A minimal valid GCP service account JSON that google.CredentialsFromJSON accepts.
	saJSON := []byte(`{
		"type": "service_account",
		"project_id": "test-project",
		"private_key_id": "key-id",
		"private_key": "` + minimalRSAPEM() + `",
		"client_email": "test@test-project.iam.gserviceaccount.com",
		"client_id": "123456789",
		"auth_uri": "https://accounts.google.com/o/oauth2/auth",
		"token_uri": "https://oauth2.googleapis.com/token"
	}`)

	store := newMockStore()
	store.data["acct1/gcp_service_account"] = saJSON

	account := &config.CloudAccount{
		ID:          "acct1",
		GCPAuthMode: "service_account_key",
	}
	src, err := ResolveGCPTokenSource(context.Background(), account, store)
	require.NoError(t, err)
	assert.NotNil(t, src)
}

func TestResolveWebIdentityProvider_PathTraversal(t *testing.T) {
	account := &config.CloudAccount{
		ID:                      "acct1",
		AWSAuthMode:             "workload_identity_federation",
		AWSRoleARN:              "arn:aws:iam::123456789012:role/test",
		AWSWebIdentityTokenFile: "../../../etc/shadow",
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}

func TestResolveWebIdentityProvider_RelativePath(t *testing.T) {
	account := &config.CloudAccount{
		ID:                      "acct1",
		AWSAuthMode:             "workload_identity_federation",
		AWSRoleARN:              "arn:aws:iam::123456789012:role/test",
		AWSWebIdentityTokenFile: "relative/path/token",
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}

func TestResolveAccessKeyProvider_NilStore(t *testing.T) {
	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "access_keys",
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credential store required")
}

// ---------------------------------------------------------------------------
// 03-N5 — Required payload fields validated after unmarshal
// ---------------------------------------------------------------------------

// TestResolveAccessKeyProvider_EmptyAccessKeyID verifies that an AWS payload
// with a missing access_key_id is rejected with a descriptive error (03-N5).
func TestResolveAccessKeyProvider_EmptyAccessKeyID(t *testing.T) {
	store := newMockStore()
	payload, _ := json.Marshal(map[string]string{
		"access_key_id":     "",
		"secret_access_key": "not-empty",
	})
	store.data["acct1/aws_access_keys"] = payload

	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "access_keys",
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, store, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access_key_id is empty")
}

// TestResolveAccessKeyProvider_EmptySecretKey verifies that an AWS payload
// with a missing secret_access_key is rejected with a descriptive error (03-N5).
func TestResolveAccessKeyProvider_EmptySecretKey(t *testing.T) {
	store := newMockStore()
	payload, _ := json.Marshal(map[string]string{
		"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"secret_access_key": "",
	})
	store.data["acct1/aws_access_keys"] = payload

	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "access_keys",
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, store, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret_access_key is empty")
}

// TestResolveAzureCredentials_EmptyClientSecret verifies that an Azure payload
// with a missing client_secret is rejected with a descriptive error (03-N5).
func TestResolveAzureCredentials_EmptyClientSecret(t *testing.T) {
	store := newMockStore()
	payload, _ := json.Marshal(map[string]string{"client_secret": ""})
	store.data["acct1/azure_client_secret"] = payload

	account := &config.CloudAccount{ID: "acct1"}
	_, err := ResolveAzureCredentials(context.Background(), account, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client_secret is empty")
}

// minimalRSAPEM returns a minimal RSA private key PEM for use in JSON
// (newlines replaced with \n literal so it fits in a JSON string).
func minimalRSAPEM() string {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der := x509.MarshalPKCS1PrivateKey(key)
	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	// JSON requires literal \n for newlines inside a string value.
	result := ""
	for _, b := range string(block) {
		if b == '\n' {
			result += `\n`
		} else {
			result += string(b)
		}
	}
	return result
}
