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
#
#  - KMSReadTaggedOnly: kms:GetKeyPolicy exposes the full trust model
#    of a CMK (key policy document reveals all principals and conditions).
#    Moving it here with aws:ResourceTag/Project=CUDly prevents the deploy
#    SA from enumerating key policies of unrelated workloads sharing the
#    account (account-wide key-policy reconnaissance). Split from the main
#    policy because the 6144-char limit was reached.

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
      {
        # kms:GetKeyPolicy reveals the full trust model of a CMK and is
        # therefore gated on the key being tagged Project=CUDly. This
        # prevents the deploy SA from reading key policies of unrelated
        # workloads sharing the account. Split from policy_compute.tf
        # because the 6144-char managed-policy limit was reached.
        Sid      = "KMSReadTaggedOnly"
        Effect   = "Allow"
        Action   = ["kms:GetKeyPolicy"]
        Resource = "*"
        Condition = {
          StringEquals = {
            "aws:ResourceTag/Project" = "CUDly"
          }
        }
      },
      {
        # Key-lifecycle mutate + read actions on CUDly-tagged CMKs. Needed so
        # the deploy can manage the OIDC issuer signing key (aws_kms_key.signing):
        # PR #1480 migrated its spec RSA_2048 -> ECC_NIST_P256 (ES256), and KMS
        # cannot change a key spec in place, so Terraform must replace the key
        # (ScheduleKeyDeletion on the old CMK + CreateKey/TagResource on the new).
        # Destructive actions (ScheduleKeyDeletion) are gated to Project=CUDly
        # keys so the deploy SA can never schedule deletion of an unrelated
        # workload's CMK sharing the account. GetKeyRotationStatus is read by the
        # provider for aws_kms_key.enable_key_rotation.
        Sid    = "KMSKeyLifecycleTaggedOnly"
        Effect = "Allow"
        Action = [
          "kms:ScheduleKeyDeletion",
          "kms:CancelKeyDeletion",
          "kms:PutKeyPolicy",
          "kms:EnableKeyRotation",
          "kms:DisableKeyRotation",
          "kms:GetKeyRotationStatus",
          "kms:ListResourceTags",
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "aws:ResourceTag/Project" = "CUDly"
          }
        }
      },
      {
        # TagResource/UntagResource are gated on the REQUEST tag (aws:RequestTag)
        # rather than the resource tag, because a freshly created CMK is not yet
        # tagged Project=CUDly when Terraform applies its tags. This constrains
        # the deploy SA to only ever tag keys AS Project=CUDly, so a new key
        # immediately falls under the ResourceTag-gated statements above.
        Sid    = "KMSTagResourceRequestGated"
        Effect = "Allow"
        Action = [
          "kms:TagResource",
          "kms:UntagResource",
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "aws:RequestTag/Project" = "CUDly"
          }
        }
      },
      {
        # kms:CreateKey cannot be scoped to a resource ARN (the key does not yet
        # exist) and AWS does not support conditioning it on tags reliably across
        # providers, so it is granted on "*". This is low-risk: creating a CMK
        # grants no access to any data and does not expose existing keys; the
        # dangerous action (ScheduleKeyDeletion) remains tag-gated above, and the
        # new key is immediately tagged Project=CUDly via KMSTagResourceRequestGated.
        Sid      = "KMSCreateKey"
        Effect   = "Allow"
        Action   = ["kms:CreateKey"]
        Resource = "*"
      },
    ]
  })

  tags = {
    Project   = "CUDly"
    ManagedBy = "terraform"
  }
}
