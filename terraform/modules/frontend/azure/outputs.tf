# Azure Frontend Module Outputs

output "cdn_endpoint_hostname" {
  description = "Classic CDN endpoint hostname (format: <name>.azureedge.net)"
  value       = try("${azurerm_cdn_endpoint.frontend[0].name}.azureedge.net", "")
}

output "frontdoor_endpoint_hostname" {
  description = "Front Door endpoint hostname (if enabled)"
  value       = try(azurerm_cdn_frontdoor_endpoint.frontend[0].host_name, "")
}

output "frontend_url" {
  description = "Frontend URL (custom domain, Front Door, or CDN)"
  value = length(var.domain_names) > 0 ? "https://${var.domain_names[0]}" : (
    var.custom_domain != "" ? "https://${var.custom_domain}" : (
      var.use_front_door ? try("https://${azurerm_cdn_frontdoor_endpoint.frontend[0].host_name}", "") : try("https://${azurerm_cdn_endpoint.frontend[0].name}.azureedge.net", "")
    )
  )
}

output "subdomain_zone_id" {
  description = "Azure DNS zone ID for subdomain"
  value       = try(azurerm_dns_zone.subdomain[0].id, "")
}

output "subdomain_zone_nameservers" {
  description = "Nameservers for subdomain zone (add these as NS records in parent zone)"
  value       = try(azurerm_dns_zone.subdomain[0].name_servers, [])
}
