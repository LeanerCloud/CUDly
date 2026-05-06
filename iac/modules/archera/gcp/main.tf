# ==============================================
# Archera Integration — GCP (Shared Module)
# ==============================================
#
# Single source of truth for the Archera GCP IAM resources.
# The permission lists are loaded from scope.gcp.yaml in the parent directory —
# edit that file to update permissions across all callers simultaneously.

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

locals {
  # Load the canonical permission list from the YAML source of truth.
  scope = yamldecode(file("${path.module}/../scope.gcp.yaml"))

  archera_custom_role_id = "cudlyArcheraIntegration"
  read_permissions       = local.scope.read_permissions
  purchase_permissions   = local.scope.purchase_permissions
}

# Custom IAM role for Archera — permission list from scope.gcp.yaml.
# Do NOT grant roles/billing.admin or roles/editor — they include write
# permissions across all project resources.
resource "google_project_iam_custom_role" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  project     = var.project_id
  role_id     = local.archera_custom_role_id
  title       = "CUDly Archera Integration"
  description = "Archera integration role — read cost data, optionally purchase CUDs (confirm scope before enabling)"
  stage       = "BETA"

  permissions = concat(
    local.read_permissions,
    var.enable_archera_purchase_actions ? local.purchase_permissions : []
  )
}

# Bind the custom role to Archera's GCP service account.
resource "google_project_iam_member" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  project = var.project_id
  role    = google_project_iam_custom_role.archera_integration[0].name
  member  = "serviceAccount:${var.archera_gcp_service_account}"
}
