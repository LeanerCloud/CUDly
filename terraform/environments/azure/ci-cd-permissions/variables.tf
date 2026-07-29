variable "subscription_id" {
  description = "Azure subscription ID"
  type        = string
}

variable "github_repo" {
  description = "GitHub repository (owner/name) whose Actions workflows may authenticate via federated identity credentials (e.g. 'LeanerCloud/CUDly'). Leave empty to skip federated credential setup."
  type        = string
  default     = "LeanerCloud/CUDly"
}

variable "github_environments" {
  description = <<-EOT
    GitHub deployment environment names whose jobs may authenticate via federated
    identity credentials. A job carrying `environment: <name>` presents the OIDC
    subject repo:<github_repo>:environment:<name>, so a name absent from this list
    cannot authenticate (AADSTS70021).

    NOTE the subject is ref-agnostic: it does not encode the branch, so a
    credential here lets ANY branch that can reach a job bound to that environment
    obtain the deploy service principal. Add a name only once a workflow actually
    presents it, and restrict the branch via the environment's
    deployment_branch_policy — an in-workflow ref check does not bind, because
    workflow_dispatch runs the file as it exists on the dispatched ref.

    This list is NOT a complete enumeration of the environment subjects this repo
    presents to Azure. It covers exactly the two destroy jobs bound by
    cleanup-staging.yml (`staging`) and destroy-fargate-dev.yml (`dev`).

    Knowingly NOT covered, tracked in #1648 — these Azure jobs bind to compound
    environment names and therefore still fail with AADSTS70021:
      - rollback.yml           -> azure-{dev,staging,prod}-rollback
      - database-migration.yml -> azure-db-{dev,staging,prod}

    They are excluded here rather than fixed because each needs its environment
    created and gated first; minting a credential for an ungated environment that
    does not yet exist would widen access without adding a control. Do not read
    the absence of a name as "no job uses it".
  EOT
  type        = list(string)
  default     = ["dev", "staging"]

  validation {
    condition     = length(var.github_environments) == length(distinct(var.github_environments))
    error_message = "github_environments must not contain duplicates; toset() would silently collapse them, so a duplicate indicates a typo."
  }

  validation {
    condition     = alltrue([for e in var.github_environments : trimspace(e) != ""])
    error_message = "github_environments entries must be non-empty; an empty name yields the unmatchable subject repo:<repo>:environment:."
  }
}
