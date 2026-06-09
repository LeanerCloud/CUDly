# AWS CDN Module Variables

variable "project_name" {
  description = "Project name for resource naming"
  type        = string
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "origin_domain_name" {
  description = "Domain name of the compute origin (Lambda Function URL or Fargate ALB, without https://)"
  type        = string
}

variable "origin_protocol" {
  description = "Protocol policy for the origin (https-only for Lambda URLs, http-only for ALB)"
  type        = string
  default     = "https-only"
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

variable "enable_oac" {
  description = <<-EOT
    When true, create a CloudFront Origin Access Control (OAC) of type "lambda"
    and attach it to the compute origin in the distribution. Required when the
    Lambda Function URL uses authorization_type = "AWS_IAM": CloudFront will
    SigV4-sign every forwarded request so the Function URL only accepts traffic
    from this distribution.
    Set to false when the origin is a Fargate ALB or when the Lambda uses
    authorization_type = "NONE".
  EOT
  type        = bool
  default     = false
}

variable "tags" {
  description = "Additional tags for resources"
  type        = map(string)
  default     = {}
}
