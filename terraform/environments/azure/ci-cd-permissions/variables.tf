variable "subscription_id" {
  description = "Azure subscription ID"
  type        = string
}

variable "assignee_object_id" {
  description = "Azure AD object ID of the service principal to assign the deploy role to"
  type        = string
}
