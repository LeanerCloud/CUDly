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
| `aws_iam_policy.data` | RDS, ElastiCache, S3 (state bucket), Secrets Manager |
| `aws_iam_openid_connect_provider.github` | GitHub Actions OIDC provider (conditional on `github_repo`) |

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

The trust policy allows only workflows from `repo:LeanerCloud/CUDly:*`. If you fork CUDly or use a
different repo, set `github_repo` in `terraform.tfvars` to the new `owner/repo` value and re-apply.
