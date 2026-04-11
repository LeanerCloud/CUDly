data "aws_caller_identity" "current" {}

locals {
  do_register      = var.cudly_api_url != "" && var.contact_email != ""
  reg_account_name = var.account_name != "" ? var.account_name : "AWS ${data.aws_caller_identity.current.account_id}"

  reg_payload = local.do_register ? jsonencode({
    provider      = "aws"
    external_id   = data.aws_caller_identity.current.account_id
    account_name  = local.reg_account_name
    contact_email = var.contact_email
    description   = "Registered via Terraform federation IaC (aws-cross-account)"
    aws_role_arn  = aws_iam_role.cudly.arn
    aws_auth_mode = "role_arn"
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
