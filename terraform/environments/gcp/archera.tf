# ==============================================
# Archera Integration — GCP
# ==============================================
#
# When enable_archera = true, this block creates a custom IAM role and
# grants it to Archera's service account, providing least-privilege read
# access to cost and commitment data.  Purchase-execution permissions are
# separately gated behind enable_archera_purchase_actions so telemetry-only
# rollouts never accidentally include financial writes.
#
# PROVISIONAL SCOPE — must be confirmed against Archera integration docs
# before flipping enable_archera = true in any tfvars.
# TODO(@cristim): confirm Archera scope list against integration docs
# before enabling.  Reference: https://archera.ai/docs (integration guide).
#
# Placement rationale (bootstrap vs runtime split):
#   Archera is a RUNTIME integration — it reads cost telemetry and submits
#   purchases during normal operation.  This file lives in the main
#   environment (alongside compute.tf / database.tf), NOT in
#   ci-cd-permissions/ (which is applied once by a privileged human and
#   grants deploy-SA capabilities only).
#
# Prefer a custom role over predefined roles like roles/billing.admin or
# roles/recommender.commitmentsPlanAdmin — those are too broad.

locals {
  archera_custom_role_id = "cudlyArcheraIntegration"
}

# Custom IAM role for Archera — PROVISIONAL.
# Permissions are scoped to read-only billing/cost data and, optionally,
# CUD purchase execution (gated by enable_archera_purchase_actions).
# Do NOT grant roles/billing.admin or roles/editor — they include write
# permissions across all project resources.
#
# TODO(@cristim): narrow to the exact permission list from Archera's GCP
# onboarding docs before setting enable_archera = true.
resource "google_project_iam_custom_role" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  project     = var.project_id
  role_id     = local.archera_custom_role_id
  title       = "CUDly Archera Integration"
  description = "Provisional Archera integration role — read cost data, optionally purchase CUDs (confirm scope before enabling)"
  stage       = "BETA"

  permissions = concat(
    [
      # ── Read-only: Billing / Cost data ─────────────────────────────────
      # Archera needs to read historical usage and costs to size commitments.
      # TODO(@cristim): confirm whether Archera also needs
      # billing.accounts.getSpendingInformation — add if required.
      "billing.accounts.get",
      "billing.accounts.list",
      "billing.budgets.get",
      "billing.budgets.list",

      # ── Read-only: Recommender / Committed Use Discounts ───────────────
      # Archera reads CUD recommendations and existing commitments.
      # Note: .update is a write action and intentionally excluded here.
      "recommender.commitmentUtilizationInsights.get",
      "recommender.commitmentUtilizationInsights.list",
      "recommender.commitments.get",
      "recommender.commitments.list",

      # ── Read-only: Resource Manager (project metadata) ─────────────────
      "resourcemanager.projects.get",
    ],
    # ── Purchase-execution: Committed Use Discounts ─────────────────────
    # Gated behind enable_archera_purchase_actions so financial writes are
    # never included by accident at initial rollout.
    # TODO(@cristim): enable only after confirming approval workflow with
    # Archera (i.e. Archera requires customer approval before purchases).
    var.enable_archera_purchase_actions ? [
      "recommender.commitments.create",
    ] : []
  )
}

# Bind the custom role to Archera's GCP service account.
# archera_gcp_service_account must be the full service account email that
# Archera provides during onboarding, e.g.:
#   "archera-integration@archera-prod.iam.gserviceaccount.com"
resource "google_project_iam_member" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  project = var.project_id
  role    = google_project_iam_custom_role.archera_integration[0].name
  member  = "serviceAccount:${var.archera_gcp_service_account}"
}
