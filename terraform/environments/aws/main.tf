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
      version = "~> 3.3"
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

  # FROM_EMAIL resolution order:
  #   1. Explicit var.from_email (use when subdomain_zone_name is unset
  #      but a different SES-verified identity exists in the account).
  #   2. noreply@<subdomain_zone_name> (the default for deployments with
  #      a custom domain and matching DKIM records in ses.tf).
  #   3. Empty string — handed to the Lambda env, which the app's
  #      Sender validates and maps to ErrNoFromEmail so the UI surfaces
  #      "FROM_EMAIL not configured" instead of producing SES 400s from
  #      a malformed "noreply@" (trailing empty domain).
  effective_from_email = (
    var.from_email != "" ? var.from_email :
    var.subdomain_zone_name != "" ? "noreply@${var.subdomain_zone_name}" :
    ""
  )
  # Derive the domain half of effective_from_email for SES IAM scoping.
  # An empty string disables the SES policy entirely in the compute
  # module (its count gate on email_from_domain is the kill switch for
  # "no SES send permissions" deployments).
  effective_email_from_domain = (
    local.effective_from_email == "" ? "" :
    split("@", local.effective_from_email)[1]
  )

  common_tags = {
    Project     = var.project_name
    Environment = var.environment
    ManagedBy   = "Terraform"
  }
}

# Get current AWS account ID
data "aws_caller_identity" "current" {}
