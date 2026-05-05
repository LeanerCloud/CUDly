# AWS Environment Configuration

This directory contains the Terraform configuration for CUDly on AWS with environment-specific values extracted into `.tfvars` files.

## Structure

```text
aws/
├── main.tf                 # Provider + locals
├── variables.tf            # Variable declarations
├── outputs.tf              # Output definitions
├── backend.tf              # Backend configuration (S3)
├── networking.tf           # VPC, subnets, security groups
├── compute.tf              # Lambda / Fargate resources
├── database.tf             # RDS PostgreSQL
├── secrets.tf              # Secrets Manager
├── build.tf                # Docker build (optional)
├── frontend.tf             # CloudFront + S3 (optional)
├── archera.tf              # Archera integration (gated on enable_archera)
├── dev.tfvars.example      # Example dev values (copy to dev.tfvars)
└── ci-cd-permissions/      # Bootstrap-only: deploy IAM role (apply once)
    └── README.md
```

## Usage

```bash
# Copy example config
cp dev.tfvars.example dev.tfvars
# Edit dev.tfvars with your values

# Initialize with dev backend
terraform init -backend-config=backends/dev.tfbackend

# Plan
terraform plan -var-file=dev.tfvars

# Apply
terraform apply -var-file=dev.tfvars
```

## Archera Integration

The Archera commitment-optimisation integration is gated behind the
`enable_archera` tfvars flag (default: `false`) so non-Archera customers
see no drift.

### Enabling Archera

> **IMPORTANT — scope confirmation required before enabling.**
> The permission list in `archera.tf` is provisional. Confirm the exact
> AWS IAM actions required against [Archera's integration docs](https://archera.ai/docs)
> and validate with `@cristim` before setting `enable_archera = true` in
> any environment. See `TODO(@cristim)` comments in `archera.tf`.

1. Obtain the Archera **AWS account ID** from Archera during onboarding
   (the account whose IAM principal will assume the cross-account role).
2. Set the following in your `*.tfvars`:

   ```hcl
   enable_archera         = true
   archera_aws_account_id = "123456789012"           # Archera's account ID
   archera_external_id    = "replace-with-archera-extid"
   # Optional: only after the purchase approval workflow is confirmed
   # enable_archera_purchase_actions = true
   ```

3. Run `terraform plan` to review the cross-account IAM role and policy
   that will be created, then apply.

### What gets created

When `enable_archera = true`:

| Resource | Purpose |
| --- | --- |
| `aws_iam_role.archera_integration[0]` | Cross-account role trusted by Archera's AWS account |
| `aws_iam_policy.archera_read[0]` | Provisional read-only policy for cost / commitment telemetry |
| `aws_iam_role_policy_attachment.archera_read[0]` | Attaches the read-only policy to the role |
| `aws_iam_policy.archera_purchase[0]` | Optional RI/SP purchase policy, created only when `enable_archera_purchase_actions = true` |
| `aws_iam_role_policy_attachment.archera_purchase[0]` | Attaches the optional purchase policy to the role |
