terraform {
  required_version = ">= 1.6.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }

  backend "gcs" {
    bucket = "cudly-terraform-state-cloudprowess"
    prefix = "ci-cd-permissions"
  }
}

provider "google" {
  project = var.project_id
}
