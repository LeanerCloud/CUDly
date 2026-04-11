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

resource "google_iam_workload_identity_pool" "cudly" {
  project                   = var.project
  workload_identity_pool_id = var.pool_id
  display_name              = "CUDly WIF pool"
}

resource "google_iam_workload_identity_pool_provider" "cudly" {
  project                            = var.project
  workload_identity_pool_id          = google_iam_workload_identity_pool.cudly.workload_identity_pool_id
  workload_identity_pool_provider_id = var.provider_id

  attribute_mapping = var.provider_type == "oidc" ? var.oidc_attribute_mapping : null

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
}

# Use _member (not _binding) to add one member without replacing existing bindings.
resource "google_service_account_iam_member" "cudly_wif" {
  service_account_id = "projects/${var.project}/serviceAccounts/${var.service_account_email}"
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/*"
}
