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
  subscription_id = var.subscription_id
  features {}
}

provider "azuread" {
  tenant_id = var.tenant_id
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

# Upload the public certificate — CUDly signs JWTs with the private key and Azure
# verifies them against this certificate (workload identity federation via client assertion).
resource "azuread_application_certificate" "cudly" {
  application_id = azuread_application.cudly.id
  type           = "AsymmetricX509Cert"
  value          = var.certificate_pem
}

# Reservations Administrator is the built-in Azure role for purchasing and managing reservations.
resource "azurerm_role_assignment" "cudly_reservations" {
  scope                = "/subscriptions/${var.subscription_id}"
  role_definition_name = "Reservation Purchaser"
  principal_id         = azuread_service_principal.cudly.object_id
}
