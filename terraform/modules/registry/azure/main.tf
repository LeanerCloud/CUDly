# Azure Container Registry
resource "azurerm_container_registry" "main" {
  name                = var.acr_name
  resource_group_name = var.resource_group_name
  location            = var.location
  sku                 = var.sku
  admin_enabled       = var.enable_admin_user

  # Premium-SKU policy attributes. In azurerm 4.x these are scalar
  # attributes on the resource (the v3.x nested `retention_policy` /
  # `trust_policy` blocks were removed). Set them conditionally on SKU
  # — null leaves the provider default in place for non-Premium SKUs.
  quarantine_policy_enabled = var.sku == "Premium"
  trust_policy_enabled      = var.sku == "Premium"
  retention_policy_in_days  = var.sku == "Premium" ? var.image_retention_days : null

  tags = var.tags
}

# ACR Task for cleanup (runs daily)
resource "azurerm_container_registry_task" "cleanup" {
  name                  = "cleanup-old-images"
  container_registry_id = azurerm_container_registry.main.id

  platform {
    os = "Linux"
  }

  # ACR purge command to clean up old images
  encoded_step {
    task_content = base64encode(<<-EOT
      version: v1.1.0
      steps:
        # Keep last ${var.keep_image_count} tagged images
        - cmd: acr purge --filter 'cudly:.*' --ago ${var.image_retention_days}d --keep ${var.keep_image_count} --untagged
          disableWorkingDirectoryOverride: true
          timeout: 3600
    EOT
    )
  }

  # Run daily at 2 AM UTC
  timer_trigger {
    name     = "daily-cleanup"
    schedule = "0 2 * * *"
    enabled  = true
  }

  tags = var.tags
}

# Role assignment to allow Container Apps to pull images
resource "azurerm_role_assignment" "acr_pull" {
  scope                = azurerm_container_registry.main.id
  role_definition_name = "AcrPull"
  principal_id         = var.container_app_identity_principal_id

  # This will be provided by the container apps module
  count = var.container_app_identity_principal_id != "" ? 1 : 0
}
