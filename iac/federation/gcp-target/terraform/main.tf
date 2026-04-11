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

data "google_project" "current" {
  project_id = var.project
}

resource "google_project_service" "iam" {
  project            = var.project
  service            = "iam.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "iamcredentials" {
  project            = var.project
  service            = "iamcredentials.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "sts" {
  project            = var.project
  service            = "sts.googleapis.com"
  disable_on_destroy = false
}

resource "google_iam_workload_identity_pool" "cudly" {
  project                   = var.project
  workload_identity_pool_id = var.pool_id
  display_name              = "CUDly WIF pool"

  depends_on = [
    google_project_service.iam,
    google_project_service.iamcredentials,
    google_project_service.sts,
  ]
}

resource "google_iam_workload_identity_pool_provider" "cudly" {
  project                            = var.project
  workload_identity_pool_id          = google_iam_workload_identity_pool.cudly.workload_identity_pool_id
  workload_identity_pool_provider_id = var.provider_id

  attribute_mapping = var.provider_type == "aws" ? {
    "google.subject"     = "assertion.arn"
    "attribute.aws_role" = "assertion.arn"
    "attribute.account"  = "assertion.account"
  } : var.oidc_attribute_mapping

  attribute_condition = var.provider_type == "aws" ? (
    var.aws_role_name != "" ? "attribute.aws_role.contains('assumed-role/${var.aws_role_name}')" : null
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
  service_account_id = "projects/${var.project}/serviceAccounts/${var.service_account_email}"
  role               = "roles/iam.workloadIdentityUser"
  member = var.provider_type == "aws" && var.aws_role_name != "" ? (
    "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/attribute.aws_role/arn:aws:sts::${var.aws_account_id}:assumed-role/${var.aws_role_name}"
    ) : var.provider_type == "oidc" && var.oidc_subject != "" ? (
    "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/subject/${var.oidc_subject}"
    ) : (
    "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/*"
  )
}
