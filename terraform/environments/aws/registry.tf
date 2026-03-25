# ==============================================
# Container Registry (ECR)
# ==============================================

module "registry" {
  source          = "../../modules/registry/aws"
  repository_name = local.stack_name
  tags            = local.common_tags
}
