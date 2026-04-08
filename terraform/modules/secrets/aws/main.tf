# AWS Secrets Manager Module
# Manages application secrets with optional rotation

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

# ==============================================
# Database Password Secret
# ==============================================

# Generate random password if not provided
resource "random_password" "database" {
  count = var.database_password == null ? 1 : 0

  length  = 32
  special = true
  # Exclude characters that might cause issues in connection strings
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

# Create secret
resource "aws_secretsmanager_secret" "database_password" {
  name_prefix             = "${var.stack_name}-db-password-"
  description             = "Database master password for ${var.stack_name}"
  recovery_window_in_days = var.recovery_window_days

  tags = merge(var.tags, {
    Name        = "${var.stack_name}-db-password"
    ManagedBy   = "terraform"
    Environment = var.environment
  })
}

# Store password value in JSON format (required by RDS Proxy)
resource "aws_secretsmanager_secret_version" "database_password" {
  secret_id = aws_secretsmanager_secret.database_password.id
  secret_string = jsonencode({
    username = var.database_username
    password = var.database_password != null ? var.database_password : random_password.database[0].result
  })
}

# ==============================================
# Admin Password Secret
# ==============================================

resource "random_password" "admin_password" {
  count = var.create_admin_password_secret && (var.admin_password == null || var.admin_password == "") ? 1 : 0

  length           = 32
  special          = true
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

resource "aws_secretsmanager_secret" "admin_password" {
  count = var.create_admin_password_secret ? 1 : 0

  name_prefix             = "${var.stack_name}-admin-password-"
  description             = "Admin user password for ${var.stack_name}"
  recovery_window_in_days = var.recovery_window_days

  tags = merge(var.tags, {
    Name        = "${var.stack_name}-admin-password"
    ManagedBy   = "terraform"
    Environment = var.environment
  })
}

resource "aws_secretsmanager_secret_version" "admin_password" {
  count = var.create_admin_password_secret ? 1 : 0

  secret_id     = aws_secretsmanager_secret.admin_password[0].id
  secret_string = var.admin_password != null && var.admin_password != "" ? var.admin_password : random_password.admin_password[0].result
}

# ==============================================
# Application Secrets (API keys, tokens, etc.)
# ==============================================

# JWT signing key
resource "random_password" "jwt_secret" {
  count = var.create_jwt_secret ? 1 : 0

  length  = 64
  special = false # Base64-friendly
}

resource "aws_secretsmanager_secret" "jwt_secret" {
  count = var.create_jwt_secret ? 1 : 0

  name_prefix             = "${var.stack_name}-jwt-secret-"
  description             = "JWT signing secret for ${var.stack_name}"
  recovery_window_in_days = var.recovery_window_days

  tags = merge(var.tags, {
    Name        = "${var.stack_name}-jwt-secret"
    ManagedBy   = "terraform"
    Environment = var.environment
  })
}

resource "aws_secretsmanager_secret_version" "jwt_secret" {
  count = var.create_jwt_secret ? 1 : 0

  secret_id     = aws_secretsmanager_secret.jwt_secret[0].id
  secret_string = random_password.jwt_secret[0].result
}

# Session encryption key
resource "random_password" "session_secret" {
  count = var.create_session_secret ? 1 : 0

  length  = 64
  special = false # Base64-friendly
}

resource "aws_secretsmanager_secret" "session_secret" {
  count = var.create_session_secret ? 1 : 0

  name_prefix             = "${var.stack_name}-session-secret-"
  description             = "Session encryption secret for ${var.stack_name}"
  recovery_window_in_days = var.recovery_window_days

  tags = merge(var.tags, {
    Name        = "${var.stack_name}-session-secret"
    ManagedBy   = "terraform"
    Environment = var.environment
  })
}

resource "aws_secretsmanager_secret_version" "session_secret" {
  count = var.create_session_secret ? 1 : 0

  secret_id     = aws_secretsmanager_secret.session_secret[0].id
  secret_string = random_password.session_secret[0].result
}

# ==============================================
# Credential Encryption Key (Multi-Account)
# ==============================================

resource "aws_secretsmanager_secret" "credential_encryption_key" {
  count = var.create_credential_encryption_key ? 1 : 0

  name_prefix             = "${var.stack_name}-credential-enc-key-"
  description             = "AES-256-GCM key for encrypting cloud account credentials"
  recovery_window_in_days = var.recovery_window_days

  tags = merge(var.tags, {
    Name        = "${var.stack_name}-credential-enc-key"
    ManagedBy   = "terraform"
    Environment = var.environment
  })
}

resource "aws_secretsmanager_secret_version" "credential_encryption_key" {
  count = var.create_credential_encryption_key ? 1 : 0

  secret_id     = aws_secretsmanager_secret.credential_encryption_key[0].id
  secret_string = var.credential_encryption_key

  lifecycle {
    ignore_changes = [secret_string] # Allow out-of-band rotation without Terraform drift
  }
}

# ==============================================
# Additional Custom Secrets
# ==============================================

# For storing additional secrets (e.g., API keys for external services)
resource "aws_secretsmanager_secret" "additional" {
  for_each = var.additional_secrets

  name_prefix             = "${var.stack_name}-${each.key}-"
  description             = each.value.description
  recovery_window_in_days = var.recovery_window_days

  tags = merge(var.tags, {
    Name        = "${var.stack_name}-${each.key}"
    ManagedBy   = "terraform"
    Environment = var.environment
  })
}

resource "aws_secretsmanager_secret_version" "additional" {
  for_each = var.additional_secrets

  secret_id     = aws_secretsmanager_secret.additional[each.key].id
  secret_string = each.value.value
}

# ==============================================
# Secret Rotation (Optional)
# ==============================================

# Lambda function for secret rotation
resource "aws_lambda_function" "rotation" {
  count = var.enable_secret_rotation ? 1 : 0

  function_name = "${var.stack_name}-secret-rotation"
  role          = aws_iam_role.rotation[0].arn
  handler       = "index.handler"
  runtime       = "python3.11"
  timeout       = 60

  # Use AWS-provided rotation function
  # In production, you'd upload a custom rotation function
  filename         = "${path.module}/rotation_function.zip"
  source_code_hash = fileexists("${path.module}/rotation_function.zip") ? filebase64sha256("${path.module}/rotation_function.zip") : null

  environment {
    variables = {
      SECRETS_MANAGER_ENDPOINT = "https://secretsmanager.${var.region}.amazonaws.com"
    }
  }

  dynamic "vpc_config" {
    for_each = var.rotation_lambda_vpc_config != null ? [var.rotation_lambda_vpc_config] : []
    content {
      subnet_ids         = vpc_config.value.subnet_ids
      security_group_ids = vpc_config.value.security_group_ids
    }
  }

  tags = var.tags
}

# IAM role for rotation Lambda
resource "aws_iam_role" "rotation" {
  count = var.enable_secret_rotation ? 1 : 0

  name_prefix = "${var.stack_name}-rotation-"

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
resource "aws_iam_role_policy_attachment" "rotation_basic" {
  count = var.enable_secret_rotation ? 1 : 0

  role       = aws_iam_role.rotation[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

# VPC execution policy (if rotation Lambda is in VPC)
resource "aws_iam_role_policy_attachment" "rotation_vpc" {
  count = var.enable_secret_rotation && var.rotation_lambda_vpc_config != null ? 1 : 0

  role       = aws_iam_role.rotation[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

# Secrets Manager permissions for rotation
resource "aws_iam_role_policy" "rotation_secrets" {
  count = var.enable_secret_rotation ? 1 : 0

  name_prefix = "${var.stack_name}-rotation-secrets-"
  role        = aws_iam_role.rotation[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:DescribeSecret",
          "secretsmanager:GetSecretValue",
          "secretsmanager:PutSecretValue",
          "secretsmanager:UpdateSecretVersionStage"
        ]
        Resource = [
          aws_secretsmanager_secret.database_password.arn
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetRandomPassword"
        ]
        Resource = "*"
      }
    ]
  })
}

# RDS permissions for rotation (to update password)
resource "aws_iam_role_policy" "rotation_rds" {
  count = var.enable_secret_rotation && var.rds_cluster_id != null ? 1 : 0

  name_prefix = "${var.stack_name}-rotation-rds-"
  role        = aws_iam_role.rotation[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "rds:ModifyDBInstance",
          "rds:DescribeDBInstances"
        ]
        Resource = "arn:aws:rds:${var.region}:*:db:*"
      }
    ]
  })
}

