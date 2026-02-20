# Azure AKS Compute Module
# Azure Kubernetes Service with managed node pools

locals {
  name_prefix = "${var.project_name}-${var.environment}-aks"

  common_tags = merge(
    var.tags,
    {
      Module      = "compute/azure/aks"
      Environment = var.environment
    }
  )
}

# ==============================================
# Log Analytics Workspace (for Container Insights)
# ==============================================

resource "azurerm_log_analytics_workspace" "aks" {
  count = var.enable_log_analytics ? 1 : 0

  name                = "${local.name_prefix}-logs"
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = "PerGB2018"
  retention_in_days   = 30

  tags = local.common_tags
}

# ==============================================
# AKS Cluster
# ==============================================

resource "azurerm_kubernetes_cluster" "main" {
  name                = local.name_prefix
  location            = var.location
  resource_group_name = var.resource_group_name
  dns_prefix          = local.name_prefix
  kubernetes_version  = var.kubernetes_version

  # Default node pool
  default_node_pool {
    name                = "default"
    node_count          = var.node_count
    vm_size             = var.node_vm_size
    vnet_subnet_id      = var.vnet_subnet_id
    enable_auto_scaling = var.enable_auto_scaling
    min_count           = var.enable_auto_scaling ? var.min_node_count : null
    max_count           = var.enable_auto_scaling ? var.max_node_count : null
    os_disk_size_gb     = 30
    type                = "VirtualMachineScaleSets"

    upgrade_settings {
      max_surge = "10%"
    }

    tags = local.common_tags
  }

  # Managed identity
  identity {
    type = "SystemAssigned"
  }

  # Network profile
  network_profile {
    network_plugin    = "azure"
    network_policy    = "azure"
    load_balancer_sku = "standard"
    service_cidr      = "10.0.0.0/16"
    dns_service_ip    = "10.0.0.10"
  }

  # Add-ons
  dynamic "oms_agent" {
    for_each = var.enable_log_analytics ? [1] : []
    content {
      log_analytics_workspace_id = azurerm_log_analytics_workspace.aks[0].id
    }
  }

  azure_policy_enabled = var.enable_azure_policy

  # RBAC and Azure AD integration
  role_based_access_control_enabled = true

  tags = local.common_tags
}

# ==============================================
# User Assigned Identity for Workload
# ==============================================

resource "azurerm_user_assigned_identity" "workload" {
  name                = "${local.name_prefix}-workload-identity"
  location            = var.location
  resource_group_name = var.resource_group_name

  tags = local.common_tags
}

# Grant workload identity access to Key Vault secrets
resource "azurerm_key_vault_access_policy" "workload" {
  key_vault_id = var.key_vault_id
  tenant_id    = azurerm_user_assigned_identity.workload.tenant_id
  object_id    = azurerm_user_assigned_identity.workload.principal_id

  secret_permissions = [
    "Get",
    "List"
  ]
}

# Grant AKS cluster identity access to pull images from ACR
resource "azurerm_role_assignment" "aks_acr" {
  principal_id                     = azurerm_kubernetes_cluster.main.kubelet_identity[0].object_id
  role_definition_name             = "AcrPull"
  scope                            = "/subscriptions/${data.azurerm_client_config.current.subscription_id}/resourceGroups/${var.resource_group_name}"
  skip_service_principal_aad_check = true
}

data "azurerm_client_config" "current" {}

# ==============================================
# Kubernetes Resources (CONDITIONAL)
# ==============================================
#
# NOTE: These resources are conditionally created based on var.deploy_kubernetes_resources
# To use these resources:
# 1. Set deploy_kubernetes_resources = true
# 2. Configure kubernetes/helm providers at the root level (see providers.tf)
# 3. The providers will use the cluster's kube_config output
#
# Default: false (only creates the AKS cluster infrastructure)
# ==============================================

# ==============================================
# Kubernetes Namespace
# ==============================================

resource "kubernetes_namespace" "app" {

  metadata {
    name = var.environment

    labels = {
      environment = var.environment
      project     = var.project_name
    }
  }

  depends_on = [azurerm_kubernetes_cluster.main]
}

# ==============================================
# Kubernetes Secret for Database
# ==============================================

# Note: In production, use Azure Key Vault CSI driver instead
# This is a simplified approach for getting started

resource "kubernetes_secret" "database" {

  metadata {
    name      = "database-credentials"
    namespace = kubernetes_namespace.app.metadata[0].name
  }

  data = {
    host     = var.database_host
    name     = var.database_name
    username = var.database_username
    # Password should be injected via Key Vault CSI driver in production
  }

  type = "Opaque"

  depends_on = [kubernetes_namespace.app]
}

# ==============================================
# Kubernetes Service Account
# ==============================================

resource "kubernetes_service_account" "app" {

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app.metadata[0].name

    annotations = {
      "azure.workload.identity/client-id" = azurerm_user_assigned_identity.workload.client_id
    }
  }

  depends_on = [kubernetes_namespace.app]
}

# ==============================================
# Kubernetes Deployment
# ==============================================

