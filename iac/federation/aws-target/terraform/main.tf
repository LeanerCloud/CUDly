terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
    http = {
      source  = "hashicorp/http"
      version = ">= 3.4"
    }
  }
}

locals {
  audience = var.oidc_audience != "" ? var.oidc_audience : "sts.amazonaws.com"
  # Normalize the issuer URL once: strip the https:// scheme and any trailing
  # slash. AWS IAM uses the host portion (no scheme, no trailing slash) as the
  # prefix for the condition keys; the OpenIDConnectProvider Url argument also
  # rejects trailing slashes, so normalize before binding either value.
  oidc_issuer_url_normalized = trimsuffix(var.oidc_issuer_url, "/")
  oidc_condition_host        = trimsuffix(trimprefix(var.oidc_issuer_url, "https://"), "/")
}

resource "aws_iam_openid_connect_provider" "cudly" {
  url             = local.oidc_issuer_url_normalized
  client_id_list  = [local.audience]
  thumbprint_list = var.thumbprint_list
}

resource "aws_iam_policy" "cudly" {
  name        = "${var.role_name}-Policy"
  description = "Permissions required by CUDly to purchase and manage AWS commitments"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
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
    ]
  })
}

resource "aws_iam_role" "cudly" {
  name        = var.role_name
  description = "Role assumed by CUDly via workload identity federation"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [
        {
          Effect = "Allow"
          Principal = {
            Federated = aws_iam_openid_connect_provider.cudly.arn
          }
          Action = "sts:AssumeRoleWithWebIdentity"
          Condition = {
            StringEquals = merge(
              {
                "${local.oidc_condition_host}:aud" = local.audience
              },
              var.oidc_subject_claim != "" ? {
                "${local.oidc_condition_host}:sub" = var.oidc_subject_claim
              } : {}
            )
          }
        }
      ],
      []
    )
  })
}

resource "aws_iam_role_policy_attachment" "cudly" {
  role       = aws_iam_role.cudly.name
  policy_arn = aws_iam_policy.cudly.arn
}
