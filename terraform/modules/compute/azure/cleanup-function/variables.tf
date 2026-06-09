variable "resource_group_name" {
  description = "Name of the resource group"
  type        = string
}

variable "location" {
  description = "Azure region"
  type        = string
}

variable "function_app_name" {
  description = "Name of the Function App"
  type        = string
}

variable "storage_account_name" {
  description = "Name of the storage account for Function App"
  type        = string
}

variable "image_uri" {
  description = "Container image URI for the cleanup function"
  type        = string
}

variable "db_host" {
  description = "Database host (Azure Database for PostgreSQL FQDN)"
  type        = string
}

variable "db_password_secret_uri" {
  description = "Azure Key Vault secret URI containing the database password"
  type        = string
}

variable "key_vault_id" {
  description = "ID of the Key Vault containing secrets"
  type        = string
}

variable "subnet_id" {
  description = "Subnet ID for VNet integration"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
