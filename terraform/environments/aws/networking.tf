# ==============================================
# Networking
# ==============================================

module "networking" {
  source = "../../modules/networking/aws"

  stack_name = local.stack_name
  region     = var.region

  vpc_cidr                  = var.vpc_cidr
  az_count                  = var.az_count
  create_alb_security_group = var.compute_platform == "fargate"
  enable_flow_logs          = var.enable_flow_logs
  flow_logs_retention_days  = var.flow_logs_retention_days
  enable_nat_gateway        = true # Required for ECR access from private subnets

  tags = local.common_tags
}
