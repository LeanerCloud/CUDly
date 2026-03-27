# CUDly GCP Development Environment
# Terraform configuration for dev deployment with Cloud Run

terraform {
  required_version = ">= 1.10.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "~> 5.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.23"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.11"
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

}

provider "google" {
  project = var.project_id
  region  = var.region

  default_labels = {
    project     = "cudly"
    environment = var.environment
    managed_by  = "terraform"
  }
}

provider "google-beta" {
  project = var.project_id
  region  = var.region

  default_labels = {
    project     = "cudly"
    environment = var.environment
    managed_by  = "terraform"
  }
}

# Kubernetes and Helm providers for GKE
# These are configured after the cluster is created
data "google_client_config" "default" {}

provider "kubernetes" {
  host  = try("https://${module.compute_gke[0].cluster_endpoint}", "https://localhost")
  token = try(data.google_client_config.default.access_token, "")
  cluster_ca_certificate = try(
    base64decode(module.compute_gke[0].cluster_ca_certificate),
    ""
  )
}

provider "helm" {
  kubernetes {
    host  = try("https://${module.compute_gke[0].cluster_endpoint}", "https://localhost")
    token = try(data.google_client_config.default.access_token, "")
    cluster_ca_certificate = try(
      base64decode(module.compute_gke[0].cluster_ca_certificate),
      ""
    )
  }
}

# ==============================================
# Local Variables
# ==============================================

locals {
  service_name = "${var.project_name}-${var.environment}"

  common_labels = {
    project     = var.project_name
    environment = var.environment
    managed_by  = "terraform"
  }

  # Dashboard URL for password reset emails
  # Use custom domain if configured, otherwise will be set after deployment
  dashboard_url = length(var.frontend_domain_names) > 0 ? "https://${var.frontend_domain_names[0]}" : ""

  # Container image parsing (simplifies module calls)
  full_image_uri = var.enable_docker_build ? module.build[0].image_uri : var.image_uri
  image_name     = split(":", local.full_image_uri)[0]
  image_tag      = try(split(":", local.full_image_uri)[1], "latest")
}
