variable "stack_name" {
  description = "Name of the stack"
  type        = string
}

variable "region" {
  description = "AWS region"
  type        = string
}

variable "use_existing_vpc" {
  description = "Use an existing VPC instead of creating a new one"
  type        = bool
  default     = false
}

variable "existing_vpc_id" {
  description = "ID of existing VPC to use (if use_existing_vpc = true)"
  type        = string
  default     = ""
}

variable "use_default_vpc" {
  description = "Use the default VPC (overrides use_existing_vpc)"
  type        = bool
  default     = false
}

variable "existing_public_subnet_ids" {
  description = "List of existing public subnet IDs (if using existing VPC)"
  type        = list(string)
  default     = []
}

variable "existing_private_subnet_ids" {
  description = "List of existing private subnet IDs (if using existing VPC)"
  type        = list(string)
  default     = []
}

variable "vpc_cidr" {
  description = "CIDR block for VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "az_count" {
  description = "Number of availability zones (2-3 recommended)"
  type        = number
  default     = 2

  validation {
    condition     = var.az_count >= 2 && var.az_count <= 3
    error_message = "AZ count must be between 2 and 3 for high availability."
  }
}

variable "create_alb_security_group" {
  description = "Create security group for Application Load Balancer (Fargate)"
  type        = bool
  default     = false
}

variable "enable_flow_logs" {
  description = "Enable VPC Flow Logs for debugging"
  type        = bool
  default     = false
}

variable "flow_logs_retention_days" {
  description = "VPC Flow Logs retention in days"
  type        = number
  default     = 7
}

variable "enable_nat_gateway" {
  description = "Enable fck-nat instance for IPv4 egress (required for services without IPv6 support like SES) - costs ~$3/month"
  type        = bool
  default     = false
}

variable "enable_ipv6" {
  description = "Enable IPv6 on the VPC (assign_generated_ipv6_cidr_block)"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
