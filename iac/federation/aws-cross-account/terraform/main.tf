terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

data "aws_iam_policy_document" "trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${var.source_account_id}:root"]
    }
  }
}

resource "aws_iam_role" "cudly" {
  name               = var.role_name
  assume_role_policy = data.aws_iam_policy_document.trust.json
  description        = "Role assumed by CUDly via cross-account IAM role assumption"
}

resource "aws_iam_role_policy" "cudly" {
  name = "CUDlyPermissions"
  role = aws_iam_role.cudly.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Resource = "*"
      Action = [
        "ec2:PurchaseReservedInstancesOffering", "ec2:DescribeReservedInstancesOfferings",
        "ec2:DescribeReservedInstances", "ec2:DescribeInstanceTypeOfferings",
        "ec2:GetReservedInstancesExchangeQuote", "ec2:AcceptReservedInstancesExchangeQuote",
        "rds:PurchaseReservedDBInstancesOffering", "rds:DescribeReservedDBInstancesOfferings",
        "rds:DescribeReservedDBInstances",
        "elasticache:PurchaseReservedCacheNodesOffering",
        "elasticache:DescribeReservedCacheNodesOfferings", "elasticache:DescribeReservedCacheNodes",
        "redshift:PurchaseReservedNodeOffering", "redshift:DescribeReservedNodeOfferings",
        "redshift:DescribeReservedNodes",
        "memorydb:PurchaseReservedNodesOffering", "memorydb:DescribeReservedNodesOfferings",
        "memorydb:DescribeReservedNodes",
        "savingsplans:CreateSavingsPlan", "savingsplans:DescribeSavingsPlans",
        "savingsplans:DescribeSavingsPlansOfferings", "savingsplans:DescribeSavingsPlansOfferingRates",
        "es:PurchaseReservedInstanceOffering", "es:DescribeReservedInstanceOfferings",
        "es:DescribeReservedInstances",
      ]
    }]
  })
}
