variable "resource_group_name" {
  description = "Name of the resource group"
  type        = string
}

variable "location" {
  description = "Azure region"
  type        = string
}

variable "acr_name" {
  description = "Name of the Azure Container Registry (must be globally unique)"
  type        = string
}

variable "sku" {
  description = "SKU for ACR (Basic, Standard, Premium)"
  type        = string
  default     = "Standard"
}

variable "keep_image_count" {
  description = "Number of images to keep"
  type        = number
  default     = 10
}

variable "image_retention_days" {
  description = "Days to retain images"
  type        = number
  default     = 30
}

variable "enable_admin_user" {
  description = "Enable admin user (not recommended for production)"
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}

# Azure Container Registry
resource "azurerm_container_registry" "main" {
  name                = var.acr_name
  resource_group_name = var.resource_group_name
  location            = var.location
  sku                 = var.sku
  admin_enabled       = var.enable_admin_user

  # Enable vulnerability scanning (Premium SKU only)
  dynamic "quarantine_policy_enabled" {
    for_each = var.sku == "Premium" ? [1] : []
    content {
      enabled = true
    }
  }

  # Retention policy (Premium SKU only)
  dynamic "retention_policy" {
    for_each = var.sku == "Premium" ? [1] : []
    content {
      days    = var.image_retention_days
      enabled = true
    }
  }

  # Trust policy (Premium SKU only)
  dynamic "trust_policy" {
    for_each = var.sku == "Premium" ? [1] : []
    content {
      enabled = true
    }
  }

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

variable "container_app_identity_principal_id" {
  description = "Principal ID of the container app managed identity"
  type        = string
  default     = ""
}

# Outputs
output "registry_url" {
  description = "Login server URL for the ACR"
  value       = azurerm_container_registry.main.login_server
}

output "registry_id" {
  description = "ID of the container registry"
  value       = azurerm_container_registry.main.id
}

output "registry_name" {
  description = "Name of the container registry"
  value       = azurerm_container_registry.main.name
}

output "admin_username" {
  description = "Admin username (only if admin_enabled is true)"
  value       = var.enable_admin_user ? azurerm_container_registry.main.admin_username : null
  sensitive   = true
}

output "admin_password" {
  description = "Admin password (only if admin_enabled is true)"
  value       = var.enable_admin_user ? azurerm_container_registry.main.admin_password : null
  sensitive   = true
}
