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
# wildcards). We create two: one for main-branch deployments and one for
# pull-request plan checks.

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
