package credentials

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

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
	CredTypeAzureWIF          = "azure_wif_private_key"
	CredTypeGCPServiceAccount = "gcp_service_account"
	CredTypeGCPWIFConfig      = "gcp_workload_identity_config"
)

const gcpCloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

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

// STSClient is the STS interface required for role assumption and web identity federation.
// Satisfied by *sts.Client from github.com/aws/aws-sdk-go-v2/service/sts.
type STSClient interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
	AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
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
	case "workload_identity_federation":
		return resolveWebIdentityProvider(account, stsClient)
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

// resolveWebIdentityProvider returns a credential provider that exchanges an OIDC token
// for temporary AWS credentials via STS AssumeRoleWithWebIdentity.
// The token file path comes from account.AWSWebIdentityTokenFile, with a fallback to
// the AWS_WEB_IDENTITY_TOKEN_FILE environment variable (standard AKS/GKE projected token path).
func resolveWebIdentityProvider(account *config.CloudAccount, stsClient STSClient) (aws.CredentialsProvider, error) {
	if account.AWSRoleARN == "" {
		return nil, fmt.Errorf("credentials: aws_role_arn required for workload_identity_federation mode (account %s)", account.ID)
	}
	tokenFile := account.AWSWebIdentityTokenFile
	if tokenFile == "" {
		tokenFile = os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")
	}
	if tokenFile == "" {
		return nil, fmt.Errorf("credentials: aws_web_identity_token_file required for workload_identity_federation mode (account %s)", account.ID)
	}
	sessionSuffix := account.ID
	if len(sessionSuffix) > 8 {
		sessionSuffix = sessionSuffix[:8]
	}
	return stscreds.NewWebIdentityRoleProvider(stsClient, account.AWSRoleARN,
		stscreds.IdentityTokenFile(tokenFile),
		func(o *stscreds.WebIdentityRoleOptions) {
			o.RoleSessionName = "cudly-" + sessionSuffix
			// Note: WebIdentityRoleOptions does not have ExternalID;
			// OIDC identity is verified by the token subject claim instead.
		}), nil
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

// ResolveAzureTokenCredential returns an azcore.TokenCredential for the account.
// Routes by AzureAuthMode:
//   - managed_identity  → ManagedIdentityCredential (no stored cred needed)
//   - workload_identity_federation → loads RSA private key PEM, builds client assertion
//   - client_secret (default) → loads stored secret and returns ClientSecretCredential
func ResolveAzureTokenCredential(ctx context.Context, account *config.CloudAccount, store CredentialStore) (azcore.TokenCredential, error) {
	switch account.AzureAuthMode {
	case "managed_identity":
		return azidentity.NewManagedIdentityCredential(nil)
	case "workload_identity_federation":
		if store == nil {
			return nil, fmt.Errorf("credentials: credential store required for azure wif account %s", account.ID)
		}
		raw, err := store.LoadRaw(ctx, account.ID, CredTypeAzureWIF)
		if err != nil {
			return nil, fmt.Errorf("credentials: load azure wif key for account %s: %w", account.ID, err)
		}
		if raw == nil {
			return nil, fmt.Errorf("credentials: no wif key stored for account %s", account.ID)
		}
		return buildAzureWIFCredential(account, raw)
	default: // "client_secret" or empty
		if store == nil {
			return nil, fmt.Errorf("credentials: credential store required for azure client_secret account %s", account.ID)
		}
		if account.AzureTenantID == "" || account.AzureClientID == "" {
			return nil, fmt.Errorf("credentials: azure_tenant_id and azure_client_id required for client_secret mode (account %s)", account.ID)
		}
		creds, err := ResolveAzureCredentials(ctx, account, store)
		if err != nil {
			return nil, err
		}
		return azidentity.NewClientSecretCredential(account.AzureTenantID, account.AzureClientID, creds.ClientSecret, nil)
	}
}

// buildAzureWIFCredential creates a client-assertion credential using a stored RSA private key PEM.
func buildAzureWIFCredential(account *config.CloudAccount, pemKey []byte) (azcore.TokenCredential, error) {
	block, _ := pem.Decode(pemKey)
	if block == nil {
		return nil, fmt.Errorf("credentials: invalid PEM key for account %s", account.ID)
	}
	var rsaKey *rsa.PrivateKey
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		if rsaKey, ok = key.(*rsa.PrivateKey); !ok {
			return nil, fmt.Errorf("credentials: expected RSA key for account %s", account.ID)
		}
	} else if rsaKey, err = x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		return nil, fmt.Errorf("credentials: parse rsa key for account %s: %w", account.ID, err)
	}
	tenantID := account.AzureTenantID
	clientID := account.AzureClientID
	assertionFunc := func(_ context.Context) (string, error) {
		now := time.Now()
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"aud": fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
			"iss": clientID,
			"sub": clientID,
			"jti": fmt.Sprintf("%d", now.UnixNano()),
			"nbf": now.Unix(),
			"iat": now.Unix(),
			"exp": now.Add(5 * time.Minute).Unix(),
		})
		return token.SignedString(rsaKey)
	}
	return azidentity.NewClientAssertionCredential(tenantID, clientID, assertionFunc, nil)
}

// ResolveGCPTokenSource returns an oauth2.TokenSource for the account.
// Returns (nil, nil) for application_default mode — callers should use ADC directly.
// For service_account_key and workload_identity_federation, loads JSON from the
// credential store; google.CredentialsFromJSON handles both formats.
func ResolveGCPTokenSource(ctx context.Context, account *config.CloudAccount, store CredentialStore) (oauth2.TokenSource, error) {
	if account.GCPAuthMode == "application_default" {
		return nil, nil
	}
	if store == nil {
		return nil, fmt.Errorf("credentials: credential store required for gcp account %s", account.ID)
	}
	credType := CredTypeGCPServiceAccount
	if account.GCPAuthMode == "workload_identity_federation" {
		credType = CredTypeGCPWIFConfig
	}
	raw, err := store.LoadRaw(ctx, account.ID, credType)
	if err != nil {
		return nil, fmt.Errorf("credentials: load gcp credentials for account %s: %w", account.ID, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("credentials: no gcp credentials stored for account %s", account.ID)
	}
	creds, err := google.CredentialsFromJSON(ctx, raw, gcpCloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("credentials: parse gcp credentials for account %s: %w", account.ID, err)
	}
	return creds.TokenSource, nil
}
