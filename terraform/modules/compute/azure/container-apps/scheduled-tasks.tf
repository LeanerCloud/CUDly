# Azure Logic Apps for scheduled tasks on Container Apps
# This is the Azure equivalent of AWS EventBridge + Lambda or GCP Cloud Scheduler + Cloud Run
#
# SECURITY: The shared scheduled-task secret is NEVER interpolated into the
# workflow definition or Terraform state. Each Logic App workflow has a
# system-assigned managed identity that holds "Key Vault Secrets User" on the
# vault that stores `scheduled-task-secret`. At workflow runtime the first
# action (`get-secret`) calls the Key Vault data-plane REST API authenticated
# by the workflow's managed identity, and the call-endpoint action references
# `@body('get-secret')['value']` in the outgoing Authorization header.
#
# Effect: `terraform show` / `az logicapp show` only ever reveal the Key Vault
# URL the workflow is going to call — the actual secret value is fetched
# in-process by the Logic Apps engine and never lands in any persisted artifact.

# Parse cron schedule into Logic Apps recurrence format
# Azure Logic Apps uses a different format than cron
# Convert "0 2 * * *" to: frequency=Day, interval=1, startTime=02:00
locals {
  # For simplicity, support daily schedules at a specific hour
  # Full cron parsing would require more complex logic
  schedule_hour = var.enable_scheduled_tasks ? split(" ", var.recommendation_schedule)[1] : "2"

  # Data-plane URL of the scheduled-task secret in Key Vault. The Logic App
  # workflows fetch this at runtime via managed identity. `key_vault_uri`
  # already includes the trailing slash (e.g. https://<vault>.vault.azure.net/).
  scheduled_task_secret_url = (
    var.enable_scheduled_tasks || var.enable_ri_exchange_schedule
  ) ? "${var.key_vault_uri}secrets/${var.scheduled_task_secret_name}?api-version=7.4" : ""
}

# Plan-time guard: if any scheduled-task workflow is enabled, the secret name
# and key vault URI must be set correctly. Without these checks, an empty
# scheduled_task_secret_name silently produces `<vault>/secrets/?api-version=...`
# (the list-secrets endpoint), and a key_vault_uri without a trailing slash
# breaks the URL. Both surface late as runtime 401/403; the precondition
# fails them at plan/apply instead.
resource "terraform_data" "scheduled_task_secret_preconditions" {
  count = (var.enable_scheduled_tasks || var.enable_ri_exchange_schedule) ? 1 : 0

  lifecycle {
    precondition {
      condition     = length(var.scheduled_task_secret_name) > 0
      error_message = "scheduled_task_secret_name must be set when enable_scheduled_tasks or enable_ri_exchange_schedule is true."
    }
    precondition {
      condition     = endswith(var.key_vault_uri, "/")
      error_message = "key_vault_uri must end with '/' (e.g. https://<vault>.vault.azure.net/)."
    }
  }
}

# ==============================================
# Logic App workflow for recommendations refresh
# ==============================================

resource "azurerm_logic_app_workflow" "recommendations" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name                = "${var.app_name}-recommendations"
  location            = var.location
  resource_group_name = var.resource_group_name

  # System-assigned managed identity used to read the shared secret from
  # Key Vault at workflow runtime. See header comment.
  identity {
    type = "SystemAssigned"
  }

  tags = var.tags
}

# Recurrence trigger (daily)
resource "azurerm_logic_app_trigger_recurrence" "daily" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "daily-trigger"
  logic_app_id = azurerm_logic_app_workflow.recommendations[0].id
  frequency    = "Day"
  interval     = 1
  start_time   = "${formatdate("YYYY-MM-DD", timestamp())}T${format("%02s", local.schedule_hour)}:00:00Z"
  time_zone    = "UTC"
}

# Step 1: Fetch the shared secret from Key Vault using the workflow's
# system-assigned managed identity. The secret value lives in the workflow
# run's transient state only — never in the workflow definition or TF state.
resource "azurerm_logic_app_action_custom" "recommendations_get_secret" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "get-secret"
  logic_app_id = azurerm_logic_app_workflow.recommendations[0].id

  body = jsonencode({
    type = "Http"
    inputs = {
      method = "GET"
      uri    = local.scheduled_task_secret_url
      authentication = {
        type     = "ManagedServiceIdentity"
        audience = "https://vault.azure.net"
      }
    }
    runAfter = {}
    runtimeConfiguration = {
      secureData = {
        properties = ["outputs"]
      }
    }
  })

  # Ensure the role assignment exists before this action so the very first
  # post-apply manual run doesn't 403 while RBAC propagation completes.
  # Scheduled runs (next 02:00 UTC) almost certainly fall after propagation,
  # but this keeps `terraform apply && trigger now` deterministic.
  depends_on = [azurerm_role_assignment.recommendations_kv_secrets_user]
}

