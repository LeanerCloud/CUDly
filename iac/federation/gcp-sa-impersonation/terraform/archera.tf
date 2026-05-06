# ==============================================
# Archera Integration — GCP (Federation Bundle, gcp-sa-impersonation)
# ==============================================
#
# Thin caller — all IAM resources and permission lists live in the shared
# module at iac/modules/archera/gcp/.  Edit scope.gcp.yaml in that directory
# to update the permission scope across ALL callers simultaneously.
#
# PROVISIONAL SCOPE — must be confirmed against Archera integration docs
# before flipping enable_archera = true in any tfvars.
# TODO(@cristim): confirm Archera scope list against integration docs
# before enabling.  Reference: https://archera.ai/docs (integration guide).

module "archera" {
  source = "../../../modules/archera/gcp"

  enable_archera                  = var.enable_archera
  project_id                      = var.project_id
  archera_gcp_service_account     = var.archera_gcp_service_account
  enable_archera_purchase_actions = var.enable_archera_purchase_actions
}
