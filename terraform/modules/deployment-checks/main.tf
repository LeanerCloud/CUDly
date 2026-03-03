# ==============================================
# Post-deploy Validation Checks
# ==============================================
#
# 13 check blocks run during terraform plan/apply and produce warnings
# (not errors) if assertions fail. They cover health, security headers,
# public endpoints, auth enforcement, error handling, method enforcement
# (POST only), content-type enforcement, request size limits, and frontend
# serving.
#
# Tests requiring multi-step flows, TLS introspection, or HTTP methods not
# supported by data "http" (DELETE/PUT/OPTIONS) are covered by the
# terraform_data local-exec provisioner at the bottom of this file.

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    http = {
      source  = "hashicorp/http"
      version = "~> 3.0"
    }
  }
}

locals {
  # 1.1MB body for request size limit test (two-step build stays under range's 1024 limit)
  pad        = join("", [for i in range(1024) : "0123456789"]) # 10,240 chars
  large_body = join("", [for i in range(110) : local.pad])     # 1,126,400 chars
}

# --- Health & Headers ---

check "health_and_headers" {
  data "http" "health" {
    url = "${var.api_base_url}/health"

    request_headers = {
      Accept = "application/json"
    }

    request_timeout_ms = 10000
  }

  assert {
    condition     = data.http.health.status_code == 200
    error_message = "Health check failed: ${var.api_base_url}/health returned ${data.http.health.status_code}"
  }

  assert {
    condition     = lookup(data.http.health.response_headers, "strict-transport-security", "") != ""
    error_message = "HSTS header missing from /health response"
  }

  assert {
    condition     = lookup(data.http.health.response_headers, "x-content-type-options", "") != ""
    error_message = "X-Content-Type-Options header missing from /health response"
  }

  assert {
    condition     = lookup(data.http.health.response_headers, "x-frame-options", "") != ""
    error_message = "X-Frame-Options header missing from /health response"
  }

  assert {
    condition     = can(regex("no-(store|cache)", lookup(data.http.health.response_headers, "cache-control", "")))
    error_message = "Cache-Control header missing no-store/no-cache on /health response"
  }
}

check "api_health_alias" {
  data "http" "api_health" {
    url = "${var.api_base_url}/api/health"

    request_headers = {
      Accept = "application/json"
    }

    request_timeout_ms = 10000
  }

  assert {
    condition     = data.http.api_health.status_code == 200
    error_message = "/api/health alias returned ${data.http.api_health.status_code}, expected 200"
  }
}

# --- Public Endpoints ---

check "public_check_admin" {
  data "http" "check_admin" {
    url = "${var.api_base_url}/api/auth/check-admin"

    request_headers = {
      Accept = "application/json"
    }

    request_timeout_ms = 10000
  }

  assert {
    condition     = data.http.check_admin.status_code == 200
    error_message = "/api/auth/check-admin returned ${data.http.check_admin.status_code}, expected 200"
  }
}

check "public_info" {
  data "http" "info" {
    url = "${var.api_base_url}/api/info"

    request_headers = {
      Accept = "application/json"
    }

    request_timeout_ms = 10000
  }

  assert {
    condition     = data.http.info.status_code == 200
    error_message = "/api/info returned ${data.http.info.status_code}, expected 200"
  }
}

# --- Auth Enforcement ---

check "auth_enforcement" {
  data "http" "configs" {
    url = "${var.api_base_url}/api/configs"

    request_timeout_ms = 10000
  }

  assert {
    condition     = contains([401, 403], data.http.configs.status_code)
    error_message = "/api/configs without auth returned ${data.http.configs.status_code}, expected 401 or 403"
  }
}

# --- Error Handling ---

check "error_empty_body" {
  data "http" "empty_body" {
    url    = "${var.api_base_url}/api/auth/login"
    method = "POST"

    request_headers = {
      Content-Type = "application/json"
    }

    request_body = "{}"

    request_timeout_ms = 10000
  }

  assert {
    condition     = contains([400, 401], data.http.empty_body.status_code)
    error_message = "POST /api/auth/login with empty body returned ${data.http.empty_body.status_code}, expected 400 or 401"
  }
}

