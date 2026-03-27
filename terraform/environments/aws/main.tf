# CUDly AWS Development Environment
# Terraform configuration for dev deployment with Lambda compute platform

terraform {
  required_version = ">= 1.10.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
    http = {
      source  = "hashicorp/http"
      version = "~> 3.4"
    }
    external = {
      source  = "hashicorp/external"
      version = "~> 2.0"
    }
  }

  # Backend configuration for state management
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

# Random suffix to ensure resource names are unique per Terraform state.
# This prevents naming conflicts when Lambda and Fargate states both manage
# the same AWS environment (e.g., both trying to create an ECR repo named "cudly").
resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  stack_name = "${var.project_name}-${var.environment}-${random_id.suffix.hex}"
  # Dashboard URL for CORS and email links
  # Priority: custom domain > CDN domain > compute default endpoint
  dashboard_url = length(var.frontend_domain_names) > 0 ? "https://${var.frontend_domain_names[0]}" : ""

  common_tags = {
    Project     = var.project_name
    Environment = var.environment
    ManagedBy   = "Terraform"
  }
}

# Get current AWS account ID
data "aws_caller_identity" "current" {}