# Lambda permission for Secrets Manager to invoke
resource "aws_lambda_permission" "rotation" {
  count = var.enable_secret_rotation ? 1 : 0

  statement_id  = "AllowExecutionFromSecretsManager"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.rotation[0].function_name
  principal     = "secretsmanager.amazonaws.com"
}

# Configure rotation
resource "aws_secretsmanager_secret_rotation" "database_password" {
  count = var.enable_secret_rotation ? 1 : 0

  secret_id           = aws_secretsmanager_secret.database_password.id
  rotation_lambda_arn = aws_lambda_function.rotation[0].arn

  rotation_rules {
    automatically_after_days = var.rotation_days
  }
}

# ==============================================
# IAM Policy for Secret Access
# ==============================================

# Policy document for applications to read secrets
data "aws_iam_policy_document" "secret_read" {
  statement {
    sid    = "ReadSecrets"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret"
    ]
    resources = concat(
      [aws_secretsmanager_secret.database_password.arn],
      var.create_admin_password_secret ? [aws_secretsmanager_secret.admin_password[0].arn] : [],
      var.create_jwt_secret ? [aws_secretsmanager_secret.jwt_secret[0].arn] : [],
      var.create_session_secret ? [aws_secretsmanager_secret.session_secret[0].arn] : [],
      [for secret in aws_secretsmanager_secret.additional : secret.arn]
    )
  }
}

# IAM policy resource for attaching to roles
resource "aws_iam_policy" "secret_read" {
  name_prefix = "${var.stack_name}-secret-read-"
  description = "Allow reading secrets for ${var.stack_name}"
  policy      = data.aws_iam_policy_document.secret_read.json

  tags = var.tags
}
