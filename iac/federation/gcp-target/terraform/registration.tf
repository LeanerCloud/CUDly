locals {
  do_register      = var.cudly_api_url != "" && var.contact_email != ""
  reg_account_name = var.account_name != "" ? var.account_name : "GCP ${var.project}"

  reg_payload = local.do_register ? jsonencode({
    provider         = "gcp"
    external_id      = var.project
    account_name     = local.reg_account_name
    contact_email    = var.contact_email
    description      = "Registered via Terraform federation IaC (gcp-target/wif)"
    gcp_project_id   = var.project
    gcp_client_email = var.service_account_email
    gcp_auth_mode    = "workload_identity_federation"
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

  # Defer to apply phase — ensure GCP resources are created before registering.
  depends_on = [google_service_account_iam_member.cudly_wif]

  lifecycle {
    postcondition {
      condition     = contains([200, 201, 409], self.status_code)
      error_message = "CUDly registration failed with HTTP ${self.status_code}: ${self.response_body}"
    }
  }
}
