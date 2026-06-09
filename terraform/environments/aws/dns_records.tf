# DNS record for frontend via CDN (CloudFront)
resource "aws_route53_record" "frontend_alias" {
  count = var.enable_cdn && var.subdomain_zone_name != "" && length(var.frontend_domain_names) > 0 ? 1 : 0

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

# DNS record for frontend directly to Fargate ALB (when CDN is disabled)
# Points the frontend domain to the ALB so the container serves the frontend
resource "aws_route53_record" "frontend_compute_alias" {
  count = !var.enable_cdn && var.compute_platform == "fargate" && var.subdomain_zone_name != "" && length(var.frontend_domain_names) > 0 ? 1 : 0

  zone_id = local.subdomain_zone_id
  name    = var.frontend_domain_names[0]
  type    = "A"

  alias {
    name                   = module.compute_fargate[0].alb_dns_name
    zone_id                = module.compute_fargate[0].alb_zone_id
    evaluate_target_health = true
  }

  depends_on = [module.compute_fargate]
}

# DNS record for Fargate ALB API endpoint (enables HTTPS with wildcard cert)
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
