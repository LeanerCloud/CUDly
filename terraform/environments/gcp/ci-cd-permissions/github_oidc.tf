resource "google_iam_workload_identity_pool" "github" {
  count = var.github_repo != "" ? 1 : 0

  workload_identity_pool_id = "github-actions"
  display_name              = "GitHub Actions"
  description               = "Workload Identity Pool for GitHub Actions OIDC"
  project                   = var.project_id
}

resource "google_iam_workload_identity_pool_provider" "github" {
  count = var.github_repo != "" ? 1 : 0

  workload_identity_pool_id          = google_iam_workload_identity_pool.github[0].workload_identity_pool_id
  workload_identity_pool_provider_id = "github-actions"
  display_name                       = "GitHub Actions"
  project                            = var.project_id

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }

  # Map GitHub token claims to Google attributes for use in conditions and bindings.
  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.actor"      = "assertion.actor"
    "attribute.repository" = "assertion.repository"
    "attribute.ref"        = "assertion.ref"
  }

  # Restrict to the specific repo AND only allow the designated deploy branch.
  # PRs, tags, forks, and workflow_dispatch from other refs are blocked.
  attribute_condition = "assertion.repository == '${var.github_repo}' && assertion.ref == '${var.deploy_ref}'"
}

# Allow workflows from the permitted repo+ref to impersonate the deploy SA.
resource "google_service_account_iam_member" "github_actions" {
  count = var.github_repo != "" ? 1 : 0

  service_account_id = google_service_account.cudly_deploy.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github[0].name}/attribute.repository/${var.github_repo}"
}
