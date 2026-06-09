# AWS VPC Module with IPv6
# Creates VPC with IPv6 support - no NAT Gateway or VPC Endpoints needed
# Cost savings: ~$54/month (NAT Gateway + VPC Endpoints eliminated)
# Supports: new VPC, existing VPC, or default VPC

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# ==============================================
# Data Sources for Existing/Default VPC
# ==============================================

# Get default VPC (if use_default_vpc = true)
data "aws_vpc" "default" {
  count   = var.use_default_vpc ? 1 : 0
  default = true
}

# Get existing VPC (if use_existing_vpc = true and existing_vpc_id provided)
data "aws_vpc" "existing" {
  count = var.use_existing_vpc && var.existing_vpc_id != "" && !var.use_default_vpc ? 1 : 0
  id    = var.existing_vpc_id
}

# Get default subnets (if using default VPC)
data "aws_subnets" "default" {
  count = var.use_default_vpc ? 1 : 0

  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default[0].id]
  }
}

# Local values to determine which VPC/subnets to use
locals {
  create_vpc = !var.use_existing_vpc && !var.use_default_vpc

  vpc_id = local.create_vpc ? aws_vpc.main[0].id : (
    var.use_default_vpc ? data.aws_vpc.default[0].id : data.aws_vpc.existing[0].id
  )

  vpc_cidr = local.create_vpc ? var.vpc_cidr : (
    var.use_default_vpc ? data.aws_vpc.default[0].cidr_block : data.aws_vpc.existing[0].cidr_block
  )

  # IPv6 CIDR (only available if VPC has IPv6 enabled)
  vpc_ipv6_cidr = local.create_vpc ? aws_vpc.main[0].ipv6_cidr_block : (
    var.use_default_vpc ? try(data.aws_vpc.default[0].ipv6_cidr_block, null) : try(data.aws_vpc.existing[0].ipv6_cidr_block, null)
  )

  # Subnet IDs
  public_subnet_ids = local.create_vpc ? aws_subnet.public[*].id : (
    var.use_default_vpc ? data.aws_subnets.default[0].ids : var.existing_public_subnet_ids
  )

  private_subnet_ids = local.create_vpc ? aws_subnet.private[*].id : (
    var.use_default_vpc ? [] : var.existing_private_subnet_ids
  )
}

# ==============================================
# VPC with IPv6 (only created if not using existing VPC)
# ==============================================

resource "aws_vpc" "main" {
  count = local.create_vpc ? 1 : 0

  cidr_block                       = var.vpc_cidr
  enable_dns_hostnames             = true
  enable_dns_support               = true
  assign_generated_ipv6_cidr_block = var.enable_ipv6

  tags = merge(var.tags, {
    Name = "${var.stack_name}-vpc"
  })
}

# ==============================================
# Internet Gateway (for both IPv4 and IPv6)
# ==============================================

resource "aws_internet_gateway" "main" {
  count = local.create_vpc ? 1 : 0

  vpc_id = local.vpc_id

  tags = merge(var.tags, {
    Name = "${var.stack_name}-igw"
  })
}

# ==============================================
# Egress-Only Internet Gateway (for IPv6 outbound)
# ==============================================

resource "aws_egress_only_internet_gateway" "main" {
  count = local.create_vpc && var.enable_ipv6 ? 1 : 0

  vpc_id = local.vpc_id

  tags = merge(var.tags, {
    Name = "${var.stack_name}-eigw"
  })
}

# ==============================================
# fck-nat (cost-effective NAT alternative)
# ==============================================

# Get latest fck-nat AMI (ARM64, free tier eligible)
data "aws_ami" "fck_nat" {
  count = var.enable_nat_gateway ? 1 : 0

  most_recent = true
  owners      = ["568608671756"] # fck-nat official AWS account

  filter {
    name   = "name"
    values = ["fck-nat-al2023-hvm-*-arm64-ebs"]
  }

  filter {
    name   = "state"
    values = ["available"]
  }
}

# Security group for fck-nat instance
resource "aws_security_group" "fck_nat" {
  count = var.enable_nat_gateway ? 1 : 0

  name_prefix = "${var.stack_name}-fck-nat-"
  description = "Security group for fck-nat instance"
  vpc_id      = local.vpc_id

  # Allow all traffic from VPC CIDR
  ingress {
    description = "Allow all from VPC"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [local.vpc_cidr]
  }

  # Allow all outbound traffic
  egress {
    description = "Allow all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.stack_name}-fck-nat-sg"
  })
}

# IAM role for fck-nat instance (for SSM access)
resource "aws_iam_role" "fck_nat" {
  count = var.enable_nat_gateway ? 1 : 0

  name_prefix = "${var.stack_name}-fck-nat-"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ec2.amazonaws.com"
      }
    }]
  })

  tags = merge(var.tags, {
    Name = "${var.stack_name}-fck-nat-role"
  })
}

