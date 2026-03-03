# ==============================================
# Compute Platform: Lambda (default)
# ==============================================

module "compute_lambda" {
  source = "../../modules/compute/aws/lambda"
  count  = var.compute_platform == "lambda" ? 1 : 0

  stack_name  = local.stack_name
  environment = var.environment
  region      = var.region

  # Container image (from build module or var.image_uri)
  image_uri    = var.enable_docker_build ? module.build[0].image_uri : var.image_uri
  architecture = var.lambda_architecture
  memory_size  = var.lambda_memory_size
  timeout      = var.lambda_timeout

  # Database connection (use RDS Proxy endpoint)
  database_host                = module.database.proxy_endpoint != null ? module.database.proxy_endpoint : module.database.instance_address
  database_name                = module.database.database_name
  database_username            = var.database_username
  database_password_secret_arn = module.database.password_secret_arn

  # Admin user configuration
  admin_email    = module.database.admin_email
  admin_password = var.admin_password

  # Auto-migrate on cold start
  auto_migrate = var.database_auto_migrate

  # VPC configuration
  vpc_config = {
    vpc_id                        = module.networking.vpc_id
    subnet_ids                    = module.networking.private_subnet_ids
    additional_security_group_ids = []
  }

  # Function URL
  enable_function_url    = var.lambda_enable_function_url
  function_url_auth_type = var.lambda_function_url_auth_type
  allowed_origins        = var.lambda_allowed_origins

  # Concurrency
  reserved_concurrent_executions = var.lambda_reserved_concurrency

  # Logging
  log_retention_days = var.lambda_log_retention_days

  # Scheduled tasks
  enable_scheduled_tasks  = var.enable_scheduled_tasks
  recommendation_schedule = var.recommendation_schedule

  # Additional environment variables
  additional_env_vars = merge(
    {
      STATIC_DIR          = "/app/static"
      DASHBOARD_URL       = local.dashboard_url
      CORS_ALLOWED_ORIGIN = local.dashboard_url != "" ? local.dashboard_url : "*"
      FROM_EMAIL          = "noreply@${var.subdomain_zone_name}"
    },
    var.additional_env_vars
  )

  tags = local.common_tags

  # CRITICAL: Wait for resources before creating/updating Lambda
  # Build dependency must be explicit — image_uri is computed before docker push completes
  depends_on = [module.networking, module.database, module.secrets, module.build]
}

# ==============================================
# Compute Platform: Fargate (alternative)
# ==============================================

module "compute_fargate" {
  source = "../../modules/compute/aws/fargate"
  count  = var.compute_platform == "fargate" ? 1 : 0

  stack_name  = local.stack_name
  environment = var.environment
  region      = var.region

  # Container image (from build module or var.image_uri)
  image_uri = var.enable_docker_build ? module.build[0].image_uri : var.image_uri

  # Fargate resources
  cpu           = var.fargate_cpu
  memory        = var.fargate_memory
  desired_count = var.fargate_desired_count
  min_capacity  = var.fargate_min_capacity
  max_capacity  = var.fargate_max_capacity

  # Database connection
  database_host                = module.database.instance_address
  database_name                = module.database.database_name
  database_username            = var.database_username
  database_password_secret_arn = module.database.password_secret_arn

  # Admin user configuration
  admin_email    = var.admin_email
  admin_password = var.admin_password

  # Auto-migrate on startup
  auto_migrate = var.database_auto_migrate

  # Networking
  vpc_id                = module.networking.vpc_id
  private_subnet_ids    = module.networking.private_subnet_ids
  public_subnet_ids     = module.networking.public_subnet_ids
  alb_security_group_id = module.networking.alb_security_group_id

  # HTTPS - use wildcard cert for TLS on ALB
  enable_https = length(var.frontend_domain_names) > 0 && var.subdomain_zone_name != ""
  certificate_arn = (
    length(aws_acm_certificate.frontend) > 0
    ? aws_acm_certificate_validation.frontend[0].certificate_arn
    : var.fargate_certificate_arn
  )

  # Health checks
  health_check_path = "/health"

  # CORS
  allowed_origins = var.lambda_allowed_origins

  # Logging
  log_retention_days = var.lambda_log_retention_days

  # Scheduled tasks
  enable_scheduled_tasks  = var.enable_scheduled_tasks
  recommendation_schedule = var.recommendation_schedule

  # ECS Exec for debugging
  enable_execute_command = var.fargate_enable_execute_command

  # Additional environment variables
  additional_env_vars = merge(
    {
      STATIC_DIR          = "/app/static"
      DASHBOARD_URL       = local.dashboard_url
      CORS_ALLOWED_ORIGIN = local.dashboard_url != "" ? local.dashboard_url : "*"
      FROM_EMAIL          = "noreply@${var.subdomain_zone_name}"
    },
    var.additional_env_vars
  )

  tags = local.common_tags

  # CRITICAL: Wait for resources before creating/updating Fargate
  # Build dependency must be explicit — image_uri is computed before docker push completes
  depends_on = [module.networking, module.database, module.secrets, module.build]
}
