terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
    http = {
      source  = "hashicorp/http"
      version = ">= 3.4"
    }
  }
}

provider "google" {
  project = var.project
}

data "google_project" "current" {}

locals {
  project                = var.project != "" ? var.project : data.google_project.current.project_id
  create_service_account = var.service_account_email == ""
  service_account_email  = local.create_service_account ? google_service_account.cudly[0].email : var.service_account_email
}

resource "google_project_service" "iam" {
  project            = local.project
  service            = "iam.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "iamcredentials" {
  project            = local.project
  service            = "iamcredentials.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "sts" {
  project            = local.project
  service            = "sts.googleapis.com"
  disable_on_destroy = false
}

# Optional: create a dedicated least-privilege SA when the operator didn't
# pre-provision one (var.service_account_email left empty).
resource "google_service_account" "cudly" {
  count        = local.create_service_account ? 1 : 0
  project      = local.project
  account_id   = var.service_account_id
  display_name = "CUDly WIF target SA"
  description  = "Impersonated by CUDly via Workload Identity Federation to manage GCP commitments."

  depends_on = [google_project_service.iam]
}

resource "google_project_iam_custom_role" "cudly" {
  count   = local.create_service_account ? 1 : 0
  project = local.project
  role_id = var.custom_role_id
  title   = "CUDly Commitment Writer"
  # Description matches terraform/modules/compute/gcp/cloud-run exactly
  # so applies from the two modules are idempotent.
  description = "Minimum permissions for CUDly to purchase and update committed use discounts."
  permissions = var.custom_role_permissions
  stage       = "GA"
}

resource "google_project_iam_member" "cudly_custom" {
  count   = local.create_service_account ? 1 : 0
  project = local.project
  role    = google_project_iam_custom_role.cudly[0].name
  member  = "serviceAccount:${google_service_account.cudly[0].email}"
}

resource "google_project_iam_member" "cudly" {
  for_each = local.create_service_account ? toset(var.service_account_project_roles) : toset([])
  project  = local.project
  role     = each.value
  member   = "serviceAccount:${google_service_account.cudly[0].email}"
}

resource "google_iam_workload_identity_pool" "cudly" {
  project                   = local.project
  workload_identity_pool_id = var.pool_id
  display_name              = "CUDly WIF pool"

  depends_on = [
    google_project_service.iam,
    google_project_service.iamcredentials,
    google_project_service.sts,
  ]
}

resource "google_iam_workload_identity_pool_provider" "cudly" {
  project                            = local.project
  workload_identity_pool_id          = google_iam_workload_identity_pool.cudly.workload_identity_pool_id
  workload_identity_pool_provider_id = var.provider_id

  attribute_mapping = var.provider_type == "aws" ? {
    "google.subject"     = "assertion.arn"
    "attribute.aws_role" = "assertion.arn"
    "attribute.account"  = "assertion.account"
  } : var.oidc_attribute_mapping

  attribute_condition = var.provider_type == "aws" ? (
    var.aws_role_name != "" ? "attribute.aws_role.contains('assumed-role/${var.aws_role_name}/')" : null
    ) : (
    var.oidc_subject != "" ? "google.subject == '${var.oidc_subject}'" : null
  )

  dynamic "aws" {
    for_each = var.provider_type == "aws" ? [1] : []
    content {
      account_id = var.aws_account_id
    }
  }

  dynamic "oidc" {
    for_each = var.provider_type == "oidc" ? [1] : []
    content {
      issuer_uri = var.oidc_issuer_uri
    }
  }

  lifecycle {
    precondition {
      condition     = var.provider_type != "aws" || var.aws_account_id != ""
      error_message = "aws_account_id is required when provider_type is 'aws'."
    }
    precondition {
      condition     = var.provider_type != "oidc" || var.oidc_issuer_uri != ""
      error_message = "oidc_issuer_uri is required when provider_type is 'oidc'."
    }
  }
}

# Use _member (not _binding) to add one member without replacing existing bindings.
resource "google_service_account_iam_member" "cudly_wif" {
  service_account_id = "projects/${local.project}/serviceAccounts/${local.service_account_email}"
  role               = "roles/iam.workloadIdentityUser"
  # For AWS: always use wildcard principalSet — role restriction is enforced by
  # attribute_condition on the provider (session ARNs include variable session
  # names, so exact-match principalSet cannot work).
  # For OIDC: scope to specific subject when provided.
  member = var.provider_type == "oidc" && var.oidc_subject != "" ? (
    "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/subject/${var.oidc_subject}"
    ) : (
    "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/*"
  )
}
