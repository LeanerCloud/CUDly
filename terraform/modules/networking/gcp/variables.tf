variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "service_name" {
  description = "Service name"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
}

variable "subnet_cidr" {
  description = "CIDR range for private subnet"
  type        = string
  default     = "10.0.0.0/24"
}

variable "connector_subnet_cidr" {
  description = "CIDR range for VPC Access Connector subnet (must be /28)"
  type        = string
  default     = "10.8.0.0/28"
}

variable "secondary_ranges" {
  description = "Secondary IP ranges for GKE (if needed)"
  type = list(object({
    name = string
    cidr = string
  }))
  default = []
}

variable "enable_nat_logging" {
  description = "Enable Cloud NAT logging"
  type        = bool
  default     = false
}

variable "connector_machine_type" {
  description = "Machine type for VPC Access Connector (e2-micro, e2-standard-4, f1-micro)"
  type        = string
  default     = "e2-micro"
}

variable "connector_min_instances" {
  description = "Minimum instances for VPC Access Connector"
  type        = number
  default     = 2
}

variable "connector_max_instances" {
  description = "Maximum instances for VPC Access Connector"
  type        = number
  default     = 3
}

variable "enable_iap_ssh" {
  description = "Enable SSH access via Identity-Aware Proxy"
  type        = bool
  default     = false
}
