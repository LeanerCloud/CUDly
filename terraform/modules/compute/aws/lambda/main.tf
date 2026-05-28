# AWS Lambda with Function URL Module
# Supports ARM64 architecture with ECR container images

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# ==============================================
# Data sources
# ==============================================

# Used to compose this Lambda's own ARN for the SCHEDULER_LAMBDA_ARN env var
# without creating a self-reference cycle on aws_lambda_function.main.
data "aws_partition" "current" {}
data "aws_caller_identity" "current" {}

# ==============================================
# Lambda Function
# ==============================================

resource "aws_lambda_function" "main" {
  function_name = "${var.stack_name}-api"
  role          = aws_iam_role.lambda.arn

  # Container image configuration
  package_type  = "Image"
  image_uri     = var.image_uri
  architectures = [var.architecture]

  # Resource configuration
  memory_size = var.memory_size
  timeout     = var.timeout

  # Environment variables
  environment {
    variables = merge(
      {
        ENVIRONMENT           = var.environment
        RUNTIME_MODE          = "lambda"
        DB_HOST               = var.database_host
        DB_PORT               = "5432"
        DB_NAME               = var.database_name
        DB_USER               = var.database_username
        DB_PASSWORD_SECRET    = var.database_password_secret_arn
        DB_SSL_MODE           = "require"
        DB_CONNECT_TIMEOUT    = "8s" # Short timeout per attempt (retries handle long waits)
        DB_AUTO_MIGRATE       = var.auto_migrate
        DB_MIGRATIONS_PATH    = "/app/migrations"
        ADMIN_EMAIL           = var.admin_email
        ADMIN_PASSWORD_SECRET = var.admin_password_secret_arn
        SECRET_PROVIDER       = "aws"
        AWS_REGION_CONFIG     = var.region
        CUDLY_SIGNING_KEY_ID  = aws_kms_key.signing.arn
        # CUDLY_ISSUER_URL is deliberately NOT wired here: referencing
        # aws_lambda_function_url.main[0].function_url would create a
        # cycle with aws_lambda_function.main. The server resolves the
        # issuer URL lazily from the first inbound request's
        # DomainName and caches it — see internal/oidc.IssuerCache.

        # Scheduled-task auth: AWS uses EventBridge -> direct Lambda
        # invocation (lambda:InvokeFunction, see
        # scheduled_recommendations / ri_exchange event_target rules
        # below), so no real scheduler hits /api/scheduled/*. The HTTP
        # path IS reachable on the public Lambda URL
        # (authorization_type=NONE) and would be an unauthenticated
        # public trigger without a gate. Bearer mode resolves the
        # value from Secrets Manager at startup
        # (resolveScheduledTaskSecret in internal/server/app.go); the
        # plaintext never lives in Lambda env or Terraform state.
        SCHEDULED_TASK_AUTH_MODE   = "bearer"
        SCHEDULED_TASK_SECRET_NAME = var.scheduled_task_secret_name
        # Self-ARN for the async refresh path (#257). Empty in environments
        # that haven't applied this Terraform yet — the handler degrades to
        # a synchronous collect when the var is absent. Using a derived
        # function_name + account_id ARN avoids the self-reference cycle
        # that aws_lambda_function.main.arn would create here.
        SCHEDULER_LAMBDA_ARN = "arn:${data.aws_partition.current.partition}:lambda:${var.region}:${data.aws_caller_identity.current.account_id}:function:${var.stack_name}-api"
      },
      # Stuck-purchase reaper threshold (#678). Only set when the
      # operator overrides the default — empty string means "use the
      # in-code DefaultReapAfter (10m)" so we don't pin a Lambda env
      # value when ops hasn't taken a position.
      var.purchase_approved_reap_after != "" ? {
        PURCHASE_APPROVED_REAP_AFTER = var.purchase_approved_reap_after
      } : {},
      var.additional_env_vars
    )
  }

  # VPC configuration (required for RDS Proxy access)
  dynamic "vpc_config" {
    for_each = var.vpc_config != null ? [var.vpc_config] : []
    content {
      subnet_ids                  = vpc_config.value.subnet_ids
      security_group_ids          = concat([aws_security_group.lambda[0].id], vpc_config.value.additional_security_group_ids)
      ipv6_allowed_for_dual_stack = true # Enable IPv6 for egress to AWS services
    }
  }

  # Reserved concurrency
  reserved_concurrent_executions = var.reserved_concurrent_executions

  tags = var.tags
}

# ==============================================
# Lambda Function URL (for HTTP access)
# ==============================================

