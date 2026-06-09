variable "app_name" {
  description = "Application name"
  type        = string
}

variable "environment" {
  description = "Environment name (dev/staging/prod)"
  type        = string
}

variable "resource_group_name" {
  description = "Resource group name"
  type        = string
}

variable "location" {
  description = "Azure region"
  type        = string
}

variable "vnet_cidr" {
  description = "VNet CIDR block"
  type        = string
  default     = "10.0.0.0/16"
}

variable "container_apps_subnet_cidr" {
  description = "Container Apps subnet CIDR block"
  type        = string
  default     = "10.0.1.0/24"
}

variable "database_subnet_cidr" {
  description = "Database subnet CIDR block"
  type        = string
  default     = "10.0.2.0/24"
}

variable "private_subnet_cidr" {
  description = "Private subnet CIDR block (if created)"
  type        = string
  default     = "10.0.3.0/24"
}

variable "create_private_subnet" {
  description = "Create additional private subnet"
  type        = bool
  default     = false
}

variable "allow_inbound_from_internet" {
  description = "Allow inbound HTTPS traffic from internet to Container Apps"
  type        = bool
  default     = true
}

variable "create_route_table" {
  description = "Create custom route table"
  type        = bool
  default     = false
}

variable "create_log_analytics" {
  description = "Create Log Analytics workspace"
  type        = bool
  default     = true
}

variable "log_retention_days" {
  description = "Log Analytics retention in days"
  type        = number
  default     = 30

  validation {
    condition     = var.log_retention_days >= 30 && var.log_retention_days <= 730
    error_message = "Log retention must be between 30 and 730 days."
  }
}

variable "create_network_watcher" {
  description = "Create Network Watcher for diagnostics"
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}
