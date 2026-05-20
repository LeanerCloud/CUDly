resource "aws_iam_policy" "data" {
  name        = "cudly-deploy-data"
  description = "CUDly Terraform deploy: RDS, SecretsManager, IAM, S3, CloudTrail"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "IAMRolesAndPolicies"
        Effect = "Allow"
        Action = [
          "iam:AddRoleToInstanceProfile",
          "iam:AttachRolePolicy",
          "iam:CreateInstanceProfile",
          "iam:CreatePolicy",
          "iam:CreatePolicyVersion",
          "iam:CreateRole",
          "iam:DeleteInstanceProfile",
          "iam:DeletePolicy",
          "iam:DeletePolicyVersion",
          "iam:DeleteRole",
          "iam:DeleteRolePolicy",
          "iam:DetachRolePolicy",
          "iam:GetInstanceProfile",
          "iam:GetPolicy",
          "iam:GetPolicyVersion",
          "iam:GetRole",
          "iam:GetRolePolicy",
          "iam:ListAttachedRolePolicies",
          "iam:ListInstanceProfilesForRole",
          "iam:ListPolicyVersions",
          "iam:ListRolePolicies",
          "iam:ListRoleTags",
          "iam:PutRolePolicy",
          "iam:RemoveRoleFromInstanceProfile",
          "iam:TagInstanceProfile",
          "iam:TagPolicy",
          "iam:TagRole",
          "iam:UntagInstanceProfile",
          "iam:UntagPolicy",
          "iam:UntagRole",
        ]
        Resource = [
          "arn:aws:iam::*:role/cudly-*",
          "arn:aws:iam::*:policy/cudly-*",
          "arn:aws:iam::*:instance-profile/cudly-*",
        ]
      },
      {
        # iam:PassRole is split into its own statement so we can attach the
        # iam:PassedToService condition without affecting the other IAM actions
        # in IAMRolesAndPolicies. Without this condition an attacker who
        # compromises the deploy role could pass any cudly-* role to any AWS
        # service (Lambda, EC2, Glue, etc.) to escalate privileges beyond
        # what Terraform deploy needs.
        Sid    = "IAMPassRoleScopedByService"
        Effect = "Allow"
        Action = ["iam:PassRole"]
        Resource = [
          "arn:aws:iam::*:role/cudly-*",
        ]
        Condition = {
          StringEquals = {
            "iam:PassedToService" = [
              "lambda.amazonaws.com",
              "ecs-tasks.amazonaws.com",
              "rds.amazonaws.com",
            ]
          }
        }
      },
      {
        # The IAMPassRoleScopedByService Allow above still matches the deploy
        # role itself (cudly-terraform-deploy is a cudly-* role) and permits
        # lambda.amazonaws.com as a target. That leaves open the exact #426
        # escalation: an actor holding the deploy role calls lambda:CreateFunction
        # and passes cudly-terraform-deploy as the Lambda execution role, giving
        # that Lambda the full deploy permissions (CreateRole, PassRole, KMS
        # destructive, Secrets read, Terraform state R/W) as a persistent
        # backdoor. An explicit Deny on passing the deploy role always wins over
        # the conditional Allow, closing the self-pass loop while leaving
        # legitimate execution-role passes (cudly-* exec roles) intact. (#542)
        Sid      = "IAMDenyPassDeployRole"
        Effect   = "Deny"
        Action   = ["iam:PassRole"]
        Resource = ["arn:aws:iam::*:role/cudly-terraform-deploy"]
      },
      {
        Sid    = "IAMReadForPassRole"
        Effect = "Allow"
        Action = [
          "iam:GetRole",
          "iam:GetInstanceProfile",
        ]
        Resource = "*"
      },
      {
        Sid    = "IAMServiceLinkedRole"
        Effect = "Allow"
        Action = ["iam:CreateServiceLinkedRole"]
        Resource = [
          "arn:aws:iam::*:role/aws-service-role/rds.amazonaws.com/AWSServiceRoleForRDS",
          "arn:aws:iam::*:role/aws-service-role/ecs.application-autoscaling.amazonaws.com/AWSServiceRoleForApplicationAutoScaling_ECSService",
        ]
        Condition = {
          StringLike = {
            "iam:AWSServiceName" = [
              "rds.amazonaws.com",
              "ecs.application-autoscaling.amazonaws.com",
            ]
          }
        }
      },
      {
        Sid    = "RDS"
        Effect = "Allow"
        Action = [
          "rds:AddTagsToResource",
          "rds:CreateDBInstance",
          "rds:CreateDBSnapshot",
          "rds:CreateDBSubnetGroup",
          "rds:DeleteDBInstance",
          "rds:DeleteDBSubnetGroup",
          "rds:DescribeDBEngineVersions",
          "rds:DescribeDBInstances",
          "rds:DescribeDBSubnetGroups",
          "rds:ListTagsForResource",
          "rds:DeleteDBProxy",
          "rds:DeregisterDBProxyTargets",
          "rds:DescribeDBProxies",
          "rds:DescribeDBProxyTargetGroups",
          "rds:DescribeDBProxyTargets",
          "rds:ModifyDBInstance",
          "rds:ModifyDBProxy",
          "rds:RegisterDBProxyTargets",
          "rds:RemoveTagsFromResource",
        ]
        Resource = "*"
      },
      {
        # rds:CreateDBProxy has no resource type per the AWS Service
        # Authorization Reference (Actions defined by Amazon RDS) and can only
        # be granted on Resource="*"; constraining it to specific ARNs makes IAM
        # unable to satisfy the request, so the grant is silently non-functional
        # and any future terraform apply that creates a DB proxy gets
        # AccessDenied. Keep it in its own wildcard statement so a later move of
        # the resource-scopeable RDS actions onto cudly-* ARNs (see PR #524's
        # RDSResourceScoped) does not sweep CreateDBProxy back into a scoped
        # statement. (#547)
        Sid      = "RDSCreateProxy"
        Effect   = "Allow"
        Action   = ["rds:CreateDBProxy"]
        Resource = "*"
      },
      {
        Sid    = "SecretsManager"
        Effect = "Allow"
        Action = [
          "secretsmanager:CreateSecret",
          "secretsmanager:DeleteSecret",
          "secretsmanager:DescribeSecret",
          "secretsmanager:GetResourcePolicy",
          "secretsmanager:GetSecretValue",
          "secretsmanager:ListSecrets",
          "secretsmanager:PutSecretValue",
          "secretsmanager:RestoreSecret",
          "secretsmanager:RotateSecret",
          "secretsmanager:TagResource",
          "secretsmanager:UntagResource",
          "secretsmanager:UpdateSecret",
        ]
        Resource = "arn:aws:secretsmanager:*:*:secret:cudly-*"
      },
      {
        Sid      = "SecretsManagerDescribe"
        Effect   = "Allow"
        Action   = ["secretsmanager:ListSecrets"]
        Resource = "*"
      },
      {
        Sid    = "S3TerraformState"
        Effect = "Allow"
        Action = [
          "s3:DeleteObject",
          "s3:GetBucketLocation",
          "s3:GetBucketVersioning",
          "s3:GetObject",
          "s3:ListBucket",
          "s3:PutObject",
        ]
        Resource = [
          "arn:aws:s3:::cudly-terraform-state*",
          "arn:aws:s3:::cudly-terraform-state*/*",
        ]
      },
      {
        Sid      = "CloudTrailReadOnly"
        Effect   = "Allow"
        Action   = ["cloudtrail:LookupEvents"]
        Resource = "*"
      },
    ]
  })

  tags = {
    Project   = "CUDly"
    ManagedBy = "terraform"
  }
}