# Step 2: Call the Container App scheduled-recommendations endpoint, using
# the secret pulled by the previous action. `@body('get-secret')['value']` is
# evaluated by the Logic Apps engine at runtime; it is never expanded into
# the persisted workflow definition.
resource "azurerm_logic_app_action_custom" "call_recommendations" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "call-recommendations-endpoint"
  logic_app_id = azurerm_logic_app_workflow.recommendations[0].id

  body = jsonencode({
    type = "Http"
    inputs = {
      method = "POST"
      uri    = "https://${azurerm_container_app.main.ingress[0].fqdn}/api/scheduled/recommendations"
      headers = {
        "Content-Type"  = "application/json"
        "Authorization" = "Bearer @{body('get-secret')['value']}"
        "X-Trigger"     = "scheduled"
        "X-Source"      = "azure-logic-apps"
      }
      body = {
        source    = "azure-logic-apps"
        timestamp = "@{utcNow()}"
      }
    }
    runAfter = {
      "get-secret" = ["Succeeded"]
    }
    runtimeConfiguration = {
      secureData = {
        properties = ["inputs"]
      }
    }
  })

  depends_on = [azurerm_logic_app_action_custom.recommendations_get_secret]
}

# ==============================================
# Logic App workflow for RI Exchange Automation
# ==============================================

# Parse RI exchange cron schedule hour (same approach as recommendations)
locals {
  ri_exchange_schedule_hour = var.enable_ri_exchange_schedule ? split(" ", var.ri_exchange_schedule)[1] : "0"
  # Extract interval from cron hour field (e.g., "*/6" -> 6)
  ri_exchange_interval = var.enable_ri_exchange_schedule ? (
    can(regex("\\*/([0-9]+)", local.ri_exchange_schedule_hour))
    ? tonumber(regex("\\*/([0-9]+)", local.ri_exchange_schedule_hour)[0])
    : 24
  ) : 6
}

resource "azurerm_logic_app_workflow" "ri_exchange" {
  count = var.enable_ri_exchange_schedule ? 1 : 0

  name                = "${var.app_name}-ri-exchange"
  location            = var.location
  resource_group_name = var.resource_group_name

  identity {
    type = "SystemAssigned"
  }

  tags = var.tags
}

resource "azurerm_logic_app_trigger_recurrence" "ri_exchange" {
  count = var.enable_ri_exchange_schedule ? 1 : 0

  name         = "ri-exchange-trigger"
  logic_app_id = azurerm_logic_app_workflow.ri_exchange[0].id
  frequency    = "Hour"
  interval     = local.ri_exchange_interval
  start_time   = "${formatdate("YYYY-MM-DD", timestamp())}T00:00:00Z"
  time_zone    = "UTC"
}

resource "azurerm_logic_app_action_custom" "ri_exchange_get_secret" {
  count = var.enable_ri_exchange_schedule ? 1 : 0

  name         = "get-secret"
  logic_app_id = azurerm_logic_app_workflow.ri_exchange[0].id

  body = jsonencode({
    type = "Http"
    inputs = {
      method = "GET"
      uri    = local.scheduled_task_secret_url
      authentication = {
        type     = "ManagedServiceIdentity"
        audience = "https://vault.azure.net"
      }
    }
    runAfter = {}
    runtimeConfiguration = {
      secureData = {
        properties = ["outputs"]
      }
    }
  })

  # See recommendations_get_secret.depends_on rationale.
  depends_on = [azurerm_role_assignment.ri_exchange_kv_secrets_user]
}

