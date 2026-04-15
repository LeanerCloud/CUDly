# Azure Container Apps Module
# Serverless container platform with automatic scaling
#
# ARCHITECTURE NOTE:
# This module uses the Consumption workload profile (default) which runs on X86_64 architecture.
# ARM64 support is available in Azure Container Apps via Dedicated workload profiles, but requires:
# - Minimum capacity commitment (less flexible for variable workloads)
# - Higher base cost compared to Consumption plan
#
# Decision: Stay with Consumption plan (X86_64) for cost flexibility and pay-per-use pricing.
# For predictable workloads with consistent usage, Dedicated ARM64 profiles can reduce costs by 13%.
#
# To enable ARM64 in the future (Dedicated plan):
# Add workload_profile block to azurerm_container_app_environment with ARM64 profile type

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}

# ==============================================
# Container App Environment
# ==============================================

resource "azurerm_container_app_environment" "main" {
  name                = "${var.app_name}-env"
  location            = var.location
  resource_group_name = var.resource_group_name

  # VNet integration
  infrastructure_subnet_id       = var.infrastructure_subnet_id
  internal_load_balancer_enabled = var.internal_load_balancer_enabled

  # Logging
  log_analytics_workspace_id = var.log_analytics_workspace_id

  # ARCHITECTURE: Consumption plan (default)
  # Uses X86_64 architecture for maximum flexibility and pay-per-use pricing
  # No workload_profile block = Consumption plan (serverless, auto-scaling)

  tags = merge(var.tags, {
    managed_by   = "terraform"
    architecture = "x86_64"
  })

  lifecycle {
    ignore_changes = [
      # Azure auto-generates this; ignoring prevents unnecessary force-replacement
      infrastructure_resource_group_name,
    ]
  }
}

# ==============================================
# Managed Identity for Container App
# ==============================================

resource "azurerm_user_assigned_identity" "container_app" {
  name                = "${var.app_name}-identity"
  location            = var.location
  resource_group_name = var.resource_group_name

  tags = var.tags
}

# ==============================================
# Container App
# ==============================================