# Attach SSM managed policy for Session Manager access
resource "aws_iam_role_policy_attachment" "fck_nat_ssm" {
  count = var.enable_nat_gateway ? 1 : 0

  role       = aws_iam_role.fck_nat[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# Instance profile for fck-nat
resource "aws_iam_instance_profile" "fck_nat" {
  count = var.enable_nat_gateway ? 1 : 0

  name_prefix = "${var.stack_name}-fck-nat-"
  role        = aws_iam_role.fck_nat[0].name

  tags = merge(var.tags, {
    Name = "${var.stack_name}-fck-nat-profile"
  })
}

# Launch template for fck-nat instance
resource "aws_launch_template" "fck_nat" {
  count = var.enable_nat_gateway ? 1 : 0

  name_prefix   = "${var.stack_name}-fck-nat-"
  image_id      = data.aws_ami.fck_nat[0].id
  instance_type = "t4g.nano" # ARM64, free tier eligible

  iam_instance_profile {
    name = aws_iam_instance_profile.fck_nat[0].name
  }

  network_interfaces {
    associate_public_ip_address = true
    delete_on_termination       = true
    security_groups             = [aws_security_group.fck_nat[0].id]
  }

  # User data to disable source/destination check for NAT functionality
  user_data = base64encode(<<-EOF
    #!/bin/bash
    # Get instance ID and ENI ID
    INSTANCE_ID=$(ec2-metadata --instance-id | cut -d " " -f 2)
    ENI_ID=$(aws ec2 describe-instances --instance-ids $INSTANCE_ID --region ${var.region} --query 'Reservations[0].Instances[0].NetworkInterfaces[0].NetworkInterfaceId' --output text)

    # Disable source/destination check
    aws ec2 modify-network-interface-attribute --network-interface-id $ENI_ID --region ${var.region} --no-source-dest-check
  EOF
  )

  # Disable source/destination check for NAT functionality
  metadata_options {
    http_endpoint = "enabled"
    http_tokens   = "required" # IMDSv2 required for security
  }

  tag_specifications {
    resource_type = "instance"
    tags = merge(var.tags, {
      Name = "${var.stack_name}-fck-nat"
    })
  }

  tag_specifications {
    resource_type = "volume"
    tags = merge(var.tags, {
      Name = "${var.stack_name}-fck-nat-volume"
    })
  }

  lifecycle {
    create_before_destroy = true
  }
}

# Auto Scaling Group for fck-nat (ensures it stays running)
resource "aws_autoscaling_group" "fck_nat" {
  count = var.enable_nat_gateway ? 1 : 0

  name_prefix         = "${var.stack_name}-fck-nat-"
  desired_capacity    = 1
  min_size            = 1
  max_size            = 1
  vpc_zone_identifier = [local.public_subnet_ids[0]] # Deploy in first public subnet

  launch_template {
    id      = aws_launch_template.fck_nat[0].id
    version = "$Latest"
  }

  health_check_type         = "EC2"
  health_check_grace_period = 60

  tag {
    key                 = "Name"
    value               = "${var.stack_name}-fck-nat-asg"
    propagate_at_launch = false
  }

  dynamic "tag" {
    for_each = var.tags
    content {
      key                 = tag.key
      value               = tag.value
      propagate_at_launch = false
    }
  }

  lifecycle {
    create_before_destroy = true
  }
}

# IAM policy for fck-nat to modify its own network interface
resource "aws_iam_role_policy" "fck_nat_ec2" {
  count = var.enable_nat_gateway ? 1 : 0

  name = "ec2-network-interface"
  role = aws_iam_role.fck_nat[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "ec2:DescribeInstances",
        "ec2:ModifyNetworkInterfaceAttribute"
      ]
      Resource = "*"
    }]
  })
}

# Get the instance from the ASG
data "aws_instances" "fck_nat" {
  count = var.enable_nat_gateway ? 1 : 0

  filter {
    name   = "tag:aws:autoscaling:groupName"
    values = [aws_autoscaling_group.fck_nat[0].name]
  }

  filter {
    name   = "instance-state-name"
    values = ["running"]
  }

  depends_on = [aws_autoscaling_group.fck_nat]
}

# Get the ENI of the fck-nat instance for routing
data "aws_network_interfaces" "fck_nat" {
  count = var.enable_nat_gateway ? 1 : 0

  filter {
    name   = "attachment.instance-id"
    values = data.aws_instances.fck_nat[0].ids
  }

  filter {
    name   = "attachment.device-index"
    values = ["0"] # Primary network interface
  }

  depends_on = [data.aws_instances.fck_nat]
}

# ==============================================
# Availability Zones
# ==============================================

