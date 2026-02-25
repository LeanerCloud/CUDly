# ==============================================
# Frontend (CloudFront + S3)
# ==============================================

resource "random_password" "cloudfront_secret" {
  count   = var.enable_frontend ? 1 : 0
  length  = 32
  special = true
}

resource "random_id" "frontend_bucket" {
  count       = var.enable_frontend ? 1 : 0
  byte_length = 4
}

module "frontend" {
  source = "../../modules/frontend/aws"
  count  = var.enable_frontend ? 1 : 0

  project_name = var.project_name
  environment  = var.environment

  # Frontend build configuration
  enable_frontend_build = var.enable_frontend_build

  # S3 bucket for frontend files (random suffix avoids naming conflicts on destroy/recreate)
  bucket_name = "${local.stack_name}-frontend-${random_id.frontend_bucket[0].hex}"

  # API endpoint - Lambda Function URL or Fargate ALB CNAME (for TLS cert matching)
  api_domain_name = var.compute_platform == "lambda" ? (
    replace(replace(module.compute_lambda[0].function_url, "https://", ""), "/", "")
    ) : (
    "api-${var.environment}.${var.subdomain_zone_name}"
  )

  # CloudFront secret for origin verification
  cloudfront_secret = random_password.cloudfront_secret[0].result

  # Optional: Custom domain
  domain_names = var.frontend_domain_names
  acm_certificate_arn = (
    length(aws_acm_certificate.frontend) > 0
    ? aws_acm_certificate_validation.frontend[0].certificate_arn
    : var.frontend_acm_certificate_arn
  )
  # Don't let module create DNS record if we're managing it in dns_records.tf
  route53_zone_id = var.subdomain_zone_name != "" ? null : var.frontend_route53_zone_id

  # CloudFront configuration
  price_class = var.frontend_price_class

  # Optional: WAF
  waf_acl_arn = var.frontend_waf_acl_arn

  tags = local.common_tags

  depends_on = [module.compute_lambda, module.compute_fargate]
}
