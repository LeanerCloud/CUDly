# Azure Frontend Module Outputs

output "storage_account_id" {
  description = "Storage account ID"
  value       = azurerm_storage_account.frontend.id
}

output "storage_account_name" {
  description = "Storage account name"
  value       = azurerm_storage_account.frontend.name
}

output "storage_primary_web_endpoint" {
  description = "Primary web endpoint for static website"
  value       = azurerm_storage_account.frontend.primary_web_endpoint
}

output "storage_primary_web_host" {
  description = "Primary web host for static website"
  value       = azurerm_storage_account.frontend.primary_web_host
}

output "cdn_profile_id" {
  description = "CDN profile ID"
  value       = var.use_front_door ? "" : azurerm_cdn_profile.frontend[0].id
}

output "cdn_endpoint_id" {
  description = "CDN endpoint ID"
  value       = var.use_front_door ? "" : azurerm_cdn_endpoint.frontend[0].id
}

output "cdn_endpoint_hostname" {
  description = "CDN endpoint hostname (format: <name>.azureedge.net)"
  value       = var.use_front_door ? "" : "${azurerm_cdn_endpoint.frontend[0].name}.azureedge.net"
}

output "frontend_url" {
  description = "Frontend URL (CDN, Front Door, or custom domain)"
  value = var.custom_domain != "" ? "https://${var.custom_domain}" : (
    var.use_front_door ? "https://${azurerm_cdn_frontdoor_endpoint.frontend[0].host_name}" : "https://${azurerm_cdn_endpoint.frontend[0].name}.azureedge.net"
  )
}

output "frontdoor_endpoint_hostname" {
  description = "Front Door endpoint hostname (if enabled)"
  value       = var.use_front_door ? azurerm_cdn_frontdoor_endpoint.frontend[0].host_name : ""
}

output "subdomain_zone_id" {
  description = "Azure DNS zone ID for subdomain"
  value       = var.subdomain_zone_name != "" ? azurerm_dns_zone.subdomain[0].id : ""
}

output "subdomain_zone_nameservers" {
  description = "Nameservers for subdomain zone (add these as NS records in parent zone)"
  value       = var.subdomain_zone_name != "" ? azurerm_dns_zone.subdomain[0].name_servers : []
}
