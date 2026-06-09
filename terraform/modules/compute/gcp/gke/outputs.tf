# GCP GKE Module Outputs

output "cluster_id" {
  description = "GKE cluster ID"
  value       = google_container_cluster.main.id
}

output "cluster_name" {
  description = "GKE cluster name"
  value       = google_container_cluster.main.name
}

output "cluster_endpoint" {
  description = "GKE cluster endpoint"
  value       = google_container_cluster.main.endpoint
  sensitive   = true
}

output "cluster_ca_certificate" {
  description = "Cluster CA certificate"
  value       = google_container_cluster.main.master_auth[0].cluster_ca_certificate
  sensitive   = true
}

output "cluster_location" {
  description = "GKE cluster location (region)"
  value       = google_container_cluster.main.location
}

output "node_pool_name" {
  description = "Primary node pool name"
  value       = google_container_node_pool.primary.name
}

output "workload_identity_email" {
  description = "Workload identity service account email"
  value       = google_service_account.workload.email
}

output "load_balancer_ip" {
  description = "Load Balancer external IP"
  value       = var.deploy_kubernetes_resources ? google_compute_global_address.ingress[0].address : ""
}

output "api_url" {
  description = "API URL (Load Balancer IP)"
  value       = var.deploy_kubernetes_resources ? "http://${google_compute_global_address.ingress[0].address}" : ""
}

output "namespace" {
  description = "Kubernetes namespace"
  value       = var.deploy_kubernetes_resources ? kubernetes_namespace.app[0].metadata[0].name : ""
}

output "service_name" {
  description = "Kubernetes service name"
  value       = var.deploy_kubernetes_resources ? kubernetes_service.app[0].metadata[0].name : ""
}

output "deployment_name" {
  description = "Kubernetes deployment name"
  value       = var.deploy_kubernetes_resources ? kubernetes_deployment.app[0].metadata[0].name : ""
}

output "kubeconfig_command" {
  description = "Command to get kubeconfig"
  value       = "gcloud container clusters get-credentials ${google_container_cluster.main.name} --region ${google_container_cluster.main.location} --project ${var.project_id}"
}
