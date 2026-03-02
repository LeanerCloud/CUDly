# ==============================================
# Networking Outputs
# ==============================================

output "vpc_id" {
  description = "VPC ID"
  value       = module.networking.vpc_id
}

output "private_subnet_ids" {
  description = "Private subnet IDs"
  value       = module.networking.private_subnet_ids
}

output "public_subnet_ids" {
  description = "Public subnet IDs"
  value       = module.networking.public_subnet_ids
}

output "vpc_ipv6_cidr" {
  description = "VPC IPv6 CIDR block"
  value       = module.networking.vpc_ipv6_cidr
}

# ==============================================
# Database Outputs
# ==============================================

output "database_endpoint" {
  description = "Database endpoint (use proxy endpoint if available)"
  value       = module.database.proxy_endpoint != null ? module.database.proxy_endpoint : module.database.instance_address
}

output "database_instance_endpoint" {
  description = "RDS instance endpoint"
  value       = module.database.instance_endpoint
}

output "database_proxy_endpoint" {
  description = "RDS Proxy endpoint (recommended for Lambda)"
  value       = module.database.proxy_endpoint
}

output "database_name" {
  description = "Database name"
  value       = module.database.database_name
}

output "database_password_secret_arn" {
  description = "ARN of database password secret"
  value       = module.database.password_secret_arn
  sensitive   = true
}

# ==============================================
# Compute Outputs (Platform-specific)
# ==============================================

# Lambda Outputs
output "lambda_function_name" {
  description = "Lambda function name"
  value       = var.compute_platform == "lambda" ? module.compute_lambda[0].function_name : null
}

output "lambda_function_arn" {
  description = "Lambda function ARN"
  value       = var.compute_platform == "lambda" ? module.compute_lambda[0].function_arn : null
}

output "lambda_function_url" {
  description = "Lambda Function URL"
  value       = var.compute_platform == "lambda" ? module.compute_lambda[0].function_url : null
}

output "lambda_role_arn" {
  description = "Lambda IAM role ARN"
  value       = var.compute_platform == "lambda" ? module.compute_lambda[0].role_arn : null
}

output "lambda_log_group_name" {
  description = "Lambda CloudWatch log group name"
  value       = var.compute_platform == "lambda" ? module.compute_lambda[0].log_group_name : null
}

# Fargate Outputs
output "fargate_cluster_name" {
  description = "ECS cluster name"
  value       = var.compute_platform == "fargate" ? module.compute_fargate[0].cluster_name : null
}

output "fargate_service_name" {
  description = "ECS service name"
  value       = var.compute_platform == "fargate" ? module.compute_fargate[0].service_name : null
}

output "fargate_alb_dns_name" {
  description = "Application Load Balancer DNS name"
  value       = var.compute_platform == "fargate" ? module.compute_fargate[0].alb_dns_name : null
}

output "fargate_api_url" {
  description = "Fargate API URL"
  value       = var.compute_platform == "fargate" ? module.compute_fargate[0].api_url : null
}

output "fargate_log_group_name" {
  description = "Fargate CloudWatch log group name"
  value       = var.compute_platform == "fargate" ? module.compute_fargate[0].log_group_name : null
}

# Unified API endpoint output
output "api_endpoint" {
  description = "API endpoint URL (Lambda or Fargate)"
  value       = var.compute_platform == "lambda" ? module.compute_lambda[0].function_url : module.compute_fargate[0].api_url
}

# ==============================================
# Secrets Outputs
# ==============================================

output "jwt_secret_arn" {
  description = "JWT secret ARN"
  value       = module.secrets.jwt_secret_arn
  sensitive   = true
}

output "session_secret_arn" {
  description = "Session secret ARN"
  value       = module.secrets.session_secret_arn
  sensitive   = true
}

output "secret_read_policy_arn" {
  description = "IAM policy ARN for reading secrets"
  value       = module.secrets.secret_read_policy_arn
}

# ==============================================
# Connection Information
# ==============================================

output "connection_info" {
  description = "Connection information for the application"
  value = {
    api_endpoint = var.compute_platform == "lambda" ? module.compute_lambda[0].function_url : null
    db_endpoint  = module.database.proxy_endpoint != null ? module.database.proxy_endpoint : module.database.instance_address
    db_name      = module.database.database_name
    environment  = var.environment
    region       = var.region
  }
  sensitive = true
}

# ==============================================
# Deployment Information
# ==============================================

output "deployment_info" {
  description = "Deployment configuration summary"
  value = {
    stack_name        = local.stack_name
    environment       = var.environment
    region            = var.region
    compute_platform  = var.compute_platform
    vpc_cidr          = var.vpc_cidr
    az_count          = var.az_count
    nat_enabled       = false # Using IPv6 dual-stack, no NAT Gateway needed
    vpc_endpoints     = false # Not needed with IPv6
    db_instance_class = var.database_instance_class
    db_storage_gb     = var.database_allocated_storage
  }
}

# ==============================================
# Frontend Outputs
# ==============================================

output "frontend_url" {
  description = "Frontend URL"
  value       = var.enable_frontend ? module.frontend[0].frontend_url : null
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID for cache invalidation"
  value       = var.enable_frontend ? module.frontend[0].cloudfront_distribution_id : null
}

output "cloudfront_domain_name" {
  description = "CloudFront distribution domain name"
  value       = var.enable_frontend ? module.frontend[0].cloudfront_domain_name : null
}

output "frontend_bucket" {
  description = "S3 bucket name for frontend files"
  value       = var.enable_frontend ? module.frontend[0].s3_bucket_id : null
}

# ==============================================
# DNS Outputs
# ==============================================

output "subdomain_zone_nameservers" {
  description = "Nameservers for subdomain zone (add these as NS records in parent zone)"
  value       = local.subdomain_zone_nameservers
}

output "subdomain_zone_id" {
  description = "Route53 zone ID for subdomain"
  value       = local.subdomain_zone_id
}

# ==============================================
# Quick Start Commands
# ==============================================

output "quick_start_commands" {
  description = "Quick start commands for common operations"
  value       = <<-EOT
    # Access the frontend
    ${var.enable_frontend ? "open ${module.frontend[0].frontend_url}" : "# Frontend not enabled"}

    # Test the API health check
    curl ${var.compute_platform == "lambda" ? module.compute_lambda[0].function_url : "N/A"}/health

    # View Lambda logs (if using Lambda)
    ${var.compute_platform == "lambda" ? "aws logs tail ${module.compute_lambda[0].log_group_name} --follow" : "N/A"}

    # Connect to database (requires bastion or VPN)
    psql "postgresql://${var.database_username}@${module.database.proxy_endpoint != null ? module.database.proxy_endpoint : module.database.instance_address}:5432/${module.database.database_name}?sslmode=require"

    # Get database password
    aws secretsmanager get-secret-value --secret-id ${module.database.password_secret_name} --query SecretString --output text

    # Update Lambda function image
    ${var.compute_platform == "lambda" ? "aws lambda update-function-code --function-name ${module.compute_lambda[0].function_name} --image-uri NEW_IMAGE_URI" : "N/A"}

    # Deploy frontend
    ${var.enable_frontend ? "cd ../../../../frontend && ./deploy.sh -p aws -e ${var.environment} -b ${module.frontend[0].s3_bucket_id} -d ${module.frontend[0].cloudfront_distribution_id}" : "# Frontend not enabled"}
  EOT
}
