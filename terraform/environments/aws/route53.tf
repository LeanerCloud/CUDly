# Route53 Hosted Zone for subdomain
# Zone creation is optional - set create_subdomain_zone=true to create
# Otherwise uses data source to reference existing zone (managed in centralized DNS infrastructure)

resource "aws_route53_zone" "subdomain" {
  count = var.subdomain_zone_name != "" && var.create_subdomain_zone ? 1 : 0

  name = var.subdomain_zone_name

  tags = merge(local.common_tags, {
    Name = var.subdomain_zone_name
  })
}

# Reference existing zone if not creating it
data "aws_route53_zone" "subdomain" {
  count = var.subdomain_zone_name != "" && !var.create_subdomain_zone ? 1 : 0

  name         = var.subdomain_zone_name
  private_zone = false
}

# Local to get the zone ID from either resource or data source
locals {
  subdomain_zone_id = var.subdomain_zone_name != "" ? (
    var.create_subdomain_zone ? aws_route53_zone.subdomain[0].zone_id : data.aws_route53_zone.subdomain[0].zone_id
  ) : ""

  subdomain_zone_nameservers = var.subdomain_zone_name != "" ? (
    var.create_subdomain_zone ? aws_route53_zone.subdomain[0].name_servers : data.aws_route53_zone.subdomain[0].name_servers
  ) : []
}

# Output the nameservers for delegation in parent zone
output "subdomain_zone_nameservers" {
  description = "Nameservers for subdomain zone (add these as NS records in parent zone)"
  value       = local.subdomain_zone_nameservers
}

output "subdomain_zone_id" {
  description = "Route53 zone ID for subdomain"
  value       = local.subdomain_zone_id
}
