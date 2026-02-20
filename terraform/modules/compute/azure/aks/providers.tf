# Terraform and Provider Configuration for AKS Module
# Note: Provider blocks have been removed to allow this module to be used with count/for_each
# Providers (azurerm, kubernetes, helm) should be configured at the root module level

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
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
# provider "kubernetes" {
#   host                   = module.compute_aks[0].kube_config.host
#   client_certificate     = base64decode(module.compute_aks[0].kube_config.client_certificate)
#   client_key             = base64decode(module.compute_aks[0].kube_config.client_key)
#   cluster_ca_certificate = base64decode(module.compute_aks[0].kube_config.cluster_ca_certificate)
# }
#
# provider "helm" {
#   kubernetes {
#     host                   = module.compute_aks[0].kube_config.host
#     client_certificate     = base64decode(module.compute_aks[0].kube_config.client_certificate)
#     client_key             = base64decode(module.compute_aks[0].kube_config.client_key)
#     cluster_ca_certificate = base64decode(module.compute_aks[0].kube_config.cluster_ca_certificate)
#   }
# }
