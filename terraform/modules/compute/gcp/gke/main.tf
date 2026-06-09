# GCP GKE Compute Module
# Google Kubernetes Engine with managed node pools

locals {
  name_prefix = "${var.project_name}-${var.environment}-gke"

  # Use provided zones or default to regional cluster
  zones = length(var.zones) > 0 ? var.zones : null

  # Secret Manager project ID (defaults to main project)
  secret_project_id = var.secret_manager_project_id != "" ? var.secret_manager_project_id : var.project_id

  common_labels = merge(
    var.labels,
    {
      module      = "compute-gcp-gke"
      environment = var.environment
      managed_by  = "terraform"
    }
  )

  # Scheduled-task OIDC validator wiring — see
  # internal/server/scheduledauth.
  #
  # `aud` (audience) names the RECEIVING endpoint — the URL Cloud
  # Scheduler is calling. Pinning aud to the endpoint keeps tokens
  # recipient-bound (the OAuth/OIDC convention) and limits cross-service
  # replay if the scheduler SA is ever reused.
  #
  # `sub` (subject) names the CALLER — the scheduler SA's unique
  # numeric ID. Google puts the SA unique_id (not its email) in the
  # JWT `sub` claim for SA-signed ID tokens, so the validator's
  # SCHEDULED_TASK_OIDC_SUBJECTS env var must contain that numeric form.
  #
  # Auth mode is derived from scheduler SA presence, NOT from
  # var.enable_scheduled_tasks. kubernetes_ingress_v1.app exposes
  # /api/scheduled/* via the catch-all rule even when the scheduler is
  # disabled, so tying auth to enable_scheduled_tasks would turn a
  # disabled scheduler into an unauthenticated public trigger. With no
  # SA, var.scheduled_task_auth_mode_override decides — its default is
  # the fail-closed "oidc" so an unauthenticated boot requires opting
  # in to "disabled" deliberately.
  scheduled_task_oidc_audience = "${var.app_url}/api/scheduled/recommendations"
  scheduled_task_oidc_subject  = try(google_service_account.scheduler[0].unique_id, "")
  scheduled_task_auth_mode = (
    length(google_service_account.scheduler) > 0
    ? "oidc"
    : var.scheduled_task_auth_mode_override
  )
}

# ==============================================
# GKE Cluster
# ==============================================

resource "google_container_cluster" "main" {
  name     = local.name_prefix
  location = var.region

  # Use node_locations for zonal placement if zones are specified
  node_locations = local.zones

  # Minimum version for the cluster
  min_master_version = var.kubernetes_version

  # Remove default node pool immediately
  remove_default_node_pool = true
  initial_node_count       = 1

  # Network configuration
  network    = var.network_name
  subnetwork = var.subnetwork_name

  # IP allocation policy for VPC-native cluster
  ip_allocation_policy {
    cluster_ipv4_cidr_block  = ""
    services_ipv4_cidr_block = ""
  }

  # Workload Identity
  workload_identity_config {
    workload_pool = var.enable_workload_identity ? "${var.project_id}.svc.id.goog" : null
  }

  # Add-ons
  addons_config {
    http_load_balancing {
      disabled = !var.enable_http_load_balancing
    }

    horizontal_pod_autoscaling {
      disabled = !var.enable_horizontal_pod_autoscaling
    }

    network_policy_config {
      disabled = false
    }
  }

  # Network policy
  network_policy {
    enabled  = true
    provider = "PROVIDER_UNSPECIFIED"
  }

  # Maintenance window
  maintenance_policy {
    daily_maintenance_window {
      start_time = "03:00"
    }
  }

  # Enable Binary Authorization
  binary_authorization {
    evaluation_mode = "PROJECT_SINGLETON_POLICY_ENFORCE"
  }

  # Resource labels
  resource_labels = local.common_labels

  lifecycle {
    ignore_changes = [
      node_pool,
      initial_node_count
    ]
  }
}

# ==============================================
# Node Pool
# ==============================================

resource "google_container_node_pool" "primary" {
  name     = "primary-pool"
  location = var.region
  cluster  = google_container_cluster.main.name

  # Node count per zone
  initial_node_count = var.node_count

  # Auto-scaling configuration
  dynamic "autoscaling" {
    for_each = var.enable_auto_scaling ? [1] : []
    content {
      min_node_count = var.min_node_count
      max_node_count = var.max_node_count
    }
  }

  # Management
  management {
    auto_repair  = var.enable_auto_repair
    auto_upgrade = var.enable_auto_upgrade
  }

  # Node configuration
  node_config {
    machine_type = var.node_machine_type
    disk_size_gb = var.node_disk_size_gb
    disk_type    = "pd-standard"

    # OAuth scopes
    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform"
    ]

    # Workload Identity
    dynamic "workload_metadata_config" {
      for_each = var.enable_workload_identity ? [1] : []
      content {
        mode = "GKE_METADATA"
      }
    }

    # Metadata
    metadata = {
      disable-legacy-endpoints = "true"
    }

    # Labels
    labels = local.common_labels

    # Shielded instance config
    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }

    # Tags
    tags = ["${var.project_name}-${var.environment}-gke-node"]
  }

  # Upgrade settings
  upgrade_settings {
    max_surge       = 1
    max_unavailable = 0
  }
}

