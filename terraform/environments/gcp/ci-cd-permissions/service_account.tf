locals {
  deploy_roles = toset([
    "roles/run.admin",
    "roles/cloudsql.admin",
    "roles/secretmanager.admin",
    "roles/artifactregistry.admin",
    "roles/cloudscheduler.admin",
    "roles/compute.networkAdmin",
    "roles/compute.securityAdmin",
    "roles/iam.serviceAccountAdmin",
    "roles/iam.serviceAccountUser",
    "roles/storage.admin",
    "roles/dns.admin",
    "roles/logging.admin",
    "roles/monitoring.admin",
    "roles/vpcaccess.admin",
    "roles/cloudfunctions.developer",
    "roles/servicenetworking.networksAdmin",
    "roles/pubsub.admin",
    "roles/container.admin",
    "roles/resourcemanager.projectIamAdmin",
    "roles/serviceusage.serviceUsageAdmin",
  ])
}

resource "google_service_account" "cudly_deploy" {
  account_id   = "cudly-terraform-deploy"
  display_name = "CUDly Terraform Deploy"
  project      = var.project_id
}

resource "google_project_iam_member" "service_account" {
  for_each = local.deploy_roles

  project = var.project_id
  role    = each.key
  member  = "serviceAccount:${google_service_account.cudly_deploy.email}"
}
