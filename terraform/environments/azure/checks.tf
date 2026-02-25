# ==============================================
# Post-deploy Health Checks
# ==============================================
#
# These check blocks run during terraform plan/apply and produce warnings
# (not errors) if the app is unreachable or unhealthy. They verify that
# the deployed application is responding correctly after infrastructure changes.

locals {
  api_health_url = var.compute_platform == "container-apps" ? (
    "https://${module.compute_container_apps[0].container_app_fqdn}/health"
    ) : (
    "${module.compute_aks[0].api_url}/health"
  )
}

check "api_health" {
  data "http" "api" {
    url = local.api_health_url

    request_headers = {
      Accept = "application/json"
    }

    request_timeout_ms = 10000
  }

  assert {
    condition     = data.http.api.status_code == 200
    error_message = "API health check failed: ${data.http.api.url} returned ${data.http.api.status_code}"
  }
}
