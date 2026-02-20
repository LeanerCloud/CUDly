output "network_id" {
  description = "VPC network ID"
  value       = google_compute_network.main.id
}

output "network_name" {
  description = "VPC network name"
  value       = google_compute_network.main.name
}

output "network_self_link" {
  description = "VPC network self link"
  value       = google_compute_network.main.self_link
}

output "subnet_id" {
  description = "Private subnet ID"
  value       = google_compute_subnetwork.private.id
}

output "subnet_name" {
  description = "Private subnet name"
  value       = google_compute_subnetwork.private.name
}

output "subnet_cidr" {
  description = "Private subnet CIDR range"
  value       = google_compute_subnetwork.private.ip_cidr_range
}

output "vpc_connector_id" {
  description = "Serverless VPC Access Connector ID"
  value       = google_vpc_access_connector.main.id
}

output "vpc_connector_name" {
  description = "Serverless VPC Access Connector name"
  value       = google_vpc_access_connector.main.name
}

output "vpc_connector_self_link" {
  description = "Serverless VPC Access Connector self link"
  value       = google_vpc_access_connector.main.self_link
}

output "private_vpc_connection_peering" {
  description = "Private VPC connection peering name"
  value       = google_service_networking_connection.private_vpc_connection.peering
}

output "cloud_router_name" {
  description = "Cloud Router name"
  value       = google_compute_router.main.name
}

output "cloud_nat_name" {
  description = "Cloud NAT name"
  value       = google_compute_router_nat.main.name
}
