# ==============================================
# Archera Integration — AWS (Shared Module)
# ==============================================
#
# Single source of truth for the Archera AWS IAM resources.
# The permission lists are loaded from scope.aws.yaml in the parent directory —
# edit that file to update permissions across all callers simultaneously.
#
# Callers (federation bundles, environment self-deploy):
#   module "archera" {
#     source                          = "../../iac/modules/archera/aws"
#     enable_archera                  = var.enable_archera
#     archera_aws_account_id          = var.archera_aws_account_id
#     archera_external_id             = var.archera_external_id
#     enable_archera_purchase_actions = var.enable_archera_purchase_actions
#   }

terraform {
  # Modules in this directory family use cross-variable validation (referencing
  # other vars inside a validation block) — Terraform 1.9+. Pin all three
  # sibling modules together so behaviour stays consistent.
  required_version = ">= 1.9.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

locals {
  # Load the canonical permission list from the YAML source of truth.
  scope = yamldecode(file("${path.module}/../scope.aws.yaml"))

  archera_role_name = "cudly-archera-integration"
  read_actions      = local.scope.read_actions
  purchase_actions  = local.scope.purchase_actions
}

# IAM role that Archera's AWS account assumes to access this account.
resource "aws_iam_role" "archera_integration" {
  count = var.enable_archera ? 1 : 0

  lifecycle {
    precondition {
      condition     = !var.enable_archera || (trimspace(var.archera_aws_account_id) != "" && trimspace(var.archera_external_id) != "")
      error_message = "archera_aws_account_id and archera_external_id must both be non-empty when enable_archera = true."
    }
  }

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

  tags = {
    Integration = "archera"
    Purpose     = "commitment-optimisation"
    ManagedBy   = var.managed_by_tag
  }
}

# Read-only policy for Archera — permission list from scope.aws.yaml.
# Safe to attach at initial rollout (no financial writes).
resource "aws_iam_policy" "archera_read" {
  count = var.enable_archera ? 1 : 0

  name        = "cudly-archera-read"
  description = "Archera read-only policy — cost Explorer + RI/SP telemetry (no purchase actions)"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "ArcheraReadOnly"
        Effect   = "Allow"
        Action   = local.read_actions
        Resource = "*"
      },
    ]
  })

  tags = {
    Integration = "archera"
    ManagedBy   = var.managed_by_tag
  }
}

resource "aws_iam_role_policy_attachment" "archera_read" {
  count = var.enable_archera ? 1 : 0

  role       = aws_iam_role.archera_integration[0].name
  policy_arn = aws_iam_policy.archera_read[0].arn
}

# Purchase-execution policy for Archera — permission list from scope.aws.yaml.
# Gated behind enable_archera_purchase_actions (default false) so financial
# writes are never included by accident at initial rollout.
resource "aws_iam_policy" "archera_purchase" {
  count = (var.enable_archera && var.enable_archera_purchase_actions) ? 1 : 0

  name        = "cudly-archera-purchase"
  description = "Archera purchase policy — RI/SP write actions (confirm with Archera before enabling)"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "ArcheraPurchase"
        Effect   = "Allow"
        Action   = local.purchase_actions
        Resource = "*"
      },
    ]
  })

  tags = {
    Integration = "archera"
    ManagedBy   = var.managed_by_tag
  }
}

resource "aws_iam_role_policy_attachment" "archera_purchase" {
  count = (var.enable_archera && var.enable_archera_purchase_actions) ? 1 : 0

  role       = aws_iam_role.archera_integration[0].name
  policy_arn = aws_iam_policy.archera_purchase[0].arn
}
