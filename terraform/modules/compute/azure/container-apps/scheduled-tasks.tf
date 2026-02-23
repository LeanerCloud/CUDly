# Azure Logic Apps for scheduled tasks on Container Apps
# This is the Azure equivalent of AWS EventBridge + Lambda or GCP Cloud Scheduler + Cloud Run

# Parse cron schedule into Logic Apps recurrence format
# Azure Logic Apps uses a different format than cron
# Convert "0 2 * * *" to: frequency=Day, interval=1, startTime=02:00
locals {
  # For simplicity, support daily schedules at a specific hour
  # Full cron parsing would require more complex logic
  schedule_hour = var.enable_scheduled_tasks ? split(" ", var.recommendations_schedule)[1] : "2"
}

# Logic App workflow for recommendations refresh
resource "azurerm_logic_app_workflow" "recommendations" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name                = "${var.app_name}-recommendations"
  location            = var.location
  resource_group_name = var.resource_group_name

  tags = var.tags
}

# Recurrence trigger (daily)
resource "azurerm_logic_app_trigger_recurrence" "daily" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "daily-trigger"
  logic_app_id = azurerm_logic_app_workflow.recommendations[0].id
  frequency    = "Day"
  interval     = 1
  start_time   = formatdate("YYYY-MM-DD'T'${local.schedule_hour}:00:00'Z'", timestamp())
  time_zone    = "UTC"
}

# HTTP action to call Container App endpoint
resource "azurerm_logic_app_action_http" "call_recommendations" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "call-recommendations-endpoint"
  logic_app_id = azurerm_logic_app_workflow.recommendations[0].id

  method = "POST"
  uri    = "https://${azurerm_container_app.main.latest_revision_fqdn}/api/recommendations/refresh"

  headers = {
    "Content-Type" = "application/json"
    "X-Trigger"    = "scheduled"
    "X-Source"     = "azure-logic-apps"
  }

  body = jsonencode({
    source    = "azure-logic-apps"
    timestamp = "@{utcNow()}"
  })
}

# Optional: Add authentication to the HTTP call
# If your Container App requires authentication, uncomment this section
# resource "azurerm_logic_app_action_http" "call_recommendations" {
#   ...
#   authentication {
#     type = "ManagedServiceIdentity"
#   }
# }

# Logic App workflow for cleanup (sessions and executions)
resource "azurerm_logic_app_workflow" "cleanup" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name                = "${var.app_name}-cleanup"
  location            = var.location
  resource_group_name = var.resource_group_name

  tags = var.tags
}

# Recurrence trigger for cleanup (daily at 3 AM UTC)
resource "azurerm_logic_app_trigger_recurrence" "cleanup_daily" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "cleanup-trigger"
  logic_app_id = azurerm_logic_app_workflow.cleanup[0].id
  frequency    = "Day"
  interval     = 1
  start_time   = formatdate("YYYY-MM-DD'T'03:00:00'Z'", timestamp())
  time_zone    = "UTC"
}

# HTTP action to call cleanup endpoint
resource "azurerm_logic_app_action_http" "call_cleanup" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name         = "call-cleanup-endpoint"
  logic_app_id = azurerm_logic_app_workflow.cleanup[0].id

  method = "POST"
  uri    = "https://${azurerm_container_app.main.latest_revision_fqdn}/api/cleanup"

  headers = {
    "Content-Type" = "application/json"
    "X-Trigger"    = "scheduled"
    "X-Source"     = "azure-logic-apps"
  }

  body = jsonencode({
    dryRun = false
    source = "azure-logic-apps"
  })
}