resource "azurerm_container_app" "main" {
  name                         = var.app_name
  container_app_environment_id = azurerm_container_app_environment.main.id
  resource_group_name          = var.resource_group_name
  revision_mode                = "Single"

  # Managed identity
  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.container_app.id]
  }

  # Registry authentication
  dynamic "registry" {
    for_each = var.registry_server != "" ? [1] : []
    content {
      server               = var.registry_server
      username             = var.registry_username
      password_secret_name = "registry-password"
    }
  }

  # Container configuration
  template {
    # Scaling
    min_replicas = var.min_replicas
    max_replicas = var.max_replicas

    # Container
    container {
      name   = "main"
      image  = var.image_uri
      cpu    = var.cpu
      memory = var.memory

      # Environment variables
      dynamic "env" {
        for_each = merge(
          {
            ENVIRONMENT           = var.environment
            RUNTIME_MODE          = "http"
            DB_HOST               = var.database_host
            DB_PORT               = "5432"
            DB_NAME               = var.database_name
            DB_USER               = var.database_username
            DB_PASSWORD_SECRET    = var.database_password_secret_name
            DB_SSL_MODE           = "require"
            DB_CONNECT_TIMEOUT    = "8s"
            DB_AUTO_MIGRATE       = tostring(var.auto_migrate)
            DB_MIGRATIONS_PATH    = "/app/migrations"
            ADMIN_EMAIL           = var.admin_email
            ADMIN_PASSWORD_SECRET = var.admin_password_secret_name
            SECRET_PROVIDER       = "azure"
            AZURE_CLIENT_ID       = azurerm_user_assigned_identity.container_app.client_id
            AZURE_KEY_VAULT_URL   = var.key_vault_uri
            AZURE_REGION          = var.location
            PORT                  = "8080"
            ALLOWED_ORIGINS       = join(",", var.allowed_origins)
            # OIDC issuer signing key — see internal/oidc/azure_signer.go.
            CUDLY_SOURCE_CLOUD          = "azure"
            CUDLY_SIGNING_KEY_VAULT_URL = var.key_vault_uri
            CUDLY_SIGNING_KEY_NAME      = var.signing_key_name
          },
          var.additional_env_vars
        )
        content {
          name  = env.key
          value = env.value
        }
      }

      # Liveness probe
      liveness_probe {
        transport = "HTTP"
        port      = 8080
        path      = "/health"

        initial_delay           = 10
        interval_seconds        = 30
        timeout                 = 3
        failure_count_threshold = 3
      }

      # Readiness probe
      readiness_probe {
        transport = "HTTP"
        port      = 8080
        path      = "/health"

        interval_seconds        = 10
        timeout                 = 3
        failure_count_threshold = 3
        success_count_threshold = 1
      }

      # Startup probe
      startup_probe {
        transport = "HTTP"
        port      = 8080
        path      = "/health"

        interval_seconds        = 10
        timeout                 = 3
        failure_count_threshold = 3
      }
    }
  }

  # Ingress configuration
  ingress {
    external_enabled = var.external_ingress_enabled
    target_port      = 8080
    transport        = "auto"

    traffic_weight {
      latest_revision = true
      percentage      = 100
    }

    # CORS (if needed)
    dynamic "custom_domain" {
      for_each = var.custom_domains
      content {
        name           = custom_domain.value.name
        certificate_id = custom_domain.value.certificate_id
      }
    }
  }

  # Secrets (for Key Vault references)
  dynamic "secret" {
    for_each = var.secrets
    content {
      name  = secret.value.name
      value = secret.value.value
    }
  }

  # Registry password secret (for ACR admin auth)
  dynamic "secret" {
    for_each = var.registry_server != "" ? [1] : []
    content {
      name  = "registry-password"
      value = var.registry_password
    }
  }

  tags = merge(var.tags, {
    managed_by   = "terraform"
    architecture = "x86_64"
  })
}

# ==============================================
# Runtime RBAC: CUDly application cloud API access
# ==============================================

data "azurerm_subscription" "current" {}

# Cost Management Reader: grants access to Azure Consumption API (reservation
# recommendations, reservation details) needed for CUDly RI/SP features.
resource "azurerm_role_assignment" "cost_management_reader" {
  scope                = data.azurerm_subscription.current.id
  role_definition_name = "Cost Management Reader"
  principal_id         = azurerm_user_assigned_identity.container_app.principal_id
}

# Reservation Purchaser: allows writing reservationOrders so CUDly can
# purchase/exchange Azure reservations on behalf of users.
# Note: "Reservations Reader" does not exist as a built-in Azure role.
# Read access to reservations is covered by the Reservation Purchaser role itself.
resource "azurerm_role_assignment" "reservations_purchaser" {
  scope                = data.azurerm_subscription.current.id
  role_definition_name = "Reservation Purchaser"
  principal_id         = azurerm_user_assigned_identity.container_app.principal_id
}

# Key Vault Crypto User: allows the container app's managed identity
# to call Sign + GetKey on the OIDC signing key. The key never leaves
# the vault — the app only receives signatures computed inside Azure.
resource "azurerm_role_assignment" "signing_key_crypto_user" {
  scope                = var.signing_key_id
  role_definition_name = "Key Vault Crypto User"
  principal_id         = azurerm_user_assigned_identity.container_app.principal_id
}

# ==============================================
# Diagnostic Settings
# ==============================================

resource "azurerm_monitor_diagnostic_setting" "container_app" {
  count = var.enable_diagnostics ? 1 : 0

  name                       = "${var.app_name}-container-app-diag"
  target_resource_id         = azurerm_container_app.main.id
  log_analytics_workspace_id = var.log_analytics_workspace_id

  enabled_log {
    category = "ContainerAppSystemLogs"
  }

  metric {
    category = "AllMetrics"
    enabled  = true
  }
}