check "error_invalid_json" {
  data "http" "invalid_json" {
    url    = "${var.api_base_url}/api/auth/login"
    method = "POST"

    request_headers = {
      Content-Type = "application/json"
    }

    request_body = "{invalid"

    request_timeout_ms = 10000
  }

  assert {
    condition     = contains([400, 401], data.http.invalid_json.status_code)
    error_message = "POST /api/auth/login with invalid JSON returned ${data.http.invalid_json.status_code}, expected 400 or 401"
  }
}

# --- Method Enforcement ---
# NOTE: DELETE and PUT checks are in the bash test harness because
# data "http" only supports GET, POST, and HEAD methods.

check "method_post_summary" {
  data "http" "post_summary" {
    url    = "${var.api_base_url}/api/dashboard/summary"
    method = "POST"

    request_headers = {
      Content-Type = "application/json"
    }

    request_body = "{}"

    request_timeout_ms = 10000
  }

  assert {
    condition     = contains([401, 404], data.http.post_summary.status_code)
    error_message = "POST /api/dashboard/summary returned ${data.http.post_summary.status_code}, expected 401 or 404"
  }
}

# --- Content-Type Enforcement ---

check "content_type_xml" {
  data "http" "xml_body" {
    url    = "${var.api_base_url}/api/auth/login"
    method = "POST"

    request_headers = {
      Content-Type = "text/xml"
    }

    request_body = "<login/>"

    request_timeout_ms = 10000
  }

  assert {
    condition     = contains([400, 415], data.http.xml_body.status_code)
    error_message = "POST /api/auth/login with text/xml returned ${data.http.xml_body.status_code}, expected 400 or 415"
  }
}

check "content_type_missing" {
  data "http" "no_content_type" {
    url    = "${var.api_base_url}/api/auth/login"
    method = "POST"

    request_body = "{\"email\":\"test\"}"

    request_timeout_ms = 10000
  }

  assert {
    condition     = contains([400, 415], data.http.no_content_type.status_code)
    error_message = "POST /api/auth/login without Content-Type returned ${data.http.no_content_type.status_code}, expected 400 or 415"
  }
}

# --- Request Size Limit ---

check "request_size_limit" {
  data "http" "large_body" {
    url    = "${var.api_base_url}/api/auth/login"
    method = "POST"

    request_headers = {
      Content-Type = "application/json"
    }

    request_body = local.large_body

    request_timeout_ms = 30000
  }

  assert {
    condition     = data.http.large_body.status_code == 413
    error_message = "POST /api/auth/login with 1.1MB body returned ${data.http.large_body.status_code}, expected 413"
  }
}

# --- CORS ---
# NOTE: CORS preflight check is in the bash test harness because
# data "http" only supports GET, POST, and HEAD (not OPTIONS).

# --- Frontend Serving ---

check "frontend_serving" {
  data "http" "root" {
    url = "${var.api_base_url}/"

    request_timeout_ms = 10000
  }

  assert {
    condition     = data.http.root.status_code == 200
    error_message = "GET / returned ${data.http.root.status_code}, expected 200"
  }

  assert {
    condition     = can(regex("text/html", lookup(data.http.root.response_headers, "content-type", "")))
    error_message = "GET / content-type is not text/html"
  }
}

check "spa_routing" {
  data "http" "settings" {
    url = "${var.api_base_url}/settings"

    request_timeout_ms = 10000
  }

  assert {
    condition     = data.http.settings.status_code == 200
    error_message = "GET /settings (SPA route) returned ${data.http.settings.status_code}, expected 200"
  }
}

# --- Remaining tests via local-exec ---
#
# These tests require capabilities that data "http" check blocks lack:
# - HTTPS/TLS version inspection
# - HTTP->HTTPS redirect detection (data "http" follows redirects)
# - Multi-step auth flows (login -> test -> cleanup)
# - Response time measurement
# - DELETE/PUT/OPTIONS methods (data "http" only supports GET/POST/HEAD)
# - CORS preflight (requires OPTIONS method)

resource "terraform_data" "deployment_tests" {
  triggers_replace = [var.api_base_url]

  provisioner "local-exec" {
    command    = "${path.module}/../../../scripts/test-deployment.sh ${var.provider_name}"
    on_failure = continue

    environment = {
      AWS_URL   = var.provider_name == "aws" ? var.api_base_url : ""
      GCP_URL   = var.provider_name == "gcp" ? var.api_base_url : ""
      AZURE_URL = var.provider_name == "azure" ? var.api_base_url : ""
    }
  }
}
