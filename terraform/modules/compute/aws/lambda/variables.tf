variable "stack_name" {
  description = "Name of the stack"
  type        = string
}

variable "enable_migration_alarm" {
  description = "Create the migration-failure CloudWatch metric filter + alarm. Defaults to false because the metric filter requires logs:PutMetricFilter on the deploy SA, which is granted via the ci-cd-permissions bootstrap (root CLAUDE.md CI/CD IAM split). Leaving it false keeps deploys unblocked when that permission is absent; set true only after re-applying the bootstrap so the deploy role can manage the filter."
  type        = bool
  default     = false
}

variable "environment" {
  description = "Environment name (dev/staging/prod)"
  type        = string
}

variable "region" {
  description = "AWS region"
  type        = string
}

variable "image_uri" {
  description = "ECR image URI for Lambda container"
  type        = string
}

variable "architecture" {
  description = "Lambda architecture (x86_64 or arm64)"
  type        = string
  default     = "arm64"
}

variable "memory_size" {
  description = "Lambda memory size in MB"
  type        = number
  default     = 512
}

variable "timeout" {
  description = "Lambda timeout in seconds"
  type        = number
  default     = 30
}

variable "database_host" {
  description = "Database endpoint (RDS Proxy endpoint recommended)"
  type        = string
}

variable "database_name" {
  description = "Database name"
  type        = string
}

variable "database_username" {
  description = "Database username"
  type        = string
}

variable "database_password_secret_arn" {
  description = "ARN of Secrets Manager secret containing database password"
  type        = string
}

variable "admin_email" {
  description = "Email address for the default admin user (created without password - must use password reset)"
  type        = string
}

variable "admin_password_secret_arn" {
  description = "ARN of Secrets Manager secret containing admin password"
  type        = string
  default     = ""
}

variable "auto_migrate" {
  description = "Automatically run database migrations on startup"
  type        = bool
  default     = true
}

variable "vpc_config" {
  description = "VPC configuration for Lambda"
  type = object({
    vpc_id                        = string
    subnet_ids                    = list(string)
    additional_security_group_ids = list(string)
  })
  default = null
}


variable "function_url_auth_type" {
  description = <<-EOT
    Function URL authorization type. Use "AWS_IAM" (default, recommended) when a
    CloudFront distribution with an OAC is deployed in front of the Function URL —
    AWS then enforces SigV4 signing on every request so the Function URL is
    unreachable without a valid AWS identity.
    Use "NONE" only in environments where direct access without CloudFront is
    acceptable (e.g. local dev, or environments not yet provisioned with CloudFront).
    Pair with cloudfront_distribution_arn when switching to AWS_IAM so the module can
    create the necessary resource-based policy granting CloudFront invocation rights.
  EOT
  type        = string
  default     = "AWS_IAM"

  validation {
    condition     = contains(["NONE", "AWS_IAM"], var.function_url_auth_type)
    error_message = "function_url_auth_type must be \"NONE\" or \"AWS_IAM\"."
  }
}

variable "allowed_origins" {
  description = <<-EOT
    Explicit CORS origin allowlist for the Lambda Function URL. Must be non-empty
    and must not contain the wildcard "*". The Function URL CORS block uses
    allow_credentials = true; AWS reflects the inbound Origin header verbatim for
    each listed origin, which the browser treats as a trusted cross-origin endpoint.
    A wildcard origin combined with credentials is equivalent to any-origin CSRF:
    any website can read the response with credentials included.
    Set this to the actual CloudFront or frontend domain for each environment,
    e.g. ["https://app.example.com"] or ["https://<lambda-url-id>.lambda-url.us-east-1.on.aws"].
  EOT
  type        = list(string)
  default     = []

  validation {
    condition     = length(var.allowed_origins) > 0
    error_message = "allowed_origins must not be empty for Lambda Function URL CORS — set an explicit origin list in tfvars."
  }

  validation {
    condition     = !contains(var.allowed_origins, "*")
    error_message = "allowed_origins must not contain \"*\". The Function URL uses allow_credentials=true; a wildcard origin reflects any inbound Origin header with credentials allowed, enabling cross-site request forgery from any website."
  }
}

variable "reserved_concurrent_executions" {
  description = "Reserved concurrent executions (-1 for unreserved)"
  type        = number
  default     = -1
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 7
}

variable "enable_scheduled_tasks" {
  description = "Enable scheduled EventBridge tasks"
  type        = bool
  default     = true
}

