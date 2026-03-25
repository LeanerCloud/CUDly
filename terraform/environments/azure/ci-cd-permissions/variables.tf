variable "subscription_id" {
  description = "Azure subscription ID"
  type        = string
}

variable "github_repo" {
  description = "GitHub repository (owner/name) whose Actions workflows may authenticate via federated identity credentials (e.g. 'LeanerCloud/CUDly'). Leave empty to skip federated credential setup."
  type        = string
  default     = "LeanerCloud/CUDly"
}
