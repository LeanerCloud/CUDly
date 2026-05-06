# ==============================================
# Archera Integration — AWS (Federation Bundle, aws-cross-account)
# ==============================================
#
# Thin caller — all IAM resources and permission lists live in the shared
# module at iac/modules/archera/aws/.  Edit scope.aws.yaml in that directory
# to update the permission scope across ALL callers simultaneously.
#
# PROVISIONAL SCOPE — must be confirmed against Archera integration docs
# before flipping enable_archera = true in any tfvars.
# TODO(@cristim): confirm Archera scope list against integration docs
# before enabling.  Reference: https://archera.ai/docs (integration guide).

module "archera" {
  source = "../../../modules/archera/aws"

  enable_archera                  = var.enable_archera
  archera_aws_account_id          = var.archera_aws_account_id
  archera_external_id             = var.archera_external_id
  enable_archera_purchase_actions = var.enable_archera_purchase_actions
  managed_by_tag                  = "cudly-federation-bundle"
}