# moved: aws_lambda_function_url.main lost its `count` in PR #574 (drop
# of dead-knob lambda_enable_function_url). The existing deployment's
# state holds the resource at index [0]; this block tells terraform
# to update the state index in-place instead of destroying the
# existing Function URL and creating a fresh one with a different
# host (which would break the dashboard until origins/DASHBOARD_URL
# are re-propagated everywhere).
moved {
  from = aws_lambda_function_url.main[0]
  to   = aws_lambda_function_url.main
}

resource "aws_lambda_function_url" "main" {
  function_name      = aws_lambda_function.main.function_name
  authorization_type = var.function_url_auth_type

  cors {
    allow_credentials = true
    allow_origins     = var.allowed_origins
    allow_methods     = ["GET", "POST", "PUT", "DELETE"]
    allow_headers     = ["Content-Type", "Authorization", "X-CSRF-Token", "X-Session-Token"]
    max_age           = 3600
  }
}

# Resource-based policy grant for the Function URL. Without this, every request
# is rejected at the Lambda edge with HTTP 403 AccessDeniedException, even when
# authorization_type = "NONE". The AWS provider used to create this implicitly
# but stopped doing so in recent versions -- it must be declared explicitly.
resource "aws_lambda_permission" "function_url" {
  count = var.function_url_auth_type == "NONE" ? 1 : 0

  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.main.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}

# ==============================================
# Security Group for Lambda (VPC mode)
# ==============================================

resource "aws_security_group" "lambda" {
  count = var.vpc_config != null ? 1 : 0

  name_prefix = "${var.stack_name}-lambda-"
  description = "Security group for Lambda function"
  vpc_id      = var.vpc_config.vpc_id

  # IPv4 egress
  egress {
    description = "Allow all outbound IPv4"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # IPv6 egress
  egress {
    description      = "Allow all outbound IPv6"
    from_port        = 0
    to_port          = 0
    protocol         = "-1"
    ipv6_cidr_blocks = ["::/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.stack_name}-lambda-sg"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# ==============================================
# IAM Role for Lambda
# ==============================================

resource "aws_iam_role" "lambda" {
  name_prefix = "${var.stack_name}-lambda-"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "lambda.amazonaws.com"
        }
      }
    ]
  })

  tags = var.tags
}

# Basic Lambda execution policy
resource "aws_iam_role_policy_attachment" "lambda_basic" {
  role       = aws_iam_role.lambda.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

# VPC execution policy (if VPC is enabled)
resource "aws_iam_role_policy_attachment" "lambda_vpc" {
  count = var.vpc_config != null ? 1 : 0

  role       = aws_iam_role.lambda.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

# Secrets Manager access
resource "aws_iam_role_policy" "secrets_access" {
  name_prefix = "${var.stack_name}-secrets-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue"
        ]
        Resource = compact([
          var.database_password_secret_arn,
          "${var.database_password_secret_arn}*",
          var.admin_password_secret_arn,
          var.admin_password_secret_arn != "" ? "${var.admin_password_secret_arn}*" : "",
          var.credential_encryption_key_secret_arn,
          var.credential_encryption_key_secret_arn != "" ? "${var.credential_encryption_key_secret_arn}*" : "",
          var.scheduled_task_secret_arn,
          var.scheduled_task_secret_arn != "" ? "${var.scheduled_task_secret_arn}*" : "",
        ])
      },
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:PutSecretValue"
        ]
        Resource = compact([
          var.admin_password_secret_arn,
          var.admin_password_secret_arn != "" ? "${var.admin_password_secret_arn}*" : "",
        ])
      }
    ]
  })
}

# SES email sending access. ses:CreateEmailIdentity is intentionally NOT in
# the action list; identity verification is a one-time operator task, and
# keeping it out of the runtime role blocks a compromised Lambda from
# registering arbitrary identities for phishing/spam.
#
# Resource = "*" on the send actions — the narrower identity/${domain}
# patterns we tried earlier don't cover the real case we hit in production:
# a From address on an unverified subdomain (e.g. noreply@cudly.leanercloud.com)
# where SES walks up the identity hierarchy and evaluates IAM against the
# verified parent's ARN (identity/leanercloud.com). Enumerating every
# ancestor the operator might verify would work on paper but drifts every
# time the SES identity tree is reorganised.
#
# To still narrow the blast radius (a compromised Lambda must not be able
# to spoof emails from arbitrary verified identities in the same account),
# the SendEmail/SendRawEmail actions are gated by a `ses:FromAddress`
# condition restricting the From header to *@${var.email_from_domain}.
# SES enforces FromAddress on the wire, so this prevents phishing from
# unrelated identities even though the resource ARN remains broad.
# Configuration-set access stays scoped to ${stack_name}* so a compromised
# Lambda can't touch unrelated stacks' config sets either.
#
# Only attached when var.email_from_domain is set — deployments without email
# notifications don't get any SES permissions at all.
resource "aws_iam_role_policy" "ses_access" {
  count = var.email_from_domain != "" ? 1 : 0

  name_prefix = "${var.stack_name}-ses-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SendFromCUDlyDomain"
        Effect = "Allow"
        Action = [
          "ses:SendEmail",
          "ses:SendRawEmail",
        ]
        Resource = "*"
        Condition = {
          StringLike = {
            "ses:FromAddress" = "*@${var.email_from_domain}"
          }
        }
      },
      {
        # Read-only SES status checks for healthchecks and quota
        # introspection. No spoofing risk — these don't send mail.
        Sid    = "SESReadOnly"
        Effect = "Allow"
        Action = [
          "ses:GetAccount",
          "ses:GetEmailIdentity",
        ]
        Resource = "*"
      },
      {
        Sid    = "StackScopedConfigurationSet"
        Effect = "Allow"
        Action = [
          "ses:UseConfigurationSet",
        ]
        Resource = "arn:aws:ses:*:*:configuration-set/${var.stack_name}*"
      }
    ]
  })
}

