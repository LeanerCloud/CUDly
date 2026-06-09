terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
    http = {
      source  = "hashicorp/http"
      version = ">= 3.4"
    }
  }
}

resource "random_uuid" "external_id" {
  count = var.external_id == "" ? 1 : 0
}

locals {
  # Prefer the specific execution role ARN if provided; fall back to the account root.
  trust_principal = var.cudly_execution_role_arn != "" ? var.cudly_execution_role_arn : "arn:aws:iam::${var.source_account_id}:root"
  # Auto-generate external ID for confused deputy protection if not provided.
  effective_external_id = var.external_id != "" ? var.external_id : random_uuid.external_id[0].result
}

data "aws_iam_policy_document" "trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type        = "AWS"
      identifiers = [local.trust_principal]
    }
    condition {
      test     = "StringEquals"
      variable = "sts:ExternalId"
      values   = [local.effective_external_id]
    }
  }
}

resource "aws_iam_role" "cudly" {
  name               = var.role_name
  assume_role_policy = data.aws_iam_policy_document.trust.json
  description        = "Role assumed by CUDly via cross-account IAM role assumption"
}

# Standalone managed policy — matches the aws-target/terraform WIF module's
# pattern (separate policy + attachment) for consistency across both AWS paths.
resource "aws_iam_policy" "cudly" {
  name        = "${var.role_name}-Policy"
  description = "Permissions required by CUDly to purchase and manage AWS commitments"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [
        {
          Sid    = "EC2Reservations"
          Effect = "Allow"
          Action = [
            "ec2:PurchaseReservedInstancesOffering",
            "ec2:DescribeReservedInstancesOfferings",
            "ec2:DescribeReservedInstances",
            "ec2:DescribeInstanceTypeOfferings",
            "ec2:GetReservedInstancesExchangeQuote",
            "ec2:AcceptReservedInstancesExchangeQuote",
            "ec2:DescribeRegions",
          ]
          Resource = "*"
        },
        {
          Sid    = "RDSReservations"
          Effect = "Allow"
          Action = [
            "rds:PurchaseReservedDBInstancesOffering",
            "rds:DescribeReservedDBInstancesOfferings",
            "rds:DescribeReservedDBInstances",
          ]
          Resource = "*"
        },
        {
          Sid    = "ElastiCacheReservations"
          Effect = "Allow"
          Action = [
            "elasticache:PurchaseReservedCacheNodesOffering",
            "elasticache:DescribeReservedCacheNodesOfferings",
            "elasticache:DescribeReservedCacheNodes",
          ]
          Resource = "*"
        },
        {
          Sid    = "RedshiftReservations"
          Effect = "Allow"
          Action = [
            "redshift:PurchaseReservedNodeOffering",
            "redshift:DescribeReservedNodeOfferings",
            "redshift:DescribeReservedNodes",
          ]
          Resource = "*"
        },
        {
          Sid    = "MemoryDBReservations"
          Effect = "Allow"
          Action = [
            "memorydb:PurchaseReservedNodesOffering",
            "memorydb:DescribeReservedNodesOfferings",
            "memorydb:DescribeReservedNodes",
          ]
          Resource = "*"
        },
        {
          Sid    = "SavingsPlans"
          Effect = "Allow"
          Action = [
            "savingsplans:CreateSavingsPlan",
            "savingsplans:DescribeSavingsPlans",
            "savingsplans:DescribeSavingsPlansOfferings",
            "savingsplans:DescribeSavingsPlansOfferingRates",
          ]
          Resource = "*"
        },
        {
          Sid    = "OpenSearchReservations"
          Effect = "Allow"
          Action = [
            "es:PurchaseReservedInstanceOffering",
            "es:DescribeReservedInstanceOfferings",
            "es:DescribeReservedInstances",
          ]
          Resource = "*"
        },
      ],
      var.enable_org_discovery ? [
        {
          Sid    = "OrganizationsDiscovery"
          Effect = "Allow"
          Action = [
            "organizations:ListAccounts",
            "organizations:DescribeOrganization",
          ]
          Resource = "*"
        }
      ] : []
    )
  })
}

resource "aws_iam_role_policy_attachment" "cudly" {
  role       = aws_iam_role.cudly.name
  policy_arn = aws_iam_policy.cudly.arn
}
