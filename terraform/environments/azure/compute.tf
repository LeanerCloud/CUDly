# ==============================================
# Compute Module: Container Apps (Serverless)
# ==============================================

module "compute_container_apps" {
  source = "../../modules/compute/azure/container-apps"
  count  = var.compute_platform == "container-apps" ? 1 : 0

  app_name            = local.app_name
  environment         = var.environment
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location

  # Container image (from build module or var.image_uri)
  image_uri                      = var.enable_docker_build ? module.build[0].image_uri : var.image_uri
  cpu                            = var.container_cpu
  memory                         = var.container_memory
  min_replicas                   = var.min_replicas
  max_replicas                   = var.max_replicas
  external_ingress_enabled       = var.external_ingress_enabled
  infrastructure_subnet_id       = module.networking.container_apps_subnet_id
  internal_load_balancer_enabled = var.internal_load_balancer_enabled
  log_analytics_workspace_id     = module.networking.log_analytics_workspace_id
  enable_diagnostics             = true
  database_host                  = module.database.server_fqdn
  database_name                  = module.database.database_name
  database_username              = module.database.administrator_login
  database_password_secret_id    = module.secrets.database_password_secret_id
  key_vault_uri                  = module.secrets.key_vault_uri
  auto_migrate                   = var.auto_migrate
  admin_email                    = var.admin_email
  admin_password                 = var.admin_password
  additional_env_vars = merge(
    {
      JWT_SECRET_ARN      = module.secrets.jwt_secret_id
      SESSION_SECRET_ARN  = module.secrets.session_secret_id
      AZURE_SMTP_USERNAME = module.secrets.smtp_username_id
      AZURE_SMTP_PASSWORD = module.secrets.smtp_password_id
      FROM_EMAIL          = var.enable_email_service ? module.email[0].sender_address : "noreply@${var.app_name}.example.com"
      DASHBOARD_URL       = local.dashboard_url
      CORS_ALLOWED_ORIGIN = local.dashboard_url != "" ? local.dashboard_url : "*"
    },
    var.additional_env_vars
  )
  enable_scheduled_jobs   = var.enable_scheduled_jobs
  recommendation_schedule = var.recommendation_schedule

  tags = local.common_tags

  depends_on = [module.networking, module.database, module.secrets, module.build]
}

# ==============================================
# Compute Module: AKS (Kubernetes)
# ==============================================

module "compute_aks" {
  source = "../../modules/compute/azure/aks"
  count  = var.compute_platform == "aks" ? 1 : 0

  project_name        = var.app_name
  environment         = var.environment
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location

  # Container image (from build module or var.image_uri)
  image_name = local.image_name
  image_tag  = local.image_tag

  # Networking
  vnet_subnet_id = module.networking.container_apps_subnet_id

  # Kubernetes configuration
  kubernetes_version  = var.aks_kubernetes_version
  node_count          = var.aks_node_count
  node_vm_size        = var.aks_node_vm_size
  min_node_count      = var.aks_min_node_count
  max_node_count      = var.aks_max_node_count
  enable_auto_scaling = var.aks_enable_auto_scaling

  # Database connection
  database_host                 = module.database.server_fqdn
  database_name                 = module.database.database_name
  database_username             = module.database.administrator_login
  database_password_secret_name = "database-password"

  # Key Vault for secrets
  key_vault_id = module.secrets.key_vault_id

  # Add-ons
  enable_azure_policy  = var.aks_enable_azure_policy
  enable_log_analytics = var.aks_enable_log_analytics

  tags = local.common_tags

  depends_on = [module.networking, module.database, module.secrets]
}

# ==============================================
# RBAC - Grant Compute access to Key Vault
# ==============================================

# This is handled in the compute modules via managed identities
# Container Apps and AKS both have their own identity management

resource "null_resource" "update_key_vault_rbac" {
  triggers = {
    compute_platform = var.compute_platform
    principal_id = var.compute_platform == "container-apps" ? (
      length(module.compute_container_apps) > 0 ? module.compute_container_apps[0].managed_identity_principal_id : ""
      ) : (
      length(module.compute_aks) > 0 ? module.compute_aks[0].workload_identity_client_id : ""
    )
  }

  depends_on = [module.compute_container_apps, module.compute_aks, module.secrets]
}
