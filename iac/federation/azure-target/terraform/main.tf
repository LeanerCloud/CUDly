terraform {
  required_version = ">= 1.5"
  required_providers {
    azuread = {
      source  = "hashicorp/azuread"
      version = "~> 3.8"
    }
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
    http = {
      source  = "hashicorp/http"
      version = ">= 3.4"
    }
  }
}

provider "azurerm" {
  subscription_id = var.subscription_id != "" ? var.subscription_id : null
  features {}
}

provider "azuread" {
  tenant_id = var.tenant_id != "" ? var.tenant_id : null
}

# Auto-detect subscription and tenant from CLI context.
data "azurerm_subscription" "current" {}
data "azuread_client_config" "current" {}

locals {
  subscription_id = var.subscription_id != "" ? var.subscription_id : data.azurerm_subscription.current.subscription_id
  tenant_id       = var.tenant_id != "" ? var.tenant_id : data.azuread_client_config.current.tenant_id
}

# App Registration
resource "azuread_application" "cudly" {
  display_name     = var.app_display_name
  sign_in_audience = "AzureADMyOrg"
}

# Service Principal for the App Registration
resource "azuread_service_principal" "cudly" {
  client_id = azuread_application.cudly.client_id
}

# Federated identity credential bound to CUDly's OIDC issuer.
# CUDly signs JWTs via its own KMS-backed OIDC issuer — no certificate
# or client secret is needed. Azure AD verifies the JWT signature by
# fetching the JWKS from the issuer's /.well-known/jwks.json endpoint.
resource "azuread_application_federated_identity_credential" "cudly" {
  application_id = azuread_application.cudly.id
  display_name   = "cudly"
  description    = "CUDly OIDC issuer (KMS-backed). No secret stored."
  audiences      = [var.cudly_federated_audience]
  issuer         = var.cudly_issuer_url
  subject        = var.cudly_federated_subject
}

# Custom role: grants the exact Microsoft.Capacity and Microsoft.BillingBenefits actions
# used by the calculatePrice -> purchase flow (introduced in PR #680). The built-in
# Reservation Purchaser role lacks reservationOrders/purchase/action, which causes 403 on
# production reservation purchases.
#
# The role definition is factored into a shared module so the customer-side (here) and
# host-side (terraform/modules/compute/azure/container-apps) definitions stay in lockstep.
module "cudly_reservation_role" {
  source      = "../../../../terraform/modules/iam/azure/cudly-reservation-role"
  scope       = data.azurerm_subscription.current.id
  name_suffix = local.subscription_id
}

# Assign the custom role at subscription scope.
# depends_on ensures the role definition is fully propagated before the assignment
# is created (Azure RBAC propagation can take up to 10 minutes).
resource "azurerm_role_assignment" "cudly_reservations" {
  scope              = "/subscriptions/${local.subscription_id}"
  role_definition_id = module.cudly_reservation_role.role_definition_resource_id
  principal_id       = azuread_service_principal.cudly.object_id

  depends_on = [module.cudly_reservation_role]
}
