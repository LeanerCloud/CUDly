# Terraform and Provider Configuration for GKE Module
# Note: Provider blocks have been removed to allow this module to be used with count/for_each
# Providers (google, kubernetes, helm) should be configured at the root module level

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.23"
      # Provider will be configured at root level after cluster creation
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.11"
      # Provider will be configured at root level after cluster creation
    }
  }
}

# Note: Provider configurations removed from module
# To use kubernetes/helm providers with this cluster, configure them at the root level:
#
# data "google_client_config" "default" {}
#
# provider "kubernetes" {
#   host  = "https://${module.compute_gke[0].cluster_endpoint}"
#   token = data.google_client_config.default.access_token
#   cluster_ca_certificate = base64decode(
#     module.compute_gke[0].cluster_ca_certificate
#   )
# }
#
# provider "helm" {
#   kubernetes {
#     host  = "https://${module.compute_gke[0].cluster_endpoint}"
#     token = data.google_client_config.default.access_token
#     cluster_ca_certificate = base64decode(
#       module.compute_gke[0].cluster_ca_certificate
#     )
#   }
# }
