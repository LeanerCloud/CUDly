output "service_account_email" {
  description = "Email of the CUDly Terraform deploy service account"
  value       = google_service_account.cudly_deploy.email
}

output "service_account_id" {
  description = "ID of the CUDly Terraform deploy service account"
  value       = google_service_account.cudly_deploy.id
}

output "roles" {
  description = "IAM roles granted to the deploy principal"
  value       = tolist(local.deploy_roles)
}
