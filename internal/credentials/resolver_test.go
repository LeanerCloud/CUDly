package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
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

// mockSTSClient implements STSClient for testing.
type mockSTSClient struct {
	out *sts.AssumeRoleOutput
	err error
}

func (m *mockSTSClient) AssumeRole(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	return m.out, m.err
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

	stsClient := &mockSTSClient{err: errors.New("access denied")}
	_, err := ResolveAWSCredentialProvider(context.Background(), account, newMockStore(), stsClient)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AssumeRole")
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
