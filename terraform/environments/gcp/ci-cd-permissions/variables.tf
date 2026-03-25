variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "github_repo" {
  description = "GitHub repository (owner/name) whose Actions workflows may impersonate the deploy SA via Workload Identity Federation (e.g. 'LeanerCloud/CUDly'). Leave empty to skip WIF setup."
  type        = string
  default     = "LeanerCloud/CUDly"
}
