terraform {
  required_version = ">= 1.5"

  required_providers {
    azurerm = {
      # Deliberately >= 3.0 (not ~> 3.0): this module is consumed by both
      # terraform/environments/azure/ci-cd-permissions (azurerm ~> 3.0) and
      # iac/federation/azure-target/terraform (azurerm ~> 4.0), so the
      # constraint must intersect with both provider lines.
      source  = "hashicorp/azurerm"
      version = ">= 3.0"
    }
  }
}

# Custom role: grants the exact Microsoft.Capacity and Microsoft.BillingBenefits
# actions used by the calculatePrice -> purchase flow. The built-in Reservation
# Purchaser role lacks reservationOrders/purchase/action, which causes 403 on
# production reservation purchases.
#
# Used by both:
#   - customer-side IaC (iac/federation/azure-target/terraform) for the
#     customer SP that CUDly authenticates with via workload identity federation
#   - host-side IaC (terraform/modules/compute/azure/container-apps) for the
#     container-app user-assigned identity running the CUDly process itself
#
# Keep the actions list here in sync with arm/CUDly-CrossSubscription/template.json.
locals {
  # Single source of truth for the role display name. Consumers that look the
  # role up via data.azurerm_role_definition (rather than creating it) must
  # reconstruct this exact string; see role_definition_name output below.
  role_definition_name = "CUDly Reservation Purchaser (custom) - ${var.name_suffix}"
}

resource "azurerm_role_definition" "cudly_reservation_purchaser" {
  name        = local.role_definition_name
  scope       = var.scope
  description = "Custom role granting CUDly exactly the Microsoft.Capacity and Microsoft.BillingBenefits actions required by the calculatePrice -> purchase flow. Replaces the built-in Reservation Purchaser, which lacks reservationOrders/purchase/action."

  permissions {
    actions = [
      "Microsoft.Capacity/register/action",
      "Microsoft.Capacity/calculatePrice/action",
      "Microsoft.Capacity/catalogs/read",
      "Microsoft.Capacity/reservationOrders/read",
      "Microsoft.Capacity/reservationOrders/write",
      "Microsoft.Capacity/reservationOrders/purchase/action",
      "Microsoft.Capacity/reservationOrders/reservations/read",
      "Microsoft.BillingBenefits/register/action",
      "Microsoft.BillingBenefits/savingsPlanOrderAliases/write",
      "Microsoft.BillingBenefits/savingsPlanOrders/read",
      "Microsoft.BillingBenefits/savingsPlanOrders/savingsPlans/read",
      "Microsoft.BillingBenefits/savingsPlanOrders/action",
    ]
    not_actions = []
  }

  # assignable_scopes is intentionally limited to the subscription scope.
  #
  # The tenant-root provider scope "/providers/Microsoft.Capacity" was removed:
  # a subscription-scoped principal cannot register an assignable scope above its
  # own subscription, so including it makes the role-definition write 403 at the
  # higher scope. The role is only ever assigned at subscription scope (see the
  # azurerm_role_assignment blocks in the consuming modules), so the subscription
  # scope is sufficient. Set include_capacity_provider_scope = true only when the
  # applying principal has tenant-root authority and a tenant-level assignment is
  # actually required.
  assignable_scopes = compact([
    var.scope,
    var.include_capacity_provider_scope ? "/providers/Microsoft.Capacity" : "",
  ])
}
