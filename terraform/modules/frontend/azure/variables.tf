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

variable "api_hostname" {
  description = "Hostname of the Container App (without https://). All traffic is routed here."
  type        = string
}

variable "cdn_sku" {
  description = "CDN SKU (Standard_Microsoft, Standard_Akamai, Standard_Verizon, Premium_Verizon)"
  type        = string
  default     = "Standard_Microsoft"
}

variable "custom_domain" {
  description = "Custom domain name for the classic CDN endpoint"
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
  description = "Use Azure Front Door instead of classic CDN"
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
