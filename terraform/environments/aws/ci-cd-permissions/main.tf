terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      # This bootstrap root is already applied with the 6.x provider line
      # (see .terraform.lock.hcl); pinned to ~> 6.0 rather than the
      # workload environments' ~> 5.0 to match the committed lock file.
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }

  backend "s3" {
    bucket       = "cudly-terraform-state-dev"
    key          = "iam/terraform.tfstate"
    region       = "us-east-1"
    encrypt      = true
    use_lockfile = true
  }
}

provider "aws" {
  region = var.aws_region
}
