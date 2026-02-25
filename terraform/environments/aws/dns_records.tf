# DNS record for frontend (created here to avoid count issues with computed zone_id)
resource "aws_route53_record" "frontend_alias" {
  count = var.enable_frontend && var.subdomain_zone_name != "" && length(var.frontend_domain_names) > 0 ? 1 : 0

  zone_id = local.subdomain_zone_id
  name    = var.frontend_domain_names[0]
  type    = "A"

  alias {
    name                   = module.frontend[0].cloudfront_domain_name
    zone_id                = module.frontend[0].cloudfront_hosted_zone_id
    evaluate_target_health = false
  }

  depends_on = [
    module.frontend
  ]
}

# DNS record for Fargate ALB (enables HTTPS with wildcard cert)
resource "aws_route53_record" "fargate_alb_alias" {
  count = var.compute_platform == "fargate" && var.subdomain_zone_name != "" ? 1 : 0

  zone_id = local.subdomain_zone_id
  name    = "api-${var.environment}.${var.subdomain_zone_name}"
  type    = "A"

  alias {
    name                   = module.compute_fargate[0].alb_dns_name
    zone_id                = module.compute_fargate[0].alb_zone_id
    evaluate_target_health = true
  }

  depends_on = [module.compute_fargate]
}
