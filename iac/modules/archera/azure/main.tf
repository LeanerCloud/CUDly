# ==============================================
# Archera Integration — Azure (Shared Module)
# ==============================================
#
# Single source of truth for the Archera Azure RBAC resources.
# The permission lists are loaded from scope.azure.yaml in the parent directory —
# edit that file to update permissions across all callers simultaneously.

terraform {
  required_version = ">= 1.5"
  required_providers {
    azurerm = {
      source = "hashicorp/azurerm"
      # Accept 3.x (environment) and 4.x (federation bundle) — caller pins the exact major.
      version = ">= 3.0"
    }
  }
}

locals {
  # Load the canonical permission list from the YAML source of truth.
  scope = yamldecode(file("${path.module}/../scope.azure.yaml"))

  read_actions     = local.scope.read_actions
  purchase_actions = local.scope.purchase_actions
}

# Custom RBAC role for Archera — permission list from scope.azure.yaml.
# Do NOT grant broad Contributor or Cost Management Contributor predefined
# roles — they include write permissions on resource deployments.
resource "azurerm_role_definition" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  name        = "CUDly Archera Integration"
  scope       = "/subscriptions/${var.subscription_id}"
  description = "Archera integration role — read cost data, optionally purchase RIs (confirm scope before enabling)"

  permissions {
    actions = concat(
      local.read_actions,
      var.enable_archera_purchase_actions ? local.purchase_actions : []
    )
    not_actions = []
  }

  assignable_scopes = [
    "/subscriptions/${var.subscription_id}",
  ]
}

# Assign the custom role to Archera's service principal.
# archera_azure_sp_object_id must be the Object ID of the service principal
# that Archera provides during onboarding (NOT the Application/Client ID).
resource "azurerm_role_assignment" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  scope              = "/subscriptions/${var.subscription_id}"
  role_definition_id = azurerm_role_definition.archera_integration[0].role_definition_resource_id
  principal_id       = var.archera_azure_sp_object_id
  principal_type     = "ServicePrincipal"
}
