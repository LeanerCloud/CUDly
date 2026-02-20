variable "resource_group_name" {
  description = "Name of the resource group"
  type        = string
}

variable "location" {
  description = "Azure region"
  type        = string
}

variable "function_app_name" {
  description = "Name of the Function App"
  type        = string
}

variable "storage_account_name" {
  description = "Name of the storage account for Function App"
  type        = string
}

variable "image_uri" {
  description = "Container image URI for the cleanup function"
  type        = string
}

variable "db_host" {
  description = "Database host (Azure Database for PostgreSQL FQDN)"
  type        = string
}

variable "db_password_secret_uri" {
  description = "Azure Key Vault secret URI containing the database password"
  type        = string
}

variable "key_vault_id" {
  description = "ID of the Key Vault containing secrets"
  type        = string
}

variable "subnet_id" {
  description = "Subnet ID for VNet integration"
  type        = string
  default     = ""
}

variable "schedule" {
  description = "CRON schedule (NCRONTAB format)"
  type        = string
  default     = "0 2 * * *"
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}

# Storage account for Function App
resource "azurerm_storage_account" "cleanup" {
  name                     = var.storage_account_name
  resource_group_name      = var.resource_group_name
  location                 = var.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
  tags                     = var.tags
}

# App Service Plan for Linux containers
resource "azurerm_service_plan" "cleanup" {
  name                = "${var.function_app_name}-plan"
  resource_group_name = var.resource_group_name
  location            = var.location
  os_type             = "Linux"
  sku_name            = "B1" # Basic tier for scheduled functions

  tags = var.tags
}

# Managed identity for Function App
resource "azurerm_user_assigned_identity" "cleanup" {
  name                = "${var.function_app_name}-identity"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = var.tags
}

# Grant Key Vault access to managed identity
resource "azurerm_key_vault_access_policy" "cleanup" {
  key_vault_id = var.key_vault_id
  tenant_id    = azurerm_user_assigned_identity.cleanup.tenant_id
  object_id    = azurerm_user_assigned_identity.cleanup.principal_id

  secret_permissions = [
    "Get",
    "List"
  ]
}

# Linux Function App with container
resource "azurerm_linux_function_app" "cleanup" {
  name                = var.function_app_name
  resource_group_name = var.resource_group_name
  location            = var.location
  service_plan_id     = azurerm_service_plan.cleanup.id

  storage_account_name       = azurerm_storage_account.cleanup.name
  storage_account_access_key = azurerm_storage_account.cleanup.primary_access_key

  # Use container image
  site_config {
    always_on = true

    application_stack {
      docker {
        registry_url = split("/", var.image_uri)[0]
        image_name   = join("/", slice(split("/", var.image_uri), 1, length(split("/", var.image_uri)) - 1))
        image_tag    = split(":", var.image_uri)[1]
      }
    }

    # VNet integration
    dynamic "vnet_route_all_enabled" {
      for_each = var.subnet_id != "" ? [1] : []
      content {
        vnet_route_all_enabled = true
      }
    }
  }

  # App settings (environment variables)
  app_settings = {
    DB_HOST            = var.db_host
    DB_PORT            = "5432"
    DB_NAME            = "cudly"
    DB_USER            = "cudly"
    DB_PASSWORD_SECRET = var.db_password_secret_uri
    DB_SSL_MODE        = "require"
    SECRET_PROVIDER    = "azure"
    AZURE_KEY_VAULT_URL = replace(var.db_password_secret_uri, "/secrets/.*", "")

    # Function runtime settings
    FUNCTIONS_WORKER_RUNTIME       = "custom"
    WEBSITES_ENABLE_APP_SERVICE_STORAGE = "false"
  }

  # Managed identity
  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.cleanup.id]
  }

  # VNet integration
  dynamic "virtual_network_subnet_id" {
    for_each = var.subnet_id != "" ? [1] : []
    content {
      virtual_network_subnet_id = var.subnet_id
    }
  }

  tags = var.tags
}

# Timer trigger function (defined in host.json and function.json)
# Note: Azure Functions require function.json in the container image
# The container should have a structure like:
#   /home/site/wwwroot/cleanup/function.json
#   /home/site/wwwroot/host.json

# Logic App workflow to trigger the function (alternative to timer trigger)
resource "azurerm_logic_app_workflow" "cleanup_trigger" {
  name                = "${var.function_app_name}-trigger"
  location            = var.location
  resource_group_name = var.resource_group_name
  tags                = var.tags
}

# Recurrence trigger
resource "azurerm_logic_app_trigger_recurrence" "cleanup" {
  name         = "cleanup-schedule"
  logic_app_id = azurerm_logic_app_workflow.cleanup_trigger.id
  frequency    = "Day"
  interval     = 1
  start_time   = formatdate("YYYY-MM-DD'T'02:00:00'Z'", timestamp())
  time_zone    = "UTC"
}

# HTTP action to call Function App
resource "azurerm_logic_app_action_http" "cleanup" {
  name         = "call-cleanup-function"
  logic_app_id = azurerm_logic_app_workflow.cleanup_trigger.id
  method       = "POST"
  uri          = "https://${azurerm_linux_function_app.cleanup.default_hostname}/api/cleanup"

  headers = {
    "Content-Type" = "application/json"
    "x-functions-key" = "@listKeys('${azurerm_linux_function_app.cleanup.id}/host/default', '2022-03-01').functionKeys.default"
  }

  body = jsonencode({
    dryRun = false
  })
}

# Outputs
output "function_app_name" {
  description = "Name of the Function App"
  value       = azurerm_linux_function_app.cleanup.name
}

output "function_app_url" {
  description = "Default hostname of the Function App"
  value       = "https://${azurerm_linux_function_app.cleanup.default_hostname}"
}

output "identity_principal_id" {
  description = "Principal ID of the managed identity"
  value       = azurerm_user_assigned_identity.cleanup.principal_id
}

output "schedule" {
  description = "Cleanup schedule"
  value       = var.schedule
}
