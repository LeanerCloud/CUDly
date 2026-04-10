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