resource "kubernetes_deployment" "app" {

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app.metadata[0].name

    labels = {
      app         = "${var.project_name}-api"
      environment = var.environment
    }
  }

  spec {
    replicas = 2

    selector {
      match_labels = {
        app = "${var.project_name}-api"
      }
    }

    template {
      metadata {
        labels = {
          app         = "${var.project_name}-api"
          environment = var.environment
        }
      }

      spec {
        container {
          name  = "api"
          image = "${var.image_name}:${var.image_tag}"

          port {
            container_port = 8080
            protocol       = "TCP"
          }

          env {
            name  = "PORT"
            value = "8080"
          }

          env {
            name  = "ENVIRONMENT"
            value = var.environment
          }

          env {
            name = "DATABASE_HOST"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.database.metadata[0].name
                key  = "host"
              }
            }
          }

          env {
            name = "DATABASE_NAME"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.database.metadata[0].name
                key  = "name"
              }
            }
          }

          env {
            name = "DATABASE_USER"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.database.metadata[0].name
                key  = "username"
              }
            }
          }

          resources {
            requests = {
              cpu    = "250m"
              memory = "512Mi"
            }
            limits = {
              cpu    = "1000m"
              memory = "1Gi"
            }
          }

          liveness_probe {
            http_get {
              path = "/health"
              port = 8080
            }
            initial_delay_seconds = 30
            period_seconds        = 10
            timeout_seconds       = 5
            failure_threshold     = 3
          }

          readiness_probe {
            http_get {
              path = "/health"
              port = 8080
            }
            initial_delay_seconds = 10
            period_seconds        = 5
            timeout_seconds       = 3
            failure_threshold     = 3
          }
        }

        service_account_name = kubernetes_service_account.app.metadata[0].name
      }
    }

    strategy {
      type = "RollingUpdate"
      rolling_update {
        max_surge       = "25%"
        max_unavailable = "25%"
      }
    }
  }

  depends_on = [
    kubernetes_namespace.app,
    kubernetes_secret.database
  ]
}

# ==============================================
# Kubernetes Service (ClusterIP)
# ==============================================

resource "kubernetes_service" "app" {

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app.metadata[0].name

    labels = {
      app = "${var.project_name}-api"
    }
  }

  spec {
    selector = {
      app = "${var.project_name}-api"
    }

    port {
      name        = "http"
      port        = 80
      target_port = 8080
      protocol    = "TCP"
    }

    type = "ClusterIP"
  }

  depends_on = [kubernetes_namespace.app]
}

# ==============================================
# Kubernetes Ingress (NGINX)
# ==============================================

resource "kubernetes_ingress_v1" "app" {

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app.metadata[0].name

    annotations = {
      "kubernetes.io/ingress.class"                = "nginx"
      "nginx.ingress.kubernetes.io/rewrite-target" = "/"
      "nginx.ingress.kubernetes.io/ssl-redirect"   = "false"
    }
  }

  spec {
    rule {
      http {
        path {
          path      = "/"
          path_type = "Prefix"

          backend {
            service {
              name = kubernetes_service.app.metadata[0].name
              port {
                number = 80
              }
            }
          }
        }
      }
    }
  }

  depends_on = [
    kubernetes_service.app,
    helm_release.nginx_ingress
  ]
}

# ==============================================
# Horizontal Pod Autoscaler
# ==============================================

resource "kubernetes_horizontal_pod_autoscaler_v2" "app" {

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app.metadata[0].name
  }

  spec {
    scale_target_ref {
      api_version = "apps/v1"
      kind        = "Deployment"
      name        = kubernetes_deployment.app.metadata[0].name
    }

    min_replicas = 2
    max_replicas = 10

    metric {
      type = "Resource"
      resource {
        name = "cpu"
        target {
          type                = "Utilization"
          average_utilization = 70
        }
      }
    }

    metric {
      type = "Resource"
      resource {
        name = "memory"
        target {
          type                = "Utilization"
          average_utilization = 80
        }
      }
    }
  }

  depends_on = [kubernetes_deployment.app]
}

# ==============================================
# Helm Release: NGINX Ingress Controller
# ==============================================

resource "helm_release" "nginx_ingress" {

  name       = "nginx-ingress"
  repository = "https://kubernetes.github.io/ingress-nginx"
  chart      = "ingress-nginx"
  namespace  = "ingress-nginx"
  version    = "4.8.0"

  create_namespace = true

  set {
    name  = "controller.service.type"
    value = "LoadBalancer"
  }

  set {
    name  = "controller.service.annotations.service\\.beta\\.kubernetes\\.io/azure-load-balancer-health-probe-request-path"
    value = "/healthz"
  }

  depends_on = [azurerm_kubernetes_cluster.main]
}

# ==============================================
# Get Ingress Load Balancer IP
# ==============================================

data "kubernetes_service" "nginx_ingress" {

  metadata {
    name      = "nginx-ingress-ingress-nginx-controller"
    namespace = "ingress-nginx"
  }

  depends_on = [helm_release.nginx_ingress]
}
