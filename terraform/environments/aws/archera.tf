# ==============================================
# Archera Integration — AWS
# ==============================================
#
# When enable_archera = true, this block creates a cross-account IAM role
# that Archera assumes to read commitment and cost data and to execute
# Reserved Instance / Savings Plan purchases on behalf of the customer.
#
# PROVISONAL SCOPE — must be confirmed against Archera integration docs
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
  description = "Assumed by Archera SaaS to read cost data and execute RI/SP purchases (provisional — confirm scope before enabling)"

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
        # Optional: require ExternalId supplied by Archera during onboarding.
        # Uncomment and set archera_external_id once confirmed with Archera.
        # Condition = {
        #   StringEquals = { "sts:ExternalId" = var.archera_external_id }
        # }
      },
    ]
  })

  tags = merge(local.common_tags, {
    Integration = "archera"
    Purpose     = "commitment-optimisation"
  })
}

# Least-privilege policy for Archera — PROVISIONAL.
# Actions are split into read-only cost/commitment data (safe to enable
# immediately) and purchase-execution (requires explicit confirmation with
# Archera before enabling).
#
# TODO(@cristim): narrow to the exact action list from Archera's onboarding
# docs before setting enable_archera = true in any environment.
resource "aws_iam_policy" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  name        = "cudly-archera-integration"
  description = "Provisional Archera integration policy — read commitment + cost data, execute RI/SP purchases"

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
      # ── Purchase-execution: Reserved Instances ────────────────────────────
      # TODO(@cristim): enable only after confirming approval workflow with
      # Archera (i.e. Archera requires customer approval before purchases).
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
      # TODO(@cristim): same confirmation requirement as RI purchase above.
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

resource "aws_iam_role_policy_attachment" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  role       = aws_iam_role.archera_integration[0].name
  policy_arn = aws_iam_policy.archera_integration[0].arn
}
