# Second compute-permissions policy. This file exists because the main
# `aws_iam_policy.compute` is at AWS's 6144-char managed-policy limit;
# additional ARN-scoped statements (added in response to a CodeRabbit
# review of fix/iam-resource-wildcards) wouldn't fit there without
# evicting unrelated existing grants. Each statement here corrects a
# scoping bug that the AWS service authorization reference forced us to
# split out of `policy_compute.tf`:
#
#  - CloudFrontFnMutate: cloudfront:DeleteFunction/PublishFunction/
#    UpdateFunction operate on the `function` resource type which does
#    not support aws:ResourceTag, so tag-gating them is a no-op. ARN
#    scope used instead.
#
#  - ECSListTasks: ecs:ListTasks does not support resource-level
#    permissions, so Resource must be "*". The ecs:cluster condition
#    key restricts it to CUDly clusters.
#
#  - KMSAliasMutate: kms:CreateAlias/DeleteAlias/UpdateAlias act on
#    alias resources, but aws:ResourceTag/Project gates only key
#    resources (aliases inherit nothing). KMS evaluates these actions
#    against BOTH the alias and the target key, so the alias-side
#    check lives here (ARN-scoped) and the key-side check lives in
#    policy_compute.tf KMSMutateTaggedOnly (tag-gated to CUDly keys).

resource "aws_iam_policy" "compute_b" {
  name        = "cudly-deploy-compute-b"
  description = "CUDly Terraform deploy: ARN-scoped statements split out of compute policy due to size"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "CloudFrontFnMutate"
        Effect = "Allow"
        Action = [
          "cloudfront:DeleteFunction",
          "cloudfront:PublishFunction",
          "cloudfront:UpdateFunction",
        ]
        Resource = "arn:aws:cloudfront::*:function/cudly-*"
      },
      {
        Sid    = "ECSListTasks"
        Effect = "Allow"
        Action = [
          "ecs:ListTasks",
        ]
        Resource = "*"
        Condition = {
          ArnLike = {
            "ecs:cluster" = "arn:aws:ecs:*:*:cluster/cudly-*"
          }
        }
      },
      {
        Sid    = "KMSAliasMutate"
        Effect = "Allow"
        Action = [
          "kms:CreateAlias",
          "kms:DeleteAlias",
          "kms:UpdateAlias",
        ]
        Resource = "arn:aws:kms:*:*:alias/cudly-*"
      },
    ]
  })

  tags = {
    Project   = "CUDly"
    ManagedBy = "terraform"
  }
}
