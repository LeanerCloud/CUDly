# AWS RDS PostgreSQL Database Module
# Standalone instance with optional RDS Proxy for Lambda connection pooling

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
# Database Password Secret (only if not provided)
# ==============================================

resource "random_password" "db_password" {
  count = var.master_password_secret_arn == null ? 1 : 0

  length  = 32
  special = true
  # Exclude characters that may cause issues in connection strings
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

resource "aws_secretsmanager_secret" "db_password" {
  count = var.master_password_secret_arn == null ? 1 : 0

  name_prefix = "${var.stack_name}-db-password-"
  description = "PostgreSQL database password for ${var.stack_name}"

  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "db_password" {
  count = var.master_password_secret_arn == null ? 1 : 0

  secret_id = aws_secretsmanager_secret.db_password[0].id
  secret_string = jsonencode({
    username = var.master_username
    password = random_password.db_password[0].result
  })
}

# Data source to read existing secret if provided
data "aws_secretsmanager_secret" "existing_password" {
  count = var.master_password_secret_arn != null ? 1 : 0
  arn   = var.master_password_secret_arn
}

data "aws_secretsmanager_secret_version" "existing_password" {
  count     = var.master_password_secret_arn != null ? 1 : 0
  secret_id = data.aws_secretsmanager_secret.existing_password[0].id
}

# Local value for the actual secret ARN and password to use
locals {
  db_password_secret_arn = var.master_password_secret_arn != null ? var.master_password_secret_arn : aws_secretsmanager_secret.db_password[0].arn
  # Parse JSON to extract password from secret (both generated and existing secrets use JSON format)
  db_password = var.master_password_secret_arn != null ? jsondecode(data.aws_secretsmanager_secret_version.existing_password[0].secret_string)["password"] : jsondecode(aws_secretsmanager_secret_version.db_password[0].secret_string)["password"]
}

# ==============================================
# DB Subnet Group
# ==============================================

resource "aws_db_subnet_group" "main" {
  name       = "${var.stack_name}-db-subnet"
  subnet_ids = var.private_subnet_ids

  tags = merge(var.tags, {
    Name = "${var.stack_name}-db-subnet-group"
  })
}

# ==============================================
# Security Group for RDS Instance
# ==============================================

resource "aws_security_group" "aurora" {
  name_prefix = "${var.stack_name}-rds-"
  description = "Security group for RDS PostgreSQL instance"
  vpc_id      = var.vpc_id

  ingress {
    description = "PostgreSQL from VPC"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  egress {
    description = "Allow all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.stack_name}-rds-sg"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# ==============================================
# RDS PostgreSQL Instance
# ==============================================

resource "aws_db_instance" "main" {
  identifier = "${var.stack_name}-postgres"
  engine     = "postgres"

  engine_version = var.engine_version
  instance_class = var.instance_class

  db_name  = var.database_name
  username = var.master_username
  password = local.db_password

  allocated_storage     = var.allocated_storage
  max_allocated_storage = var.max_allocated_storage

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.aurora.id]

  # Backup configuration
  backup_retention_period = var.backup_retention_days
  backup_window           = "03:00-04:00"
  maintenance_window      = "sun:04:00-sun:05:00"

  # Encryption
  storage_encrypted = true
  kms_key_id        = var.kms_key_id

  # Deletion protection
  deletion_protection       = var.deletion_protection
  skip_final_snapshot       = var.skip_final_snapshot
  final_snapshot_identifier = var.skip_final_snapshot ? null : "${var.stack_name}-final-snapshot"

  # Monitoring
  performance_insights_enabled = var.performance_insights_enabled

  tags = merge(var.tags, {
    Name = "${var.stack_name}-postgres"
  })
}

# ==============================================
# RDS Proxy (for Lambda connection pooling)
# ==============================================

resource "aws_db_proxy" "main" {
  count = var.enable_rds_proxy ? 1 : 0

  name                   = "${var.stack_name}-proxy"
  engine_family          = "POSTGRESQL"
  require_tls            = true
  vpc_subnet_ids         = var.private_subnet_ids
  vpc_security_group_ids = [aws_security_group.rds_proxy[0].id]

  auth {
    auth_scheme = "SECRETS"
    iam_auth    = "DISABLED"
    secret_arn  = local.db_password_secret_arn
  }

  role_arn = aws_iam_role.rds_proxy[0].arn

  tags = merge(var.tags, {
    Name = "${var.stack_name}-rds-proxy"
  })
}

resource "aws_db_proxy_default_target_group" "main" {
  count = var.enable_rds_proxy ? 1 : 0

  db_proxy_name = aws_db_proxy.main[0].name

  connection_pool_config {
    max_connections_percent      = 100
    max_idle_connections_percent = 50
    connection_borrow_timeout    = 120
  }
}

resource "aws_db_proxy_target" "main" {
  count = var.enable_rds_proxy ? 1 : 0

  db_proxy_name          = aws_db_proxy.main[0].name
  target_group_name      = aws_db_proxy_default_target_group.main[0].name
  db_instance_identifier = aws_db_instance.main.identifier
}

# ==============================================
# Security Group for RDS Proxy
# ==============================================

resource "aws_security_group" "rds_proxy" {
  count = var.enable_rds_proxy ? 1 : 0

  name_prefix = "${var.stack_name}-rds-proxy-"
  description = "Security group for RDS Proxy"
  vpc_id      = var.vpc_id

  ingress {
    description = "PostgreSQL from VPC (IPv4)"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  # Allow egress to RDS instance
  egress {
    description     = "To RDS instance"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.aurora.id]
  }

  # Allow general egress for health checks and internal communication
  egress {
    description = "Allow all outbound (IPv4)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.stack_name}-rds-proxy-sg"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# ==============================================
# IAM Role for RDS Proxy
# ==============================================

resource "aws_iam_role" "rds_proxy" {
  count = var.enable_rds_proxy ? 1 : 0

  name_prefix = "${var.stack_name}-rds-proxy-"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "rds.amazonaws.com"
        }
      }
    ]
  })

  tags = var.tags
}

resource "aws_iam_role_policy" "rds_proxy" {
  count = var.enable_rds_proxy ? 1 : 0

  name_prefix = "${var.stack_name}-rds-proxy-"
  role        = aws_iam_role.rds_proxy[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue"
        ]
        Resource = [
          local.db_password_secret_arn
        ]
      }
    ]
  })
}

# ==============================================
# Admin User Setup
# ==============================================
# Admin user is created during migrations with no password set.
# The admin must use the password reset feature to set their initial password.
