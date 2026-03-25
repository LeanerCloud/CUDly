# ==============================================
# Container Registry (Artifact Registry)
# ==============================================

module "registry" {
  source        = "../../modules/registry/gcp"
  project_id    = var.project_id
  location      = var.region
  repository_id = "cudly"
  labels        = local.common_labels
}
