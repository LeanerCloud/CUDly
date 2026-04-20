package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	stypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCredentialStore is a test implementation of CredentialStore.
type mockCredentialStore struct {
	data map[string][]byte // key: accountID+"/"+credType
	err  error
}

func newMockStore() *mockCredentialStore {
	return &mockCredentialStore{data: make(map[string][]byte)}
}

func (m *mockCredentialStore) key(accountID, credType string) string {
	return accountID + "/" + credType
}

func (m *mockCredentialStore) SaveCredential(_ context.Context, accountID, credType string, payload []byte) error {
	if m.err != nil {
		return m.err
	}
	m.data[m.key(accountID, credType)] = payload
	return nil
}

func (m *mockCredentialStore) LoadRaw(_ context.Context, accountID, credType string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	v, ok := m.data[m.key(accountID, credType)]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (m *mockCredentialStore) DeleteCredential(_ context.Context, accountID, credType string) error {
	if m.err != nil {
		return m.err
	}
	delete(m.data, m.key(accountID, credType))
	return nil
}

func (m *mockCredentialStore) HasCredential(_ context.Context, accountID, credType string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	_, ok := m.data[m.key(accountID, credType)]
	return ok, nil
}

func (m *mockCredentialStore) EncryptPayload(plaintext []byte) (string, error) {
	return string(plaintext), nil // no-op: return plaintext as "encrypted" for tests
}

func (m *mockCredentialStore) DecryptPayload(ciphertext string) ([]byte, error) {
	return []byte(ciphertext), nil // no-op: return ciphertext as "decrypted" for tests
}

// mockSTSClient implements STSClient for testing.
type mockSTSClient struct {
	out *sts.AssumeRoleOutput
	err error
}

func (m *mockSTSClient) AssumeRole(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	return m.out, m.err
}

func (m *mockSTSClient) AssumeRoleWithWebIdentity(_ context.Context, _ *sts.AssumeRoleWithWebIdentityInput, _ ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	return nil, m.err
}

func TestResolveAWSCredentialProvider_AccessKeys(t *testing.T) {
	store := newMockStore()
	payload, _ := json.Marshal(awsAccessKeyPayload{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})
	store.data["acct1/aws_access_keys"] = payload

	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "access_keys",
	}

	provider, err := ResolveAWSCredentialProvider(context.Background(), account, store, nil)
	require.NoError(t, err)
	assert.NotNil(t, provider)

	creds, err := provider.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", creds.AccessKeyID)
}

func TestResolveAWSCredentialProvider_AccessKeys_NotFound(t *testing.T) {
	store := newMockStore() // no credentials stored

	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "access_keys",
	}

	_, err := ResolveAWSCredentialProvider(context.Background(), account, store, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no access keys stored")
}

func TestResolveAWSCredentialProvider_RoleARN(t *testing.T) {
	roleARN := "arn:aws:iam::123456789012:role/CUDly"
	accessKeyID := "ASIATESTSESSION"
	secretKey := "testsecret"
	sessionToken := "testtoken"

	stsClient := &mockSTSClient{
		out: &sts.AssumeRoleOutput{
			Credentials: &stypes.Credentials{
				AccessKeyId:     &accessKeyID,
				SecretAccessKey: &secretKey,
				SessionToken:    &sessionToken,
			},
		},
	}

	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "role_arn",
		AWSRoleARN:  roleARN,
	}

	provider, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), stsClient)
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestResolveAWSCredentialProvider_RoleARN_NoARN(t *testing.T) {
	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "role_arn",
		AWSRoleARN:  "", // missing
	}

	_, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), &mockSTSClient{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aws_role_arn is required")
}

