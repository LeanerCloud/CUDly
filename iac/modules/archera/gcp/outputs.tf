output "custom_role_name" {
  description = "Full name of the Archera custom IAM role (empty string when enable_archera = false)."
  value       = length(google_project_iam_custom_role.archera_integration) > 0 ? google_project_iam_custom_role.archera_integration[0].name : ""
}

output "iam_member_id" {
  description = "Terraform resource ID of the Archera IAM member binding (empty string when enable_archera = false)."
  value       = length(google_project_iam_member.archera_integration) > 0 ? google_project_iam_member.archera_integration[0].id : ""
}
