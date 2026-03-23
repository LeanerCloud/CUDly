variable "subscription_id" {
  description = "Azure subscription ID"
  type        = string
  default     = "24d185cc-6437-4582-8db8-4c84f3f7fa5a"
}

variable "assignee_object_id" {
  description = "Azure AD object ID of the user/SP to assign the deploy role to"
  type        = string
  default     = "2187c37b-8380-40f3-9403-955a6040dad1"
}