data "aws_availability_zones" "available" {
  state = "available"

  # Exclude local zones
  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

# ==============================================
# Public Subnets (for ALB, bastion) - only created for new VPC
# ==============================================

resource "aws_subnet" "public" {
  count = local.create_vpc ? var.az_count : 0

  vpc_id                          = local.vpc_id
  cidr_block                      = cidrsubnet(var.vpc_cidr, 8, count.index)
  ipv6_cidr_block                 = var.enable_ipv6 ? cidrsubnet(local.vpc_ipv6_cidr, 8, count.index) : null
  availability_zone               = data.aws_availability_zones.available.names[count.index]
  map_public_ip_on_launch         = true
  assign_ipv6_address_on_creation = var.enable_ipv6

  tags = merge(var.tags, {
    Name = "${var.stack_name}-public-${data.aws_availability_zones.available.names[count.index]}"
    Type = "public"
  })
}

# ==============================================
# Private Subnets (for Lambda, RDS, Fargate) - only created for new VPC
# ==============================================

resource "aws_subnet" "private" {
  count = local.create_vpc ? var.az_count : 0

  vpc_id                          = local.vpc_id
  cidr_block                      = cidrsubnet(var.vpc_cidr, 8, count.index + var.az_count)
  ipv6_cidr_block                 = var.enable_ipv6 ? cidrsubnet(local.vpc_ipv6_cidr, 8, count.index + var.az_count) : null
  availability_zone               = data.aws_availability_zones.available.names[count.index]
  assign_ipv6_address_on_creation = var.enable_ipv6

  tags = merge(var.tags, {
    Name = "${var.stack_name}-private-${data.aws_availability_zones.available.names[count.index]}"
    Type = "private"
  })
}

# ==============================================
# Route Tables (only for new VPC)
# ==============================================

# Public route table (for both IPv4 and IPv6)
resource "aws_route_table" "public" {
  count = local.create_vpc ? 1 : 0

  vpc_id = local.vpc_id

  tags = merge(var.tags, {
    Name = "${var.stack_name}-public-rt"
    Type = "public"
  })
}

# IPv4 route to Internet Gateway
resource "aws_route" "public_internet_ipv4" {
  count = local.create_vpc ? 1 : 0

  route_table_id         = aws_route_table.public[0].id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.main[0].id
}

# IPv6 route to Internet Gateway
resource "aws_route" "public_internet_ipv6" {
  count = local.create_vpc && var.enable_ipv6 ? 1 : 0

  route_table_id              = aws_route_table.public[0].id
  destination_ipv6_cidr_block = "::/0"
  gateway_id                  = aws_internet_gateway.main[0].id
}

# Associate public subnets with public route table
resource "aws_route_table_association" "public" {
  count = local.create_vpc ? var.az_count : 0

  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public[0].id
}

# Private route tables (one per AZ for flexibility)
resource "aws_route_table" "private" {
  count = local.create_vpc ? var.az_count : 0

  vpc_id = local.vpc_id

  tags = merge(var.tags, {
    Name = "${var.stack_name}-private-rt-${count.index + 1}"
    Type = "private"
  })
}

# IPv6 egress route for private subnets
resource "aws_route" "private_internet_ipv6" {
  count = local.create_vpc && var.enable_ipv6 ? var.az_count : 0

  route_table_id              = aws_route_table.private[count.index].id
  destination_ipv6_cidr_block = "::/0"
  egress_only_gateway_id      = aws_egress_only_internet_gateway.main[0].id
}

# IPv4 egress route via fck-nat (optional, for services without IPv6 support like SES)
resource "aws_route" "private_internet_ipv4" {
  count = local.create_vpc && var.enable_nat_gateway ? var.az_count : 0

  route_table_id         = aws_route_table.private[count.index].id
  destination_cidr_block = "0.0.0.0/0"
  network_interface_id   = data.aws_network_interfaces.fck_nat[0].ids[0]
}

# Associate private subnets with private route tables
resource "aws_route_table_association" "private" {
  count = local.create_vpc ? var.az_count : 0

  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# ==============================================
# Security Groups
# ==============================================

# Security group for database access
resource "aws_security_group" "database" {
  name_prefix = "${var.stack_name}-database-"
  description = "Security group for database access"
  vpc_id      = local.vpc_id

  # PostgreSQL from VPC (IPv4)
  ingress {
    description = "PostgreSQL from VPC (IPv4)"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [local.vpc_cidr]
  }

  # PostgreSQL from VPC (IPv6)
  ingress {
    description      = "PostgreSQL from VPC (IPv6)"
    from_port        = 5432
    to_port          = 5432
    protocol         = "tcp"
    ipv6_cidr_blocks = var.enable_ipv6 ? [local.vpc_ipv6_cidr] : []
  }

  # Allow all outbound (IPv4)
  egress {
    description = "Allow all outbound (IPv4)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow all outbound (IPv6)
  egress {
    description      = "Allow all outbound (IPv6)"
    from_port        = 0
    to_port          = 0
    protocol         = "-1"
    ipv6_cidr_blocks = ["::/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.stack_name}-database-sg"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# Security group for ALB (if using Fargate)
resource "aws_security_group" "alb" {
  count = var.create_alb_security_group ? 1 : 0

  name_prefix = "${var.stack_name}-alb-"
  description = "Security group for Application Load Balancer"
  vpc_id      = local.vpc_id

  # HTTP from internet (IPv4)
  ingress {
    description = "HTTP from internet (IPv4)"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # HTTP from internet (IPv6)
  ingress {
    description      = "HTTP from internet (IPv6)"
    from_port        = 80
    to_port          = 80
    protocol         = "tcp"
    ipv6_cidr_blocks = ["::/0"]
  }

  # HTTPS from internet (IPv4)
  ingress {
    description = "HTTPS from internet (IPv4)"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # HTTPS from internet (IPv6)
  ingress {
    description      = "HTTPS from internet (IPv6)"
    from_port        = 443
    to_port          = 443
    protocol         = "tcp"
    ipv6_cidr_blocks = ["::/0"]
  }

  # Allow all outbound (IPv4)
  egress {
    description = "Allow all outbound (IPv4)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow all outbound (IPv6)
  egress {
    description      = "Allow all outbound (IPv6)"
    from_port        = 0
    to_port          = 0
    protocol         = "-1"
    ipv6_cidr_blocks = ["::/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.stack_name}-alb-sg"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# ==============================================
# VPC Flow Logs (optional, for debugging)
# ==============================================

resource "aws_flow_log" "main" {
  count = var.enable_flow_logs ? 1 : 0

  iam_role_arn    = aws_iam_role.flow_logs[0].arn
  log_destination = aws_cloudwatch_log_group.flow_logs[0].arn
  traffic_type    = "ALL"
  vpc_id          = local.vpc_id

  tags = merge(var.tags, {
    Name = "${var.stack_name}-flow-logs"
  })
}

resource "aws_cloudwatch_log_group" "flow_logs" {
  count = var.enable_flow_logs ? 1 : 0

  name              = "/aws/vpc/${var.stack_name}"
  retention_in_days = var.flow_logs_retention_days

  tags = var.tags
}

resource "aws_iam_role" "flow_logs" {
  count = var.enable_flow_logs ? 1 : 0

  name_prefix = "${var.stack_name}-flow-logs-"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "vpc-flow-logs.amazonaws.com"
        }
      }
    ]
  })

  tags = var.tags
}

resource "aws_iam_role_policy" "flow_logs" {
  count = var.enable_flow_logs ? 1 : 0

  name_prefix = "${var.stack_name}-flow-logs-"
  role        = aws_iam_role.flow_logs[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
          "logs:DescribeLogGroups",
          "logs:DescribeLogStreams"
        ]
        Effect   = "Allow"
        Resource = "*"
      }
    ]
  })
}

# ==============================================
# VPC Endpoints (for services without IPv6 support)
# ==============================================

# Security group for VPC endpoints
resource "aws_security_group" "vpc_endpoints" {
  name_prefix = "${var.stack_name}-vpc-endpoints-"
  description = "Security group for VPC endpoints"
  vpc_id      = local.vpc_id

  # HTTPS from VPC (IPv4)
  ingress {
    description = "HTTPS from VPC (IPv4)"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = [local.vpc_cidr]
  }

  # HTTPS from VPC (IPv6)
  ingress {
    description      = "HTTPS from VPC (IPv6)"
    from_port        = 443
    to_port          = 443
    protocol         = "tcp"
    ipv6_cidr_blocks = var.enable_ipv6 ? [local.vpc_ipv6_cidr] : []
  }

  # Allow all outbound (IPv4)
  egress {
    description = "Allow all outbound (IPv4)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow all outbound (IPv6)
  egress {
    description      = "Allow all outbound (IPv6)"
    from_port        = 0
    to_port          = 0
    protocol         = "-1"
    ipv6_cidr_blocks = ["::/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.stack_name}-vpc-endpoints-sg"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# Secrets Manager VPC Endpoint (required - no IPv6 support)
resource "aws_vpc_endpoint" "secretsmanager" {
  vpc_id              = local.vpc_id
  service_name        = "com.amazonaws.${data.aws_region.current.name}.secretsmanager"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = local.private_subnet_ids
  security_group_ids  = [aws_security_group.vpc_endpoints.id]
  private_dns_enabled = true

  tags = merge(var.tags, {
    Name = "${var.stack_name}-secretsmanager-endpoint"
  })
}

# Data source for current region
data "aws_region" "current" {}
