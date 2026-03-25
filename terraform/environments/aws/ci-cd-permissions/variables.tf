variable "aws_region" {
  description = "AWS region for the provider and state backend"
  type        = string
  default     = "us-east-1"
}

variable "trust_principal" {
  description = "IAM principal allowed to assume the deploy role, relative to the account ARN (e.g. 'user/alice', 'role/GitHubActionsRole')"
  type        = string
}
