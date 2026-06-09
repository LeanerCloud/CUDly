output "role_arn" {
  description = "ARN of the CUDly IAM role — use as aws_role_arn in CUDly account registration"
  value       = aws_iam_role.cudly.arn
}

output "oidc_provider_arn" {
  description = "ARN of the created OIDC identity provider"
  value       = aws_iam_openid_connect_provider.cudly.arn
}

output "cudly_account_registration" {
  description = "Values to use when registering this account in CUDly"
  value       = <<-EOT
    provider               : aws
    aws_auth_mode          : workload_identity_federation
    external_id            : ${data.aws_caller_identity.current.account_id}
    aws_role_arn           : ${aws_iam_role.cudly.arn}
    aws_web_identity_token_file : /var/run/secrets/token  (or set AWS_WEB_IDENTITY_TOKEN_FILE)
  EOT
}

data "aws_caller_identity" "current" {}

output "registration_response" {
  description = "CUDly registration API response (contains reference_token for status checks)"
  value       = local.do_register ? data.http.cudly_registration[0].response_body : "Skipped (cudly_api_url or contact_email not set)"
  sensitive   = false
}