# RI Exchange and Savings Plans: all reservation services and Cost Explorer
resource "aws_iam_role_policy" "ri_exchange" {
  name_prefix = "${var.stack_name}-ri-exchange-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          # EC2 reserved instances
          "ec2:DescribeReservedInstances",
          "ec2:DescribeReservedInstancesOfferings",
          "ec2:GetReservedInstancesExchangeQuote",
          "ec2:AcceptReservedInstancesExchangeQuote",
          "ec2:PurchaseReservedInstancesOffering",
          "ec2:DescribeInstanceTypeOfferings",
          # RDS reserved instances
          "rds:DescribeReservedDBInstances",
          "rds:DescribeReservedDBInstancesOfferings",
          "rds:PurchaseReservedDBInstancesOffering",
          # ElastiCache reserved nodes
          "elasticache:DescribeReservedCacheNodes",
          "elasticache:DescribeReservedCacheNodesOfferings",
          "elasticache:PurchaseReservedCacheNodesOffering",
          # OpenSearch reserved instances
          "es:DescribeReservedElasticsearchInstances",
          "es:DescribeReservedElasticsearchInstanceOfferings",
          "es:PurchaseReservedElasticsearchInstanceOffering",
          # Redshift reserved nodes
          "redshift:DescribeReservedNodes",
          "redshift:DescribeReservedNodeOfferings",
          "redshift:PurchaseReservedNodeOffering",
          # MemoryDB reserved nodes
          "memorydb:DescribeReservedNodes",
          "memorydb:DescribeReservedNodesOfferings",
          "memorydb:PurchaseReservedNodesOffering",
          # Cost Explorer
          "ce:GetReservationUtilization",
          "ce:GetReservationPurchaseRecommendation",
          "ce:GetSavingsPlansPurchaseRecommendation",
          "ce:GetSavingsPlansUtilization",
          # Savings Plans
          "savingsplans:DescribeSavingsPlans",
          "savingsplans:DescribeSavingsPlanRates",
          "savingsplans:DescribeSavingsPlansOfferingRates",
          "savingsplans:DescribeSavingsPlansOfferings",
          "savingsplans:CreateSavingsPlan",
        ]
        Resource = "*"
      }
    ]
  })
}

# Cross-account role assumption for multi-account plan execution.
#
# Scoped by var.cross_account_role_name_prefix (default "CUDly") so the
# Lambda can only assume roles whose names start with that prefix. The
# shipped federation templates (iac/federation/aws-*) create roles matching
# this prefix. ExternalId validation also happens at the application layer
# (resolver.go); this IAM condition is defence-in-depth so a single app-
# layer bug cannot pivot into arbitrary roles without a non-empty ExternalId.
#
# The StringLike "*" condition requires that sts:ExternalId is present and
# non-empty in every AssumeRole call. Per-account ExternalId values are
# validated at the application layer; IAM here enforces that the field is
# present at all, closing the gap where an app-layer bug could omit it.
resource "aws_iam_role_policy" "cross_account_sts" {
  count = var.enable_cross_account_sts ? 1 : 0

  name_prefix = "${var.stack_name}-cross-account-sts-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["sts:AssumeRole"]
        Resource = "arn:aws:iam::*:role/${var.cross_account_role_name_prefix}*"
        Condition = {
          StringLike = {
            "sts:ExternalId" = "*"
          }
        }
      }
    ]
  })
}

# lambda:InvokeFunction on the API Lambda's own ARN — used by the
# async-refresh path in handler_recommendations_refresh.go to fire-and-forget
# self-invoke the scheduler-task code path with InvocationType=Event so the
# user-facing /api/recommendations/refresh request returns immediately
# instead of blocking on a 60s+ provider fan-out (closes #257).
#
# Scope: the policy intentionally constrains Resource to this Lambda's own
# ARN so the role cannot invoke arbitrary functions in the account.
resource "aws_iam_role_policy" "async_self_invoke" {
  name_prefix = "${var.stack_name}-async-self-invoke-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["lambda:InvokeFunction"]
        Resource = aws_lambda_function.main.arn
      }
    ]
  })
}

