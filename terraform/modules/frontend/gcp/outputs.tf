# GCP Frontend Module Outputs

output "load_balancer_ip" {
  description = "Global load balancer IP address"
  value       = google_compute_global_address.frontend.address
}

output "frontend_url" {
  description = "Frontend URL (always HTTPS, uses custom domain or IP)"
  value       = length(var.domain_names) > 0 ? "https://${var.domain_names[0]}" : "https://${google_compute_global_address.frontend.address}"
}

output "backend_service_id" {
  description = "Cloud Run backend service ID"
  value       = google_compute_backend_service.cloudrun.id
}

output "ssl_certificate_id" {
  description = "SSL certificate ID (managed cert for custom domains, self-signed for dev)"
  value = length(var.domain_names) > 0 ? (
    google_compute_managed_ssl_certificate.frontend[0].id
  ) : google_compute_ssl_certificate.self_signed[0].id
}

output "subdomain_zone_name" {
  description = "Cloud DNS managed zone name for subdomain"
  value       = var.subdomain_zone_name != "" ? google_dns_managed_zone.subdomain[0].name : ""
}

output "subdomain_zone_nameservers" {
  description = "Nameservers for subdomain zone (add these as NS records in parent zone)"
  value       = var.subdomain_zone_name != "" ? google_dns_managed_zone.subdomain[0].name_servers : []
}
