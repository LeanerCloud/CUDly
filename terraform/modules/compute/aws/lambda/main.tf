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
        ENVIRONMENT        = var.environment
        RUNTIME_MODE       = "lambda"
        DB_HOST            = var.database_host
        DB_PORT            = "5432"
        DB_NAME            = var.database_name
        DB_USER            = var.database_username
        DB_PASSWORD_SECRET = var.database_password_secret_arn
        DB_SSL_MODE        = "require"
        DB_CONNECT_TIMEOUT = "8s" # Short timeout per attempt (retries handle long waits)
        DB_AUTO_MIGRATE    = var.auto_migrate
        DB_MIGRATIONS_PATH = "/app/migrations"
        ADMIN_EMAIL        = var.admin_email
        SECRET_PROVIDER    = "aws"
        AWS_REGION_CONFIG  = var.region
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
    allow_methods     = ["*"]
    allow_headers     = ["*"]
    max_age           = 86400
  }
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
        Resource = [
          var.database_password_secret_arn,
          "${var.database_password_secret_arn}*"
        ]
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

# ==============================================
# CloudWatch Log Group
# ==============================================

resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/${aws_lambda_function.main.function_name}"
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
