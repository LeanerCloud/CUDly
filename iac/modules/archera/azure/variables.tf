variable "enable_archera" {
  description = <<-EOT
    When true, provision the Archera custom RBAC role and role assignment so
    Archera can underwrite commitment-overuse insurance. Leave false (default)
    unless you are enrolled with Archera.
    PROVISIONAL — confirm scope with Archera before enabling.
  EOT
  type        = bool
  default     = false
}

variable "subscription_id" {
  description = "Azure subscription ID to scope the Archera role to. Required when enable_archera = true."
  type        = string
}

variable "archera_azure_sp_object_id" {
  description = "Object ID of the service principal Archera provides during onboarding. NOT the Application/Client ID — use the Object ID from Azure Portal. Required when enable_archera = true."
  type        = string
  default     = ""

  validation {
    condition     = !var.enable_archera || trimspace(var.archera_azure_sp_object_id) != ""
    error_message = "archera_azure_sp_object_id must be set (non-empty) when enable_archera = true."
  }
}

variable "enable_archera_purchase_actions" {
  description = <<-EOT
    When true (and enable_archera = true), include reservation write actions in
    the Archera custom role. Only enable after confirming Archera requires
    customer approval before executing purchases. Default false.
  EOT
  type        = bool
  default     = false
}
