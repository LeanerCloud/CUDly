# ==============================================
# Compute Platform: Cloud Run (Serverless)
# ==============================================

module "compute_cloud_run" {
  source = "../../modules/compute/gcp/cloud-run"
  count  = var.compute_platform == "cloud-run" ? 1 : 0

  project_id   = var.project_id
  service_name = local.service_name
  environment  = var.environment
  region       = var.region

  # Container image (from build module or var.image_uri)
  image_uri = var.enable_docker_build ? module.build[0].image_uri : var.image_uri

  # Resources
  cpu    = var.cloud_run_cpu
  memory = var.cloud_run_memory

  # Scaling
  min_instances   = var.cloud_run_min_instances
  max_instances   = var.cloud_run_max_instances
  request_timeout = var.cloud_run_request_timeout

  # Access
  allow_unauthenticated = var.cloud_run_allow_unauthenticated

  # Database connection
  database_host               = module.database.private_ip_address
  database_name               = module.database.database_name
  database_username           = var.database_username
  database_password_secret_id = module.secrets.database_password_secret_id

  # Admin email
  admin_email                  = var.admin_email
  admin_password_secret_name   = coalesce(module.secrets.admin_password_secret_name, "")
  enable_admin_password_writer = true # secret name comes from secrets module output, not a literal

  # VPC Access (for Cloud SQL)
  vpc_connector_id = module.networking.vpc_connector_id

  # Scheduled tasks
  enable_scheduled_tasks  = var.enable_scheduled_tasks
  recommendation_schedule = var.recommendation_schedule
  scheduled_task_secret   = module.secrets.scheduled_task_secret_value

  # RI exchange automation
  enable_ri_exchange_schedule = var.enable_ri_exchange_schedule
  ri_exchange_schedule        = var.ri_exchange_schedule

  # Database migration
  auto_migrate = var.auto_migrate

  # Billing account (for billing.viewer IAM binding at account level)
  billing_account_id = var.billing_account_id

  # Additional environment variables
  additional_env_vars = merge(
    {
      STATIC_DIR                          = "/app/static"
      SENDGRID_API_KEY_SECRET             = module.secrets.sendgrid_api_key_id
      CREDENTIAL_ENCRYPTION_KEY_SECRET_ID = module.secrets.additional_secret_ids["credential-encryption-key"]
      FROM_EMAIL                          = var.subdomain_zone_name != "" ? "noreply@${var.subdomain_zone_name}" : "noreply@${var.project_name}.example.com"
      DASHBOARD_URL                       = local.dashboard_url
      CORS_ALLOWED_ORIGIN                 = local.dashboard_url != "" ? local.dashboard_url : "http://localhost:3000"
      SCHEDULED_TASK_SECRET               = module.secrets.scheduled_task_secret_value
      CUDLY_MAX_ACCOUNT_PARALLELISM       = tostring(var.max_account_parallelism)
    },
    var.additional_env_vars
  )

  labels = local.common_labels

  depends_on = [module.networking, module.database, module.secrets, module.build]
}

# ==============================================
# Compute Platform: GKE (Kubernetes)
# ==============================================

module "compute_gke" {
  source = "../../modules/compute/gcp/gke"
  count  = var.compute_platform == "gke" ? 1 : 0

  project_name = var.project_name
  environment  = var.environment
  project_id   = var.project_id
  region       = var.region

  # Container image (from build module or var.image_uri)
  image_name = local.image_name
  image_tag  = local.image_tag

  # Networking
  network_name    = module.networking.network_name
  subnetwork_name = module.networking.subnet_name
  zones           = var.gke_zones

  # Kubernetes configuration
  kubernetes_version       = var.gke_kubernetes_version
  node_count               = var.gke_node_count
  node_machine_type        = var.gke_node_machine_type
  node_disk_size_gb        = var.gke_node_disk_size_gb
  min_node_count           = var.gke_min_node_count
  max_node_count           = var.gke_max_node_count
  enable_auto_scaling      = var.gke_enable_auto_scaling
  enable_auto_repair       = var.gke_enable_auto_repair
  enable_auto_upgrade      = var.gke_enable_auto_upgrade
  enable_workload_identity = var.gke_enable_workload_identity

  # Database connection
  database_host                 = module.database.instance_connection_name
  database_name                 = module.database.database_name
  database_username             = var.database_username
  database_password_secret_name = module.secrets.database_password_secret_name

  # Application configuration
  admin_email                = var.admin_email
  admin_password_secret_name = coalesce(module.secrets.admin_password_secret_name, "")
  auto_migrate               = var.auto_migrate
  additional_env_vars = merge(
    {
      STATIC_DIR                          = "/app/static"
      SENDGRID_API_KEY_SECRET             = module.secrets.sendgrid_api_key_id
      CREDENTIAL_ENCRYPTION_KEY_SECRET_ID = module.secrets.additional_secret_ids["credential-encryption-key"]
      FROM_EMAIL                          = var.subdomain_zone_name != "" ? "noreply@${var.subdomain_zone_name}" : "noreply@${var.project_name}.example.com"
      DASHBOARD_URL                       = local.dashboard_url
      CORS_ALLOWED_ORIGIN                 = local.dashboard_url != "" ? local.dashboard_url : "http://localhost:3000"
      SCHEDULED_TASK_SECRET               = module.secrets.scheduled_task_secret_value
      CUDLY_MAX_ACCOUNT_PARALLELISM       = tostring(var.max_account_parallelism)
    },
    var.additional_env_vars
  )

  labels = local.common_labels

  depends_on = [module.networking, module.database, module.secrets]
}
