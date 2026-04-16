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

# Reservation Purchaser is the built-in Azure role for purchasing and managing reservations.
resource "azurerm_role_assignment" "cudly_reservations" {
  scope                = "/subscriptions/${local.subscription_id}"
  role_definition_name = "Reservation Purchaser"
  principal_id         = azuread_service_principal.cudly.object_id
}
