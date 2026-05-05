# ==============================================
# Archera Integration — Azure
# ==============================================
#
# When enable_archera = true, this block creates a custom RBAC role and
# assigns it to Archera's service principal, granting least-privilege
# read access to cost and commitment data.  Purchase-execution permissions
# are separately gated behind enable_archera_purchase_actions so
# telemetry-only rollouts never accidentally include financial writes.
#
# PROVISIONAL SCOPE — must be confirmed against Archera integration docs
# before flipping enable_archera = true in any tfvars.
# TODO(@cristim): confirm Archera scope list against integration docs
# before enabling.  Reference: https://archera.ai/docs (integration guide).
#
# Placement rationale (bootstrap vs runtime split):
#   Archera is a RUNTIME integration — it reads cost telemetry and submits
#   purchases during normal operation.  This file lives in the main
#   environment (alongside compute.tf / database.tf), NOT in
#   ci-cd-permissions/ (which is applied once by a privileged human and
#   grants deploy-SA capabilities only).

locals {
  archera_role_name_azure = "CUDly Archera Integration"
}

# Custom RBAC role for Archera — PROVISIONAL.
# Actions are scoped to read-only cost management and, optionally,
# reservation purchase execution via enable_archera_purchase_actions.
# Do NOT grant broad Contributor or Cost Management Contributor predefined
# roles — they include write permissions on resource deployments.
#
# TODO(@cristim): narrow to the exact action list from Archera's Azure
# onboarding docs before setting enable_archera = true.
resource "azurerm_role_definition" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  name        = local.archera_role_name_azure
  scope       = "/subscriptions/${var.subscription_id}"
  description = "Provisional Archera integration role — read cost data, optionally purchase RIs (confirm scope before enabling)"

  permissions {
    actions = concat(
      [
        # ── Read-only: Cost Management ──────────────────────────────────────
        # Archera needs to read historical usage and costs to size commitments.
        # TODO(@cristim): confirm whether Archera also needs
        # Microsoft.Consumption/*/read — add if required by their docs.
        "Microsoft.CostManagement/*/read",
        "Microsoft.Consumption/*/read",
        "Microsoft.Billing/*/read",

        # ── Read-only: Reserved Instances ─────────────────────────────────
        # Archera needs to see existing reservations to avoid over-purchasing.
        "Microsoft.Capacity/reservations/read",
        "Microsoft.Capacity/reservationOrders/read",
      ],
      # ── Purchase-execution: Reserved Instances ────────────────────────
      # Gated behind enable_archera_purchase_actions so financial writes
      # are never included by accident at initial rollout.
      # TODO(@cristim): enable only after confirming approval workflow with
      # Archera (i.e. Archera requires customer approval before purchases).
      var.enable_archera_purchase_actions ? [
        "Microsoft.Capacity/reservationOrders/write",
      ] : []
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
