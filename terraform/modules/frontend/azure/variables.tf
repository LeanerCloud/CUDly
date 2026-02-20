# Azure Frontend Module Variables

variable "project_name" {
  description = "Project name for resource naming"
  type        = string
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
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

variable "storage_account_name" {
  description = "Storage account name (must be globally unique, 3-24 lowercase alphanumeric)"
  type        = string
}

variable "api_hostname" {
  description = "Hostname of the Container App API (without https://)"
  type        = string
}

variable "cdn_sku" {
  description = "CDN SKU (Standard_Microsoft, Standard_Akamai, Standard_Verizon, Premium_Verizon)"
  type        = string
  default     = "Standard_Microsoft"
}

variable "custom_domain" {
  description = "Custom domain name for the CDN endpoint"
  type        = string
  default     = ""
}

variable "domain_names" {
  description = "List of custom domain names for CDN endpoint (first domain is primary)"
  type        = list(string)
  default     = []
}

variable "subdomain_zone_name" {
  description = "Azure DNS subdomain zone name to create (e.g., cudly.leanercloud.com). Leave empty to skip zone creation."
  type        = string
  default     = ""
}

variable "use_front_door" {
  description = "Use Azure Front Door instead of CDN (for premium features)"
  type        = bool
  default     = false
}

variable "action_group_id" {
  description = "Azure Monitor action group ID for alerts"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Additional tags for resources"
  type        = map(string)
  default     = {}
}

variable "enable_frontend_build" {
  description = "Enable frontend build and deployment (set to false to skip npm build and file uploads)"
  type        = bool
  default     = true
}

variable "frontend_path" {
  description = "Path to frontend directory relative to Terraform root (default assumes terraform/environments/<provider> structure)"
  type        = string
  default     = "../../../frontend"
}
