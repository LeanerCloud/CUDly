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
        Sid    = "ECR"
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
          "ecr:GetAuthorizationToken",
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
        Resource = "*"
      },
      {
        Sid    = "CloudFrontFrontend"
        Effect = "Allow"
        Action = [
          "cloudfront:CreateDistribution",
          "cloudfront:CreateFunction",
          "cloudfront:DeleteDistribution",
          "cloudfront:DeleteFunction",
          "cloudfront:DescribeFunction",
          "cloudfront:GetDistribution",
          "cloudfront:GetDistributionConfig",
          "cloudfront:GetFunction",
          "cloudfront:ListTagsForResource",
          "cloudfront:PublishFunction",
          "cloudfront:TagResource",
          "cloudfront:UntagResource",
          "cloudfront:UpdateDistribution",
          "cloudfront:UpdateFunction",
        ]
        Resource = "*"
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
        Sid    = "ECSFargate"
        Effect = "Allow"
        Action = [
          "ecs:CreateCluster",
          "ecs:CreateService",
          "ecs:DeleteCluster",
          "ecs:DeleteService",
          "ecs:DeregisterTaskDefinition",
          "ecs:DescribeClusters",
          "ecs:DescribeServices",
          "ecs:DescribeTaskDefinition",
          "ecs:ListTagsForResource",
          "ecs:ListTasks",
          "ecs:PutClusterCapacityProviders",
          "ecs:StopTask",
          "ecs:RegisterTaskDefinition",
          "ecs:TagResource",
          "ecs:UntagResource",
          "ecs:UpdateCluster",
          "ecs:UpdateService",
        ]
        Resource = "*"
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
    ]
  })

  tags = {
    Project   = "CUDly"
    ManagedBy = "terraform"
  }
}
