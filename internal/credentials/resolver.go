package credentials

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Credential type constants used as credType in CredentialStore.
const (
	CredTypeAWSAccessKeys     = "aws_access_keys"
	CredTypeAzureClientSecret = "azure_client_secret"
	CredTypeGCPServiceAccount = "gcp_service_account"
)

// awsAccessKeyPayload is the JSON structure stored for CredTypeAWSAccessKeys.
type awsAccessKeyPayload struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// AWSCredentials holds resolved AWS access key credentials.
type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

// String returns a redacted representation to prevent accidental logging.
func (c *AWSCredentials) String() string { return "[REDACTED AWS CREDENTIALS]" }

// AzureCredentials holds resolved Azure service principal credentials.
type AzureCredentials struct {
	ClientSecret string
}

// String returns a redacted representation.
func (c *AzureCredentials) String() string { return "[REDACTED AZURE CREDENTIALS]" }

// STSClient is the minimal STS interface required for role assumption.
// Satisfied by *sts.Client from github.com/aws/aws-sdk-go-v2/service/sts.
type STSClient interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// ResolveAWSCredentialProvider returns an aws.CredentialsProvider for the account.
//
// Logic per auth mode:
//   - access_keys: load stored key pair → static credentials provider
//   - role_arn:    use ambient (instance role / env) → STS AssumeRole
//   - bastion:     load bastion account creds → STS AssumeRole into target
func ResolveAWSCredentialProvider(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	stsClient STSClient,
) (aws.CredentialsProvider, error) {
	switch account.AWSAuthMode {
	case "access_keys":
		return resolveAccessKeyProvider(ctx, account, store)
	case "role_arn":
		return resolveRoleARNProvider(ctx, account, stsClient, nil)
	case "bastion":
		return resolveBastionProvider(ctx, account, store, stsClient)
	default:
		return nil, fmt.Errorf("credentials: unsupported aws_auth_mode %q for account %s", account.AWSAuthMode, account.ID)
	}
}

func resolveAccessKeyProvider(ctx context.Context, account *config.CloudAccount, store CredentialStore) (aws.CredentialsProvider, error) {
	raw, err := store.LoadRaw(ctx, account.ID, CredTypeAWSAccessKeys)
	if err != nil {
		return nil, fmt.Errorf("credentials: load access keys for account %s: %w", account.ID, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("credentials: no access keys stored for account %s", account.ID)
	}
	var payload awsAccessKeyPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("credentials: parse access key payload for account %s: %w", account.ID, err)
	}
	return credentials.NewStaticCredentialsProvider(payload.AccessKeyID, payload.SecretAccessKey, ""), nil
}

// resolveRoleARNProvider returns an auto-refreshing credential provider that
// assumes account.AWSRoleARN using stsClient. stscreds.AssumeRoleProvider
// transparently refreshes credentials before they expire, avoiding the
// 1-hour STS token expiry problem of static credentials.
func resolveRoleARNProvider(
	_ context.Context,
	account *config.CloudAccount,
	stsClient STSClient,
	_ aws.CredentialsProvider,
) (aws.CredentialsProvider, error) {
	if account.AWSRoleARN == "" {
		return nil, fmt.Errorf("credentials: aws_role_arn is required for role_arn auth mode (account %s)", account.ID)
	}

	sessionSuffix := account.ID
	if len(sessionSuffix) > 8 {
		sessionSuffix = sessionSuffix[:8]
	}

	provider := stscreds.NewAssumeRoleProvider(stsClient, account.AWSRoleARN, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = "cudly-" + sessionSuffix
		if account.AWSExternalID != "" {
			o.ExternalID = aws.String(account.AWSExternalID)
		}
	})
	return provider, nil
}

func resolveBastionProvider(
	ctx context.Context,
	account *config.CloudAccount,
	store CredentialStore,
	stsClient STSClient,
) (aws.CredentialsProvider, error) {
	if account.AWSBastionID == "" {
		return nil, fmt.Errorf("credentials: aws_bastion_id is required for bastion auth mode (account %s)", account.ID)
	}
	// The bastion account must be resolved by the caller as a full CloudAccount.
	// Here we only have the account struct — the bastion account's credentials were
	// loaded separately and passed in via stsClient which already uses bastion creds.
	// This resolver simply assumes the target role using the provided stsClient.
	return resolveRoleARNProvider(ctx, account, stsClient, nil)
}

// ResolveAzureCredentials decrypts and returns the Azure client secret for the account.
func ResolveAzureCredentials(ctx context.Context, account *config.CloudAccount, store CredentialStore) (*AzureCredentials, error) {
	raw, err := store.LoadRaw(ctx, account.ID, CredTypeAzureClientSecret)
	if err != nil {
		return nil, fmt.Errorf("credentials: load azure secret for account %s: %w", account.ID, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("credentials: no client secret stored for account %s", account.ID)
	}
	var payload struct {
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("credentials: parse azure secret for account %s: %w", account.ID, err)
	}
	return &AzureCredentials{ClientSecret: payload.ClientSecret}, nil
}

// ResolveGCPCredentials decrypts and returns the GCP service account JSON for the account.
func ResolveGCPCredentials(ctx context.Context, account *config.CloudAccount, store CredentialStore) ([]byte, error) {
	raw, err := store.LoadRaw(ctx, account.ID, CredTypeGCPServiceAccount)
	if err != nil {
		return nil, fmt.Errorf("credentials: load gcp service account for account %s: %w", account.ID, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("credentials: no service account stored for account %s", account.ID)
	}
	return raw, nil
}
