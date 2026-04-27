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
