package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAzureKeyVaultClient is a minimal AzureKeyVaultClient backed by an
// in-process EC key. Used to exercise resolveOnce without a real Key Vault.
type fakeAzureKeyVaultClient struct {
	signErr error
	key     *ecdsa.PublicKey
	// xBytes and yBytes allow callers to inject nil to simulate incomplete
	// responses from the Key Vault API.
	xBytes []byte
	yBytes []byte
	nilKey bool // if true, return a KeyBundle with Key==nil
}

func (f *fakeAzureKeyVaultClient) Sign(_ context.Context, _, _ string, _ azkeys.SignParameters, _ *azkeys.SignOptions) (azkeys.SignResponse, error) {
	return azkeys.SignResponse{}, f.signErr
}

func (f *fakeAzureKeyVaultClient) GetKey(_ context.Context, _, _ string, _ *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error) {
	if f.nilKey {
		return azkeys.GetKeyResponse{
			KeyBundle: azkeys.KeyBundle{Key: nil},
		}, nil
	}
	xBytes := f.xBytes
	yBytes := f.yBytes
	if xBytes == nil || yBytes == nil {
		// Derive the fixed-width coordinates from the uncompressed SEC 1
		// point (0x04 || X || Y) via crypto/ecdh, matching ComputeKeyID
		// and avoiding the deprecated ecdsa.PublicKey.X/Y fields.
		ecdhKey, err := f.key.ECDH()
		if err != nil {
			return azkeys.GetKeyResponse{}, err
		}
		uncompressed := ecdhKey.Bytes()
		byteLen := (f.key.Curve.Params().BitSize + 7) / 8
		if xBytes == nil {
			xBytes = uncompressed[1 : 1+byteLen]
		}
		if yBytes == nil {
			yBytes = uncompressed[1+byteLen:]
		}
	}
	// Use sentinel value to represent "caller explicitly passed nil" vs
	// "caller didn't override" -- an empty slice signals nil field.
	var jwkX, jwkY []byte
	if f.xBytes != nil || xBytes != nil {
		jwkX = xBytes
	}
	if f.yBytes != nil || yBytes != nil {
		jwkY = yBytes
	}
	keyBundle := azkeys.JSONWebKey{
		X: jwkX,
		Y: jwkY,
	}
	return azkeys.GetKeyResponse{
		KeyBundle: azkeys.KeyBundle{Key: &keyBundle},
	}, nil
}

// ---------------------------------------------------------------------------
// M6 -- Azure EC key completeness guard (replaces RSA exponent-range check)
// ---------------------------------------------------------------------------

// TestAzureSigner_ECKeyCompleteness verifies that resolveOnce rejects Key
// Vault responses that are missing the EC public-key coordinates, which
// would otherwise produce a zero-valued *ecdsa.PublicKey and sign invalid
// tokens. A valid response with both X and Y must be accepted.
func TestAzureSigner_ECKeyCompleteness(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	cases := []struct {
		client    *fakeAzureKeyVaultClient
		name      string
		errSubstr string
		wantErr   bool
	}{
		{
			name: "valid EC key accepted",
			client: &fakeAzureKeyVaultClient{
				key: &key.PublicKey,
			},
			wantErr: false,
		},
		{
			name: "key bundle with Key=nil rejected",
			client: &fakeAzureKeyVaultClient{
				key:    &key.PublicKey,
				nilKey: true,
			},
			wantErr:   true,
			errSubstr: "missing X or Y",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			signer := NewAzureKeyVaultSignerFromClient(tc.client, "test-key", "")
			ctx := context.Background()

			_, err := signer.PublicKey(ctx)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M7 -- factory precise error for half-configured Azure
// ---------------------------------------------------------------------------

// TestNewSignerFromEnv_AzureHalfConfigured verifies that exactly one of the
// two Azure env vars being set results in a clear error rather than propagating
// to the constructor (03-M7).
func TestNewSignerFromEnv_AzureHalfConfigured(t *testing.T) {
	t.Setenv(envSourceCloud, "azure")

	cases := []struct {
		name      string
		vaultURL  string
		keyName   string
		errSubstr string
		wantErr   bool
		wantNil   bool
	}{
		{
			name:    "both empty = disabled (nil, nil)",
			wantNil: true,
		},
		{
			name:      "only vault URL set = precise error",
			vaultURL:  "https://my-vault.vault.azure.net/",
			wantErr:   true,
			errSubstr: envAzureKeyName,
		},
		{
			name:      "only key name set = precise error",
			keyName:   "my-key",
			wantErr:   true,
			errSubstr: envAzureVaultURL,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envAzureVaultURL, tc.vaultURL)
			t.Setenv(envAzureKeyName, tc.keyName)
			// Clear AWS/GCP vars so the azure branch is reached.
			t.Setenv(envAWSSigningKeyID, "")
			t.Setenv(envGCPKeyResource, "")

			signer, err := NewSignerFromEnv(context.Background())
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr, "error must name the missing var")
				assert.Nil(t, signer)
			} else if tc.wantNil {
				require.NoError(t, err)
				assert.Nil(t, signer, "both vars empty must return nil signer (issuer disabled)")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// #1464 -- Azure factory branch fully configured (both env vars set)
// ---------------------------------------------------------------------------

// TestNewSignerFromEnv_AzureFullyConfigured verifies that NewSignerFromEnv
// reaches NewAzureKeyVaultSigner's client-construction path when both Azure
// env vars are set. It overrides the package-level newAzureKeyVaultClient
// factory hook with a fake so the test never spawns the real
// azidentity.NewDefaultAzureCredential chain (az/pwsh, macOS keychain
// prompts) that made this branch untestable before #1464.
func TestNewSignerFromEnv_AzureFullyConfigured(t *testing.T) {
	t.Setenv(envSourceCloud, "azure")
	t.Setenv(envAWSSigningKeyID, "")
	t.Setenv(envGCPKeyResource, "")

	const wantVaultURL = "https://my-vault.vault.azure.net/"
	t.Setenv(envAzureVaultURL, wantVaultURL)
	t.Setenv(envAzureKeyName, "my-key")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	var gotVaultURL string
	origFactory := newAzureKeyVaultClient
	newAzureKeyVaultClient = func(vaultURL string) (AzureKeyVaultClient, error) {
		gotVaultURL = vaultURL
		return &fakeAzureKeyVaultClient{key: &key.PublicKey}, nil
	}
	t.Cleanup(func() { newAzureKeyVaultClient = origFactory })

	signer, err := NewSignerFromEnv(context.Background())
	require.NoError(t, err)
	require.NotNil(t, signer)
	assert.Equal(t, wantVaultURL, gotVaultURL, "factory must receive the configured vault URL")
}
