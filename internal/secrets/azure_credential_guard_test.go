package secrets

import (
	"os"
	"testing"
)

// TestMain pins the Azure credential environment for every test in this
// package so no test can walk the live DefaultAzureCredential fallback chain
// (IMDS probe, `az account get-access-token`, `pwsh Get-AzAccessToken`). The
// pwsh leg reads the MSAL token cache from the macOS login keychain, which
// pops an interactive password prompt on developer machines whenever
// `go test ./...` runs.
//
// AZURE_TOKEN_CREDENTIALS restricts the chain to EnvironmentCredential only
// (supported since azidentity v1.10), and the dummy client-secret variables
// let that credential construct without contacting anything. A test that
// accidentally acquires a real token fails fast against the dummy tenant
// instead of harvesting local developer credentials.
func TestMain(m *testing.M) {
	os.Setenv("AZURE_TOKEN_CREDENTIALS", "EnvironmentCredential")
	os.Setenv("AZURE_TENANT_ID", "00000000-0000-0000-0000-000000000000")
	os.Setenv("AZURE_CLIENT_ID", "00000000-0000-0000-0000-000000000000")
	os.Setenv("AZURE_CLIENT_SECRET", "dummy-test-client-secret")
	os.Exit(m.Run())
}

// TestAzureCredentialChainRestricted is the regression guard for the keychain
// prompt incident: it fails if TestMain stops pinning the restricted
// credential chain.
func TestAzureCredentialChainRestricted(t *testing.T) {
	if got := os.Getenv("AZURE_TOKEN_CREDENTIALS"); got != "EnvironmentCredential" {
		t.Fatalf("AZURE_TOKEN_CREDENTIALS = %q; TestMain must pin it to EnvironmentCredential so tests never invoke the az/pwsh/IMDS credential legs", got)
	}
}
