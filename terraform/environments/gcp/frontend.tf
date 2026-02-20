# ==============================================
# Frontend (Cloud CDN + Load Balancer)
# ==============================================

module "frontend" {
  source = "../../modules/frontend/gcp"
  count  = var.enable_frontend ? 1 : 0

  project_id   = var.project_id
  project_name = var.project_name
  environment  = var.environment
  region       = var.region

  # Cloud Storage bucket for frontend files
  bucket_name = var.frontend_bucket_name != "" ? var.frontend_bucket_name : "${local.service_name}-frontend"

  # Cloud Run service name for API backend
  cloud_run_service_name = var.compute_platform == "cloud-run" ? (
    length(module.compute_cloud_run) > 0 ? module.compute_cloud_run[0].service_name : ""
  ) : ""

  # Custom domain configuration
  domain_names        = var.frontend_domain_names
  subdomain_zone_name = var.subdomain_zone_name

  # Security
  enable_cloud_armor = var.enable_cloud_armor

  # Frontend build configuration
  enable_frontend_build = var.enable_frontend_build

  labels = local.common_labels

  depends_on = [module.compute_cloud_run]
}
