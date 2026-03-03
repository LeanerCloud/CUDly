# ==============================================
# Post-deploy Validation Checks
# ==============================================
#
# Runs 16 check blocks during plan/apply (health, headers, public endpoints,
# auth enforcement, errors, methods, content-type, size limits, CORS, frontend)
# plus a local-exec provisioner for tests that need TLS introspection or
# multi-step auth flows. See modules/deployment-checks/ for details.

module "deployment_checks" {
  source = "../../modules/deployment-checks"

  api_base_url = trimsuffix(
    var.compute_platform == "container-apps"
    ? "https://${module.compute_container_apps[0].container_app_fqdn}"
    : module.compute_aks[0].api_url,
    "/"
  )

  provider_name = "azure"
}
