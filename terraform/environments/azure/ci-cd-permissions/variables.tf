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
    presents it, and prefer restricting the branch in the workflow or via the
    environment's deployment_branch_policy.

    `prod` is deliberately absent: no Azure job binds to it today, and adding it
    would mint a credential for a path with no consumer.
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
