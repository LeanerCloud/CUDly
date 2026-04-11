output "role_arn" {
  description = "ARN of the CUDly IAM role. Paste into CUDly as aws_role_arn with aws_auth_mode=role_arn."
  value       = aws_iam_role.cudly.arn
}

output "cudly_account_registration" {
  description = "Values to use when registering this account in CUDly"
  value       = <<-EOT
    provider      : aws
    aws_auth_mode : role_arn
    aws_role_arn  : ${aws_iam_role.cudly.arn}
  EOT
}

output "registration_response" {
  description = "CUDly registration API response (contains reference_token for status checks)"
  value       = local.do_register ? data.http.cudly_registration[0].response_body : "Skipped (cudly_api_url or contact_email not set)"
  sensitive   = false
}
