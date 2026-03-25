variable "subscription_id" {
  description = "Azure subscription ID"
  type        = string
  default     = "24d185cc-6437-4582-8db8-4c84f3f7fa5a"
}

variable "assignee_object_id" {
  description = "Azure AD object ID of the user/SP to assign the deploy role to"
  type        = string
  # cudly-terraform-deploy SP: a3e02cbb-a854-4f8a-83a4-6c3388eaefa6
  default = "a3e02cbb-a854-4f8a-83a4-6c3388eaefa6"
}
