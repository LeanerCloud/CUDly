locals {
  do_register      = var.cudly_api_url != "" && var.contact_email != ""
  reg_account_name = var.account_name != "" ? var.account_name : "GCP ${var.project_id}"

  reg_payload = local.do_register ? jsonencode({
    provider         = "gcp"
    external_id      = var.project_id
    account_name     = local.reg_account_name
    contact_email    = var.contact_email
    description      = "Registered via Terraform federation IaC (gcp-sa-impersonation)"
    gcp_project_id   = var.project_id
    gcp_client_email = var.service_account_email
    gcp_auth_mode    = "application_default"
  }) : ""
}

data "http" "cudly_registration" {
  count  = local.do_register ? 1 : 0
  url    = "${var.cudly_api_url}/api/register"
  method = "POST"

  request_headers = {
    Content-Type = "application/json"
  }

  request_body = local.reg_payload

  lifecycle {
    postcondition {
      condition     = contains([200, 201, 409], self.status_code)
      error_message = "CUDly registration failed with HTTP ${self.status_code}: ${self.response_body}"
    }
  }
}
