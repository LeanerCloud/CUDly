package secrets

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// retiredKeyVaultModulePrefix is the module path prefix of the Azure Key Vault
// SDK beta tree that Microsoft retired in 2023 in favor of
// github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/*. The retired tree
// receives no bug or security fixes, so it must never reappear on the
// secrets-resolution path (SUP-01, issue #1146).
const retiredKeyVaultModulePrefix = "github.com/Azure/azure-sdk-for-go/sdk/keyvault/"

// TestNoRetiredAzureKeyVaultModule guards against reintroducing the retired
// sdk/keyvault/* module tree. Before the SUP-01 fix, go.mod required
// sdk/keyvault/azsecrets v0.12.0 (plus sdk/keyvault/internal v0.7.1), which
// this test fails on; after migrating to sdk/security/keyvault/azsecrets it
// passes.
func TestNoRetiredAzureKeyVaultModule(t *testing.T) {
	// Tests run with the package directory as the working directory, so the
	// root go.mod is two levels up.
	data, err := os.ReadFile("../../go.mod")
	require.NoError(t, err, "failed to read root go.mod")

	for i, line := range strings.Split(string(data), "\n") {
		require.NotContains(t, line, retiredKeyVaultModulePrefix,
			"go.mod line %d references the retired Azure Key Vault SDK tree %q; "+
				"use github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/* instead",
			i+1, retiredKeyVaultModulePrefix)
	}
}
