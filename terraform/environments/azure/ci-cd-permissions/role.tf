resource "azurerm_role_definition" "cudly_deploy" {
  name        = "CUDly Terraform Deploy"
  scope       = "/subscriptions/${var.subscription_id}"
  description = "Custom role for CUDly CI/CD pipeline to manage all required Azure resources"

  permissions {
    actions = concat(
      local.compute_actions,
      local.data_actions,
      local.networking_actions,
    )
    not_actions = []
    # Data-plane permissions (cannot be granted via control-plane actions block)
    data_actions = [
      # Blob data-plane: Terraform state backend read/write
      "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/*",
      # Key Vault data-plane: read/write secrets (enableRbacAuthorization=true vaults)
      "Microsoft.KeyVault/vaults/secrets/*",
    ]
  }

  assignable_scopes = [
    "/subscriptions/${var.subscription_id}",
  ]
}

resource "azurerm_role_assignment" "cudly_deploy" {
  scope              = "/subscriptions/${var.subscription_id}"
  role_definition_id = azurerm_role_definition.cudly_deploy.role_definition_resource_id
  principal_id       = azuread_service_principal.cudly_deploy.object_id
}
