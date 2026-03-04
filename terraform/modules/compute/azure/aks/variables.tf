# Azure AKS Module Variables

variable "project_name" {
  description = "Project name for resource naming"
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
  description = "Azure location"
  type        = string
}

variable "vnet_subnet_id" {
  description = "Subnet ID for AKS nodes"
  type        = string
}

variable "image_name" {
  description = "Container image name (without registry)"
  type        = string
}

variable "image_tag" {
  description = "Container image tag"
  type        = string
  default     = "latest"
}

variable "kubernetes_version" {
  description = "Kubernetes version"
  type        = string
  default     = "1.28"
}

variable "node_count" {
  description = "Number of nodes in the default node pool"
  type        = number
  default     = 2
}

variable "node_vm_size" {
  description = "VM size for nodes"
  type        = string
  default     = "Standard_D2s_v3"
}

variable "min_node_count" {
  description = "Minimum node count for auto-scaling"
  type        = number
  default     = 1
}

variable "max_node_count" {
  description = "Maximum node count for auto-scaling"
  type        = number
  default     = 10
}

variable "database_host" {
  description = "PostgreSQL server FQDN"
  type        = string
}

variable "database_name" {
  description = "Database name"
  type        = string
}

variable "database_username" {
  description = "Database username"
  type        = string
}

variable "database_password_secret_name" {
  description = "Name of Key Vault secret containing database password"
  type        = string
}

variable "key_vault_id" {
  description = "Key Vault ID for secrets"
  type        = string
}

variable "enable_auto_scaling" {
  description = "Enable cluster auto-scaling"
  type        = bool
  default     = true
}

variable "enable_azure_policy" {
  description = "Enable Azure Policy add-on"
  type        = bool
  default     = false
}

variable "enable_log_analytics" {
  description = "Enable Log Analytics"
  type        = bool
  default     = true
}

variable "deploy_kubernetes_resources" {
  description = "Deploy kubernetes resources (namespace, deployment, service, etc). Requires kubernetes/helm providers to be configured at root level. Set to false to only create the AKS cluster."
  type        = bool
  default     = false
}

variable "admin_email" {
  description = "Administrator email address"
  type        = string
  default     = ""
}

variable "admin_password_secret_name" {
  description = "Key Vault secret name containing admin password"
  type        = string
  default     = ""
}

variable "auto_migrate" {
  description = "Automatically run database migrations on startup"
  type        = bool
  default     = true
}

variable "allowed_origins" {
  description = "List of allowed CORS origins"
  type        = list(string)
  default     = ["*"]
}

variable "additional_env_vars" {
  description = "Additional environment variables"
  type        = map(string)
  default     = {}
}

variable "key_vault_uri" {
  description = "Key Vault URI for secrets"
  type        = string
  default     = ""
}

variable "service_cidr" {
  description = "Kubernetes service CIDR"
  type        = string
  default     = "10.0.0.0/16"
}

variable "dns_service_ip" {
  description = "Kubernetes DNS service IP (must be within service_cidr)"
  type        = string
  default     = "10.0.0.10"
}

variable "nginx_ingress_version" {
  description = "NGINX Ingress Controller Helm chart version"
  type        = string
  default     = "4.8.0"
}

variable "tags" {
  description = "Additional tags for resources"
  type        = map(string)
  default     = {}
}
