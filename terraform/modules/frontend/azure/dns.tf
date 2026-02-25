# Azure DNS Zone for subdomain (optional)
# This zone is delegated from the parent zone
# Only created if subdomain_zone_name is set

resource "azurerm_dns_zone" "subdomain" {
  count = var.subdomain_zone_name != "" ? 1 : 0

  name                = var.subdomain_zone_name
  resource_group_name = var.resource_group_name

  tags = merge(var.tags, {
    Name        = var.subdomain_zone_name
    Environment = var.environment
  })
}

# Azure CDN Custom Domain (for subdomain zone pattern)
# Note: This is only used when subdomain_zone_name is set
# Otherwise, use the custom_domain variable with the resource in main.tf
resource "azurerm_cdn_endpoint_custom_domain" "frontend_subdomain" {
  count = !var.use_front_door && var.subdomain_zone_name != "" && length(var.domain_names) > 0 ? 1 : 0

  name            = replace(var.domain_names[0], ".", "-")
  cdn_endpoint_id = azurerm_cdn_endpoint.frontend[0].id
  host_name       = var.domain_names[0]

  cdn_managed_https {
    certificate_type = "Dedicated"
    protocol_type    = "ServerNameIndication"
    tls_version      = "TLS12"
  }

  depends_on = [azurerm_dns_cname_record.frontend]
}

# CNAME record for frontend (points to CDN endpoint)
resource "azurerm_dns_cname_record" "frontend" {
  count = var.subdomain_zone_name != "" && length(var.domain_names) > 0 ? 1 : 0

  name                = split(".", var.domain_names[0])[0] # Extract subdomain part
  zone_name           = azurerm_dns_zone.subdomain[0].name
  resource_group_name = var.resource_group_name
  ttl                 = 300
  record = var.use_front_door ? (
    azurerm_cdn_frontdoor_endpoint.frontend[0].host_name
  ) : azurerm_cdn_endpoint.frontend[0].fqdn

  tags = merge(var.tags, {
    Name        = var.domain_names[0]
    Environment = var.environment
  })
}

# CDN validation record (for Azure-managed certificate)
# Azure automatically creates validation records when using cdn_managed_https
# No manual DNS validation records needed
