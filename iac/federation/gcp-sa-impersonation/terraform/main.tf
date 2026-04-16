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

# Grant the source SA permission to impersonate the target SA.
# Use _member (not _binding) to avoid replacing other existing bindings.
resource "google_service_account_iam_member" "cudly_impersonate" {
  service_account_id = "projects/${var.project_id}/serviceAccounts/${var.service_account_email}"
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = "serviceAccount:${var.source_service_account}"
}

# Grant the target SA permissions needed to manage CUDs/commitments.
# commitmentAdmin: create/manage committed-use discounts.
# billing.viewer:  read billing data (costs, invoices). Read-only —
#                  billing.user is unnecessarily broad for CUDly's use case.
resource "google_project_iam_member" "cudly_commitment_admin" {
  project = var.project_id
  role    = "roles/commerceorgpolicy.commitmentAdmin"
  member  = "serviceAccount:${var.service_account_email}"
}

resource "google_project_iam_member" "cudly_billing_viewer" {
  project = var.project_id
  role    = "roles/billing.viewer"
  member  = "serviceAccount:${var.service_account_email}"
}
