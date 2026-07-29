variable "project" {
  description = "GCP project ID where the Workload Identity Pool will be created."
  type        = string
}

variable "pool_id" {
  description = "ID for the Workload Identity Pool (must be unique within the project)."
  type        = string
  default     = "cudly-pool"
}

variable "provider_id" {
  description = "ID for the Workload Identity Pool Provider."
  type        = string
  default     = "cudly-provider"
}

variable "provider_type" {
  description = "Type of identity provider: 'aws' or 'oidc'."
  type        = string
  validation {
    condition     = contains(["aws", "oidc"], var.provider_type)
    error_message = "provider_type must be 'aws' or 'oidc'."
  }
}

variable "aws_account_id" {
  description = "AWS account ID. Required when provider_type is 'aws'."
  type        = string
  default     = ""

  validation {
    condition     = var.aws_account_id == "" || can(regex("^[0-9]{12}$", var.aws_account_id))
    error_message = "aws_account_id must be a 12-digit AWS account ID."
  }
}

variable "oidc_issuer_uri" {
  description = "OIDC issuer URI. Required when provider_type is 'oidc'. Example: https://login.microsoftonline.com/<tenant_id>/v2.0"
  type        = string
  default     = ""

  validation {
    condition     = var.oidc_issuer_uri == "" || startswith(var.oidc_issuer_uri, "https://")
    error_message = "oidc_issuer_uri must start with https://."
  }
}

variable "oidc_attribute_mapping" {
  description = "CEL attribute mapping from the OIDC token to Google attributes."
  type        = map(string)
  default = {
    "google.subject" = "assertion.sub"
  }
}

# Both values below are interpolated into a single-quoted string literal inside
# the provider's CEL attribute_condition and into the IAM principal identifier of
# the impersonation grant. The character checks reject the forms that fail OPEN
# in those two destinations rather than merely looking wrong:
#
#   ' " \ `  terminate the CEL string literal, so the rest of the value rewrites
#            the condition — aws_role_name = "x') || true || ('" renders an
#            always-true condition that admits every identity;
#   *        widens an IAM principal identifier to match every identity;
#   $        catches a pasted ${...} placeholder, which is not expanded here and
#            yields a binding matching nothing (or, where expanded, everything).
#
# The format checks that follow already exclude those characters. They are kept
# as separate validations because Terraform reports every failing validation, and
# "this would rewrite the attribute condition" is far more actionable than a bare
# charset regex when someone pastes a crafted value.

variable "aws_role_name" {
  description = "AWS IAM role name to restrict trust to (e.g. 'CUDly-Execution'). Required when provider_type is 'aws'. Without this the attribute_condition is null and any IAM role in the account can federate."
  type        = string
  default     = ""

  validation {
    condition     = !can(regex("['\"\\\\`*$]", var.aws_role_name))
    error_message = "aws_role_name must not contain quotes, backslashes, backticks, '*' or '$'. It is interpolated into the provider's CEL attribute condition and into the IAM principal identifier of the impersonation grant, where those characters can escape the string literal or widen the grant to every identity."
  }

  # Matches AWS's own role-name charset [\w+=,.@-]{1,64}. The comma is safe here:
  # the role name reaches attribute_condition (a plain string), never the
  # comma-delimited attribute_mapping keys.
  validation {
    condition     = var.aws_role_name == "" || can(regex("^[A-Za-z0-9_+=,.@-]{1,64}$", var.aws_role_name))
    error_message = "aws_role_name must be a bare IAM role name of 1-64 characters from [A-Za-z0-9_+=,.@-] (e.g. \"CUDly-Execution\"), not an ARN or a path."
  }
}

variable "oidc_subject" {
  description = "OIDC subject claim to restrict trust to. Required when provider_type is 'oidc'. Without this the attribute_condition is null and any subject from the issuer can federate."
  type        = string
  default     = ""

  validation {
    condition     = !can(regex("['\"\\\\`*$]", var.oidc_subject))
    error_message = "oidc_subject must not contain quotes, backslashes, backticks, '*' or '$'. It is interpolated into the provider's CEL attribute condition and into the IAM principal identifier of the impersonation grant, where those characters can escape the string literal or widen the grant to every identity."
  }

  # '|' is permitted because Auth0/Okta-style subjects use it (google-oauth2|123).
  # It is inert in both destinations: quotes are rejected above, so it cannot
  # escape the CEL string literal, and it carries no meaning in a principal path.
  validation {
    condition     = var.oidc_subject == "" || can(regex("^[A-Za-z0-9][-A-Za-z0-9._:/@=+~|]*$", var.oidc_subject))
    error_message = "oidc_subject must start with an alphanumeric character and contain only [-A-Za-z0-9._:/@=+~|] (e.g. \"repo:my-org/my-repo:ref:refs/heads/main\")."
  }

  # oidc_subject becomes google.subject, which GCP caps at 127 characters. A
  # longer value is rejected at token-exchange time, long after apply succeeds.
  validation {
    condition     = length(var.oidc_subject) <= 127
    error_message = "oidc_subject must be at most 127 characters: it becomes google.subject, which GCP rejects above that length at token-exchange time."
  }
}

