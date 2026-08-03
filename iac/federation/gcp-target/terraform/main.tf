terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
    http = {
      source  = "hashicorp/http"
      version = ">= 3.4"
    }
  }
}

provider "google" {
  project = var.project
}

data "google_project" "current" {}

locals {
  project                = var.project != "" ? var.project : data.google_project.current.project_id
  create_service_account = var.service_account_email == ""
  service_account_email  = local.create_service_account ? google_service_account.cudly[0].email : var.service_account_email

  # The single AWS identity this deployment trusts. Both the provider's
  # attribute_condition and the impersonation grant compare against this exact
  # string, so they cannot drift apart.
  #
  # Standard AWS partition only: a GovCloud or China-partition caller presents
  # arn:aws-us-gov:... / arn:aws-cn:..., which simply will not equal this value,
  # so the token exchange is refused rather than admitted on a partition
  # mismatch.
  aws_role_arn = "arn:aws:sts::${var.aws_account_id}:assumed-role/${var.aws_role_name}"
}

resource "google_project_service" "iam" {
  project            = local.project
  service            = "iam.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "iamcredentials" {
  project            = local.project
  service            = "iamcredentials.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "sts" {
  project            = local.project
  service            = "sts.googleapis.com"
  disable_on_destroy = false
}

# Optional: create a dedicated least-privilege SA when the operator didn't
# pre-provision one (var.service_account_email left empty).
resource "google_service_account" "cudly" {
  count        = local.create_service_account ? 1 : 0
  project      = local.project
  account_id   = var.service_account_id
  display_name = "CUDly WIF target SA"
  description  = "Impersonated by CUDly via Workload Identity Federation to manage GCP commitments."

  depends_on = [google_project_service.iam]
}

resource "google_project_iam_custom_role" "cudly" {
  count   = local.create_service_account ? 1 : 0
  project = local.project
  role_id = var.custom_role_id
  title   = "CUDly Commitment Writer"
  # Description matches terraform/modules/compute/gcp/cloud-run exactly
  # so applies from the two modules are idempotent.
  description = "Minimum permissions for CUDly to purchase and update committed use discounts."
  permissions = var.custom_role_permissions
  stage       = "GA"
}

resource "google_project_iam_member" "cudly_custom" {
  count   = local.create_service_account ? 1 : 0
  project = local.project
  role    = google_project_iam_custom_role.cudly[0].name
  member  = "serviceAccount:${google_service_account.cudly[0].email}"
}

resource "google_project_iam_member" "cudly" {
  for_each = local.create_service_account ? toset(var.service_account_project_roles) : toset([])
  project  = local.project
  role     = each.value
  member   = "serviceAccount:${google_service_account.cudly[0].email}"
}

resource "google_iam_workload_identity_pool" "cudly" {
  project                   = local.project
  workload_identity_pool_id = var.pool_id
  display_name              = "CUDly WIF pool"

  depends_on = [
    google_project_service.iam,
    google_project_service.iamcredentials,
    google_project_service.sts,
  ]
}

