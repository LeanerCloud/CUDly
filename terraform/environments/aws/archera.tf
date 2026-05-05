# ==============================================
# Archera Integration — AWS
# ==============================================
#
# When enable_archera = true, this block creates a cross-account IAM role
# that Archera assumes to read commitment and cost data.  Purchase-execution
# permissions are separately gated behind enable_archera_purchase_actions
# so telemetry-only rollouts never accidentally include financial writes.
#
# PROVISIONAL SCOPE — must be confirmed against Archera integration docs
# before flipping enable_archera = true in any tfvars.
# TODO(@cristim): confirm Archera scope list against integration docs
# before enabling.  Reference: https://archera.ai/docs (integration guide).
#
# Placement rationale (bootstrap vs runtime split):
#   Archera is a RUNTIME integration — it reads cost telemetry and submits
#   purchases during normal operation, not during Terraform deploys.  This
#   file therefore lives in the main environment alongside compute.tf /
#   database.tf, NOT in ci-cd-permissions/ (which is applied once by a
#   privileged human and grants deploy-SA capabilities only).

locals {
  archera_role_name = "cudly-archera-integration"
}

# IAM role that Archera's AWS account assumes to access this account.
resource "aws_iam_role" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  name        = local.archera_role_name
  description = "Assumed by Archera SaaS to read cost data and (optionally) execute RI/SP purchases (provisional — confirm scope before enabling)"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ArcheraAssumeRole"
        Effect = "Allow"
        Principal = {
          AWS = "arn:aws:iam::${var.archera_aws_account_id}:root"
        }
        Action = "sts:AssumeRole"
        # ExternalId prevents confused-deputy attacks — always required when
        # trusting a third-party SaaS account.  Set archera_external_id to
        # the value Archera provides during onboarding.
        Condition = {
          StringEquals = { "sts:ExternalId" = var.archera_external_id }
        }
      },
    ]
  })

  tags = merge(local.common_tags, {
    Integration = "archera"
    Purpose     = "commitment-optimisation"
  })
}

# Read-only policy for Archera — PROVISIONAL.
# Safe to attach at initial rollout (no financial writes).
#
# TODO(@cristim): narrow to the exact action list from Archera's onboarding
# docs before setting enable_archera = true in any environment.
resource "aws_iam_policy" "archera_read" {
  count = var.enable_archera ? 1 : 0

  name        = "cudly-archera-read"
  description = "Provisional Archera read-only policy — cost Explorer + RI/SP telemetry (no purchase actions)"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # ── Read-only: Cost Explorer ──────────────────────────────────────────
      # Archera needs to read historical usage and costs to size commitments.
      # TODO(@cristim): confirm whether Archera also needs ce:GetReservation*
      # and ce:GetSavingsPlan* actions — add them if so.
      {
        Sid    = "CostExplorerReadOnly"
        Effect = "Allow"
        Action = [
          "ce:GetCostAndUsage",
          "ce:GetCostAndUsageWithResources",
          "ce:GetCostForecast",
          "ce:GetDimensionValues",
          "ce:GetReservationCoverage",
          "ce:GetReservationPurchaseRecommendation",
          "ce:GetReservationUtilization",
          "ce:GetRightsizingRecommendation",
          "ce:GetSavingsPlansCoverage",
          "ce:GetSavingsPlansUtilization",
          "ce:GetSavingsPlansUtilizationDetails",
          "ce:ListCostCategoryDefinitions",
        ]
        Resource = "*"
      },
      # ── Read-only: CUR / Cost and Usage Report ────────────────────────────
      # Describes which CUR reports are configured; Archera may consume CUR
      # exports directly for more granular data.
      {
        Sid    = "CURDescribe"
        Effect = "Allow"
        Action = [
          "cur:DescribeReportDefinitions",
        ]
        Resource = "*"
      },
      # ── Read-only: Reserved Instances ─────────────────────────────────────
      # Archera needs to see existing RIs to avoid over-purchasing.
      {
        Sid    = "ReservedInstancesRead"
        Effect = "Allow"
        Action = [
          "ec2:DescribeReservedInstances",
          "ec2:DescribeReservedInstancesListings",
          "ec2:DescribeReservedInstancesModifications",
          "ec2:DescribeReservedInstancesOfferings",
          "ec2:DescribeInstanceTypeOfferings",
        ]
        Resource = "*"
      },
      # ── Read-only: Savings Plans ─────────────────────────────────────────
      {
        Sid    = "SavingsPlansRead"
        Effect = "Allow"
        Action = [
          "savingsplans:DescribeSavingsPlans",
          "savingsplans:DescribeSavingsPlansOfferings",
          "savingsplans:DescribeSavingsPlanRates",
          "savingsplans:ListTagsForResource",
        ]
        Resource = "*"
      },
    ]
  })

  tags = merge(local.common_tags, {
    Integration = "archera"
  })
}

resource "aws_iam_role_policy_attachment" "archera_read" {
  count = var.enable_archera ? 1 : 0

  role       = aws_iam_role.archera_integration[0].name
  policy_arn = aws_iam_policy.archera_read[0].arn
}

# Purchase-execution policy for Archera — PROVISIONAL.
# Gated behind enable_archera_purchase_actions (default false) so financial
# writes are never included by accident at initial rollout.
#
# TODO(@cristim): enable only after confirming approval workflow with
# Archera (i.e. Archera requires customer approval before purchases) and
# only after enable_archera_purchase_actions is explicitly set to true.
resource "aws_iam_policy" "archera_purchase" {
  count = (var.enable_archera && var.enable_archera_purchase_actions) ? 1 : 0

  name        = "cudly-archera-purchase"
  description = "Provisional Archera purchase policy — RI/SP write actions (confirm with Archera before enabling)"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # ── Purchase-execution: Reserved Instances ────────────────────────────
      {
        Sid    = "ReservedInstancesPurchase"
        Effect = "Allow"
        Action = [
          "ec2:PurchaseReservedInstancesOffering",
          "ec2:ModifyReservedInstances",
        ]
        Resource = "*"
      },
      # ── Purchase-execution: Savings Plans ────────────────────────────────
      {
        Sid    = "SavingsPlansPurchase"
        Effect = "Allow"
        Action = [
          "savingsplans:CreateSavingsPlan",
          "savingsplans:DeleteQueuedSavingsPlan",
        ]
        Resource = "*"
      },
    ]
  })

  tags = merge(local.common_tags, {
    Integration = "archera"
  })
}

resource "aws_iam_role_policy_attachment" "archera_purchase" {
  count = (var.enable_archera && var.enable_archera_purchase_actions) ? 1 : 0

  role       = aws_iam_role.archera_integration[0].name
  policy_arn = aws_iam_policy.archera_purchase[0].arn
}
