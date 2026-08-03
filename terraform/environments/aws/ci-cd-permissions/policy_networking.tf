resource "aws_iam_policy" "networking" {
  name        = "cudly-deploy-networking"
  description = "CUDly Terraform deploy: VPC, EC2, AutoScaling, Route53, ACM, KMS, STS"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "STS"
        Effect   = "Allow"
        Action   = ["sts:GetCallerIdentity"]
        Resource = "*"
      },
      {
        Sid    = "VPCNetworking"
        Effect = "Allow"
        Action = [
          "ec2:AssociateRouteTable",
          "ec2:AttachInternetGateway",
          "ec2:AuthorizeSecurityGroupEgress",
          "ec2:AuthorizeSecurityGroupIngress",
          "ec2:CreateEgressOnlyInternetGateway",
          "ec2:CreateInternetGateway",
          "ec2:CreateLaunchTemplate",
          # The fck-nat launch template sources its AMI from a
          # most_recent = true data.aws_ami lookup, so every upstream AMI
          # republish takes the launch template's UPDATE path rather than
          # create: the provider calls CreateLaunchTemplateVersion for the new
          # image_id and ModifyLaunchTemplate to move the default version
          # (resourceLaunchTemplateUpdate). Only Create/DeleteLaunchTemplate
          # were granted, so the first AMI rotation after the ASG exists would
          # have failed the apply.
          "ec2:CreateLaunchTemplateVersion",
          "ec2:DeleteLaunchTemplateVersions",
          "ec2:ModifyLaunchTemplate",
          "ec2:CreateRoute",
          "ec2:CreateRouteTable",
          "ec2:CreateSecurityGroup",
          "ec2:CreateSubnet",
          "ec2:CreateTags",
          "ec2:CreateVpc",
          "ec2:CreateFlowLogs",
          "ec2:CreateVpcEndpoint",
          "ec2:DeleteEgressOnlyInternetGateway",
          "ec2:DeleteFlowLogs",
          "ec2:DeleteInternetGateway",
          "ec2:DeleteLaunchTemplate",
          "ec2:DeleteNetworkInterface",
          "ec2:DeleteRoute",
          "ec2:DeleteRouteTable",
          "ec2:DeleteSecurityGroup",
          "ec2:DeleteSubnet",
          # ec2:DeleteTags is the other half of ec2:CreateTags: the provider's
          # tag update path removes dropped keys with DeleteTags before adding
          # new ones with CreateTags, so dropping any key from common_tags /
          # default_tags on an existing VPC, subnet, route table, gateway,
          # security group or launch template fails the apply without it.
          "ec2:DeleteTags",
          "ec2:DeleteVpc",
          "ec2:DeleteVpcEndpoints",
          "ec2:DescribeAccountAttributes",
          "ec2:DescribeAvailabilityZones",
          "ec2:DescribeEgressOnlyInternetGateways",
          "ec2:DescribeFlowLogs",
          "ec2:DescribeImages",
          "ec2:DescribeInstances",
          "ec2:DescribeInstanceStatus",
          "ec2:DescribeInternetGateways",
          "ec2:DescribeLaunchTemplateVersions",
          "ec2:DescribeLaunchTemplates",
          "ec2:DescribeNetworkAcls",
          "ec2:DescribeNetworkInterfaces",
          "ec2:DescribePrefixLists",
          "ec2:DescribeRouteTables",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeSubnets",
          "ec2:DescribeVpcAttribute",
          "ec2:DescribeVpcEndpoints",
          "ec2:DescribeVpcs",
          "ec2:DetachInternetGateway",
          "ec2:DisassociateRouteTable",
          "ec2:ModifySubnetAttribute",
          "ec2:ModifyVpcAttribute",
          "ec2:ModifyVpcEndpoint",
          "ec2:ReplaceRoute",
          # aws_route_table_association's update path calls
          # ReplaceRouteTableAssociation rather than
          # Disassociate + Associate, so moving a subnet between route tables
          # needs it even though both halves of that pair are granted.
          "ec2:ReplaceRouteTableAssociation",
          "ec2:RevokeSecurityGroupEgress",
          "ec2:RevokeSecurityGroupIngress",
        ]
        Resource = "*"
      },
      {
        # ec2:RunInstances is needed for the fck-nat AutoScaling group
        # (one t4g.nano per AZ). Restricting allowed instance types prevents
        # the deploy SA from launching arbitrary large instances and attaching
        # CUDly IAM roles to exfiltrate credentials or run compute at account
        # cost.
        #
        # The operator MUST be StringEqualsIfExists, not StringEquals. AWS
        # authorizes a single RunInstances call against every resource type it
        # touches (instance, volume, network-interface, security-group,
        # subnet, image, key-pair, launch-template), and ec2:InstanceType is
        # only in the request context for the INSTANCE leg. With a plain
        # StringEquals the key is absent on every other leg, the condition
        # evaluates false there, and the call is denied as a whole even though
        # the instance type is correct. StringEqualsIfExists keeps the t4g.nano
        # restriction exactly where the key exists and lets the supporting legs
        # through, which is the pattern the IAM condition-operators reference
        # documents for precisely this call.
        Sid      = "EC2RunInstancesFckNAT"
        Effect   = "Allow"
        Action   = ["ec2:RunInstances"]
        Resource = "*"
        Condition = {
          StringEqualsIfExists = {
            "ec2:InstanceType" = ["t4g.nano"]
          }
        }
      },
      {
        Sid    = "AutoScalingFckNAT"
        Effect = "Allow"
        Action = [
          "autoscaling:CreateAutoScalingGroup",
          "autoscaling:DeleteAutoScalingGroup",
          "autoscaling:DescribeAutoScalingGroups",
          "autoscaling:DescribeScalingActivities",
          "autoscaling:UpdateAutoScalingGroup",
          "autoscaling:CreateOrUpdateTags",
          "autoscaling:DeleteTags",
          "autoscaling:DescribeTags",
        ]
        Resource = "*"
      },
      {
        Sid    = "ACM"
        Effect = "Allow"
        Action = [
          "acm:AddTagsToCertificate",
          "acm:DeleteCertificate",
          "acm:DescribeCertificate",
          "acm:GetCertificate",
          "acm:ListTagsForCertificate",
          "acm:RequestCertificate",
        ]
        Resource = "*"
      },
      {
        Sid    = "Route53"
        Effect = "Allow"
        Action = [
          "route53:ChangeResourceRecordSets",
          "route53:GetChange",
          "route53:GetHostedZone",
          "route53:ListHostedZones",
          "route53:ListResourceRecordSets",
          "route53:ListTagsForResource",
        ]
        Resource = "*"
      },
      {
        Sid    = "KMS"
        Effect = "Allow"
        Action = [
          "kms:CreateGrant",
          "kms:Decrypt",
          "kms:DescribeKey",
          "kms:Encrypt",
          "kms:GenerateDataKey",
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
