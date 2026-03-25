output "service_account_email" {
  description = "Email of the CUDly Terraform deploy service account — use as GCP_SERVICE_ACCOUNT in GitHub Actions"
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

output "workload_identity_provider" {
  description = "Full resource name of the Workload Identity Pool Provider — use as GCP_WORKLOAD_IDENTITY_PROVIDER in GitHub Actions"
  value       = var.github_repo != "" ? google_iam_workload_identity_pool_provider.github[0].name : null
}
