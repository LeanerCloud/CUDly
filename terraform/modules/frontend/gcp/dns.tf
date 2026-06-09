# GCP Cloud DNS Zone for subdomain (optional)
# This zone is delegated from the parent zone
# Only created if subdomain_zone_name is set

resource "google_dns_managed_zone" "subdomain" {
  count = var.subdomain_zone_name != "" ? 1 : 0

  name        = replace(var.subdomain_zone_name, ".", "-")
  dns_name    = "${var.subdomain_zone_name}."
  project     = var.project_id
  description = "Managed zone for ${var.subdomain_zone_name}"

  labels = merge(var.labels, {
    name        = var.subdomain_zone_name
    environment = var.environment
  })
}

# A record for frontend (points to load balancer IP)
# Only created if subdomain zone is managed and domain names are provided
resource "google_dns_record_set" "frontend_a" {
  count = var.subdomain_zone_name != "" && length(var.domain_names) > 0 ? 1 : 0

  name         = "${var.domain_names[0]}."
  type         = "A"
  ttl          = 300
  managed_zone = google_dns_managed_zone.subdomain[0].name
  project      = var.project_id
  rrdatas      = [google_compute_global_address.frontend.address]
}
