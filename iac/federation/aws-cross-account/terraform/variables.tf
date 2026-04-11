variable "source_account_id" {
  description = "AWS account ID where CUDly runs (the source account that will assume this role)."
  type        = string
}

variable "role_name" {
  description = "Name of the IAM role created in this target account."
  type        = string
  default     = "CUDly-CrossAccount"
}

variable "cudly_execution_role_arn" {
  description = <<-EOT
    ARN of the IAM role that CUDly uses to execute (e.g. the Lambda execution role or
    the ECS task role in the source account). When provided, the trust policy is scoped
    to this specific principal instead of the entire source account (:root).
    Leave empty to allow any principal in source_account_id to assume this role.
  EOT
  type        = string
  default     = ""
}
