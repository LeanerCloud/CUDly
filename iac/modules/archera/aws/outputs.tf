output "role_arn" {
  description = "ARN of the Archera cross-account IAM role (empty string when enable_archera = false)."
  value       = length(aws_iam_role.archera_integration) > 0 ? aws_iam_role.archera_integration[0].arn : ""
}

output "role_name" {
  description = "Name of the Archera cross-account IAM role (empty string when enable_archera = false)."
  value       = length(aws_iam_role.archera_integration) > 0 ? aws_iam_role.archera_integration[0].name : ""
}

output "read_policy_arn" {
  description = "ARN of the Archera read-only IAM policy (empty string when enable_archera = false)."
  value       = length(aws_iam_policy.archera_read) > 0 ? aws_iam_policy.archera_read[0].arn : ""
}

output "purchase_policy_arn" {
  description = "ARN of the Archera purchase IAM policy (empty string when enable_archera_purchase_actions = false)."
  value       = length(aws_iam_policy.archera_purchase) > 0 ? aws_iam_policy.archera_purchase[0].arn : ""
}
