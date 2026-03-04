# Azure Key Vault Module
# Manages application secrets with RBAC access control

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
    azuread = {
      source  = "hashicorp/azuread"
      version = "~> 2.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

# Get current client for Key Vault access policy
data "azurerm_client_config" "current" {}

# ==============================================
# Key Vault
# ==============================================

resource "azurerm_key_vault" "main" {
  name                = var.key_vault_name
  location            = var.location
  resource_group_name = var.resource_group_name
  tenant_id           = data.azurerm_client_config.current.tenant_id

  sku_name = var.sku_name

  # Enable RBAC authorization
  enable_rbac_authorization = true

  # Soft delete
  soft_delete_retention_days = var.soft_delete_retention_days
  purge_protection_enabled   = var.purge_protection_enabled

  # Network ACLs
  network_acls {
    bypass                     = "AzureServices"
    default_action             = var.default_network_acl_action
    ip_rules                   = var.allowed_ip_addresses
    virtual_network_subnet_ids = var.allowed_subnet_ids
  }

  tags = merge(var.tags, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

# ==============================================
# Database Password Secret
# ==============================================

resource "random_password" "database" {
  count = var.database_password == null ? 1 : 0

  length           = 32
  special          = true
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

resource "azurerm_key_vault_secret" "database_password" {
  name         = "db-password"
  value        = var.database_password != null ? var.database_password : random_password.database[0].result
  key_vault_id = azurerm_key_vault.main.id

  content_type = "password"

  tags = merge(var.tags, {
    environment = var.environment
  })

  depends_on = [azurerm_role_assignment.current_user_secrets_officer]
}

# ==============================================
# Application Secrets
# ==============================================

# JWT signing secret
resource "random_password" "jwt_secret" {
  count = var.create_jwt_secret ? 1 : 0

  length  = 64
  special = false # Base64-friendly
}

resource "azurerm_key_vault_secret" "jwt_secret" {
  count = var.create_jwt_secret ? 1 : 0

  name         = "jwt-secret"
  value        = random_password.jwt_secret[0].result
  key_vault_id = azurerm_key_vault.main.id

  content_type = "secret"

  tags = merge(var.tags, {
    environment = var.environment
  })

  depends_on = [azurerm_role_assignment.current_user_secrets_officer]
}

# Session encryption secret
resource "random_password" "session_secret" {
  count = var.create_session_secret ? 1 : 0

  length  = 64
  special = false # Base64-friendly
}

resource "azurerm_key_vault_secret" "session_secret" {
  count = var.create_session_secret ? 1 : 0

  name         = "session-secret"
  value        = random_password.session_secret[0].result
  key_vault_id = azurerm_key_vault.main.id

  content_type = "secret"

  tags = merge(var.tags, {
    environment = var.environment
  })

  depends_on = [azurerm_role_assignment.current_user_secrets_officer]
}

# ==============================================
# Azure Communication Services SMTP Secrets
# ==============================================
#
# IMPORTANT: Azure Communication Services SMTP credentials cannot be auto-generated
# via Terraform. The Azure Resource Manager API does not expose an endpoint for
# creating or rotating SMTP credentials — this is a manual Azure Portal step.
#
# These secrets are created with placeholder values that must be replaced manually
# after the initial `terraform apply`.
#
# Prerequisites:
#   - An Azure Communication Services resource with a connected email domain
#   - The email domain must be verified and active
#
# Steps to generate SMTP credentials:
#   1. Open Azure Portal -> Azure Communication Services -> your resource
#   2. Go to "Email" -> select your connected email domain
#   3. Navigate to "Settings" -> "Keys" to get the primary connection string
#   4. The SMTP credentials are derived from the connection string:
#      - SMTP Endpoint: smtp.azurecomm.net
#      - SMTP Port: 587 (STARTTLS)
#      - Username: <ACS-Resource-Name>.<Primary-Key>.smtp (see Azure docs for exact format)
#      - Password: The primary key from the connection string
#   5. Update the Key Vault secrets:
#        az keyvault secret set --vault-name <vault> --name azure-smtp-username --value "<username>"
#        az keyvault secret set --vault-name <vault> --name azure-smtp-password --value "<password>"
#   6. Restart the Container App / AKS pods to pick up the new credentials
#
# Alternative: Pass smtp_username and smtp_password variables during terraform apply
# to avoid the manual portal step (useful for CI/CD pipelines where credentials are
# pre-generated or stored in a separate vault).

# SMTP Username (from Azure Communication Services)
resource "azurerm_key_vault_secret" "smtp_username" {
  count = var.create_smtp_secrets ? 1 : 0

  name         = "azure-smtp-username"
  value        = var.smtp_username != null ? var.smtp_username : "PLACEHOLDER_GENERATE_IN_AZURE_PORTAL"
  key_vault_id = azurerm_key_vault.main.id

  content_type = "smtp-credential"

  tags = merge(var.tags, {
    environment = var.environment
  })

  depends_on = [azurerm_role_assignment.current_user_secrets_officer]
}

# SMTP Password (from Azure Communication Services)
resource "azurerm_key_vault_secret" "smtp_password" {
  count = var.create_smtp_secrets ? 1 : 0

  name         = "azure-smtp-password"
  value        = var.smtp_password != null ? var.smtp_password : "PLACEHOLDER_GENERATE_IN_AZURE_PORTAL"
  key_vault_id = azurerm_key_vault.main.id

  content_type = "smtp-credential"

  tags = merge(var.tags, {
    environment = var.environment
  })

  depends_on = [azurerm_role_assignment.current_user_secrets_officer]
}

# ==============================================
# Scheduled Task Secret
# ==============================================

resource "random_password" "scheduled_task_secret" {
  count = var.create_scheduled_task_secret ? 1 : 0

  length  = 64
  special = false
}

resource "azurerm_key_vault_secret" "scheduled_task_secret" {
  count = var.create_scheduled_task_secret ? 1 : 0

  name         = "scheduled-task-secret"
  value        = random_password.scheduled_task_secret[0].result
  key_vault_id = azurerm_key_vault.main.id

  content_type = "secret"

  tags = merge(var.tags, {
    environment = var.environment
  })

  depends_on = [azurerm_role_assignment.current_user_secrets_officer]
}

# ==============================================
# Additional Custom Secrets
# ==============================================

resource "azurerm_key_vault_secret" "additional" {
  for_each = var.additional_secrets

  name         = each.key
  value        = each.value
  key_vault_id = azurerm_key_vault.main.id

  content_type = "secret"

  tags = merge(var.tags, {
    environment = var.environment
  })

  depends_on = [azurerm_role_assignment.current_user_secrets_officer]
}

# ==============================================
# RBAC Assignments
# ==============================================

# Grant current user Secrets Officer role (for Terraform to manage secrets)
resource "azurerm_role_assignment" "current_user_secrets_officer" {
  scope                = azurerm_key_vault.main.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = data.azurerm_client_config.current.object_id
}

# Grant Container App managed identity Secrets User role
resource "azurerm_role_assignment" "container_app_secrets_user" {
  count = var.container_app_identity_principal_id != null ? 1 : 0

  scope                = azurerm_key_vault.main.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = var.container_app_identity_principal_id
}

# ==============================================
# Diagnostic Settings (Optional)
# ==============================================

resource "azurerm_monitor_diagnostic_setting" "key_vault" {
  count = var.log_analytics_workspace_id != null ? 1 : 0

  name                       = "${var.app_name}-keyvault-diag"
  target_resource_id         = azurerm_key_vault.main.id
  log_analytics_workspace_id = var.log_analytics_workspace_id

  enabled_log {
    category = "AuditEvent"
  }

  metric {
    category = "AllMetrics"
    enabled  = true
  }
}
