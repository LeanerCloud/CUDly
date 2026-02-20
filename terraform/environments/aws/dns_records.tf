# DNS record for frontend (created here to avoid count issues with computed zone_id)
resource "aws_route53_record" "frontend_alias" {
  count = var.subdomain_zone_name != "" && length(var.frontend_domain_names) > 0 ? 1 : 0

  zone_id = local.subdomain_zone_id
  name    = var.frontend_domain_names[0]
  type    = "A"

  alias {
    name                   = module.frontend.cloudfront_domain_name
    zone_id                = module.frontend.cloudfront_hosted_zone_id
    evaluate_target_health = false
  }

  depends_on = [
    module.frontend
  ]
}
