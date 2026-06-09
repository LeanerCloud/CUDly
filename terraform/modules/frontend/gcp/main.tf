# GCP Frontend Module - Global Load Balancer + Cloud Run
# Routes all traffic (static files and API) to the Cloud Run container.
# The container serves static files directly and handles SPA routing.

terraform {
  required_version = ">= 1.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}

# Reserve external IP address for load balancer
resource "google_compute_global_address" "frontend" {
  name    = "${var.project_name}-frontend-ip"
  project = var.project_id
}

# Network Endpoint Group for Cloud Run
resource "google_compute_region_network_endpoint_group" "cloudrun" {
  name    = "${var.project_name}-cloudrun-neg"
  project = var.project_id
  region  = var.region

  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = var.cloud_run_service_name
  }
}

# Backend service for Cloud Run (serves both static files and API)
resource "google_compute_backend_service" "cloudrun" {
  name    = "${var.project_name}-backend"
  project = var.project_id

  load_balancing_scheme = "EXTERNAL_MANAGED"
  protocol              = "HTTPS"
  port_name             = "http"
  timeout_sec           = 30

  backend {
    group = google_compute_region_network_endpoint_group.cloudrun.id
  }

  enable_cdn = false

  # Security response headers - matches AWS CloudFront and Azure Front Door
  custom_response_headers = [
    "Strict-Transport-Security: max-age=31536000; includeSubDomains; preload",
    "X-Content-Type-Options: nosniff",
    "X-Frame-Options: DENY",
    "X-XSS-Protection: 1; mode=block",
    "Referrer-Policy: strict-origin-when-cross-origin",
  ]

  security_policy = var.enable_cloud_armor ? google_compute_security_policy.frontend[0].id : null

  log_config {
    enable      = true
    sample_rate = var.api_log_sample_rate
  }
}

# URL map - routes all traffic to Cloud Run
# SPA routing is handled by the container (returns index.html for unknown paths)
resource "google_compute_url_map" "frontend" {
  name            = "${var.project_name}-url-map"
  project         = var.project_id
  default_service = google_compute_backend_service.cloudrun.id
}

# HTTPS certificate (managed by Google)
resource "google_compute_managed_ssl_certificate" "frontend" {
  count = length(var.domain_names) > 0 ? 1 : 0

  name    = "${var.project_name}-cert"
  project = var.project_id

  managed {
    domains = var.domain_names
  }
}

# Self-signed certificate for dev HTTPS (when no custom domains)
resource "tls_private_key" "frontend" {
  count = length(var.domain_names) > 0 ? 0 : 1

  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_self_signed_cert" "frontend" {
  count = length(var.domain_names) > 0 ? 0 : 1

  private_key_pem = tls_private_key.frontend[0].private_key_pem

  subject {
    common_name  = "cudly-dev.local"
    organization = "CUDly Dev"
  }

  validity_period_hours = 8760 # 1 year

  allowed_uses = [
    "key_encipherment",
    "digital_signature",
    "server_auth",
  ]
}

resource "google_compute_ssl_certificate" "self_signed" {
  count = length(var.domain_names) > 0 ? 0 : 1

  name        = "${var.project_name}-self-signed-cert"
  project     = var.project_id
  private_key = tls_private_key.frontend[0].private_key_pem
  certificate = tls_self_signed_cert.frontend[0].cert_pem

  lifecycle {
    create_before_destroy = true
  }
}

# HTTPS proxy (uses managed cert with custom domains, self-signed for dev)
resource "google_compute_target_https_proxy" "frontend" {
  name    = "${var.project_name}-https-proxy"
  project = var.project_id

  url_map = google_compute_url_map.frontend.id
  ssl_certificates = length(var.domain_names) > 0 ? (
    [google_compute_managed_ssl_certificate.frontend[0].id]
  ) : [google_compute_ssl_certificate.self_signed[0].id]
}

# HTTP to HTTPS redirect (always active)
resource "google_compute_url_map" "http_redirect" {
  name    = "${var.project_name}-http-redirect"
  project = var.project_id

  default_url_redirect {
    https_redirect         = true
    redirect_response_code = "MOVED_PERMANENTLY_DEFAULT"
    strip_query            = false
  }
}

# HTTP proxy: always redirects to HTTPS
resource "google_compute_target_http_proxy" "http_redirect" {
  name    = "${var.project_name}-http-proxy"
  project = var.project_id
  url_map = google_compute_url_map.http_redirect.id
}

# Global forwarding rule for HTTPS (always active)
resource "google_compute_global_forwarding_rule" "https" {
  name                  = "${var.project_name}-https-rule"
  project               = var.project_id
  target                = google_compute_target_https_proxy.frontend.id
  port_range            = "443"
  ip_address            = google_compute_global_address.frontend.address
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

# Global forwarding rule for HTTP (redirect)
resource "google_compute_global_forwarding_rule" "http" {
  name                  = "${var.project_name}-http-rule"
  project               = var.project_id
  target                = google_compute_target_http_proxy.http_redirect.id
  port_range            = "80"
  ip_address            = google_compute_global_address.frontend.address
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

# Cloud Armor security policy (optional)
resource "google_compute_security_policy" "frontend" {
  count = var.enable_cloud_armor ? 1 : 0

  name    = "${var.project_name}-security-policy"
  project = var.project_id

  # Default rule
  rule {
    action   = "allow"
    priority = 2147483647
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
    description = "Default rule"
  }

  # Rate limiting rule
  rule {
    action   = "rate_based_ban"
    priority = 1000
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
    rate_limit_options {
      conform_action = "allow"
      exceed_action  = "deny(429)"

      rate_limit_threshold {
        count        = 100
        interval_sec = 60
      }

      ban_duration_sec = 600
    }
    description = "Rate limit rule"
  }

  # Block common attacks
  rule {
    action   = "deny(403)"
    priority = 1001
    match {
      expr {
        expression = "evaluatePreconfiguredExpr('xss-stable')"
      }
    }
    description = "Block XSS attacks"
  }

  rule {
    action   = "deny(403)"
    priority = 1002
    match {
      expr {
        expression = "evaluatePreconfiguredExpr('sqli-stable')"
      }
    }
    description = "Block SQL injection"
  }
}

# DNS record moved to dns.tf
# This allows creating a new subdomain zone or using an existing one

# Monitoring alert policy for load balancer errors
resource "google_monitoring_alert_policy" "cdn_errors" {
  count = var.enable_monitoring ? 1 : 0

  display_name = "${var.project_name} Load Balancer Error Rate"
  project      = var.project_id
  combiner     = "OR"

  conditions {
    display_name = "5xx error rate"
    condition_threshold {
      filter          = "resource.type=\"https_lb_rule\" AND metric.type=\"loadbalancing.googleapis.com/https/backend_request_count\" AND metric.labels.response_code_class=\"500\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = 5

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }

  notification_channels = var.notification_channels

  alert_strategy {
    auto_close = "86400s"
  }
}
