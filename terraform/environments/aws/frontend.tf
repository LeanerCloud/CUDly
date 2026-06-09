# ==============================================
# Frontend CDN (CloudFront)
# Only created when enable_cdn = true (custom domain + edge caching)
# When disabled, the compute endpoint serves the frontend directly
# ==============================================

module "frontend" {
  source = "../../modules/frontend/aws"
  count  = var.enable_cdn ? 1 : 0

  project_name = var.project_name
  environment  = var.environment

  # Compute origin - Lambda Function URL or Fargate ALB CNAME (for TLS cert matching)
  origin_domain_name = var.compute_platform == "lambda" ? (
    replace(replace(module.compute_lambda[0].function_url, "https://", ""), "/", "")
    ) : (
    var.subdomain_zone_name != "" ? "api-${var.environment}.${var.subdomain_zone_name}" : module.compute_fargate[0].alb_dns_name
  )

  origin_protocol = var.compute_platform == "lambda" ? "https-only" : "http-only"

  # Optional: Custom domain
  domain_names = var.frontend_domain_names
  acm_certificate_arn = (
    length(aws_acm_certificate.frontend) > 0
    ? aws_acm_certificate_validation.frontend[0].certificate_arn
    : var.frontend_acm_certificate_arn
  )
  # Don't let module create DNS record if we're managing it in dns_records.tf
  route53_zone_id = var.subdomain_zone_name != "" ? null : var.frontend_route53_zone_id

  # Enable OAC whenever Lambda is fronted by CloudFront (enable_cdn = true,
  # which also flips the Function URL auth_type to AWS_IAM via the local in
  # compute.tf, so the OAC's SigV4 signing is the auth the Function URL
  # expects).
  enable_oac = var.compute_platform == "lambda" && var.enable_cdn

  # CloudFront configuration
  price_class = var.frontend_price_class

  # Optional: WAF
  waf_acl_arn = var.frontend_waf_acl_arn

  tags = local.common_tags

  depends_on = [module.compute_lambda, module.compute_fargate]
}