resource "azurerm_logic_app_action_custom" "call_ri_exchange" {
  count = var.enable_ri_exchange_schedule ? 1 : 0

  name         = "call-ri-exchange-endpoint"
  logic_app_id = azurerm_logic_app_workflow.ri_exchange[0].id

  body = jsonencode({
    type = "Http"
    inputs = {
      method = "POST"
      uri    = "https://${azurerm_container_app.main.ingress[0].fqdn}/api/scheduled/ri-exchange"
      headers = {
        "Content-Type"  = "application/json"
        "Authorization" = "Bearer @{body('get-secret')['value']}"
        "X-Trigger"     = "scheduled"
        "X-Source"      = "azure-logic-apps"
      }
      body = {
        source    = "azure-logic-apps"
        timestamp = "@{utcNow()}"
      }
    }
    runAfter = {
      "get-secret" = ["Succeeded"]
    }
    runtimeConfiguration = {
      secureData = {
        properties = ["inputs"]
      }
    }
  })

  depends_on = [azurerm_logic_app_action_custom.ri_exchange_get_secret]
}

# ==============================================
# Logic App workflow for cleanup (sessions and executions)
# ==============================================

resource "azurerm_logic_app_workflow" "cleanup" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name                = "${var.app_name}-cleanup"
  location            = var.location
  resource_group_name = var.resource_group_name

  identity {
    type = "SystemAssigned"
  }

  tags = var.tags
}

# Recurrence trigger for cleanup (daily at 3 AM UTC)
resource "azurerm_logic_app_trigger_recurrence" "cleanup_daily" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "cleanup-trigger"
  logic_app_id = azurerm_logic_app_workflow.cleanup[0].id
  frequency    = "Day"
  interval     = 1
  start_time   = "${formatdate("YYYY-MM-DD", timestamp())}T03:00:00Z"
  time_zone    = "UTC"
}

resource "azurerm_logic_app_action_custom" "cleanup_get_secret" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "get-secret"
  logic_app_id = azurerm_logic_app_workflow.cleanup[0].id

  body = jsonencode({
    type = "Http"
    inputs = {
      method = "GET"
      uri    = local.scheduled_task_secret_url
      authentication = {
        type     = "ManagedServiceIdentity"
        audience = "https://vault.azure.net"
      }
    }
    runAfter = {}
    runtimeConfiguration = {
      secureData = {
        properties = ["outputs"]
      }
    }
  })

  # See recommendations_get_secret.depends_on rationale.
  depends_on = [azurerm_role_assignment.cleanup_kv_secrets_user]
}

resource "azurerm_logic_app_action_custom" "call_cleanup" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "call-cleanup-endpoint"
  logic_app_id = azurerm_logic_app_workflow.cleanup[0].id

  body = jsonencode({
    type = "Http"
    inputs = {
      method = "POST"
      uri    = "https://${azurerm_container_app.main.ingress[0].fqdn}/api/scheduled/cleanup"
      headers = {
        "Content-Type"  = "application/json"
        "Authorization" = "Bearer @{body('get-secret')['value']}"
        "X-Trigger"     = "scheduled"
        "X-Source"      = "azure-logic-apps"
      }
      body = {
        dryRun = false
        source = "azure-logic-apps"
      }
    }
    runAfter = {
      "get-secret" = ["Succeeded"]
    }
    runtimeConfiguration = {
      secureData = {
        properties = ["inputs"]
      }
    }
  })

  depends_on = [azurerm_logic_app_action_custom.cleanup_get_secret]
}

# ==============================================
# RBAC: grant each Logic App's managed identity read access to the
# scheduled-task secret in Key Vault.
# ==============================================

# Use a single role assignment per workflow rather than per secret. The grant
# is "Key Vault Secrets User" (read-only) scoped to the whole vault, matching
# the same pattern used by the container app's runtime identity. Vault-scoped
# read access is acceptable here because each workflow only ever reads one
# specific secret URL embedded in its definition; adding a per-secret RBAC
# scope would require splitting the vault, which is out of scope for this
# change.

resource "azurerm_role_assignment" "recommendations_kv_secrets_user" {
  count = var.enable_scheduled_tasks ? 1 : 0

  scope                = var.key_vault_id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_logic_app_workflow.recommendations[0].identity[0].principal_id
}

resource "azurerm_role_assignment" "ri_exchange_kv_secrets_user" {
  count = var.enable_ri_exchange_schedule ? 1 : 0

  scope                = var.key_vault_id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_logic_app_workflow.ri_exchange[0].identity[0].principal_id
}

resource "azurerm_role_assignment" "cleanup_kv_secrets_user" {
  count = var.enable_scheduled_tasks ? 1 : 0

  scope                = var.key_vault_id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_logic_app_workflow.cleanup[0].identity[0].principal_id
}