# ==============================================
# Workload Identity Service Account
# ==============================================

resource "google_service_account" "workload" {
  account_id   = "${var.project_name}-${var.environment}-gke-sa"
  display_name = "GKE Workload Identity for ${var.project_name} ${var.environment}"
  project      = var.project_id
}

# Grant per-secret access to Secret Manager. The database password is
# always required; additional secrets (credential encryption key, sendgrid,
# etc.) are wired through `additional_secret_accessor_ids` from the env so
# the workload can read everything it needs at startup. Previously only
# `database_password_secret_name` was bound, so any code path that read
# other secrets silently 403'd inside the pod.
resource "google_secret_manager_secret_iam_member" "workload" {
  project   = local.secret_project_id
  secret_id = var.database_password_secret_name
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.workload.email}"
}

resource "google_secret_manager_secret_iam_member" "workload_additional" {
  for_each = var.additional_secret_accessor_ids

  project   = local.secret_project_id
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.workload.email}"
}

# Bind Kubernetes Service Account to Google Service Account
resource "google_service_account_iam_member" "workload_identity" {
  service_account_id = google_service_account.workload.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[${var.environment}/${var.project_name}-api]"

  depends_on = [google_container_cluster.main]
}

# ==============================================
# Kubernetes Resources (CONDITIONAL)
# ==============================================
#
# NOTE: These resources are conditionally created based on var.deploy_kubernetes_resources
# To use these resources:
# 1. Set deploy_kubernetes_resources = true
# 2. Configure kubernetes/helm providers at the root level (see providers.tf)
# 3. The providers will use the cluster's endpoint and credentials
#
# Default: false (only creates the GKE cluster infrastructure)
# ==============================================

# ==============================================
# Kubernetes Namespace
# ==============================================

resource "kubernetes_namespace" "app" {
  count = var.deploy_kubernetes_resources ? 1 : 0

  metadata {
    name = var.environment

    labels = {
      environment = var.environment
      project     = var.project_name
    }
  }

  depends_on = [google_container_node_pool.primary]
}

# ==============================================
# Kubernetes Secret for Database
# ==============================================

# Note: In production, use Secrets Store CSI driver with Secret Manager
# This is a simplified approach for getting started

resource "kubernetes_secret" "database" {
  count = var.deploy_kubernetes_resources ? 1 : 0

  metadata {
    name      = "database-credentials"
    namespace = kubernetes_namespace.app[0].metadata[0].name
  }

  # NOTE: `password_secret_name` carries the *Secret Manager secret name*,
  # not the cleartext password. Workloads must resolve it via the Secret
  # Manager API (or the Secrets Store CSI driver) before connecting. The
  # earlier key name `password` was misleading because it implied the
  # cleartext value lived inline.
  data = {
    host                 = var.database_host
    name                 = var.database_name
    username             = var.database_username
    password_secret_name = var.database_password_secret_name
  }

  type = "Opaque"

  depends_on = [kubernetes_namespace.app]
}

# ==============================================
# Kubernetes Service Account
# ==============================================

resource "kubernetes_service_account" "app" {
  count = var.deploy_kubernetes_resources ? 1 : 0

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app[0].metadata[0].name

    annotations = {
      "iam.gke.io/gcp-service-account" = google_service_account.workload.email
    }
  }

  depends_on = [kubernetes_namespace.app]
}

# ==============================================
# Kubernetes Deployment
# ==============================================

resource "kubernetes_deployment" "app" {
  count = var.deploy_kubernetes_resources ? 1 : 0

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app[0].metadata[0].name

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
        service_account_name = kubernetes_service_account.app[0].metadata[0].name

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
            name  = "RUNTIME_MODE"
            value = "http"
          }

          env {
            name = "DB_HOST"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.database[0].metadata[0].name
                key  = "host"
              }
            }
          }

          env {
            name  = "DB_PORT"
            value = "5432"
          }

          env {
            name = "DB_NAME"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.database[0].metadata[0].name
                key  = "name"
              }
            }
          }

          env {
            name = "DB_USER"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.database[0].metadata[0].name
                key  = "username"
              }
            }
          }

          env {
            name = "DB_PASSWORD_SECRET"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.database[0].metadata[0].name
                key  = "password"
              }
            }
          }

          env {
            name  = "DB_SSL_MODE"
            value = "disable"
          }

          env {
            name  = "DB_CONNECT_TIMEOUT"
            value = "8s"
          }

          env {
            name  = "DB_AUTO_MIGRATE"
            value = tostring(var.auto_migrate)
          }

          env {
            name  = "DB_MIGRATIONS_PATH"
            value = "/app/migrations"
          }

          env {
            name  = "ADMIN_EMAIL"
            value = var.admin_email
          }

          env {
            name  = "ADMIN_PASSWORD_SECRET"
            value = var.admin_password_secret_name
          }

          env {
            name  = "SECRET_PROVIDER"
            value = "gcp"
          }

          env {
            name  = "GCP_PROJECT_ID"
            value = var.project_id
          }

          env {
            name  = "GCP_REGION"
            value = var.region
          }

          env {
            name  = "ALLOWED_ORIGINS"
            value = join(",", var.allowed_origins)
          }

          # Scheduled-task OIDC validator config — see locals block above
          # and internal/server/scheduledauth.
          env {
            name  = "SCHEDULED_TASK_AUTH_MODE"
            value = local.scheduled_task_auth_mode
          }

          env {
            name  = "SCHEDULED_TASK_OIDC_AUDIENCE"
            value = local.scheduled_task_oidc_audience
          }

          env {
            name  = "SCHEDULED_TASK_OIDC_SUBJECTS"
            value = local.scheduled_task_oidc_subject
          }

          dynamic "env" {
            for_each = var.additional_env_vars
            content {
              name  = env.key
              value = env.value
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
              path = var.health_check_path
              port = var.health_check_port
            }
            initial_delay_seconds = var.liveness_probe_initial_delay
            period_seconds        = var.liveness_probe_period
            timeout_seconds       = 5
            failure_threshold     = 3
          }

          readiness_probe {
            http_get {
              path = var.health_check_path
              port = var.health_check_port
            }
            initial_delay_seconds = var.readiness_probe_initial_delay
            period_seconds        = var.readiness_probe_period
            timeout_seconds       = 3
            failure_threshold     = 3
          }
        }
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
    kubernetes_secret.database,
    kubernetes_service_account.app
  ]
}

