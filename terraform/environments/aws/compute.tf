# ==============================================
# Compute Platform: Lambda (default)
# ==============================================

# Lambda Function URL auth type is fully derived from enable_cdn. There is no
# operator-facing variable for it: the two valid combinations are
#
#   enable_cdn = false  -> auth_type = "NONE"
#     The SPA hits the Function URL directly. Browsers cannot SigV4-sign, so
#     the URL must be unauthenticated at the AWS-IAM layer; auth is enforced
#     at the application layer (login session, CSRF, API keys).
#
#   enable_cdn = true   -> auth_type = "AWS_IAM"
#     CloudFront fronts the URL with an OAC that SigV4-signs every request
#     (see frontend module's aws_cloudfront_origin_access_control.lambda).
#     The Function URL rejects anything not signed by CloudFront's OAC,
#     closing the public-internet exposure described in #424.
#
# Tying the two flags together prevents two specific mis-configurations:
#   (a) enable_cdn = true with auth_type = "NONE" -> public Lambda URL behind
#       a pointless CloudFront (security goal of #424 defeated).
#   (b) enable_cdn = false with auth_type = "AWS_IAM" -> direct browser hits
#       to Function URL return 403 (no way to sign), site is unreachable.
locals {
  lambda_function_url_auth_type = var.enable_cdn ? "AWS_IAM" : "NONE"
}

module "compute_lambda" {
  source = "../../modules/compute/aws/lambda"
  count  = var.compute_platform == "lambda" ? 1 : 0

  stack_name  = local.stack_name
  environment = var.environment
  region      = var.region

  # Container image (from build module or var.image_uri)
  image_uri    = var.enable_docker_build ? module.build[0].image_uri : var.image_uri
  architecture = local.effective_lambda_arch
  memory_size  = var.lambda_memory_size
  timeout      = var.lambda_timeout

  # Database connection (use RDS Proxy endpoint)
  database_host                = module.database.proxy_endpoint != null ? module.database.proxy_endpoint : module.database.instance_address
  database_name                = module.database.database_name
  database_username            = var.database_username
  database_password_secret_arn = module.database.password_secret_arn

  # Admin user configuration
  admin_email               = module.database.admin_email
  admin_password_secret_arn = coalesce(module.secrets.admin_password_secret_arn, "")

  # Auto-migrate on cold start
  auto_migrate = var.database_auto_migrate

  # VPC configuration
  vpc_config = {
    vpc_id                        = module.networking.vpc_id
    subnet_ids                    = module.networking.private_subnet_ids
    additional_security_group_ids = []
  }

  # Function URL
  function_url_auth_type = local.lambda_function_url_auth_type
  allowed_origins        = var.lambda_allowed_origins

  # Concurrency
  reserved_concurrent_executions = var.lambda_reserved_concurrency

  # Logging
  log_retention_days = var.lambda_log_retention_days

  # Scheduled tasks
  enable_scheduled_tasks  = var.enable_scheduled_tasks
  recommendation_schedule = var.recommendation_schedule

  # RI exchange automation
  enable_ri_exchange_schedule = var.enable_ri_exchange_schedule
  ri_exchange_schedule        = var.ri_exchange_schedule

  # Additional environment variables
  additional_env_vars = merge(
    {
      STATIC_DIR                           = "/app/static"
      DASHBOARD_URL                        = local.dashboard_url
      CORS_ALLOWED_ORIGIN                  = local.dashboard_url != "" ? local.dashboard_url : "http://localhost:3000"
      FROM_EMAIL                           = local.effective_from_email
      CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN = module.secrets.credential_encryption_key_secret_arn
      CUDLY_MAX_ACCOUNT_PARALLELISM        = tostring(var.max_account_parallelism)
      CUDLY_SOURCE_CLOUD                   = "aws"
    },
    var.additional_env_vars
  )

  # Multi-account IAM capabilities. cross_account_role_name_prefix scopes the
  # Lambda role's sts:AssumeRole IAM grant to role names starting with the
  # prefix — defence-in-depth on top of the app-layer ExternalId check.
  enable_cross_account_sts             = true
  cross_account_role_name_prefix       = "CUDly"
  enable_org_discovery                 = true
  credential_encryption_key_secret_arn = module.secrets.credential_encryption_key_secret_arn

