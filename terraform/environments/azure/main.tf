# Azure Development Environment
# Orchestrates all Azure modules for CUDly deployment

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
    azuread = {
      source  = "hashicorp/azuread"
      version = "~> 2.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
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
    null = {
      source  = "hashicorp/null"
      version = "~> 3.0"
    }
  }

  # Backend configuration for state management (commented out for validation)
  # backend "azurerm" {
  #   # Configure with:
  #   # terraform init \
  #   #   -backend-config="resource_group_name=cudly-terraform-state" \
  #   #   -backend-config="storage_account_name=cudlytfstate" \
  #   #   -backend-config="container_name=tfstate" \
  #   #   -backend-config="key=dev/terraform.tfstate"
  # }
}

provider "azurerm" {
  features {
    key_vault {
      purge_soft_delete_on_destroy    = false
      recover_soft_deleted_key_vaults = true
    }
    resource_group {
      prevent_deletion_if_contains_resources = false
    }
  }

  subscription_id = var.subscription_id
}

# Kubernetes and Helm providers for AKS
# These are configured after the cluster is created
provider "kubernetes" {
  host                   = try(module.compute_aks[0].kube_config.host, "https://localhost")
  client_certificate     = try(base64decode(module.compute_aks[0].kube_config.client_certificate), "")
  client_key             = try(base64decode(module.compute_aks[0].kube_config.client_key), "")
  cluster_ca_certificate = try(base64decode(module.compute_aks[0].kube_config.cluster_ca_certificate), "")
}

provider "helm" {
  kubernetes {
    host                   = try(module.compute_aks[0].kube_config.host, "https://localhost")
    client_certificate     = try(base64decode(module.compute_aks[0].kube_config.client_certificate), "")
    client_key             = try(base64decode(module.compute_aks[0].kube_config.client_key), "")
    cluster_ca_certificate = try(base64decode(module.compute_aks[0].kube_config.cluster_ca_certificate), "")
  }
}

# ==============================================
# Local Variables
# ==============================================

locals {
  app_name = "${var.app_name}-${var.environment}"

  common_tags = merge(var.tags, {
    Environment = var.environment
    ManagedBy   = "terraform"
    Project     = "CUDly"
    CostCenter  = var.cost_center
  })

  # Dashboard URL for password reset emails
  # Use custom domain if configured, otherwise will be set after deployment
  dashboard_url = length(var.frontend_domain_names) > 0 ? "https://${var.frontend_domain_names[0]}" : ""

  # Container image parsing (simplifies module calls)
  full_image_uri = var.enable_docker_build ? module.build[0].image_uri : var.image_uri
  image_name     = split(":", local.full_image_uri)[0]
  image_tag      = try(split(":", local.full_image_uri)[1], "latest")
}

# ==============================================
# Resource Group
# ==============================================

# ==============================================
# Provider Registrations
# ==============================================

resource "azurerm_resource_provider_registration" "container_apps" {
  name = "Microsoft.App"
}

# ==============================================
# Resource Group
# ==============================================

resource "azurerm_resource_group" "main" {
  name     = "${local.app_name}-rg"
  location = var.location

  tags = local.common_tags
}
