terraform {
  required_version = ">= 1.5"

  required_providers {
    azurerm = {
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
resource "azurerm_role_definition" "cudly_reservation_purchaser" {
  name        = "CUDly Reservation Purchaser (custom) - ${var.name_suffix}"
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

  assignable_scopes = [
    var.scope,
    "/providers/Microsoft.Capacity",
  ]
}
