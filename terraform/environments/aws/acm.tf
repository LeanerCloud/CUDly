# ACM Certificate for Custom Domain
# Certificate must be in us-east-1 for CloudFront

resource "aws_acm_certificate" "frontend" {
  count = length(var.frontend_domain_names) > 0 ? 1 : 0

  domain_name       = var.frontend_domain_names[0]
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }

  tags = merge(local.common_tags, {
    Name = "${local.stack_name}-frontend-cert"
  })
}

# Create DNS validation records in the subdomain zone (if zone is created)
resource "aws_route53_record" "acm_validation" {
  for_each = length(aws_acm_certificate.frontend) > 0 && var.subdomain_zone_name != "" ? {
    for dvo in aws_acm_certificate.frontend[0].domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  } : {}

  allow_overwrite = true
  name            = each.value.name
  records         = [each.value.record]
  ttl             = 60
  type            = each.value.type
  zone_id         = local.subdomain_zone_id
}

# Wait for certificate validation to complete
resource "aws_acm_certificate_validation" "frontend" {
  count = length(var.frontend_domain_names) > 0 ? 1 : 0

  certificate_arn         = aws_acm_certificate.frontend[0].arn
  validation_record_fqdns = [for record in aws_route53_record.acm_validation : record.fqdn]

  timeouts {
    create = "45m"
  }
}