resource "google_iam_workload_identity_pool_provider" "cudly" {
  project                            = local.project
  workload_identity_pool_id          = google_iam_workload_identity_pool.cudly.workload_identity_pool_id
  workload_identity_pool_provider_id = var.provider_id

  # attribute.aws_role normalises the session ARN
  # (arn:aws:sts::<acct>:assumed-role/<role>/<session>) down to the role ARN, so
  # the condition below and the IAM grant on the service account can both match
  # it with == instead of testing for a substring.
  #
  # A substring test on the raw assertion.arn is bypassable. AWS IAM user paths
  # accept any printable ASCII (documented grammar: (/)|(/[!-~]+/)), so an
  # attacker holding only iam:CreateUser in the trusted account can create a
  # user at path /assumed-role/<pinned role>/ whose ARN,
  # arn:aws:iam::<acct>:user/assumed-role/<pinned role>/<name>, contains the
  # substring the old condition looked for. Normalising first maps that ARN to
  # arn:aws:iam::<acct>:user/assumed-role/<pinned role>, which is not equal to
  # the arn:aws:sts::<acct>:assumed-role/<pinned role> pinned below, so it is
  # refused. STS drops the path from assumed-role ARNs, so the same trick does
  # not work through an IAM role.
  #
  # This expression, the attribute_condition below, the impersonation grant's
  # member, and the set of keys mapped here are byte-identical to their
  # counterparts in arm/CUDly-CrossSubscription/setup-gcp-wif.sh, so the two
  # customer-facing onboarding paths pin the same identity and configure the
  # same provider. The two paths do converge on one provider rather than staying
  # independent: both default to pool 'cudly-pool' and provider
  # 'cudly-provider', so a customer who applies this module and then runs the
  # script lands on the provider this resource created.
  #
  # Do not map an attribute here without mapping it in the script too. The
  # script refuses to reuse a provider whose configuration differs from the one
  # it would have written, and the only remedy it offers is deleting the
  # provider, which detaches every live federated session. An extra key on one
  # side alone is enough to trigger that, even when nothing reads its value.
  # TestGCPTargetMappingMatchesSetupScript guards the parity.
  attribute_mapping = var.provider_type == "aws" ? {
    "google.subject"     = "assertion.arn"
    "attribute.aws_role" = "assertion.arn.contains('assumed-role') ? assertion.arn.extract('{account_arn}assumed-role/') + 'assumed-role/' + assertion.arn.extract('assumed-role/{role_name}/') : assertion.arn"
  } : var.oidc_attribute_mapping

  # Both branches are non-null: aws_role_name and oidc_subject are validated
  # as non-empty by lifecycle preconditions below, so neither falls back to null.
  attribute_condition = var.provider_type == "aws" ? (
    "attribute.aws_role == '${local.aws_role_arn}'"
    ) : (
    "google.subject == '${var.oidc_subject}'"
  )

  dynamic "aws" {
    for_each = var.provider_type == "aws" ? [1] : []
    content {
      account_id = var.aws_account_id
    }
  }

  dynamic "oidc" {
    for_each = var.provider_type == "oidc" ? [1] : []
    content {
      issuer_uri = var.oidc_issuer_uri
    }
  }

  lifecycle {
    precondition {
      condition     = var.provider_type != "aws" || var.aws_account_id != ""
      error_message = "aws_account_id is required when provider_type is 'aws'."
    }
    precondition {
      condition     = var.provider_type != "oidc" || var.oidc_issuer_uri != ""
      error_message = "oidc_issuer_uri is required when provider_type is 'oidc'."
    }
    # Security: a null attribute_condition trusts every role/subject in the account
    # or from the issuer. Both inputs must be non-empty so the condition is always set.
    precondition {
      condition     = var.provider_type != "aws" || var.aws_role_name != ""
      error_message = "aws_role_name is required when provider_type is 'aws'. Without it the attribute_condition is null and any IAM role in the AWS account can federate as the GCP service account."
    }
    precondition {
      condition     = var.provider_type != "oidc" || var.oidc_subject != ""
      error_message = "oidc_subject is required when provider_type is 'oidc'. Without it the attribute_condition is null and any subject from the OIDC issuer can federate as the GCP service account."
    }
  }
}

# Use _member (not _binding) to add one member without replacing existing bindings.
#
# Both branches name a single identity, so a future mapping or condition bug
# cannot widen impersonation beyond the pinned principal:
#   OIDC: principal://.../subject/<exact sub claim>.
#   AWS:  principalSet://.../attribute.aws_role/<exact role ARN>. This is the
#         exact-value form of a principalSet, not a prefix or wildcard: it
#         admits only identities whose attribute.aws_role equals the value.
#         Session ARNs carry a per-session suffix, which is why the grant names
#         the normalised role attribute rather than google.subject; it is not a
#         reason to fall back to the pool-wide wildcard principalSet this
#         replaced, which granted impersonation to everything in the pool.
#
# Principal identifiers are pool-scoped, not provider-scoped: any provider added
# to this pool that can mint the same attribute value satisfies this grant. Keep
# var.pool_id dedicated to CUDly unless you intend to share it.
resource "google_service_account_iam_member" "cudly_wif" {
  service_account_id = "projects/${local.project}/serviceAccounts/${local.service_account_email}"
  role               = "roles/iam.workloadIdentityUser"
  member = var.provider_type == "oidc" ? (
    "principal://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/subject/${var.oidc_subject}"
    ) : (
    "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/attribute.aws_role/${local.aws_role_arn}"
  )

  # Upgrade ordering for deployments applied before the pool-wide grant was
  # narrowed. `member` is ForceNew, so changing it replaces this resource, and
  # the wrong order breaks federation for live customers:
  #
  #   depends_on:            attribute_mapping and attribute_condition are
  #                          updated in place by the provider (neither is
  #                          ForceNew; both go into the PATCH updateMask), so the
  #                          provider is reconfigured first. Until that lands,
  #                          attribute.aws_role still holds the raw session ARN
  #                          and the new member below would match nothing.
  #   create_before_destroy: adds the narrow member while the old pool-wide one
  #                          is still present, so there is no window in which the
  #                          service account has neither.
  #
  # Net apply order: reconfigure provider -> add narrow member -> remove the old
  # pool-wide member. Every step keeps at least one matching grant in place.
  depends_on = [google_iam_workload_identity_pool_provider.cudly]

  lifecycle {
    create_before_destroy = true
  }
}
