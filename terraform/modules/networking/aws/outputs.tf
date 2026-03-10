output "vpc_id" {
  description = "VPC ID"
  value       = local.vpc_id
}

output "vpc_cidr" {
  description = "VPC CIDR block (IPv4)"
  value       = local.vpc_cidr
}

output "vpc_ipv6_cidr" {
  description = "VPC CIDR block (IPv6)"
  value       = local.vpc_ipv6_cidr
}

output "public_subnet_ids" {
  description = "List of public subnet IDs"
  value       = local.public_subnet_ids
}

output "private_subnet_ids" {
  description = "List of private subnet IDs"
  value       = local.private_subnet_ids
}

output "public_subnet_cidrs" {
  description = "List of public subnet CIDR blocks (IPv4)"
  value       = local.create_vpc ? aws_subnet.public[*].cidr_block : []
}

output "private_subnet_cidrs" {
  description = "List of private subnet CIDR blocks (IPv4)"
  value       = local.create_vpc ? aws_subnet.private[*].cidr_block : []
}

output "public_subnet_ipv6_cidrs" {
  description = "List of public subnet CIDR blocks (IPv6)"
  value       = local.create_vpc ? aws_subnet.public[*].ipv6_cidr_block : []
}

output "private_subnet_ipv6_cidrs" {
  description = "List of private subnet CIDR blocks (IPv6)"
  value       = local.create_vpc ? aws_subnet.private[*].ipv6_cidr_block : []
}

output "availability_zones" {
  description = "List of availability zones used"
  value       = local.create_vpc ? aws_subnet.private[*].availability_zone : []
}

output "database_security_group_id" {
  description = "Security group ID for database access"
  value       = aws_security_group.database.id
}

output "alb_security_group_id" {
  description = "Security group ID for ALB (if created)"
  value       = var.create_alb_security_group ? aws_security_group.alb[0].id : null
}

output "internet_gateway_id" {
  description = "Internet Gateway ID"
  value       = local.create_vpc ? aws_internet_gateway.main[0].id : null
}

output "egress_only_gateway_id" {
  description = "Egress-Only Internet Gateway ID (IPv6)"
  value       = local.create_vpc && var.enable_ipv6 ? aws_egress_only_internet_gateway.main[0].id : null
}

output "public_route_table_id" {
  description = "Public route table ID"
  value       = local.create_vpc ? aws_route_table.public[0].id : null
}

output "private_route_table_ids" {
  description = "List of private route table IDs"
  value       = local.create_vpc ? aws_route_table.private[*].id : []
}

# Convenience output for Lambda module
output "lambda_vpc_config" {
  description = "VPC configuration object for Lambda module"
  value = {
    vpc_id                        = local.vpc_id
    subnet_ids                    = local.private_subnet_ids
    additional_security_group_ids = []
  }
}

# Convenience output for database module
output "database_vpc_config" {
  description = "VPC configuration object for database module"
  value = {
    vpc_id                = local.vpc_id
    subnet_ids            = local.private_subnet_ids
    database_subnet_group = null # Will be created by database module
    security_group_id     = aws_security_group.database.id
  }
}

# VPC Endpoints
output "vpc_endpoints_security_group_id" {
  description = "Security group ID for VPC endpoints"
  value       = aws_security_group.vpc_endpoints.id
}

output "secretsmanager_endpoint_id" {
  description = "Secrets Manager VPC endpoint ID"
  value       = aws_vpc_endpoint.secretsmanager.id
}

output "secretsmanager_endpoint_dns" {
  description = "Secrets Manager VPC endpoint DNS names"
  value       = aws_vpc_endpoint.secretsmanager.dns_entry
}
