# =============================================================================
# Azure AD Service Principal for CI/CD
# =============================================================================
#
# If you already have an existing SP and want to adopt it instead of creating
# a new one, import both the application and the SP before applying:
#
#   # Get app object ID (different from the client/app ID):
#   az ad app show --id <appId> --query id -o tsv
#
#   terraform import azuread_application.cudly_deploy <app-object-id>
#   terraform import azuread_service_principal.cudly_deploy <sp-object-id>

resource "azuread_application" "cudly_deploy" {
  display_name = "cudly-terraform-deploy"
}

resource "azuread_service_principal" "cudly_deploy" {
  client_id = azuread_application.cudly_deploy.client_id
}

# =============================================================================
# GitHub Actions Federated Identity Credentials
# =============================================================================
# Azure federated credentials require one entry per allowed subject (no
# wildcards), so each named deployment environment needs its own resource.
#
# The `github_environment` entries below are what let an environment-bound job
# authenticate at all. A job carrying `environment: staging` presents the
# subject `repo:<org/repo>:environment:staging`, NOT the main-branch subject,
# so without a matching credential `azure/login` fails with AADSTS70021 even
# though the workflow is running on main. Adding an `environment:` binding to
# an Azure job and forgetting this is the exact breakage recorded in #1648.
#
# NOTE: this module is bootstrap-only (see CLAUDE.md) — it is applied manually
# by a privileged human, not by the deploy workflow. The Azure destroy job in
# cleanup-staging.yml cannot authenticate until that re-apply happens.

resource "azuread_application_federated_identity_credential" "github_main" {
  count = var.github_repo != "" ? 1 : 0

  application_id = azuread_application.cudly_deploy.id
  display_name   = "github-actions-main"
  description    = "GitHub Actions OIDC — ${var.github_repo} main branch deployments"
  audiences      = ["api://AzureADTokenExchange"]
  issuer         = "https://token.actions.githubusercontent.com"
  subject        = "repo:${var.github_repo}:ref:refs/heads/main"
}

resource "azuread_application_federated_identity_credential" "github_pr" {
  count = var.github_repo != "" ? 1 : 0

  application_id = azuread_application.cudly_deploy.id
  display_name   = "github-actions-pr"
  description    = "GitHub Actions OIDC — ${var.github_repo} pull request plan checks"
  audiences      = ["api://AzureADTokenExchange"]
  issuer         = "https://token.actions.githubusercontent.com"
  subject        = "repo:${var.github_repo}:pull_request"
}

# One credential per named deployment environment, so environment-bound jobs
# can authenticate. Mirrors the `environment:{dev,staging,prod}` subjects the
# AWS role's trust policy already allows
# (terraform/environments/aws/ci-cd-permissions/role.tf).
resource "azuread_application_federated_identity_credential" "github_environment" {
  for_each = var.github_repo != "" ? toset(var.github_environments) : toset([])

  application_id = azuread_application.cudly_deploy.id
  display_name   = "github-actions-env-${each.value}"
  description    = "GitHub Actions OIDC — ${var.github_repo} ${each.value} environment"
  audiences      = ["api://AzureADTokenExchange"]
  issuer         = "https://token.actions.githubusercontent.com"
  subject        = "repo:${var.github_repo}:environment:${each.value}"
}
