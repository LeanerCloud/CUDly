output "role_arn" {
  description = "ARN of the CUDly IAM role. Paste into CUDly as aws_role_arn with aws_auth_mode=role_arn."
  value       = aws_iam_role.cudly.arn
}

output "external_id" {
  description = "External ID for confused deputy protection. Use as aws_external_id in CUDly."
  value       = local.effective_external_id
  sensitive   = true
}

output "policy_arn" {
  description = "ARN of the standalone managed policy attached to the CUDly role."
  value       = aws_iam_policy.cudly.arn
}

output "cudly_account_registration" {
  description = "Values to use when registering this account in CUDly"
  value       = <<-EOT
    provider        : aws
    aws_auth_mode   : role_arn
    aws_role_arn    : ${aws_iam_role.cudly.arn}
    aws_external_id : (see 'external_id' output — sensitive)
  EOT
}

output "registration_response" {
  description = "CUDly registration API response (contains reference_token for status checks)"
  value       = local.do_register ? data.http.cudly_registration[0].response_body : "Skipped (cudly_api_url or contact_email not set)"
  sensitive   = false
}
