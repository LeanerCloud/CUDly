package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"math/big"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAzureKeyVaultClient is a minimal AzureKeyVaultClient backed by an
// in-process RSA key. Used to exercise resolveOnce without a real Key Vault.
type fakeAzureKeyVaultClient struct {
	signErr error
	key     *rsa.PublicKey
	eBytes  []byte
}

func (f *fakeAzureKeyVaultClient) Sign(_ context.Context, _, _ string, _ azkeys.SignParameters, _ *azkeys.SignOptions) (azkeys.SignResponse, error) {
	return azkeys.SignResponse{}, f.signErr
}

func (f *fakeAzureKeyVaultClient) GetKey(_ context.Context, _, _ string, _ *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error) {
	eBytes := f.eBytes
	if eBytes == nil {
		// Normal path: real exponent from the RSA key.
		e := big.NewInt(int64(f.key.E))
		eBytes = e.Bytes()
	}
	keyBundle := azkeys.JSONWebKey{
		N: f.key.N.Bytes(),
		E: eBytes,
	}
	return azkeys.GetKeyResponse{
		KeyBundle: azkeys.KeyBundle{Key: &keyBundle},
	}, nil
}

// ---------------------------------------------------------------------------
// M6 — Azure public-exponent overflow guard
// ---------------------------------------------------------------------------

// TestAzureSigner_ExponentRange verifies that resolveOnce rejects oversized or
// non-positive exponents before constructing the rsa.PublicKey (03-M6).
func TestAzureSigner_ExponentRange(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	cases := []struct {
		name      string
		errSubstr string
		eBytes    []byte
		wantErr   bool
	}{
		{
			name:    "normal exponent 65537 accepted",
			eBytes:  big.NewInt(65537).Bytes(),
			wantErr: false,
		},
		{
			name:      "exponent 0 rejected",
			eBytes:    big.NewInt(0).Bytes(),
			wantErr:   true,
			errSubstr: "exponent",
		},
		{
			name:      "exponent too large (> MaxInt32) rejected",
			eBytes:    new(big.Int).Add(big.NewInt(0x7fffffff), big.NewInt(1)).Bytes(),
			wantErr:   true,
			errSubstr: "exponent",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeAzureKeyVaultClient{key: &key.PublicKey, eBytes: tc.eBytes}
			signer := NewAzureKeyVaultSignerFromClient(client, "test-key", "")
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
// M7 — factory precise error for half-configured Azure
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