  # Scheduled-task bearer secret. The HTTP /api/scheduled/* path is reachable
  # via the public Lambda URL; bearer mode + Secrets Manager keeps it gated
  # without putting the secret in Lambda env or Terraform state.
  scheduled_task_secret_arn  = coalesce(module.secrets.scheduled_task_secret_arn, "")
  scheduled_task_secret_name = coalesce(module.secrets.scheduled_task_secret_name, "")

  # SES From domain — scopes the Lambda's SES policy to identity/{domain}
  # plus configuration-set/{stack}*. Leave empty to disable SES entirely
  # (deployments without email notifications don't get any SES permissions).
  # Derived from effective_from_email so an override via var.from_email
  # correctly scopes IAM to whatever identity is being used (e.g.
  # "leanercloud.com" when FROM_EMAIL is contact@leanercloud.com).
  email_from_domain = local.effective_email_from_domain

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
  image_uri        = var.enable_docker_build ? module.build[0].image_uri : var.image_uri
  cpu_architecture = local.effective_lambda_arch

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
  admin_email               = var.admin_email
  admin_password_secret_arn = coalesce(module.secrets.admin_password_secret_arn, "")

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

  # RI exchange automation
  enable_ri_exchange_schedule = var.enable_ri_exchange_schedule
  ri_exchange_schedule        = var.ri_exchange_schedule

  # ECS Exec for debugging
  enable_execute_command = var.fargate_enable_execute_command

  # Additional environment variables
  additional_env_vars = merge(
    {
      STATIC_DIR                           = "/app/static"
      DASHBOARD_URL                        = local.dashboard_url
      CORS_ALLOWED_ORIGIN                  = local.dashboard_url != "" ? local.dashboard_url : "http://localhost:3000"
      FROM_EMAIL                           = local.effective_from_email
      CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN = module.secrets.credential_encryption_key_secret_arn
      CUDLY_MAX_ACCOUNT_PARALLELISM        = tostring(var.max_account_parallelism)
      CUDLY_SOURCE_CLOUD                   = "aws"
    },
    var.additional_env_vars
  )

  # Multi-account IAM capabilities — kept at parity with the Lambda branch.
  # cross_account_role_name_prefix scopes the task role's sts:AssumeRole grant
  # to role names starting with the prefix; ExternalId validation still
  # happens at the app layer (credentials/resolver.go).
  enable_cross_account_sts             = true
  cross_account_role_name_prefix       = "CUDly"
  enable_org_discovery                 = true
  credential_encryption_key_secret_arn = module.secrets.credential_encryption_key_secret_arn
  email_from_domain                    = local.effective_email_from_domain

  # Scheduled-task bearer secret. The HTTP /api/scheduled/* path is
  # exposed via the public ALB; bearer mode + Secrets Manager keeps it
  # gated without putting the secret in the task definition or state.
  scheduled_task_secret_arn  = coalesce(module.secrets.scheduled_task_secret_arn, "")
  scheduled_task_secret_name = coalesce(module.secrets.scheduled_task_secret_name, "")

  tags = local.common_tags

  # CRITICAL: Wait for resources before creating/updating Fargate
  # Build dependency must be explicit — image_uri is computed before docker push completes
  depends_on = [module.networking, module.database, module.secrets, module.build]
}

# ==============================================
# Lambda Function URL: CloudFront OAC permission
# ==============================================
#
# This permission lives at the environment layer (not inside the lambda module)
# because both module.compute_lambda and module.frontend must be fully resolved
# before the permission can reference both the Lambda function name and the
# CloudFront distribution ARN. Placing it inside either module would create a
# dependency cycle (Lambda URL is module.frontend's origin; distribution ARN
# would need to feed back into the lambda module).
#
# Created only when:
#   - compute platform is lambda (function URL is active)
#   - enable_cdn is true (CloudFront distribution is deployed and provides the
#     source ARN); when enable_cdn = true the local also flips auth_type to
#     AWS_IAM so the IAM gate is enforced on the Function URL.
#
# When enable_cdn = false the Function URL stays NONE (no IAM gate) and this
# resource is count = 0; the SPA hits the Function URL directly and auth is
# enforced at the application layer.
resource "aws_lambda_permission" "function_url_cloudfront" {
  count = var.compute_platform == "lambda" && var.enable_cdn ? 1 : 0

  statement_id           = "FunctionURLAllowCloudFront"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = module.compute_lambda[0].function_name
  principal              = "cloudfront.amazonaws.com"
  source_arn             = module.frontend[0].cloudfront_distribution_arn
  function_url_auth_type = "AWS_IAM"
}
