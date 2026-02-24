# CUDly AWS Development Environment
# Terraform configuration for dev deployment with Lambda compute platform

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

  # Backend configuration for state management
  # Uncomment and configure after running scripts/init-backend.sh
  # backend "s3" {
  #   bucket         = "cudly-terraform-state-dev"
  #   key            = "dev/terraform.tfstate"
  #   region         = "us-east-1"
  #   encrypt        = true
  #   dynamodb_table = "cudly-terraform-locks-dev"
  # }
}

provider "aws" {
  region  = var.region
  profile = var.aws_profile

  default_tags {
    tags = {
      Project     = "CUDly"
      Environment = var.environment
      ManagedBy   = "Terraform"
      Stack       = var.stack_name
    }
  }
}

# ==============================================
# Local Variables
# ==============================================

locals {
  stack_name    = "${var.project_name}-${var.environment}"
  dashboard_url = length(var.frontend_domain_names) > 0 ? "https://${var.frontend_domain_names[0]}" : ""

  common_tags = {
    Project     = var.project_name
    Environment = var.environment
    ManagedBy   = "Terraform"
  }
}

# Get current AWS account ID
data "aws_caller_identity" "current" {}
