variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "location" {
  description = "Repository location (e.g., us-central1)"
  type        = string
  default     = "us-central1"
}

variable "repository_id" {
  description = "Repository ID"
  type        = string
}

variable "keep_image_count" {
  description = "Number of recent versions to keep"
  type        = number
  default     = 10
}

variable "tagged_expiry_days" {
  description = "Days after which old tagged images are deleted"
  type        = number
  default     = 30
}

variable "untagged_expiry_days" {
  description = "Days after which untagged images are deleted"
  type        = number
  default     = 7
}

variable "labels" {
  description = "Labels to apply to all resources"
  type        = map(string)
  default     = {}
}
