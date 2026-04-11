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
    tls = {
      source  = "hashicorp/tls"
      version = ">= 4.0"
    }
  }
}

# Auto-generate RSA key pair and self-signed certificate when certificate_pem
# is not provided. This makes registration seamless — no manual openssl needed.
resource "tls_private_key" "cudly" {
  count     = var.certificate_pem == "" ? 1 : 0
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_self_signed_cert" "cudly" {
  count           = var.certificate_pem == "" ? 1 : 0
  private_key_pem = tls_private_key.cudly[0].private_key_pem

  subject {
    common_name = "CUDly-WIF"
  }

  validity_period_hours = 17520 # 2 years
  allowed_uses          = ["digital_signature"]
}

locals {
  # Use auto-generated cert when certificate_pem is empty, otherwise use provided.
  certificate_pem = var.certificate_pem != "" ? var.certificate_pem : tls_self_signed_cert.cudly[0].cert_pem
  private_key_pem = var.certificate_pem == "" ? tls_private_key.cudly[0].private_key_pem : ""
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
  value          = local.certificate_pem
}

# Reservations Administrator is the built-in Azure role for purchasing and managing reservations.
resource "azurerm_role_assignment" "cudly_reservations" {
  scope                = "/subscriptions/${var.subscription_id}"
  role_definition_name = "Reservation Purchaser"
  principal_id         = azuread_service_principal.cudly.object_id
}