func TestResolveAWSCredentialProvider_RoleARN_STSError(t *testing.T) {
	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "role_arn",
		AWSRoleARN:  "arn:aws:iam::123456789012:role/CUDly",
	}

	// stscreds.NewAssumeRoleProvider defers the STS call to credential retrieval time.
	// Construction must succeed; the error surfaces when Retrieve() is called.
	stsClient := &mockSTSClient{err: errors.New("access denied")}
	provider, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), stsClient)
	require.NoError(t, err, "construction must succeed; STS error is deferred to Retrieve()")

	_, err = provider.Retrieve(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

func TestResolveAWSCredentialProvider_UnsupportedMode(t *testing.T) {
	account := &config.CloudAccount{
		ID:          "acct1",
		AWSAuthMode: "unknown",
	}

	_, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported aws_auth_mode")
}

// TestResolveBastionProvider_LoadsBastionCreds asserts the self-resolving path:
// when AccountLookup + STSClientFactory are wired, the resolver builds an STS
// client backed by the bastion account's own credentials before assuming the
// target role. We verify by capturing which credentials provider was passed to
// the factory.
func TestResolveBastionProvider_LoadsBastionCreds(t *testing.T) {
	bastion := &config.CloudAccount{
		ID:          "bastion-acct",
		Enabled:     true,
		AWSAuthMode: "access_keys",
	}
	target := &config.CloudAccount{
		ID:           "target-acct",
		AWSAuthMode:  "bastion",
		AWSBastionID: "bastion-acct",
		AWSRoleARN:   "arn:aws:iam::999999999999:role/Target",
	}
	// Stash bastion access keys in the store so resolveAccessKeyProvider succeeds.
	store := newMockStore()
	store.data["bastion-acct/aws_access_keys"] = []byte(`{"access_key_id":"AKIA","secret_access_key":"sk"}`)

	var capturedProvider aws.CredentialsProvider
	opts := AWSResolveOptions{
		AccountLookup: func(_ context.Context, id string) (*config.CloudAccount, error) {
			if id == "bastion-acct" {
				return bastion, nil
			}
			return nil, nil
		},
		STSClientFactory: func(p aws.CredentialsProvider) STSClient {
			capturedProvider = p
			return &mockSTSClient{}
		},
	}

	provider, err := ResolveAWSCredentialProviderWithOpts(context.Background(), target, store, &mockSTSClient{}, opts)
	require.NoError(t, err)
	assert.NotNil(t, provider)
	assert.NotNil(t, capturedProvider, "STSClientFactory should have been called with bastion creds")
}

// TestResolveBastionProvider_RejectsBastionChain ensures bastion-of-bastion is
// refused even if AccountLookup returns one.
func TestResolveBastionProvider_RejectsBastionChain(t *testing.T) {
	chainedBastion := &config.CloudAccount{
		ID:           "bastion-acct",
		Enabled:      true,
		AWSAuthMode:  "bastion",
		AWSBastionID: "another-bastion",
	}
	target := &config.CloudAccount{
		ID:           "target-acct",
		AWSAuthMode:  "bastion",
		AWSBastionID: "bastion-acct",
		AWSRoleARN:   "arn:aws:iam::999999999999:role/Target",
	}
	opts := AWSResolveOptions{
		AccountLookup: func(_ context.Context, _ string) (*config.CloudAccount, error) {
			return chainedBastion, nil
		},
		STSClientFactory: func(_ aws.CredentialsProvider) STSClient { return &mockSTSClient{} },
	}

	_, err := ResolveAWSCredentialProviderWithOpts(context.Background(), target, newMockStore(), &mockSTSClient{}, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bastion chaining not allowed")
}

// TestResolveBastionProvider_BastionNotFound surfaces a clear error when the
// referenced bastion account does not exist.
func TestResolveBastionProvider_BastionNotFound(t *testing.T) {
	target := &config.CloudAccount{
		ID:           "target-acct",
		AWSAuthMode:  "bastion",
		AWSBastionID: "missing-bastion",
		AWSRoleARN:   "arn:aws:iam::999999999999:role/Target",
	}
	opts := AWSResolveOptions{
		AccountLookup: func(_ context.Context, _ string) (*config.CloudAccount, error) {
			return nil, nil
		},
		STSClientFactory: func(_ aws.CredentialsProvider) STSClient { return &mockSTSClient{} },
	}

	_, err := ResolveAWSCredentialProviderWithOpts(context.Background(), target, newMockStore(), &mockSTSClient{}, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bastion account missing-bastion not found")
}

// TestResolveBastionProvider_BastionDisabled refuses to use a disabled bastion.
func TestResolveBastionProvider_BastionDisabled(t *testing.T) {
	disabled := &config.CloudAccount{
		ID:          "bastion-acct",
		Enabled:     false,
		AWSAuthMode: "access_keys",
	}
	target := &config.CloudAccount{
		ID:           "target-acct",
		AWSAuthMode:  "bastion",
		AWSBastionID: "bastion-acct",
		AWSRoleARN:   "arn:aws:iam::999999999999:role/Target",
	}
	opts := AWSResolveOptions{
		AccountLookup:    func(_ context.Context, _ string) (*config.CloudAccount, error) { return disabled, nil },
		STSClientFactory: func(_ aws.CredentialsProvider) STSClient { return &mockSTSClient{} },
	}

	_, err := ResolveAWSCredentialProviderWithOpts(context.Background(), target, newMockStore(), &mockSTSClient{}, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bastion account bastion-acct is disabled")
}

// TestResolveBastionProvider_LegacyFallback verifies the back-compat path:
// when AccountLookup/STSClientFactory are nil, the resolver falls through to
// the old behaviour of trusting the caller-supplied STS client.
func TestResolveBastionProvider_LegacyFallback(t *testing.T) {
	target := &config.CloudAccount{
		ID:           "target-acct",
		AWSAuthMode:  "bastion",
		AWSBastionID: "bastion-acct",
		AWSRoleARN:   "arn:aws:iam::999999999999:role/Target",
	}
	provider, err := ResolveAWSCredentialProvider(context.Background(), target, newMockStore(), &mockSTSClient{})
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestResolveAzureCredentials_Success(t *testing.T) {
	store := newMockStore()
	payload, _ := json.Marshal(map[string]string{"client_secret": "azure-secret-value"})
	store.data["acct1/azure_client_secret"] = payload

	account := &config.CloudAccount{ID: "acct1"}
	creds, err := ResolveAzureCredentials(context.Background(), account, store)
	require.NoError(t, err)
	assert.Equal(t, "azure-secret-value", creds.ClientSecret)
}

func TestResolveAzureCredentials_NotStored(t *testing.T) {
	_, err := ResolveAzureCredentials(context.Background(), &config.CloudAccount{ID: "acct1"}, newMockStore())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no client secret stored")
}

func TestResolveGCPCredentials_Success(t *testing.T) {
	store := newMockStore()
	saJSON := []byte(`{"type":"service_account","project_id":"my-project"}`)
	store.data["acct1/gcp_service_account"] = saJSON

	result, err := ResolveGCPCredentials(context.Background(), &config.CloudAccount{ID: "acct1"}, store)
	require.NoError(t, err)
	assert.Equal(t, saJSON, result)
}

func TestAWSCredentials_String_IsRedacted(t *testing.T) {
	c := &AWSCredentials{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "secret"}
	assert.NotContains(t, c.String(), "AKIAIOSFODNN7EXAMPLE")
	assert.NotContains(t, c.String(), "secret")
}

func TestAzureCredentials_String_IsRedacted(t *testing.T) {
	c := &AzureCredentials{ClientSecret: "supersecret"}
	assert.NotContains(t, c.String(), "supersecret")
}
