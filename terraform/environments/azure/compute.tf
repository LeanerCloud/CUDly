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
  enable_diagnostics             = false # Container App Environment handles logging via Log Analytics
  database_host                  = module.database.server_fqdn
  database_name                  = module.database.database_name
  database_username              = module.database.administrator_login
  database_password_secret_name  = module.secrets.database_password_secret_name
  key_vault_uri                  = module.secrets.key_vault_uri
  auto_migrate                   = var.auto_migrate
  admin_email                    = var.admin_email
  admin_password_secret_name     = coalesce(module.secrets.admin_password_secret_name, "")
  additional_env_vars = merge(
    {
      STATIC_DIR                            = "/app/static"
      CREDENTIAL_ENCRYPTION_KEY_SECRET_NAME = module.secrets.additional_secret_names["credential-encryption-key"]
      AZURE_SMTP_USERNAME_SECRET            = module.secrets.smtp_username_name
      AZURE_SMTP_PASSWORD_SECRET            = module.secrets.smtp_password_name
      FROM_EMAIL                            = var.enable_email_service ? module.email[0].sender_address : "noreply@${var.project_name}.example.com"
      DASHBOARD_URL                         = local.dashboard_url
      CORS_ALLOWED_ORIGIN                   = local.dashboard_url != "" ? local.dashboard_url : "http://localhost:3000"
      SCHEDULED_TASK_SECRET                 = module.secrets.scheduled_task_secret_value
      CUDLY_MAX_ACCOUNT_PARALLELISM         = tostring(var.max_account_parallelism)
      CUDLY_SOURCE_CLOUD                    = "azure"
    },
    var.additional_env_vars
  )
  # ACR registry credentials for image pull
  registry_server   = azurerm_container_registry.main.login_server
  registry_username = azurerm_container_registry.main.admin_username
  registry_password = azurerm_container_registry.main.admin_password

  # Scheduled tasks (Logic Apps)
  enable_scheduled_tasks  = var.enable_scheduled_tasks
  scheduled_task_secret   = module.secrets.scheduled_task_secret_value
  recommendation_schedule = var.recommendation_schedule

  # RI exchange automation
  enable_ri_exchange_schedule = var.enable_ri_exchange_schedule
  ri_exchange_schedule        = var.ri_exchange_schedule

  tags = local.common_tags

  depends_on = [module.networking, module.database, module.secrets, module.build]
}

# ==============================================
# Compute Module: AKS (Kubernetes)
# ==============================================

module "compute_aks" {
  source = "../../modules/compute/azure/aks"
  count  = var.compute_platform == "aks" ? 1 : 0

  project_name        = var.project_name
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
  database_password_secret_name = module.secrets.database_password_secret_name

  # Key Vault for secrets
  key_vault_id  = module.secrets.key_vault_id
  key_vault_uri = module.secrets.key_vault_uri

  # Application configuration
  admin_email                = var.admin_email
  admin_password_secret_name = coalesce(module.secrets.admin_password_secret_name, "")
  auto_migrate               = var.auto_migrate
  additional_env_vars = merge(
    {
      STATIC_DIR                            = "/app/static"
      CREDENTIAL_ENCRYPTION_KEY_SECRET_NAME = module.secrets.additional_secret_names["credential-encryption-key"]
      AZURE_SMTP_USERNAME_SECRET            = module.secrets.smtp_username_name
      AZURE_SMTP_PASSWORD_SECRET            = module.secrets.smtp_password_name
      FROM_EMAIL                            = var.enable_email_service ? module.email[0].sender_address : "noreply@${var.project_name}.example.com"
      DASHBOARD_URL                         = local.dashboard_url
      CORS_ALLOWED_ORIGIN                   = local.dashboard_url != "" ? local.dashboard_url : "http://localhost:3000"
      SCHEDULED_TASK_SECRET                 = module.secrets.scheduled_task_secret_value
      CUDLY_MAX_ACCOUNT_PARALLELISM         = tostring(var.max_account_parallelism)
      CUDLY_SOURCE_CLOUD                    = "azure"
    },
    var.additional_env_vars
  )

  # Add-ons
  enable_azure_policy  = var.aks_enable_azure_policy
  enable_log_analytics = var.aks_enable_log_analytics

  tags = local.common_tags

  depends_on = [module.networking, module.database, module.secrets]
}

# ==============================================
# RBAC - Grant Compute access to Key Vault
# ==============================================

# Grant Container App managed identity "Key Vault Secrets User" role
# so the app can read secrets (DB password, JWT secret, etc.) at runtime
resource "azurerm_role_assignment" "compute_key_vault_secrets_user" {
  count = var.compute_platform == "container-apps" && length(module.compute_container_apps) > 0 ? 1 : 0

  scope                = module.secrets.key_vault_id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = module.compute_container_apps[0].managed_identity_principal_id

  # Implicit dependency through principal_id and scope references is sufficient.
  # Avoid explicit depends_on on modules to prevent cascading replacements.
}
