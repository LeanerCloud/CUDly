variable "repository_name" {
  description = "Name of the ECR repository"
  type        = string
}

variable "keep_image_count" {
  description = "Number of tagged images to keep"
  type        = number
  default     = 10
}

variable "untagged_expiry_days" {
  description = "Days after which untagged images are deleted"
  type        = number
  default     = 7
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
