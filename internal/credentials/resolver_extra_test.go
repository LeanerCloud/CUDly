package credentials

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	// bastion mode delegates to resolveRoleARNProvider; a missing ARN surfaces there.
	account := &config.CloudAccount{
		ID:           "acct1",
		AWSAuthMode:  "bastion",
		AWSBastionID: "bastion-acct",
		AWSRoleARN:   "",
	}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aws_role_arn is required")
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
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "workload_identity_federation",
	}
	_, err := ResolveAzureTokenCredential(context.Background(), account, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credential store required")
}

func TestResolveAzureTokenCredential_WIF_NoStoredKey(t *testing.T) {
	store := newMockStore() // no WIF key stored
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "workload_identity_federation",
	}
	_, err := ResolveAzureTokenCredential(context.Background(), account, store)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no wif key stored")
}

func TestResolveAzureTokenCredential_WIF_WithValidKey(t *testing.T) {
	store := newMockStore()
	pemKey := generateTestKeyAndCertPEM(t) // must contain key+cert for x5t
	store.data["acct1/azure_wif_private_key"] = pemKey

	account := &config.CloudAccount{
		ID:            "acct1",
		AzureAuthMode: "workload_identity_federation",
		AzureTenantID: "00000000-0000-0000-0000-000000000001",
		AzureClientID: "00000000-0000-0000-0000-000000000002",
	}
	cred, err := ResolveAzureTokenCredential(context.Background(), account, store)
	require.NoError(t, err)
	assert.NotNil(t, cred)
}

// ---------------------------------------------------------------------------
// buildAzureWIFCredential
// ---------------------------------------------------------------------------

func generateTestRSAKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

func generateTestRSAKeyPKCS8PEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// generateTestKeyAndCertPEM returns a concatenated PEM blob with a PKCS1 RSA private key
// followed by a self-signed certificate — the format expected by buildAzureWIFCredential.
func generateTestKeyAndCertPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "CUDly-WIF-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	return append(keyPEM, certPEM...)
}

// generateTestKeyAndCertPKCS8PEM is the same as generateTestKeyAndCertPEM but uses PKCS8 key format.
func generateTestKeyAndCertPKCS8PEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "CUDly-WIF-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	return append(keyPEM, certPEM...)
}

func generateECKeyPKCS8PEM(t *testing.T) []byte {
	t.Helper()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(ecKey)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func TestBuildAzureWIFCredential_ValidPKCS1Key(t *testing.T) {
	pemBlob := generateTestKeyAndCertPEM(t) // key+cert concatenated
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureTenantID: "00000000-0000-0000-0000-000000000001",
		AzureClientID: "00000000-0000-0000-0000-000000000002",
	}
	cred, err := buildAzureWIFCredential(account, pemBlob)
	require.NoError(t, err)
	assert.NotNil(t, cred)
}

func TestBuildAzureWIFCredential_ValidPKCS8Key(t *testing.T) {
	pemBlob := generateTestKeyAndCertPKCS8PEM(t) // PKCS8 key+cert concatenated
	account := &config.CloudAccount{
		ID:            "acct1",
		AzureTenantID: "00000000-0000-0000-0000-000000000001",
		AzureClientID: "00000000-0000-0000-0000-000000000002",
	}
	cred, err := buildAzureWIFCredential(account, pemBlob)
	require.NoError(t, err)
	assert.NotNil(t, cred)
}

func TestBuildAzureWIFCredential_InvalidPEM(t *testing.T) {
	account := &config.CloudAccount{ID: "acct1"}
	_, err := buildAzureWIFCredential(account, []byte("not a pem"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no private key found in PEM blob")
}

func TestBuildAzureWIFCredential_NoCertificate(t *testing.T) {
	// Key-only PEM — missing certificate block; should fail with informative error.
	pemKey := generateTestRSAKeyPEM(t)
	account := &config.CloudAccount{ID: "acct1"}
	_, err := buildAzureWIFCredential(account, pemKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no certificate found in PEM blob")
}

func TestBuildAzureWIFCredential_NonRSAKey(t *testing.T) {
	// An EC key in PKCS8 format: x509.ParsePKCS8PrivateKey succeeds but
	// the type assertion to *rsa.PrivateKey will fail.
	ecPriv := generateECKeyPKCS8PEM(t)
	account := &config.CloudAccount{ID: "acct1"}
	_, err := buildAzureWIFCredential(account, ecPriv)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected RSA key")
}

func TestBuildAzureWIFCredential_InvalidDER(t *testing.T) {
	// PEM block is present but DER content is garbage.
	garbage := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("garbage")})
	account := &config.CloudAccount{ID: "acct1"}
	_, err := buildAzureWIFCredential(account, garbage)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse PKCS1 rsa key")
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

func TestParsePEMBlob_DuplicateKeys(t *testing.T) {
	key1, _ := rsa.GenerateKey(rand.Reader, 2048)
	key2, _ := rsa.GenerateKey(rand.Reader, 2048)
	blob := append(
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key1)}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key2)})...,
	)
	_, _, err := parsePEMBlob("test-acct", blob)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple private key")
}

func TestParsePEMBlob_DuplicateCerts(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	blob := append(keyPEM, certPEM...)
	blob = append(blob, certPEM...) // second cert
	_, _, err := parsePEMBlob("test-acct", blob)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple certificate")
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
