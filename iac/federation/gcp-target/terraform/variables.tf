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

variable "aws_role_name" {
  description = "AWS IAM role name to restrict trust to (e.g. 'CUDly-Execution'). Only used when provider_type is 'aws'. If empty, all roles in the AWS account are trusted."
  type        = string
  default     = ""
}

variable "oidc_subject" {
  description = "OIDC subject claim to restrict trust to. Only used when provider_type is 'oidc'. If empty, all subjects from the issuer are trusted."
  type        = string
  default     = ""
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
    Extra project-level built-in roles granted to the service account
    Terraform creates when var.service_account_email is empty. Ignored
    otherwise.

    The bundle already creates a dedicated custom role
    (var.custom_role_id, default "cudlyCommitmentWriter") holding the
    minimum Compute commitment permissions and grants it to the SA —
    this list is for any additional built-in roles the operator wants
    to layer on top. Leave empty (default) for least privilege.

    Note: GCP's built-in commitment/billing roles (e.g.
    roles/commerceorgpolicy.commitmentAdmin, roles/billing.viewer) are
    organization- or billing-account-scoped and will 400 if granted at
    project scope. Grant those at the correct scope outside this
    module.
  EOT
  type        = list(string)
  default     = []
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
    var.service_account_email is empty. Defaults cover the full
    purchase/update/read lifecycle of Compute Engine commitments and
    the read-only enumeration CUDly performs while matching commitments
    to workloads (regions, zones, machine types).
  EOT
  type        = list(string)
  default = [
    "compute.commitments.create",
    "compute.commitments.update",
    "compute.commitments.get",
    "compute.commitments.list",
    "compute.regions.list",
    "compute.zones.list",
    "compute.machineTypes.list",
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
