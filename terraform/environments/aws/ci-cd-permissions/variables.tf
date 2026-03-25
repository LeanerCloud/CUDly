variable "aws_region" {
  description = "AWS region for the provider and state backend"
  type        = string
  default     = "us-east-1"
}

variable "trust_principal" {
  description = "IAM principal allowed to assume the deploy role for local deployments, relative to the account ARN (e.g. 'user/alice', 'role/AdminRole'). Leave empty if only GitHub Actions OIDC is needed."
  type        = string
  default     = ""
}

variable "github_repo" {
  description = "GitHub repository (owner/name) whose Actions workflows may assume the deploy role via OIDC (e.g. 'LeanerCloud/CUDly'). Leave empty to skip OIDC setup."
  type        = string
  default     = "LeanerCloud/CUDly"
}
