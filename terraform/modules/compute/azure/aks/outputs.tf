# Azure AKS Module Outputs

output "cluster_id" {
  description = "AKS cluster ID"
  value       = azurerm_kubernetes_cluster.main.id
}

output "cluster_name" {
  description = "AKS cluster name"
  value       = azurerm_kubernetes_cluster.main.name
}

output "cluster_fqdn" {
  description = "AKS cluster FQDN"
  value       = azurerm_kubernetes_cluster.main.fqdn
}

output "kube_config" {
  description = "Kubernetes configuration"
  value       = azurerm_kubernetes_cluster.main.kube_config_raw
  sensitive   = true
}

output "cluster_ca_certificate" {
  description = "Cluster CA certificate"
  value       = azurerm_kubernetes_cluster.main.kube_config[0].cluster_ca_certificate
  sensitive   = true
}

output "host" {
  description = "Kubernetes host"
  value       = azurerm_kubernetes_cluster.main.kube_config[0].host
  sensitive   = true
}

output "client_certificate" {
  description = "Client certificate"
  value       = azurerm_kubernetes_cluster.main.kube_config[0].client_certificate
  sensitive   = true
}

output "client_key" {
  description = "Client key"
  value       = azurerm_kubernetes_cluster.main.kube_config[0].client_key
  sensitive   = true
}

output "load_balancer_ip" {
  description = "Load Balancer external IP"
  value       = try(data.kubernetes_service.nginx_ingress.status[0].load_balancer[0].ingress[0].ip, "")
}

output "api_url" {
  description = "API URL (Load Balancer IP)"
  value       = try("http://${data.kubernetes_service.nginx_ingress.status[0].load_balancer[0].ingress[0].ip}", "")
}

output "namespace" {
  description = "Kubernetes namespace"
  value       = kubernetes_namespace.app.metadata[0].name
}

output "service_name" {
  description = "Kubernetes service name"
  value       = kubernetes_service.app.metadata[0].name
}

output "deployment_name" {
  description = "Kubernetes deployment name"
  value       = kubernetes_deployment.app.metadata[0].name
}

output "workload_identity_client_id" {
  description = "Workload identity client ID"
  value       = azurerm_user_assigned_identity.workload.client_id
}

output "log_analytics_workspace_id" {
  description = "Log Analytics workspace ID"
  value       = var.enable_log_analytics ? azurerm_log_analytics_workspace.aks[0].id : null
}
