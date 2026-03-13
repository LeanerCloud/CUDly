# ==============================================
# Post-deploy Validation Checks
# ==============================================
#
# Runs 13 check blocks during plan/apply (health, headers, public endpoints,
# auth enforcement, errors, methods, content-type, size limits, frontend)
# plus a local-exec provisioner for tests that need TLS introspection or
# multi-step auth flows. See modules/deployment-checks/ for details.

module "deployment_checks" {
  source = "../../modules/deployment-checks"

  api_base_url = trimsuffix(
    var.compute_platform == "lambda"
    ? one(module.compute_lambda[*].function_url)
    : one(module.compute_fargate[*].api_url),
    "/"
  )

  provider_name = "aws"
}
