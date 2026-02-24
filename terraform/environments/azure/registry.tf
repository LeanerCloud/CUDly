# ==============================================
# Container Registry (ACR)
# ==============================================

resource "azurerm_container_registry" "main" {
  name                = local.acr_name
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  sku                 = "Basic"
  admin_enabled       = true # Enables username/password login for docker push

  tags = local.common_tags
}

# Grant Container Apps managed identity permission to pull images
resource "azurerm_role_assignment" "acr_pull" {
  count = var.compute_platform == "container-apps" && length(module.compute_container_apps) > 0 ? 1 : 0

  scope                = azurerm_container_registry.main.id
  role_definition_name = "AcrPull"
  principal_id         = module.compute_container_apps[0].managed_identity_principal_id
}
