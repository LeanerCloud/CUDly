# ==============================================
# Container Registry (ECR)
# ==============================================

module "registry" {
  source          = "../../modules/registry/aws"
  repository_name = "cudly"
  tags            = local.common_tags
}
