# Azure Environment Configuration

This directory contains the Terraform configuration for CUDly on Azure with environment-specific values extracted into `.tfvars` files.

## Structure

```text
azure/
├── main.tf                 # Main infrastructure configuration
├── variables.tf            # Variable declarations
├── outputs.tf             # Output definitions
├── backend.tf             # Backend configuration (Azure Storage)
├── dev.tfvars             # Development environment values
├── staging.tfvars         # Staging environment values (TBD)
├── prod.tfvars            # Production environment values (TBD)
└── backends/              # Backend configuration per environment
    ├── dev.tfbackend      # Azure Storage backend for dev
    ├── staging.tfbackend  # Azure Storage backend for staging
    └── prod.tfbackend     # Azure Storage backend for prod
```

## Usage

```bash
# Initialize with dev backend
terraform init -backend-config=backends/dev.tfbackend

# Plan with dev values
terraform plan -var-file=dev.tfvars

# Apply
terraform apply -var-file=dev.tfvars
```

See AWS README for detailed usage patterns.

## Archera Integration

The Archera commitment-optimisation integration is gated behind the
`enable_archera` tfvars flag (default: `false`) so non-Archera customers
see no drift.

### Enabling Archera

> **IMPORTANT — scope confirmation required before enabling.**
> The permission list in `archera.tf` is provisional. Confirm the exact
> actions required against [Archera's integration docs](https://archera.ai/docs)
> and validate with `@cristim` before setting `enable_archera = true` in
> any environment. See `TODO(@cristim)` comments in `archera.tf`.

1. Obtain the Archera service principal **Object ID** (not the Application
   Client ID) from Archera during onboarding.
2. Set the following in your `*.tfvars`:

   ```hcl
   enable_archera             = true
   archera_azure_sp_object_id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
   ```

3. Run `terraform plan` to review the custom role and role assignment that
   will be created, then apply.

### What gets created

When `enable_archera = true`:

| Resource | Purpose |
| --- | --- |
| `azurerm_role_definition.archera_integration[0]` | Custom RBAC role with read-only cost management + RI purchase actions |
| `azurerm_role_assignment.archera_integration[0]` | Assigns the custom role to Archera's service principal at subscription scope |
