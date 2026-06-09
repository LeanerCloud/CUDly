# Azure Key Vault signing key used by the CUDly OIDC issuer to sign
# client-assertion JWTs presented to target-cloud token endpoints.
# The private half never leaves the vault. The public half is
# published through the Container App's /.well-known/jwks.json endpoint.

resource "azurerm_role_assignment" "current_user_crypto_officer" {
  scope                = azurerm_key_vault.main.id
  role_definition_name = "Key Vault Crypto Officer"
  principal_id         = data.azurerm_client_config.current.object_id
}

resource "azurerm_key_vault_key" "signing" {
  name         = "cudly-oidc-signing"
  key_vault_id = azurerm_key_vault.main.id
  key_type     = "RSA"
  key_size     = 2048

  # Only the operations the Signer actually needs. Sign covers the
  # kms-equivalent signing op; get is consulted at Signer startup to
  # derive the JWK. verify is a no-op for CUDly but is conventional
  # for RSA signing keys.
  key_opts = [
    "sign",
    "verify",
  ]

  tags = merge(var.tags, {
    environment = var.environment
    purpose     = "cudly-oidc-issuer"
  })

  depends_on = [azurerm_role_assignment.current_user_crypto_officer]
}
