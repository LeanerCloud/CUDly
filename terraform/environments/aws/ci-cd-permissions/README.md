# AWS CI/CD Permissions

This Terraform module provisions a least-privilege IAM role (`cudly-terraform-deploy`) that CUDly's
CI/CD pipeline uses to deploy infrastructure on AWS. It optionally sets up keyless authentication via
**GitHub Actions OIDC** so no long-lived AWS credentials ever need to be stored as secrets.

## What this module creates

| Resource | Purpose |
| --- | --- |
| `aws_iam_role.cudly_deploy` | The deploy role; assumed by GitHub Actions or a human operator |
| `aws_iam_policy.networking` | VPC, subnets, security groups, ALB, ECS cluster |
| `aws_iam_policy.compute` | ECS services/tasks, ECR, CloudWatch Logs, SSM |
| `aws_iam_policy.compute_b` | Overflow for `aws_iam_policy.compute`, which is close to the 6144-character managed-policy limit |
| `aws_iam_policy.data` | RDS, ElastiCache, S3 (state bucket), Secrets Manager |
| `aws_iam_policy.iam` | IAM role creation and policy attachment, gated on the permissions boundary below (#1705) |
| `aws_iam_policy.workload_boundary` | `cudly-deploy-boundary`: the permissions ceiling every role the deploy role creates must carry |
| `aws_iam_openid_connect_provider.github` | GitHub Actions OIDC provider (conditional on `github_repo`) |

## Apply this root BEFORE merging changes that touch the boundary

This root is applied by hand, by a privileged human, and never by a deploy workflow. That
makes the ordering between it and `terraform/environments/aws` load-bearing whenever the
permissions boundary is involved:

- **Apply here first, then merge.** `deploy-aws-lambda.yml` triggers on pushes to `main`
  under `terraform/environments/aws/**` and the AWS compute/database/secrets/networking
  modules. A merge that adds or changes `permissions_boundary` on a module role makes the
  next deploy call `iam:PutRolePermissionsBoundary`, and that grant lives in
  `aws_iam_policy.iam` here. Merging before applying this root turns every AWS deploy red
  with `AccessDenied` until the apply happens. It is recoverable, not destructive, but it
  is avoidable.
- **Rolling back past the boundary change needs care.** `rollback.yml` applies the
  environment root from the dispatched ref's checkout. A ref that predates the boundary has
  no `permissions_boundary` in config while the live roles have one, so the provider issues
  `iam:DeleteRolePermissionsBoundary`, which `IAMDenyStripRoleBoundary` in `policy_iam.tf`
  denies. Roll back to a ref at or after the boundary change.

## Prerequisites

- Terraform >= 1.6
- AWS credentials with `iam:*` and `iam:CreateOpenIDConnectProvider` permissions
- An S3 bucket for Terraform state (see [Backend setup](#backend-setup))

## Backend setup

The remote state is stored in S3. Create the bucket once before the first `terraform init`:

```bash
# Choose a unique bucket name — must match the value in backend.hcl
BUCKET="cudly-terraform-state-dev"
REGION="us-east-1"

aws s3api create-bucket \
  --bucket "$BUCKET" \
  --region "$REGION"

# Enable versioning so you can recover from accidental state corruption
aws s3api put-bucket-versioning \
  --bucket "$BUCKET" \
  --versioning-configuration Status=Enabled

# Enable server-side encryption
aws s3api put-bucket-encryption \
  --bucket "$BUCKET" \
  --server-side-encryption-configuration '{
    "Rules": [{
      "ApplyServerSideEncryptionByDefault": {"SSEAlgorithm": "AES256"}
    }]
  }'

# Block all public access
aws s3api put-public-access-block \
  --bucket "$BUCKET" \
  --public-access-block-configuration \
    "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true"
```

A `backend.hcl.example` is provided; copy it to `backend.hcl` and fill in your values, then pass it
to `terraform init -backend-config=backend.hcl`.

## Usage

```bash
# 1. Copy example configs
cp terraform.tfvars.example terraform.tfvars
cp backend.hcl.example backend.hcl

# 2. Fill in terraform.tfvars (only github_repo is strictly required if using OIDC)
# 3. Fill in backend.hcl with your S3 bucket details

# 4. Initialise
terraform init -backend-config=backend.hcl

# 5. Plan
terraform plan

# 6. Apply
terraform apply
```

After applying, retrieve the outputs used in GitHub Actions:

```bash
terraform output role_arn               # → AWS_ROLE_TO_ASSUME
terraform output oidc_provider_arn      # informational; AWS uses this internally
```

## Importing an existing role

If the `cudly-terraform-deploy` role was created manually before this module was added:

```bash
# Find the role ARN
aws iam get-role --role-name cudly-terraform-deploy --query 'Role.Arn' --output text

terraform import aws_iam_role.cudly_deploy cudly-terraform-deploy
```

## GitHub Actions configuration

### Repository secrets / variables

Set these in **Settings → Secrets and variables → Actions** on the `LeanerCloud/CUDly` GitHub
repository (or in a GitHub Actions Environment for per-environment control):

| Name | Value | How to get it |
| --- | --- | --- |
| `AWS_ROLE_TO_ASSUME` | `arn:aws:iam::<account>:role/cudly-terraform-deploy` | `terraform output role_arn` |
| `AWS_REGION` | `us-east-1` | matches your `aws_region` tfvar |

> No `AWS_ACCESS_KEY_ID` or `AWS_SECRET_ACCESS_KEY` are needed — authentication is keyless via OIDC.

### Example workflow step

```yaml
- name: Configure AWS credentials
  uses: aws-actions/configure-aws-credentials@v4
  with:
    role-to-assume: ${{ secrets.AWS_ROLE_TO_ASSUME }}
    aws-region: ${{ vars.AWS_REGION }}
    # audience defaults to sts.amazonaws.com — matches the OIDC provider
```

The `aws-actions/configure-aws-credentials` action requests an OIDC token from GitHub, exchanges it
for temporary AWS credentials via `sts:AssumeRoleWithWebIdentity`, and injects `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, and `AWS_SESSION_TOKEN` into the environment for all subsequent steps.

### Trust policy conditions

The trust policy does **not** allow all workflows from the repo: `repo:LeanerCloud/CUDly:*` would
let any branch, including an unprotected feature branch, mint valid deploy credentials. Instead the
OIDC `sub` claim is checked against an explicit, enumerated allowlist (`role.tf`'s
`token.actions.githubusercontent.com:sub` condition):

- `repo:LeanerCloud/CUDly:ref:refs/heads/main`, for workflows dispatched on `main` with no
  `environment:` binding.
- `repo:LeanerCloud/CUDly:environment:<name>`, one entry per exact environment name a job that
  assumes this role binds to. A job's `environment:` **replaces** the ref-based subject with an
  environment-scoped one (never both), so every such value must be listed explicitly or that job
  cannot authenticate. See the comment above the `sub` list in `role.tf` for the full derivation:
  which workflow/job each entry covers, and what was deliberately left out and why (`pull_request`
  jobs, and an unreachable `workflow_call` input path), kept in one place so it cannot drift out of
  sync with the policy itself.

If you fork CUDly or use a different repo, set `github_repo` in `terraform.tfvars` to the new
`owner/repo` value and re-apply; the prefix changes, the enumerated suffixes do not. If you add a
new workflow job that assumes this role and binds to a not-yet-listed `environment:`, add its exact
subject to `role.tf` and re-apply *before* that job runs, or `configure-aws-credentials` fails with
`AssumeRoleWithWebIdentity`/`Not authorized`.

### This allowlist alone does not restrict deploys to `main`

The owner constraint on this repo is: only `main` may deploy. Three **different** GitHub-side
controls keep getting conflated across this repo's issues, and this module (and this allowlist)
implements only the first of them:

1. **Allowlist membership** (`role.tf`'s `sub` list, this module): can the job authenticate to AWS
   at all? Nothing below matters if this says no.
2. **Deployment branch policy** (a GitHub environment setting, not configured by this module): which
   *branch* may trigger a deploy to that environment. This is what "only `main` may deploy" actually
   requires, and nothing in this module sets it.
3. **Required reviewers / protection rules** (a GitHub environment setting, not configured by this
   module): *who* must approve before a job bound to that environment proceeds. The subject of
   #1591/#1674/#1660, not this module.

`ref:refs/heads/main` in the allowlist enforces (2) on its own for a job with no `environment:`
binding: no environment, no policy needed, the ref check *is* the restriction. `environment:<name>`
subjects enforce **neither (2) nor (3)** on their own, because the OIDC `sub` for those is
`repo:<repo>:environment:<name>`, which is ref-agnostic: it says which environment the job bound to,
nothing about which branch triggered the run. A `workflow_dispatch` fired from any branch against an
environment-bound job presents the exact same subject a `main` run would, so this allowlist admits
it. The only control that reattaches the branch requirement is a deployment branch policy
(`custom_branch_policies` restricted to `main`) on that environment, configured on the GitHub side.

**Live state, checked against the GitHub API directly** (`gh api repos/LeanerCloud/CUDly/environments`),
not assumed from an earlier issue's snapshot:

| Environment | Exists today? | Protection rules | Deployment branch policy |
| --- | --- | --- | --- |
| `dev` | Yes | none | none |
| `staging` | **No** | n/a | n/a |
| `prod` | **No** | n/a | n/a |
| `aws-fargate-dev` | Yes | none | none |
| `aws-fargate-staging` | Yes | none | none |
| `aws-fargate-prod` | **No** | n/a | n/a |
| `aws-db-dev` / `aws-db-staging` / `aws-db-prod` | **No** (all three) | n/a | n/a |
| `aws-lambda-{dev,staging,prod}-rollback` | **No** (all three) | n/a | n/a |
| `aws-fargate-{dev,staging,prod}-rollback` | **No** (all three) | n/a | n/a |

Only 3 of the 15 environments this allowlist names exist yet, and none of the 3, including `dev`
which multiple deploy jobs already use in production, has a branch policy or protection rules of any
kind. Every environment above needs (2) configured (and the 12 that don't exist yet also need to be
created) before this allowlist actually delivers "only `main` deploys" rather than "only these
environments deploy, from any branch". This module has no GitHub provider configured (no
`provider "github"` or `github_repository_*` resource anywhere under `terraform/` or `iac/`), so
none of this, creation, branch policy, or reviewers, can be done declaratively today; it is a
manual, per-environment step in **Settings -> Environments** on the repo. Adding a GitHub Terraform
provider so this becomes code is #1660's scope, not this module's.

Related: #1648 (this allowlist gap, control 1), #1660 (controls 2 and 3: environments need creating,
a branch policy, and protection rules), #1674 (binds the destroy workflows' jobs to environments
covered by this list).
