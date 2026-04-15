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
      },
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

resource "aws_lambda_function_url" "main" {
  count = var.enable_function_url ? 1 : 0

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
# but stopped doing so in recent versions — it must be declared explicitly.
resource "aws_lambda_permission" "function_url" {
  count = var.enable_function_url && var.function_url_auth_type == "NONE" ? 1 : 0

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

# SES email sending access
resource "aws_iam_role_policy" "ses_access" {
  name_prefix = "${var.stack_name}-ses-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ses:SendEmail",
          "ses:SendRawEmail",
          "ses:GetAccount",
          "ses:GetEmailIdentity",
          "ses:CreateEmailIdentity"
        ]
        Resource = "*" # Allow sending from any verified identity and creating identities
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

# Cross-account role assumption for multi-account plan execution
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
        Resource = "arn:aws:iam::*:role/*"
        # Recommendation: scope to a naming convention in production to reduce blast radius:
        # Resource = "arn:aws:iam::*:role/CUDly*"
        # Note: ExternalId validation is enforced at the application layer (resolver.go),
        # not here, because Lambda assumes roles on behalf of many target accounts.
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