# ------------------------------------------------------------------------
# OIDC credential source (used by gcloud_command output for provider_type=oidc)
# ------------------------------------------------------------------------

variable "oidc_credential_source_type" {
  description = <<-EOT
    Credential source type for the generated `gcloud iam workload-identity-pools
    create-cred-config` command when provider_type = "oidc". Must be "file" or
    "url". Ignored when provider_type = "aws".
  EOT
  type        = string
  default     = "file"

  validation {
    condition     = contains(["file", "url"], var.oidc_credential_source_type)
    error_message = "oidc_credential_source_type must be \"file\" or \"url\"."
  }
}

variable "oidc_credential_source" {
  description = <<-EOT
    Credential source path (file mode) or URL (url mode) that `gcloud` should
    read the OIDC token from. Only used when provider_type = "oidc". Example:
    `/var/run/secrets/cudly/token` or `https://cudly.example.com/oidc/token`.
  EOT
  type        = string
  default     = ""
}

variable "oidc_credential_source_format" {
  description = <<-EOT
    Format of the credential source response when provider_type = "oidc".
    Must be "text" (raw token) or "json" (JSON-wrapped). Ignored when
    provider_type = "aws".
  EOT
  type        = string
  default     = "text"

  validation {
    condition     = contains(["text", "json"], var.oidc_credential_source_format)
    error_message = "oidc_credential_source_format must be \"text\" or \"json\"."
  }
}

variable "service_account_email" {
  description = <<-EOT
    Email of the GCP service account CUDly impersonates via WIF.
    Leave empty (default) to have Terraform create a dedicated SA named
    var.service_account_id and grant it var.service_account_project_roles.
    Set to an existing SA email to reuse a pre-provisioned SA instead —
    in that case grant roles yourself.
  EOT
  type        = string
  default     = ""
}

variable "service_account_id" {
  description = <<-EOT
    Short ID (local-part before @) for the service account Terraform
    creates when var.service_account_email is empty. Ignored otherwise.
  EOT
  type        = string
  default     = "cudly-wif"
}

variable "service_account_project_roles" {
  description = <<-EOT
    Project-level built-in roles granted to the service account
    Terraform creates when var.service_account_email is empty. Ignored
    otherwise.

    Default: roles/compute.viewer — read access to regions/zones/
    instance types/commitments, which CUDly's collection pipeline
    needs to match commitments to workloads. Paired with the custom
    write-side role (var.custom_role_id). This split mirrors the
    convention in terraform/modules/compute/gcp/cloud-run, keeping
    the two modules' definitions of cudlyCommitmentWriter identical
    so one doesn't stomp the other on apply.

    Note: GCP's built-in commitment/billing roles (e.g.
    roles/commerceorgpolicy.commitmentAdmin, roles/billing.viewer) are
    organization- or billing-account-scoped and will 400 if granted at
    project scope. Grant those at the correct scope outside this
    module.
  EOT
  type        = list(string)
  default = [
    "roles/compute.viewer",
  ]
}

variable "custom_role_id" {
  description = <<-EOT
    Project-scoped custom role ID Terraform creates (when var.service_account_email
    is empty) and binds to the service account. The role carries the minimum
    permissions required to purchase and manage Compute Engine CUDs on behalf
    of CUDly. Ignored when service_account_email is set to an existing SA.
  EOT
  type        = string
  default     = "cudlyCommitmentWriter"
}

variable "custom_role_permissions" {
  description = <<-EOT
    Permissions bundled into the custom role created when
    var.service_account_email is empty. Defaults match the definition
    in terraform/modules/compute/gcp/cloud-run (which owns this role
    when CUDly itself is deployed to the same project), so both
    modules stay in lockstep and a CUDly deploy can't stomp the
    federation bundle's role on apply.

    Read-side permissions (regions/zones/machineTypes/commitments
    .list/.get) come from roles/compute.viewer — see
    var.service_account_project_roles — not from this role.
  EOT
  type        = list(string)
  default = [
    "compute.commitments.create",
    "compute.commitments.update",
  ]
}

variable "cudly_api_url" {
  description = "CUDly API base URL for automatic account registration. Leave empty to skip registration."
  type        = string
  default     = ""
}

variable "account_name" {
  description = "Human-readable name for this account in CUDly."
  type        = string
  default     = ""
}

variable "contact_email" {
  description = "Contact email for registration notifications."
  type        = string
  default     = ""
}
