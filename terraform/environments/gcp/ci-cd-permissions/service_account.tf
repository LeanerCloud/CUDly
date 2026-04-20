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
    # KMS admin is required for the CUDly OIDC issuer signing key
    # (key ring + asymmetric crypto key + IAM bindings) that the
    # cloud-run module creates in terraform/modules/compute/gcp/
    # cloud-run/signing-key.tf. Without this, Terraform Apply 403s
    # on cloudkms.keyRings.create.
    "roles/cloudkms.admin",
    # iam.roleAdmin is required to create/read/update/delete the
    # custom project role cudlyCommitmentWriter (see
    # terraform/modules/compute/gcp/cloud-run/main.tf). Without it,
    # Apply 403s on iam.roles.get when
    # use_custom_compute_commitment_role = true. Kept even when the
    # flag is false so operators can flip it without re-bootstrapping.
    "roles/iam.roleAdmin",
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
