package accounts

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOrgsClient implements orgListAccountsClient for testing.
type mockOrgsClient struct {
	pages [][]orgtypes.Account
	err   error
	call  int
}

func (m *mockOrgsClient) ListAccounts(_ context.Context, _ *organizations.ListAccountsInput, _ ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.call >= len(m.pages) {
		return &organizations.ListAccountsOutput{}, nil
	}
	page := m.pages[m.call]
	m.call++
	var nextToken *string
	if m.call < len(m.pages) {
		nextToken = aws.String("token")
	}
	return &organizations.ListAccountsOutput{
		Accounts:  page,
		NextToken: nextToken,
	}, nil
}

func TestDiscoverWithClient_Success(t *testing.T) {
	id1 := "111111111111"
	name1 := "Prod"
	id2 := "222222222222"
	name2 := "Staging"

	client := &mockOrgsClient{
		pages: [][]orgtypes.Account{
			{{Id: &id1, Name: &name1}},
			{{Id: &id2, Name: &name2}},
		},
	}

	result, err := discoverWithClient(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, result.Accounts, 2)

	assert.Equal(t, "aws", result.Accounts[0].Provider)
	assert.Equal(t, id1, result.Accounts[0].ExternalID)
	assert.Equal(t, name1, result.Accounts[0].Name)
	assert.True(t, result.Accounts[0].Enabled)
	assert.Equal(t, "role_arn", result.Accounts[0].AWSAuthMode)

	assert.Equal(t, id2, result.Accounts[1].ExternalID)
	assert.Equal(t, name2, result.Accounts[1].Name)
}

func TestDiscoverWithClient_SinglePage(t *testing.T) {
	id := "123456789012"
	name := "Main"

	client := &mockOrgsClient{
		pages: [][]orgtypes.Account{
			{{Id: &id, Name: &name}},
		},
	}

	result, err := discoverWithClient(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, result.Accounts, 1)
	assert.Equal(t, id, result.Accounts[0].ExternalID)
}

func TestDiscoverWithClient_Empty(t *testing.T) {
	client := &mockOrgsClient{pages: [][]orgtypes.Account{}}

	result, err := discoverWithClient(context.Background(), client)
	require.NoError(t, err)
	assert.Empty(t, result.Accounts)
}

func TestDiscoverWithClient_SkipsNilIDOrName(t *testing.T) {
	id := "111111111111"
	name := "Valid"

	client := &mockOrgsClient{
		pages: [][]orgtypes.Account{
			{
				{Id: nil, Name: &name}, // nil ID — skip
				{Id: &id, Name: nil},   // nil Name — skip
				{Id: &id, Name: &name}, // valid
			},
		},
	}

	result, err := discoverWithClient(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, result.Accounts, 1)
	assert.Equal(t, id, result.Accounts[0].ExternalID)
}

func TestDiscoverWithClient_PaginatorError(t *testing.T) {
	client := &mockOrgsClient{err: errors.New("organizations: access denied")}

	result, err := discoverWithClient(context.Background(), client)
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "accounts: list org accounts")
	assert.ErrorContains(t, err, "access denied")
}
