package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// imdsAddresses are the well-known metadata service endpoints that must never
// be reachable from application-level HTTP clients. A request that reaches
// these addresses from user-supplied input is an SSRF attack that can leak
// managed-identity credentials.
var imdsAddresses = map[string]bool{
	"169.254.169.254": true, // Azure/AWS/GCP link-local IMDS (IPv4)
	"fd00:ec2::254":   true, // AWS IMDS (IPv6)
}

// blockIMDSDialer wraps net.Dialer and rejects outbound connections to IMDS
// addresses before a TCP handshake is attempted.
type blockIMDSDialer struct {
	inner net.Dialer
}

func (d *blockIMDSDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if imdsAddresses[host] {
		return nil, fmt.Errorf("connection to metadata endpoint %s is blocked", host)
	}
	return d.inner.DialContext(ctx, network, addr)
}

// imdsBlockingTransport returns an *http.Client whose transport rejects
// connections to Azure/AWS/GCP IMDS link-local addresses, mitigating SSRF
// attacks that could exfiltrate managed-identity credentials.
func imdsBlockingTransport() *http.Client {
	dialer := &blockIMDSDialer{
		inner: net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

// AzureResolver implements Resolver for Azure Key Vault.
type AzureResolver struct {
	client   *azsecrets.Client
	vaultURL string
}

// NewAzureResolver creates a new Azure Key Vault resolver.
// The underlying HTTP client blocks connections to IMDS link-local addresses
// (169.254.169.254, fd00:ec2::254) to mitigate SSRF attacks.
func NewAzureResolver(ctx context.Context, vaultURL string) (*AzureResolver, error) {
	// Create a credential using DefaultAzureCredential.
	// Note: azidentity.NewDefaultAzureCredential does not accept a context parameter,
	// so any deadline set on ctx has no effect on credential acquisition.
	// This is an SDK limitation.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	// Wire the IMDS-blocking transport so that SSRF via a redirected URL
	// cannot reach the metadata endpoint and leak managed-identity tokens.
	opts := &azsecrets.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: imdsBlockingTransport(),
		},
	}

	client, err := azsecrets.NewClient(vaultURL, cred, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Key Vault client: %w", err)
	}

	return &AzureResolver{
		client:   client,
		vaultURL: vaultURL,
	}, nil
}

// GetSecret retrieves a secret from Azure Key Vault.
func (r *AzureResolver) GetSecret(ctx context.Context, secretID string) (string, error) {
	// Get the latest version of the secret
	resp, err := r.client.GetSecret(ctx, secretID, "", nil)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s: %w", secretID, err)
	}

	if resp.Value == nil {
		return "", fmt.Errorf("secret %s has no value", secretID)
	}

	return *resp.Value, nil
}

// PutSecret creates or updates a secret in Azure Key Vault.
func (r *AzureResolver) PutSecret(ctx context.Context, secretID, value string) error {
	params := azsecrets.SetSecretParameters{
		Value: &value,
	}

	_, err := r.client.SetSecret(ctx, secretID, params, nil)
	if err != nil {
		return fmt.Errorf("failed to set secret %s: %w", secretID, err)
	}

	return nil
}

// GetSecretJSON retrieves and parses a JSON secret.
func (r *AzureResolver) GetSecretJSON(ctx context.Context, secretID string) (map[string]any, error) {
	secretString, err := r.GetSecret(ctx, secretID)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(secretString), &result); err != nil {
		return nil, fmt.Errorf("failed to parse secret as JSON: %w", err)
	}

	return result, nil
}

// ListSecrets lists secrets in Azure Key Vault.
func (r *AzureResolver) ListSecrets(ctx context.Context, filter string) ([]string, error) {
	secrets := make([]string, 0)

	// Create pager for listing secrets
	pager := r.client.NewListSecretPropertiesPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list secrets: %w", err)
		}

		for _, secret := range page.Value {
			if secret.ID != nil {
				name := secret.ID.Name()
				// Apply filter during iteration to avoid loading all secrets into memory.
				// Using HasPrefix to match the documented prefix filter behavior.
				if filter == "" || strings.HasPrefix(name, filter) {
					secrets = append(secrets, name)
				}
			}
		}
	}

	return secrets, nil
}

// Close cleans up resources (no-op for Azure).
func (r *AzureResolver) Close() error {
	return nil
}
