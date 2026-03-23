output "role_arn" {
  description = "ARN of the CUDly Terraform deploy role"
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
