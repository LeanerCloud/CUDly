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
          # iam:UpdateAssumeRolePolicy / UpdateRole / UpdateRoleDescription are
          # what the provider calls when a role's assume_role_policy,
          # max_session_duration or description drifts (resourceRoleUpdate in
          # internal/service/iam/role.go). Without them any edit to a trust
          # policy or role description in terraform/modules/** fails the apply
          # rather than the plan, which is why this gap stayed invisible.
          # IAMDenyModifyDeployRoleAndPolicies below stops these from being
          # turned on the deploy role itself.
          "iam:UpdateAssumeRolePolicy",
          "iam:UpdateRole",
          "iam:UpdateRoleDescription",
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
              # ec2: the fck-nat instance profile referenced by the NAT launch
              # template and Auto Scaling group
              # (terraform/modules/networking/aws, enable_nat_gateway is
              # hardcoded true in terraform/environments/aws/networking.tf, so
              # this is on the deploy path in every environment).
              "ec2.amazonaws.com",
              # vpc-flow-logs: aws_flow_log passes its delivery role via
              # DeliverLogsPermissionArn (enable_flow_logs is true for staging
              # and prod).
              "vpc-flow-logs.amazonaws.com",
              # events: EventBridge targets that carry a role_arn, used by the
              # scheduled ECS tasks on the Fargate deploy path
              # (enable_scheduled_tasks is true in every tfvars).
              "events.amazonaws.com",
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
        # IAMDenyPassDeployRole above closes the pass-the-deploy-role door.
        # This closes the other two doors into the same escalation, both of
        # which IAMRolesAndPolicies leaves open because cudly-terraform-deploy
        # is itself a cudly-* role and cudly-deploy-* are cudly-* policies:
        #
        #  1. Role side: iam:AttachRolePolicy / iam:PutRolePolicy let the
        #     deploy role grant itself AdministratorAccess, and
        #     iam:UpdateAssumeRolePolicy (added to IAMRolesAndPolicies above)
        #     lets it rewrite its own trust policy so an arbitrary principal
        #     can assume it.
        #  2. Policy side: iam:CreatePolicyVersion on cudly-deploy-data (or
        #     any sibling) rewrites the deploy role's own permissions in
        #     place, reaching the same admin without ever touching the role.
        #     Denying only the role side would look complete and would not be.
        #
        # Either turns a leaked GitHub Actions token into persistent
        # account-wide admin. An explicit Deny always beats the Allow, so this
        # closes both loops while leaving the workload roles and policies
        # (cudly-<env>-<suffix>-*) fully manageable.
        #
        # This costs the deploy path nothing: cudly-terraform-deploy and the
        # four cudly-deploy-* policies are all declared in this bootstrap
        # root, which is applied manually by a privileged human and never by a
        # deploy workflow. Action and Resource are matched as a cross product,
        # so the combinations that do not apply (a role action against a
        # policy ARN and vice versa) are simply inert.
        Sid    = "IAMDenyModifyDeployRoleAndPolicies"
        Effect = "Deny"
        Action = [
          "iam:AttachRolePolicy",
          "iam:CreatePolicyVersion",
          "iam:DeletePolicy",
          "iam:DeletePolicyVersion",
          "iam:DeleteRole",
          "iam:DeleteRolePolicy",
          "iam:DetachRolePolicy",
          "iam:PutRolePolicy",
          "iam:SetDefaultPolicyVersion",
          "iam:UpdateAssumeRolePolicy",
          "iam:UpdateRole",
          "iam:UpdateRoleDescription",
        ]
        Resource = [
          "arn:aws:iam::*:role/cudly-terraform-deploy",
          "arn:aws:iam::*:policy/cudly-deploy-*",
        ]
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
        # Service-linked roles are created lazily by AWS on the FIRST use of a
        # service in an account/region, and the caller needs
        # iam:CreateServiceLinkedRole for it. That makes these gaps invisible
        # in an account that already has the role and a hard failure in a
        # fresh region, which is exactly the kind of latent break this audit
        # was meant to surface. autoscaling (fck-nat ASG, on every deploy
        # path), elasticloadbalancing and ecs (Fargate ALB and cluster) were
        # all missing; note that ecs.application-autoscaling is a different
        # principal from plain ecs and does not cover it.
        Sid    = "IAMServiceLinkedRole"
        Effect = "Allow"
        Action = ["iam:CreateServiceLinkedRole"]
        Resource = [
          "arn:aws:iam::*:role/aws-service-role/rds.amazonaws.com/AWSServiceRoleForRDS",
          "arn:aws:iam::*:role/aws-service-role/ecs.application-autoscaling.amazonaws.com/AWSServiceRoleForApplicationAutoScaling_ECSService",
          "arn:aws:iam::*:role/aws-service-role/autoscaling.amazonaws.com/AWSServiceRoleForAutoScaling",
          "arn:aws:iam::*:role/aws-service-role/elasticloadbalancing.amazonaws.com/AWSServiceRoleForElasticLoadBalancing",
          "arn:aws:iam::*:role/aws-service-role/ecs.amazonaws.com/AWSServiceRoleForECS",
        ]
        Condition = {
          StringLike = {
            "iam:AWSServiceName" = [
              "rds.amazonaws.com",
              "ecs.application-autoscaling.amazonaws.com",
              "autoscaling.amazonaws.com",
              "elasticloadbalancing.amazonaws.com",
              "ecs.amazonaws.com",
            ]
          }
        }
      },
      {
        # RDS actions whose request carries a name we control are scoped to
        # cudly-* DB instances, subnet groups and snapshots. This prevents the
        # deploy SA from deleting or modifying unrelated RDS resources in the
        # same account.
        #
        # NO rds:Describe* action lives here; they are all in
        # RDSDescribeAccountWide below. See that statement for why.
        #
        # KNOWN DEAD SCOPES: the proxy actions below (DeleteDBProxy,
        # ModifyDBProxy, RegisterDBProxyTargets, DeregisterDBProxyTargets)
        # can never be authorized, because their only resource types are
        # `proxy` and `target-group`, whose real ARNs are
        # arn:aws:rds:<r>:<a>:db-proxy:prx-<opaque> and
        # arn:aws:rds:<r>:<a>:target-group:prx-tg-<opaque>: a different ARN
        # segment (`db-proxy`, not `proxy`) and a server-assigned opaque id
        # that never contains the resource name. `proxy:cudly-*` and
        # `target-group:cudly-*` therefore match nothing. Nothing exercises
        # this today (modules/database/aws is instantiated with
        # enable_rds_proxy = false, terraform/environments/aws/database.tf),
        # so it is left as-is rather than widened to db-proxy:* here; fixing
        # it needs a scoping mechanism that actually constrains an opaque id
        # (tag condition), tracked separately. Do NOT add new proxy actions
        # to this statement; they would be dead on arrival.
        Sid    = "RDSResourceScoped"
        Effect = "Allow"
        Action = [
          "rds:AddTagsToResource",
          "rds:CreateDBInstance",
          "rds:CreateDBSnapshot",
          "rds:CreateDBSubnetGroup",
          "rds:DeleteDBInstance",
          "rds:DeleteDBSubnetGroup",
          "rds:ListTagsForResource",
          "rds:DeleteDBProxy",
          "rds:DeregisterDBProxyTargets",
          "rds:ModifyDBInstance",
          "rds:ModifyDBProxy",
          "rds:ModifyDBSubnetGroup",
          "rds:RegisterDBProxyTargets",
          "rds:RemoveTagsFromResource",
        ]
        Resource = [
          "arn:aws:rds:*:*:db:cudly-*",
          "arn:aws:rds:*:*:subgrp:cudly-*",
          "arn:aws:rds:*:*:proxy:cudly-*",
          "arn:aws:rds:*:*:target-group:cudly-*",
          "arn:aws:rds:*:*:snapshot:cudly-*",
        ]
      },
      {
        # RDS reads that an ARN-scoped grant can never authorize.
        #
        # rds:DescribeDBEngineVersions is a catalogue lookup with no resource
        # type at all, so it can only be granted on "*".
        #
        # rds:DescribeDBInstances is here because of the request shape the AWS
        # provider uses, not because the action lacks a resource type. The
        # Terraform id of aws_db_instance IS the DbiResourceId (`db-<opaque>`,
        # set at internal/service/rds/instance.go in provider v5.100.0), and
        # findDBInstanceByID branches on that: when the id looks like a
        # DbiResourceId it sends DescribeDBInstances with
        # Filters=[dbi-resource-id] and leaves DBInstanceIdentifier nil. RDS
        # authorizes an identifier-less DescribeDBInstances against the
        # wildcard ARN arn:aws:rds:<region>:<account>:db:*, which
        # `db:cudly-*` cannot match, so every refresh of the instance 403'd:
        #   AccessDenied: ... not authorized to perform: rds:DescribeDBInstances
        #   on resource: arn:aws:rds:us-east-1:...:db:*
        # This is unconditional on every read, not a not-found fallback: the
        # provider's retry with the plain identifier only fires on NotFound,
        # and AccessDenied is not NotFound, so the plan hard-fails.
        #
        # rds:DescribeDBSubnetGroups is here for the same reason as
        # DescribeDBInstances, and it is worth being explicit about why,
        # because the tempting simplification is wrong in both directions.
        # Neither action lacks a resource type: per AWS's Service
        # Authorization Reference both DO support one (`db` and `subgrp`
        # respectively), and the real names DO match the patterns
        # (cudly-<env>-<hex>-postgres against db:cudly-*,
        # cudly-<env>-<hex>-db-subnet against subgrp:cudly-*). So this is not
        # "ARN-scoped Describes are always dead" and it is not a name-prefix
        # bug; the scoped grants were syntactically valid. What differs is the
        # request shape: a resource-scoped grant authorizes only the form that
        # targets one named resource, while the enumerate form (no identifier
        # in the request) is authorized against the wildcard ARN. The
        # production 403 on DescribeDBInstances is the empirical proof that
        # the scoped form did not suffice, and DescribeDBSubnetGroups sits in
        # exactly the same position: aws_db_subnet_group.main
        # (terraform/modules/database/aws/main.tf) is created in every
        # environment, so leaving it scoped just primes the next 403.
        #
        # The DB proxy reads are here for a third reason: their resource ARNs
        # use the `db-proxy:`/`target-group:` segments with server-assigned
        # opaque ids (see the RDSResourceScoped note above), so no name-based
        # ARN scope can ever match them. Those are belt-and-braces rather than
        # load-bearing: enable_rds_proxy is a literal `false` at
        # terraform/environments/aws/database.tf:28, not a variable, so no
        # tfvars can turn the proxy resources on.
        #
        # All six are read-only and expose only resource metadata; every
        # mutating RDS action stays ARN-scoped above.
        Sid    = "RDSDescribeAccountWide"
        Effect = "Allow"
        Action = [
          "rds:DescribeDBEngineVersions",
          "rds:DescribeDBInstances",
          "rds:DescribeDBProxies",
          "rds:DescribeDBProxyTargetGroups",
          "rds:DescribeDBProxyTargets",
          "rds:DescribeDBSubnetGroups",
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
        # secretsmanager:ListSecrets used to be in this list. It has no
        # resource type per the AWS Service Authorization Reference, so
        # inside an ARN-scoped statement it could never be satisfied: a
        # silently dead grant. Nothing in the provider calls it (secret
        # lookups go through DescribeSecret with an explicit id), so it is
        # dropped rather than moved to a "*" statement.
        #
        # ListSecretVersionIds and UpdateSecretVersionStage are what
        # aws_secretsmanager_secret_version calls when a version carries a
        # stage other than a lone AWSCURRENT, and on the delete path when it
        # has to move stages off the version before removing it
        # (resourceSecretVersionDelete/Update in
        # internal/service/secretsmanager/secret_version.go). Both take the
        # secret ARN, so the cudly-* scope below applies.
        Sid    = "SecretsManager"
        Effect = "Allow"
        Action = [
          "secretsmanager:CreateSecret",
          "secretsmanager:DeleteSecret",
          "secretsmanager:DescribeSecret",
          "secretsmanager:GetResourcePolicy",
          "secretsmanager:GetSecretValue",
          "secretsmanager:ListSecretVersionIds",
          "secretsmanager:PutSecretValue",
          "secretsmanager:RestoreSecret",
          "secretsmanager:RotateSecret",
          "secretsmanager:TagResource",
          "secretsmanager:UntagResource",
          "secretsmanager:UpdateSecret",
          "secretsmanager:UpdateSecretVersionStage",
        ]
        Resource = "arn:aws:secretsmanager:*:*:secret:cudly-*"
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
