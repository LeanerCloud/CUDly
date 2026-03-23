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
    # Blob data-plane: needed for Terraform state backend read/write
    data_actions = [
      "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/*",
    ]
  }

  assignable_scopes = [
    "/subscriptions/${var.subscription_id}",
  ]
}

resource "azurerm_role_assignment" "cudly_deploy" {
  scope              = "/subscriptions/${var.subscription_id}"
  role_definition_id = azurerm_role_definition.cudly_deploy.role_definition_resource_id
  principal_id       = var.assignee_object_id
}
