# ==============================================
# Networking Outputs
# ==============================================

output "network_name" {
  description = "VPC network name"
  value       = module.networking.network_name
}

output "subnet_name" {
  description = "Private subnet name"
  value       = module.networking.subnet_name
}

output "vpc_connector_id" {
  description = "VPC Access Connector ID"
  value       = module.networking.vpc_connector_id
}

# ==============================================
# Database Outputs
# ==============================================

output "database_instance_name" {
  description = "Cloud SQL instance name"
  value       = module.database.instance_name
}

output "database_connection_name" {
  description = "Cloud SQL connection name"
  value       = module.database.instance_connection_name
}

output "database_private_ip" {
  description = "Database private IP address"
  value       = module.database.private_ip_address
}

output "database_password_secret_id" {
  description = "Database password secret ID"
  value       = module.secrets.database_password_secret_id
  sensitive   = true
}

# ==============================================
# Compute Platform Outputs
# ==============================================

output "compute_platform" {
  description = "Selected compute platform"
  value       = var.compute_platform
}

# Cloud Run Outputs (when using cloud-run platform)
output "cloud_run_service_url" {
  description = "Cloud Run service URL"
  value       = var.compute_platform == "cloud-run" ? module.compute_cloud_run[0].service_url : null
}

output "cloud_run_service_name" {
  description = "Cloud Run service name"
  value       = var.compute_platform == "cloud-run" ? module.compute_cloud_run[0].service_name : null
}

# GKE Outputs (when using gke platform)
output "gke_cluster_name" {
  description = "GKE cluster name"
  value       = var.compute_platform == "gke" ? module.compute_gke[0].cluster_name : null
}

output "gke_api_url" {
  description = "GKE API URL (Load Balancer IP)"
  value       = var.compute_platform == "gke" ? module.compute_gke[0].api_url : null
}

output "gke_load_balancer_ip" {
  description = "GKE Load Balancer IP"
  value       = var.compute_platform == "gke" ? module.compute_gke[0].load_balancer_ip : null
}

# Unified Outputs (work for both platforms)
output "api_endpoint" {
  description = "API endpoint URL (works for both platforms)"
  value       = var.compute_platform == "cloud-run" ? module.compute_cloud_run[0].service_url : module.compute_gke[0].api_url
}

output "service_account_email" {
  description = "Service account email"
  value       = var.compute_platform == "cloud-run" ? module.compute_cloud_run[0].service_account_email : module.compute_gke[0].workload_identity_email
}

# ==============================================
# Secrets Outputs
# ==============================================

output "jwt_secret_id" {
  description = "JWT secret ID"
  value       = module.secrets.jwt_secret_id
  sensitive   = true
}

output "session_secret_id" {
  description = "Session secret ID"
  value       = module.secrets.session_secret_id
  sensitive   = true
}

# ==============================================
# Connection Information
# ==============================================

output "connection_info" {
  description = "Connection information"
  value = {
    compute_platform = var.compute_platform
    api_endpoint     = var.compute_platform == "cloud-run" ? module.compute_cloud_run[0].service_url : module.compute_gke[0].api_url
    db_host          = module.database.private_ip_address
    db_name          = module.database.database_name
    environment      = var.environment
    region           = var.region
  }
  sensitive = true
}

# ==============================================
# Quick Start Commands
# ==============================================

output "quick_start_commands" {
  description = "Quick start commands"
  value       = <<-EOT
    ================================================================================
    CUDly GCP Deployment - ${upper(var.environment)} Environment
    Compute Platform: ${upper(var.compute_platform)}
    ================================================================================

    # Test the API health check
    curl ${var.compute_platform == "cloud-run" ? module.compute_cloud_run[0].service_url : module.compute_gke[0].api_url}/health

    # View Application Logs
    ${var.compute_platform == "cloud-run" ? "gcloud run services logs read ${module.compute_cloud_run[0].service_name} --region ${var.region} --limit 50" : "gcloud container clusters get-credentials ${module.compute_gke[0].cluster_name} --region ${var.region}\n    kubectl logs -f deployment/${var.project_name}-api -n ${var.environment}"}

    # Connect to Cloud SQL (requires Cloud SQL Proxy)
    cloud-sql-proxy ${module.database.instance_connection_name}

    # Get database password
    gcloud secrets versions access latest --secret ${module.secrets.database_password_secret_name}

    # Deploy new revision
    ${var.compute_platform == "cloud-run" ? "gcloud run services update ${module.compute_cloud_run[0].service_name} --image NEW_IMAGE_URI --region ${var.region}" : "kubectl set image deployment/${var.project_name}-api api=NEW_IMAGE_URI -n ${var.environment}"}

    # Scale application
    ${var.compute_platform == "cloud-run" ? "gcloud run services update ${module.compute_cloud_run[0].service_name} --min-instances 1 --max-instances 20 --region ${var.region}" : "kubectl scale deployment/${var.project_name}-api --replicas=3 -n ${var.environment}"}

    ================================================================================
  EOT
}
