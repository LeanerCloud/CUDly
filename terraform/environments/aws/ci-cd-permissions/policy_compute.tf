resource "aws_iam_policy" "compute" {
  name        = "cudly-deploy-compute"
  description = "CUDly Terraform deploy: Lambda, ECS, ELB, ECR, EventBridge, CloudWatch, AppAutoScaling, SES, CloudFront"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "Lambda"
        Effect = "Allow"
        Action = [
          "lambda:AddPermission",
          "lambda:CreateFunction",
          "lambda:CreateFunctionUrlConfig",
          "lambda:DeleteFunction",
          "lambda:DeleteFunctionUrlConfig",
          "lambda:GetFunction",
          "lambda:GetFunctionConfiguration",
          "lambda:GetFunctionUrlConfig",
          "lambda:GetPolicy",
          "lambda:InvokeFunction",
          "lambda:ListVersionsByFunction",
          "lambda:RemovePermission",
          "lambda:TagResource",
          "lambda:UntagResource",
          "lambda:UpdateFunctionCode",
          "lambda:UpdateFunctionConfiguration",
          "lambda:UpdateFunctionUrlConfig",
        ]
        Resource = "arn:aws:lambda:*:*:function:cudly-*"
      },
      {
        Sid    = "CloudWatchLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:DeleteLogGroup",
          "logs:ListTagsForResource",
          "logs:PutRetentionPolicy",
          "logs:TagResource",
          "logs:UntagResource",
        ]
        Resource = [
          "arn:aws:logs:*:*:log-group:/aws/lambda/cudly-*",
          "arn:aws:logs:*:*:log-group:/aws/vpc/cudly-*",
          "arn:aws:logs:*:*:log-group:/ecs/cudly-*",
        ]
      },
      {
        Sid      = "CloudWatchLogsDescribe"
        Effect   = "Allow"
        Action   = ["logs:DescribeLogGroups"]
        Resource = "*"
      },
      {
        Sid    = "EventBridge"
        Effect = "Allow"
        Action = [
          "events:DeleteRule",
          "events:DescribeRule",
          "events:ListTagsForResource",
          "events:ListTargetsByRule",
          "events:PutRule",
          "events:PutTargets",
          "events:RemoveTargets",
          "events:TagResource",
          "events:UntagResource",
        ]
        Resource = "arn:aws:events:*:*:rule/cudly-*"
      },
      {
        # ECR repository-scoped actions: every action that takes a
        # repository ARN is restricted to cudly-* repositories. This
        # prevents the deploy SA from poisoning, deleting, or exfiltrating
        # images in unrelated ECR repositories sharing the same account.
        Sid    = "ECRRepositoryScoped"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:BatchDeleteImage",
          "ecr:BatchGetImage",
          "ecr:CompleteLayerUpload",
          "ecr:CreateRepository",
          "ecr:DeleteLifecyclePolicy",
          "ecr:DeleteRepository",
          "ecr:DeleteRepositoryPolicy",
          "ecr:DescribeRepositories",
          "ecr:GetDownloadUrlForLayer",
          "ecr:GetLifecyclePolicy",
          "ecr:GetRepositoryPolicy",
          "ecr:InitiateLayerUpload",
          "ecr:ListImages",
          "ecr:ListTagsForResource",
          "ecr:PutImage",
          "ecr:PutImageTagMutability",
          "ecr:PutLifecyclePolicy",
          "ecr:SetRepositoryPolicy",
          "ecr:TagResource",
          "ecr:UntagResource",
          "ecr:UploadLayerPart",
        ]
        Resource = "arn:aws:ecr:*:*:repository/cudly-*"
      },
      {
        # ECR token endpoint — does not take a resource ARN and is
        # account-wide by design. Required for `docker login` against ECR.
        Sid      = "ECRGetAuthorizationToken"
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        # CloudFront create + read actions don't take a resource ARN at
        # the API level (a distribution's ARN is only known after create).
        # Read actions are also broad because terraform plan needs to
        # enumerate resources. Mutating actions are gated below.
        Sid    = "CloudFrontCreateAndRead"
        Effect = "Allow"
        Action = [
          "cloudfront:CreateDistribution",
          "cloudfront:CreateFunction",
          "cloudfront:DescribeFunction",
          "cloudfront:GetDistribution",
          "cloudfront:GetDistributionConfig",
          "cloudfront:GetFunction",
          "cloudfront:ListTagsForResource",
        ]
        Resource = "*"
      },
      {
        # CloudFront distribution mutations are restricted to distributions
        # tagged Project=CUDly. The Terraform aws_cloudfront_distribution
        # resource sets this tag at creation time (see modules/frontend/aws),
        # so subsequent Update/Delete/Tag/Untag operations against
        # CUDly-owned distributions succeed while attempts to mutate any
        # third-party distribution sharing the account are denied.
        # Function mutations live in policy_compute_b.tf — the function
        # resource type does not support aws:ResourceTag per the AWS
        # Service Authorization Reference, so it needs ARN scoping.
        Sid    = "CloudFrontMutateTaggedOnly"
        Effect = "Allow"
        Action = [
          "cloudfront:DeleteDistribution",
          "cloudfront:TagResource",
          "cloudfront:UntagResource",
          "cloudfront:UpdateDistribution",
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "aws:ResourceTag/Project" = "CUDly"
          }
        }
      },
      {
        Sid    = "CloudWatchAlarms"
        Effect = "Allow"
        Action = [
          "cloudwatch:DeleteAlarms",
          "cloudwatch:DescribeAlarms",
          "cloudwatch:ListTagsForResource",
          "cloudwatch:PutMetricAlarm",
          "cloudwatch:TagResource",
          "cloudwatch:UntagResource",
        ]
        Resource = "arn:aws:cloudwatch:*:*:alarm:cudly-*"
      },
      {
        # ECS resource-scoped actions: cluster, service, and task ARNs
        # all match cudly-* prefix. RegisterTaskDefinition cannot take a
        # specific ARN at registration time and is split below.
        # ecs:ListTasks does NOT support resource-level permissions per
        # the AWS Service Authorization Reference; it lives in
        # policy_compute_b.tf gated by an ecs:cluster condition.
        Sid    = "ECSFargateResourceScoped"
        Effect = "Allow"
        Action = [
          "ecs:CreateCluster",
          "ecs:CreateService",
          "ecs:DeleteCluster",
          "ecs:DeleteService",
          "ecs:DescribeClusters",
          "ecs:DescribeServices",
          "ecs:PutClusterCapacityProviders",
          "ecs:StopTask",
          "ecs:TagResource",
          "ecs:UntagResource",
          "ecs:UpdateCluster",
          "ecs:UpdateService",
        ]
        Resource = [
          "arn:aws:ecs:*:*:cluster/cudly-*",
          "arn:aws:ecs:*:*:service/cudly-*/*",
          "arn:aws:ecs:*:*:task/cudly-*/*",
          "arn:aws:ecs:*:*:task-definition/cudly-*:*",
        ]
      },
      {
        # ECS task-definition + tag-listing actions don't take a specific
        # ARN at API level. RegisterTaskDefinition has no resource;
        # DeregisterTaskDefinition + DescribeTaskDefinition take a
        # task-definition ARN that we constrain to cudly-*.
        # ListTagsForResource takes any ARN type but is read-only.
        Sid    = "ECSFargateAccountWide"
        Effect = "Allow"
        Action = [
          "ecs:RegisterTaskDefinition",
          "ecs:ListTagsForResource",
        ]
        Resource = "*"
      },
      {
        Sid    = "ECSFargateTaskDefinition"
        Effect = "Allow"
        Action = [
          "ecs:DeregisterTaskDefinition",
          "ecs:DescribeTaskDefinition",
        ]
        Resource = "arn:aws:ecs:*:*:task-definition/cudly-*:*"
      },
      {
        Sid    = "ELBFargate"
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:AddTags",
          "elasticloadbalancing:CreateListener",
          "elasticloadbalancing:CreateLoadBalancer",
          "elasticloadbalancing:CreateTargetGroup",
          "elasticloadbalancing:DeleteListener",
          "elasticloadbalancing:DeleteLoadBalancer",
          "elasticloadbalancing:DeleteTargetGroup",
          "elasticloadbalancing:DescribeListenerAttributes",
          "elasticloadbalancing:DescribeListeners",
          "elasticloadbalancing:DescribeLoadBalancerAttributes",
          "elasticloadbalancing:DescribeLoadBalancers",
          "elasticloadbalancing:DescribeTags",
          "elasticloadbalancing:DescribeTargetGroupAttributes",
          "elasticloadbalancing:DescribeTargetGroups",
          "elasticloadbalancing:ModifyListener",
          "elasticloadbalancing:ModifyLoadBalancerAttributes",
          "elasticloadbalancing:ModifyTargetGroup",
          "elasticloadbalancing:ModifyTargetGroupAttributes",
          "elasticloadbalancing:RemoveTags",
        ]
        Resource = "*"
      },
      {
        Sid    = "ApplicationAutoScalingFargate"
        Effect = "Allow"
        Action = [
          "application-autoscaling:DeleteScalingPolicy",
          "application-autoscaling:DeregisterScalableTarget",
          "application-autoscaling:DescribeScalableTargets",
          "application-autoscaling:DescribeScalingPolicies",
          "application-autoscaling:ListTagsForResource",
          "application-autoscaling:PutScalingPolicy",
          "application-autoscaling:RegisterScalableTarget",
          "application-autoscaling:TagResource",
          "application-autoscaling:UntagResource",
        ]
        Resource = "*"
      },
      {
        Sid    = "SES"
        Effect = "Allow"
        Action = [
          "ses:CreateEmailIdentity",
          "ses:DeleteEmailIdentity",
          "ses:GetEmailIdentity",
          "ses:PutEmailIdentityDkimSigningAttributes",
          "ses:SendEmail",
          "ses:TagResource",
          "ses:UntagResource",
        ]
        Resource = "*"
      },
      {
        # KMS create + read-only actions don't accept a key ARN at API
        # level (key ARNs are only known after CreateKey). Read-only
        # actions are also broad because terraform plan needs to
        # enumerate key state.
        # kms:CreateAlias is NOT here — alias actions act on alias
        # resources, and the aws:ResourceTag/Project gate below applies
        # only to keys (aliases inherit no IAM-visible tags). Alias
        # operations live in policy_compute_b.tf scoped by alias-ARN
        # prefix.
        Sid    = "KMSCreateAndRead"
        Effect = "Allow"
        Action = [
          "kms:CreateKey",
          "kms:DescribeKey",
          "kms:GetKeyPolicy",
          "kms:GetKeyRotationStatus",
          "kms:ListAliases",
          "kms:ListResourceTags",
        ]
        Resource = "*"
      },
      {
        # KMS mutating + destructive actions are gated on the key being
        # tagged Project=CUDly. The CUDly OIDC signing key is tagged at
        # creation by Terraform (see modules/security/aws/kms_signing.tf).
        # Without this gate the deploy SA could schedule deletion or
        # disable any KMS key in the account, causing denial of service
        # for unrelated workloads.
        # Alias mutations (DeleteAlias/UpdateAlias) live in
        # policy_compute_b.tf — the aws:ResourceTag condition does not
        # match alias resources.
        Sid    = "KMSMutateTaggedOnly"
        Effect = "Allow"
        Action = [
          "kms:DisableKey",
          "kms:EnableKey",
          "kms:PutKeyPolicy",
          "kms:ScheduleKeyDeletion",
          "kms:TagResource",
          "kms:UntagResource",
          "kms:UpdateKeyDescription",
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "aws:ResourceTag/Project" = "CUDly"
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