variable "recommendation_schedule" {
  description = "EventBridge schedule expression for recommendations"
  type        = string
  default     = "rate(1 day)"
}

variable "enable_ri_exchange_schedule" {
  description = "Enable scheduled RI exchange automation"
  type        = bool
  default     = false
}

variable "ri_exchange_schedule" {
  description = "Schedule for RI exchange automation. rate() starts from deployment time; cron(0 0/6 * * ? *) runs at fixed clock hours (EventBridge cron syntax). For predictable approval windows, prefer cron."
  type        = string
  default     = "rate(6 hours)"
}

variable "enable_reap_stuck_purchases_schedule" {
  description = "Enable scheduled sweep of purchase_executions stuck in approved/running. Issue #678 backstop for synchronous-executor crashes."
  type        = bool
  default     = true
}

variable "reap_stuck_purchases_schedule" {
  description = "EventBridge schedule for the stuck-purchase reaper. Should be more frequent than PURCHASE_APPROVED_REAP_AFTER (default 10m) so a stuck row is reaped within ~1 threshold-window. Default rate(5 minutes) gives the 10m threshold ~2 sweeps of headroom."
  type        = string
  default     = "rate(5 minutes)"
}

variable "enable_analytics_collect_schedule" {
  description = "Enable the scheduled savings-snapshot analytics collection (issues #1023/#1033). When true, EventBridge periodically invokes the analytics_collect task (snapshot + partition maintenance + retention + view refresh)."
  type        = bool
  default     = true
}

variable "analytics_collect_schedule" {
  description = "EventBridge schedule for the analytics_collect task. The collector aggregates point-in-time savings snapshots, so a daily cadence captures a clean coverage/utilization time-series without excess write volume. rate() starts from deployment time; use cron() for fixed clock times."
  type        = string
  default     = "rate(1 day)"
}

variable "purchase_approved_reap_after" {
  description = "Threshold age for the stuck-purchase reaper. Any execution sitting in approved/running longer than this gets flipped to failed on the next sweep. Parsed via Go time.ParseDuration (e.g. \"10m\", \"15m\", \"1h\"). Empty string falls back to the in-code default (10m)."
  type        = string
  default     = ""
}

variable "additional_env_vars" {
  description = "Additional environment variables"
  type        = map(string)
  default     = {}
}

variable "credential_encryption_key_secret_arn" {
  description = "ARN of Secrets Manager secret holding the AES-256-GCM credential encryption key"
  type        = string
  default     = ""
}

variable "scheduled_task_secret_arn" {
  description = "ARN of Secrets Manager secret holding the bearer secret for /api/scheduled/* (required when SCHEDULED_TASK_AUTH_MODE=bearer; resolved at runtime via SecretResolver)"
  type        = string
  default     = ""
}

variable "scheduled_task_secret_name" {
  description = "Name of the scheduled-task bearer secret. Passed to compute as SCHEDULED_TASK_SECRET_NAME and resolved by internal/server.resolveScheduledTaskSecret on cold start."
  type        = string
  default     = ""
}

variable "enable_cross_account_sts" {
  description = "Allow Lambda to assume roles in remote AWS accounts (required for multi-account support)"
  type        = bool
  default     = false
}

variable "cross_account_role_name_prefix" {
  description = "Prefix constraint for cross-account role names the Lambda may assume. The IAM policy Resource is scoped to arn:aws:iam::*:role/{prefix}*. The supplied federation CloudFormation/Terraform templates create roles matching the default prefix; change only if your target-account roles use a different naming convention. Must end with '*' when you want trailing freedom; setting this to an empty string intentionally widens to all roles (not recommended)."
  type        = string
  default     = "CUDly"
}

variable "enable_org_discovery" {
  description = "Allow Lambda to call AWS Organizations ListAccounts for member account discovery"
  type        = bool
  default     = false
}

variable "email_from_domain" {
  description = "Verified SES domain used as the From: address. When set, the SES IAM policy is scoped to identity/{domain} + configuration-set/{stack_name}*, blocking sends from any other identity in the AWS account. Leave empty only when SES is not configured (notifications disabled)."
  type        = string
  default     = ""
}

variable "alarm_sns_topic_arn" {
  description = "Optional list of SNS topic ARNs to notify on the migration-failure alarm (alarm_actions / ok_actions). Leave empty ([]) to create the alarm without a notification target -- it still transitions to ALARM state and is visible/queryable in CloudWatch. Pass the monitoring module's SNS topic ARN here to wire notifications; this module intentionally does not create its own SNS topic."
  type        = list(string)
  default     = []
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
