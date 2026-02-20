# AWS Frontend Module Variables

variable "project_name" {
  description = "Project name for resource naming"
  type        = string
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "bucket_name" {
  description = "S3 bucket name for frontend files"
  type        = string
}

variable "api_domain_name" {
  description = "Domain name of the Lambda Function URL (without https://)"
  type        = string
}

variable "cloudfront_secret" {
  description = "Secret header value to verify requests from CloudFront"
  type        = string
  sensitive   = true
}

variable "domain_names" {
  description = "Custom domain names for the CloudFront distribution"
  type        = list(string)
  default     = []
}

variable "acm_certificate_arn" {
  description = "ARN of ACM certificate for custom domain (must be in us-east-1)"
  type        = string
  default     = null
}

variable "route53_zone_id" {
  description = "Route53 hosted zone ID for DNS record (set to null to skip DNS record creation)"
  type        = string
  default     = null
}

variable "price_class" {
  description = "CloudFront price class (PriceClass_All, PriceClass_200, PriceClass_100)"
  type        = string
  default     = "PriceClass_100"
}

variable "waf_acl_arn" {
  description = "ARN of WAF Web ACL to associate with CloudFront"
  type        = string
  default     = ""
}

variable "geo_restriction_type" {
  description = "Geo restriction type (none, whitelist, blacklist)"
  type        = string
  default     = "none"
}

variable "geo_restriction_locations" {
  description = "List of country codes for geo restriction"
  type        = list(string)
  default     = []
}

variable "alarm_sns_topic_arn" {
  description = "SNS topic ARN for CloudWatch alarms"
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
