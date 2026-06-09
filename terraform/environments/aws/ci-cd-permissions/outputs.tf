output "role_arn" {
  description = "ARN of the CUDly Terraform deploy role — use as AWS_ROLE_TO_ASSUME in GitHub Actions"
  value       = aws_iam_role.cudly_deploy.arn
}

output "role_name" {
  description = "Name of the CUDly Terraform deploy role"
  value       = aws_iam_role.cudly_deploy.name
}

output "policy_arns" {
  description = "ARNs of all attached managed policies"
  value = {
    networking = aws_iam_policy.networking.arn
    compute    = aws_iam_policy.compute.arn
    data       = aws_iam_policy.data.arn
  }
}

output "oidc_provider_arn" {
  description = "ARN of the GitHub Actions OIDC provider (null if github_repo is empty)"
  value       = var.github_repo != "" ? aws_iam_openid_connect_provider.github[0].arn : null
}
