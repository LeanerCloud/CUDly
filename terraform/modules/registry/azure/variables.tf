variable "resource_group_name" {
  description = "Name of the resource group"
  type        = string
}

variable "location" {
  description = "Azure region"
  type        = string
}

variable "acr_name" {
  description = "Name of the Azure Container Registry (must be globally unique)"
  type        = string
}

variable "sku" {
  description = "SKU for ACR (Basic, Standard, Premium)"
  type        = string
  default     = "Standard"
}

variable "keep_image_count" {
  description = "Number of images to keep"
  type        = number
  default     = 10
}

variable "image_retention_days" {
  description = "Days to retain images"
  type        = number
  default     = 30
}

variable "enable_admin_user" {
  description = "Enable admin user (not recommended for production)"
  type        = bool
  default     = false
}

variable "container_app_identity_principal_id" {
  description = "Principal ID of the container app managed identity"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
