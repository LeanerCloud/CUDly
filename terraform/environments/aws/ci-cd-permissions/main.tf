terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }

  backend "s3" {
    bucket       = "cudly-terraform-state-dev"
    key          = "iam/terraform.tfstate"
    region       = "us-east-1"
    encrypt      = true
    use_lockfile = true
    profile      = "personal"
  }
}

provider "aws" {
  region  = "us-east-1"
  profile = "personal"
}
