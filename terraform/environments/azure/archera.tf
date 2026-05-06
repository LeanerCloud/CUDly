# ==============================================
# Archera Integration — Azure
# ==============================================
#
# Thin caller — all RBAC resources and permission lists live in the shared
# module at iac/modules/archera/azure/.  Edit scope.azure.yaml in that directory
# to update the permission scope across ALL callers simultaneously.
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

module "archera" {
  source = "../../../iac/modules/archera/azure"

  enable_archera                  = var.enable_archera
  subscription_id                 = var.subscription_id
  archera_azure_sp_object_id      = var.archera_azure_sp_object_id
  enable_archera_purchase_actions = var.enable_archera_purchase_actions
}
