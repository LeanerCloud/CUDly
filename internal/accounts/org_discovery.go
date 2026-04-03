// Package accounts provides AWS Organizations member account discovery.
package accounts

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
)

// OrgDiscoveryResult holds the list of member accounts found during discovery.
type OrgDiscoveryResult struct {
	Accounts []config.CloudAccount
	Errors   []error
}

// orgListAccountsClient is the minimal Organizations API surface needed for discovery.
// *organizations.Client satisfies this interface.
type orgListAccountsClient interface {
	ListAccounts(ctx context.Context, params *organizations.ListAccountsInput, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error)
}

// DiscoverOrgAccounts uses the AWS Organizations API on the management account
// to list all member accounts. It returns CloudAccount records suitable for saving
// — they are NOT automatically persisted.
//
// The caller is responsible for using the appropriate credentials for the
// management account (e.g., resolved via the credentials package).
func DiscoverOrgAccounts(ctx context.Context, cfg aws.Config) (*OrgDiscoveryResult, error) {
	return discoverWithClient(ctx, organizations.NewFromConfig(cfg))
}

// discoverWithClient performs org discovery using the provided client, enabling
// unit testing with a mock client.
func discoverWithClient(ctx context.Context, client orgListAccountsClient) (*OrgDiscoveryResult, error) {
	var accounts []config.CloudAccount

	paginator := organizations.NewListAccountsPaginator(client, &organizations.ListAccountsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("accounts: list org accounts: %w", err)
		}
		for _, a := range page.Accounts {
			if a.Id == nil || a.Name == nil {
				continue
			}
			accounts = append(accounts, config.CloudAccount{
				Provider:    "aws",
				ExternalID:  *a.Id,
				Name:        *a.Name,
				Enabled:     true,
				AWSAuthMode: "role_arn",
			})
		}
	}

	return &OrgDiscoveryResult{Accounts: accounts}, nil
}

// LoadDefaultAWSConfig is a convenience wrapper around aws-sdk-go-v2 config loading.
func LoadDefaultAWSConfig(ctx context.Context) (aws.Config, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return aws.Config{}, fmt.Errorf("accounts: load AWS config: %w", err)
	}
	return cfg, nil
}