# ==============================================
# Kubernetes Service (ClusterIP)
# ==============================================

resource "kubernetes_service" "app" {
  count = var.deploy_kubernetes_resources ? 1 : 0

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app[0].metadata[0].name

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
# Kubernetes Ingress (GCE)
# ==============================================

resource "kubernetes_ingress_v1" "app" {
  count = var.deploy_kubernetes_resources ? 1 : 0

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app[0].metadata[0].name

    annotations = {
      "kubernetes.io/ingress.class"                 = "gce"
      "kubernetes.io/ingress.global-static-ip-name" = google_compute_global_address.ingress[0].name
    }
  }

  spec {
    rule {
      http {
        path {
          path      = "/*"
          path_type = "ImplementationSpecific"

          backend {
            service {
              name = kubernetes_service.app[0].metadata[0].name
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
    google_compute_global_address.ingress
  ]
}

# ==============================================
# Horizontal Pod Autoscaler
# ==============================================

resource "kubernetes_horizontal_pod_autoscaler_v2" "app" {
  count = var.deploy_kubernetes_resources ? 1 : 0

  metadata {
    name      = "${var.project_name}-api"
    namespace = kubernetes_namespace.app[0].metadata[0].name
  }

  spec {
    scale_target_ref {
      api_version = "apps/v1"
      kind        = "Deployment"
      name        = kubernetes_deployment.app[0].metadata[0].name
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
# Global Static IP for Ingress
# ==============================================

resource "google_compute_global_address" "ingress" {
  count = var.deploy_kubernetes_resources ? 1 : 0

  name         = "${local.name_prefix}-ingress-ip"
  project      = var.project_id
  address_type = "EXTERNAL"
  ip_version   = "IPV4"
}


# ==============================================
# Cloud Scheduler for Scheduled Tasks
# ==============================================
# Uses the same HTTP trigger pattern as Cloud Run module.
# Cloud Scheduler calls the app endpoint through the load balancer.

resource "google_cloud_scheduler_job" "recommendations" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name             = "${local.name_prefix}-recommendations"
  description      = "Trigger recommendations collection"
  schedule         = var.recommendation_schedule
  time_zone        = "UTC"
  attempt_deadline = "320s"
  project          = var.project_id
  region           = var.region

  retry_config {
    retry_count = 3
  }

  http_target {
    http_method = "POST"
    # Both the request target and the OIDC audience read the same
    # local so they cannot drift. If they ever did, Cloud Scheduler
    # would mint a token with `aud` for the old endpoint while
    # actually POSTing to the new one — the validator would 401
    # silently. Sharing the local makes that class of regression a
    # syntax-level mismatch rather than a runtime auth failure.
    uri = local.scheduled_task_oidc_audience

    # Auth: oidc_token below is signed by the scheduler's service
    # account at invocation time. GKE Ingress has no Cloud-Run-style
    # IAM gate, so the OIDC token is validated by the app at
    # /api/scheduled/* via internal/server/scheduledauth — signature,
    # issuer, audience, and `sub` pinned to this SA's unique_id.
    # The previous static `Authorization: Bearer
    # ${var.scheduled_task_secret}` header leaked the shared secret into
    # the scheduler resource definition + Terraform state — closes #159.
    oidc_token {
      service_account_email = google_service_account.scheduler[0].email
      audience              = local.scheduled_task_oidc_audience
    }
  }
}

# Service account for Cloud Scheduler
resource "google_service_account" "scheduler" {
  count = var.enable_scheduled_tasks ? 1 : 0

  account_id   = "${var.project_name}-${var.environment}-gke-sched"
  display_name = "Cloud Scheduler service account for GKE ${var.project_name}"
  project      = var.project_id
}
