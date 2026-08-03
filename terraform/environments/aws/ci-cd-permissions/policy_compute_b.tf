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
#
#  - KMSTagOnCreate: kms:TagResource against a key that does NOT exist
#    yet (the CreateKey Tags parameter, per the AWS KMS API reference's
#    "Required permissions" for CreateKey) can never satisfy
#    aws:ResourceTag/Project: there is no resource, so no resource tag,
#    at authorization time. KMSMutateTaggedOnly's tag-on-EXISTING-key
#    grant (policy_compute.tf) therefore denies kms:TagResource during
#    aws_kms_key creation with Terraform's `tags` argument, breaking
#    every `terraform apply` that creates a new CMK (e.g. the OIDC
#    signing key in modules/compute/aws/lambda/signing-key.tf). AWS
#    authorizes tag-on-create via the aws:RequestTag/aws:TagKeys
#    condition keys instead, which inspect the tags being attached in
#    the request rather than tags already on the resource; see
#    "Controlling access during AWS requests" in the IAM user guide.
#    This MUST stay a separate statement: folding aws:RequestTag into
#    KMSMutateTaggedOnly would AND it with that statement's
#    aws:ResourceTag condition, which is exactly the unsatisfiable
#    combination this statement exists to avoid. Kept resource-unscoped
#    (Resource = "*") like KMSCreateAndRead/KMSReadTaggedOnly, since the
#    key ARN isn't known until CreateKey returns; the condition is what
#    limits it to CUDly-tagged create calls. StringEqualsIgnoreCase
#    matches the case-insensitive convention documented on
#    KMSMutateTaggedOnly (policy_compute.tf) for the same Project tag.
#    Added here (rather than policy_compute.tf) because the main policy
#    is within ~60 characters of the 6144-char limit; this statement
#    would not reliably fit.
#    aws:RequestTag alone would be an unintended privilege escalation:
#    it says nothing about the resource's CURRENT tags, so without more
#    it would let a leaked deploy token call kms:TagResource on ANY
#    existing key in the account (not just ones it creates), self-attach
#    Project=CUDly, and then use KMSMutateTaggedOnly to disable, delete,
#    or rewrite the key policy of that now-mistagged key, i.e. hijack a
#    key it never created. The added Null condition on
#    aws:ResourceTag/Project restricts the statement to resources with
#    NO Project tag yet (true for a brand-new key mid-CreateKey, false
#    for anything already tagged, ours or not), so it can only ever
#    perform the initial tag-on-create and can't be used to claim an
#    existing key.

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
        # ecr:PutImageScanningConfiguration is the writer for
        # aws_ecr_repository's image_scanning_configuration block. Its sibling
        # ecr:PutImageTagMutability is granted in policy_compute.tf's
        # ECRRepositoryScoped but this one was not, so any change to
        # scan_on_push (or importing a repository whose setting differs from
        # the config) fails the apply. Same repository ARN scope as the
        # statement it belongs with; it lives here only because
        # policy_compute.tf is at the 6144-char ceiling.
        Sid      = "ECRImageScanningConfig"
        Effect   = "Allow"
        Action   = ["ecr:PutImageScanningConfiguration"]
        Resource = "arn:aws:ecr:*:*:repository/cudly-*"
      },
      {
        # The ELB update path that policy_compute.tf's ELBFargate misses.
        # ELBFargate grants Create/Delete plus the Modify* family, but changing
        # a load balancer's subnets or security groups (which is what bumping
        # az_count does) goes through the Set* family instead, and
        # ModifyListenerAttributes is the writer whose reader
        # (DescribeListenerAttributes) is already granted. Resource = "*"
        # matches ELBFargate, whose ARNs are not name-scopeable.
        Sid    = "ELBFargateSetAttributes"
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:ModifyListenerAttributes",
          "elasticloadbalancing:SetIpAddressType",
          "elasticloadbalancing:SetSecurityGroups",
          "elasticloadbalancing:SetSubnets",
        ]
        Resource = "*"
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
        #
        # Uses StringEqualsIgnoreCase for the same reason as
        # KMSMutateTaggedOnly in policy_compute.tf (see PR #1496): the
        # OIDC signing key's Project tag comes from local.common_tags
        # (Project = var.project_name), which defaults to lowercase
        # "cudly" and overrides the provider default_tags value "CUDly"
        # for that resource. The AWS provider reads the key policy
        # (kms:GetKeyPolicy) on every aws_kms_key refresh, so a
        # case-sensitive StringEquals against "CUDly" would deny the
        # deploy role from refreshing/replacing the signing key
        # (AccessDenied: kms:GetKeyPolicy), blocking the ECC replace this
        # grant exists to support.
        Sid      = "KMSReadTaggedOnly"
        Effect   = "Allow"
        Action   = ["kms:GetKeyPolicy"]
        Resource = "*"
        Condition = {
          StringEqualsIgnoreCase = {
            "aws:ResourceTag/Project" = "CUDly"
          }
        }
      },
      {
        # kms:TagResource for the CreateKey Tags parameter. See the
        # KMSTagOnCreate note in the file header comment above for why
        # this must be a separate, aws:RequestTag-gated statement rather
        # than folded into KMSMutateTaggedOnly's aws:ResourceTag gate,
        # and why the Null condition on aws:ResourceTag/Project is
        # required to stop this from being usable to hijack an existing,
        # unrelated key by self-tagging it.
        Sid      = "KMSTagOnCreate"
        Effect   = "Allow"
        Action   = ["kms:TagResource"]
        Resource = "*"
        Condition = {
          StringEqualsIgnoreCase = {
            "aws:RequestTag/Project" = "CUDly"
          }
          Null = {
            "aws:ResourceTag/Project" = "true"
          }
        }
      },
    ]
  })

  tags = {
    Project   = "CUDly"
    ManagedBy = "terraform"
  }
}