# AWS Organizations ListAccounts for org-root account discovery
resource "aws_iam_role_policy" "org_discovery" {
  count = var.enable_org_discovery ? 1 : 0

  name_prefix = "${var.stack_name}-org-discovery-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["organizations:ListAccounts", "organizations:DescribeOrganization"]
        Resource = "*" # Organizations API does not support resource-level restrictions
      }
    ]
  })
}

# ==============================================
# CloudWatch Log Group
# ==============================================

resource "aws_cloudwatch_log_group" "lambda" {
  name_prefix       = "/aws/lambda/${aws_lambda_function.main.function_name}-"
  retention_in_days = var.log_retention_days

  tags = var.tags
}

# ==============================================
# EventBridge Rule for Scheduled Tasks
# ==============================================

resource "aws_cloudwatch_event_rule" "scheduled_recommendations" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name                = "${var.stack_name}-recommendations"
  description         = "Trigger recommendations collection"
  schedule_expression = var.recommendation_schedule

  tags = var.tags
}

resource "aws_cloudwatch_event_target" "lambda" {
  count = var.enable_scheduled_tasks ? 1 : 0

  rule      = aws_cloudwatch_event_rule.scheduled_recommendations[0].name
  target_id = "lambda"
  arn       = aws_lambda_function.main.arn

  input = jsonencode({
    event = "scheduled_recommendations"
  })
}

resource "aws_lambda_permission" "eventbridge" {
  count = var.enable_scheduled_tasks ? 1 : 0

  statement_id  = "AllowExecutionFromEventBridge"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.main.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.scheduled_recommendations[0].arn
}

# ==============================================
# EventBridge Rule for RI Exchange Automation
# ==============================================

resource "aws_cloudwatch_event_rule" "ri_exchange" {
  count = var.enable_ri_exchange_schedule ? 1 : 0

  name                = "${var.stack_name}-ri-exchange"
  description         = "Trigger RI exchange automation"
  schedule_expression = var.ri_exchange_schedule

  tags = var.tags
}

resource "aws_cloudwatch_event_target" "ri_exchange" {
  count = var.enable_ri_exchange_schedule ? 1 : 0

  rule      = aws_cloudwatch_event_rule.ri_exchange[0].name
  target_id = "lambda"
  arn       = aws_lambda_function.main.arn

  input = jsonencode({
    action = "ri_exchange_reshape"
  })
}

resource "aws_lambda_permission" "eventbridge_ri_exchange" {
  count = var.enable_ri_exchange_schedule ? 1 : 0

  statement_id  = "AllowExecutionFromEventBridgeRIExchange"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.main.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.ri_exchange[0].arn
}

# ==============================================
# EventBridge Rule for Stuck-Purchase Reaper (#678)
# ==============================================
#
# Periodic sweep that flips purchase_executions stuck in approved/running
# (longer than PURCHASE_APPROVED_REAP_AFTER, default 10m) to "failed" via
# the existing TransitionExecutionStatus CAS. Backstop for synchronous-
# executor crashes (Lambda timeout, OOM, network hang) that orphan rows
# in an in-flight state with no automatic recovery.
#
# Schedule cadence must be more frequent than the reap-after threshold so
# a stuck row is reaped within ~1 threshold-window — default rate(5m)
# vs. 10m threshold gives ~2 sweeps of headroom. The reaper itself is
# CAS-protected so over-running is safe (real executor wins the race).

resource "aws_cloudwatch_event_rule" "reap_stuck_purchases" {
  count = var.enable_reap_stuck_purchases_schedule ? 1 : 0

  name                = "${var.stack_name}-reap-stuck-purchases"
  description         = "Trigger stuck-purchase reaper sweep (issue #678)"
  schedule_expression = var.reap_stuck_purchases_schedule

  tags = var.tags
}

resource "aws_cloudwatch_event_target" "reap_stuck_purchases" {
  count = var.enable_reap_stuck_purchases_schedule ? 1 : 0

  rule      = aws_cloudwatch_event_rule.reap_stuck_purchases[0].name
  target_id = "lambda"
  arn       = aws_lambda_function.main.arn

  input = jsonencode({
    action = "reap_stuck_purchases"
  })
}

resource "aws_lambda_permission" "eventbridge_reap_stuck_purchases" {
  count = var.enable_reap_stuck_purchases_schedule ? 1 : 0

  statement_id  = "AllowExecutionFromEventBridgeReapStuckPurchases"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.main.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.reap_stuck_purchases[0].arn
}
