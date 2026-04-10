variable "source_account_id" {
  description = "AWS account ID where CUDly runs (the source account that will assume this role)."
  type        = string
}

variable "role_name" {
  description = "Name of the IAM role created in this target account."
  type        = string
  default     = "CUDly-CrossAccount"
}
