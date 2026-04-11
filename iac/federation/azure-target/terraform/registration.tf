locals {
  do_register      = var.cudly_api_url != "" && var.contact_email != ""
  reg_account_name = var.account_name != "" ? var.account_name : "Azure ${var.subscription_id}"

  # Concatenate private key + certificate PEM for CUDly's azure_wif_private_key credential.
  azure_credential_blob = local.private_key_pem != "" ? "${local.private_key_pem}\n${local.certificate_pem}" : ""

  reg_payload = local.do_register ? jsonencode(merge({
    provider              = "azure"
    external_id           = var.subscription_id
    account_name          = local.reg_account_name
    contact_email         = var.contact_email
    description           = "Registered via Terraform federation IaC (azure-target/wif)"
    azure_subscription_id = var.subscription_id
    azure_tenant_id       = var.tenant_id
    azure_client_id       = azuread_application.cudly.client_id
    azure_auth_mode       = "workload_identity_federation"
    }, local.azure_credential_blob != "" ? {
    credential_type    = "azure_wif_private_key"
    credential_payload = local.azure_credential_blob
  } : {})) : ""
}

data "http" "cudly_registration" {
  count  = local.do_register ? 1 : 0
  url    = "${var.cudly_api_url}/api/register"
  method = "POST"

  request_headers = {
    Content-Type = "application/json"
  }

  request_body = local.reg_payload

  # Defer to apply phase — ensure Azure resources are created before registering.
  depends_on = [azurerm_role_assignment.cudly_reservations]

  lifecycle {
    postcondition {
      condition     = contains([200, 201, 409], self.status_code)
      error_message = "CUDly registration failed with HTTP ${self.status_code}: ${self.response_body}"
    }
  }
}
