variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "github_repo" {
  description = "GitHub repository (owner/name) whose Actions workflows may impersonate the deploy SA via Workload Identity Federation (e.g. 'LeanerCloud/CUDly'). Leave empty to skip WIF setup."
  type        = string
  default     = "LeanerCloud/CUDly"
}

variable "deploy_ref" {
  description = "Git ref allowed to deploy (e.g. refs/heads/main). Only workflows from this exact ref can impersonate the deploy SA. PRs, tags, and other branches are blocked."
  type        = string
  default     = "refs/heads/main"
}
