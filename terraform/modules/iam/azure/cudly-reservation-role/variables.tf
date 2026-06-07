variable "scope" {
  description = "Subscription resource ID used as the role definition scope and the base of assignable_scopes (e.g. data.azurerm_subscription.current.id)."
  type        = string
}

variable "name_suffix" {
  description = "Suffix appended to the role display name to keep customer and host role definitions distinct within the same Azure AD tenant (e.g. the subscription ID)."
  type        = string
}

variable "include_capacity_provider_scope" {
  description = "When true, adds the tenant-root \"/providers/Microsoft.Capacity\" scope to assignable_scopes. Leave false (the default) for subscription-scoped principals: registering an assignable scope above the subscription requires tenant-root authority and otherwise 403s."
  type        = bool
  default     = false
}
