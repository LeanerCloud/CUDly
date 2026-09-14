# ==============================================
# Container Registry (ACR)
# ==============================================

resource "azurerm_container_registry" "main" {
  name                = local.acr_name
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  sku                 = "Basic"
  admin_enabled       = false

  tags = local.common_tags
}

# The Container App pulls with its user-assigned identity; the AcrPull grant
# lives inside the container-apps module so the app can depend on it.
moved {
  from = azurerm_role_assignment.acr_pull[0]
  to   = module.compute_container_apps[0].azurerm_role_assignment.acr_pull
}
