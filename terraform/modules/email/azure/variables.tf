variable "app_name" {
  description = "Application name"
  type        = string
}

variable "resource_group_name" {
  description = "Resource group name"
  type        = string
}

variable "data_location" {
  description = "Data residency location (United States, Europe, etc.)"
  type        = string
  default     = "United States"
}

variable "use_azure_managed_domain" {
  description = "Use Azure-managed domain (*.azurecomm.net) - recommended for dev/test"
  type        = bool
  default     = true
}

variable "custom_domain_name" {
  description = "Custom domain name for email (e.g., cudly.leanercloud.com) - for production"
  type        = string
  default     = ""
}

variable "key_vault_name" {
  description = "Key Vault name for storing SMTP credentials"
  type        = string
}

variable "auto_generate_smtp_credentials" {
  description = "Attempt to auto-retrieve SMTP configuration via Azure CLI"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}
