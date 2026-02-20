# Terraform Profiles

This directory contains deployment profiles for different environments and cloud providers.

## What Are Profiles?

Profiles are pre-configured Terraform variable files (`.tfvars`) that define environment-specific settings. Think of them as deployment presets.

## Directory Structure

```
profiles/
├── aws/
│   ├── dev.tfvars           # AWS development environment
│   ├── staging.tfvars       # AWS staging environment
│   └── prod.tfvars          # AWS production environment
├── azure/
│   ├── dev.tfvars           # Azure development environment
│   └── prod.tfvars          # Azure production environment
├── gcp/
│   ├── dev.tfvars           # GCP development environment
│   └── prod.tfvars          # GCP production environment
└── common/
    ├── defaults.tfvars      # Common defaults across all profiles
    └── secrets.tfvars.example  # Example secrets file (gitignored)
```

## Using Profiles

### Quick Deployment with Profile

```bash
# Deploy to AWS dev
cd terraform/environments/aws/dev
terraform apply -var-file="../../../profiles/aws/dev.tfvars"

# Deploy to AWS prod
cd terraform/environments/aws/prod
terraform apply -var-file="../../../profiles/aws/prod.tfvars"
```

### With Helper Script (Even Easier)

```bash
# From project root
./scripts/tf-deploy.sh aws dev       # Deploy to AWS dev
./scripts/tf-deploy.sh aws prod      # Deploy to AWS prod
./scripts/tf-deploy.sh azure dev     # Deploy to Azure dev
./scripts/tf-deploy.sh gcp dev       # Deploy to GCP dev

# Plan only (dry run)
./scripts/tf-deploy.sh aws dev plan
```

## Creating a New Profile

### Option 1: Copy from Example

```bash
# Copy example profile
cp profiles/aws/dev.tfvars profiles/aws/my-profile.tfvars

# Edit with your settings
vim profiles/aws/my-profile.tfvars

# Use it
terraform apply -var-file="../../../profiles/aws/my-profile.tfvars"
```

### Option 2: Use Profile Generator

```bash
# Generate new profile interactively
./scripts/generate-profile.sh

# Prompts for:
# - Cloud provider (aws/azure/gcp)
# - Environment name
# - Region
# - Compute platform
# - Other settings

# Creates: profiles/{provider}/{name}.tfvars
```

## Profile Contents

Each profile contains environment-specific variables:

```hcl
# profiles/aws/dev.tfvars

# Project identification
project_name = "cudly"
environment  = "dev"

# Cloud configuration
region       = "us-east-1"
provider     = "aws"

# Compute platform
compute_platform = "lambda"
architecture     = "arm64"
memory_size      = 512

# Database
database_name          = "cudly"
database_version       = "16.4"
database_min_capacity  = 0.5
database_max_capacity  = 2.0

# Frontend
enable_frontend_build = true

# Networking
vpc_cidr = "10.0.0.0/16"

# Feature flags
enable_dashboard = true

# Docker build (auto-generated)
# skip_docker_build = false
# skip_docker_push = false

# Tags
tags = {
  Environment = "dev"
  ManagedBy   = "terraform"
  Project     = "cudly"
}
```

## Common Patterns

### Development Profile

```hcl
# profiles/aws/dev.tfvars
environment             = "dev"
database_min_capacity   = 0.5      # Minimum resources
database_max_capacity   = 1.0
enable_monitoring       = false    # Less monitoring
database_deletion_protection = false
database_skip_final_snapshot = true
```

### Staging Profile

```hcl
# profiles/aws/staging.tfvars
environment             = "staging"
database_min_capacity   = 1.0      # More resources
database_max_capacity   = 2.0
enable_monitoring       = true     # Full monitoring
database_deletion_protection = false
database_skip_final_snapshot = false
```

### Production Profile

```hcl
# profiles/aws/prod.tfvars
environment             = "prod"
database_min_capacity   = 2.0      # Maximum resources
database_max_capacity   = 8.0
enable_monitoring       = true
database_high_availability = true
database_deletion_protection = true
database_skip_final_snapshot = false
database_backup_retention_days = 30
```

## Multi-Cloud Profiles

### AWS Lambda (Serverless)

```hcl
# profiles/aws/lambda-dev.tfvars
provider         = "aws"
compute_platform = "lambda"
region           = "us-east-1"
architecture     = "arm64"
memory_size      = 512
```

### AWS Fargate (Container)

```hcl
# profiles/aws/fargate-dev.tfvars
provider         = "aws"
compute_platform = "fargate"
region           = "us-east-1"
fargate_cpu      = 512
fargate_memory   = 1024
```

### Azure Container Apps

```hcl
# profiles/azure/dev.tfvars
provider         = "azure"
compute_platform = "container-apps"
location         = "eastus"
container_cpu    = 0.5
container_memory = "1Gi"
```

### GCP Cloud Run

```hcl
# profiles/gcp/dev.tfvars
provider         = "gcp"
compute_platform = "cloud-run"
region           = "us-central1"
cloud_run_cpu    = "1"
cloud_run_memory = "512Mi"
```

## Secrets Management

**Never commit secrets to profiles!**

### Using Environment Variables

```bash
# Set secrets as environment variables
export TF_VAR_database_password="secret123"
export TF_VAR_admin_email="admin@example.com"

# Terraform automatically picks them up
terraform apply -var-file="../../../profiles/aws/dev.tfvars"
```

### Using secrets.tfvars (Gitignored)

```bash
# Create secrets file (not tracked in git)
cat > profiles/aws/secrets.tfvars <<EOF
database_password = "secret123"
admin_email       = "admin@example.com"
admin_password    = "admin456"
EOF

# Use both profile and secrets
terraform apply \
  -var-file="../../../profiles/aws/dev.tfvars" \
  -var-file="../../../profiles/aws/secrets.tfvars"
```

### Using Secret Manager

```hcl
# profiles/aws/dev.tfvars
# Reference secrets from AWS Secrets Manager
database_password_secret_arn = "arn:aws:secretsmanager:us-east-1:123456789012:secret:cudly-dev-db-pass"
```

## Profile Validation

Validate profile before applying:

```bash
# Check syntax
terraform validate -var-file="../../../profiles/aws/dev.tfvars"

# Dry run (plan)
terraform plan -var-file="../../../profiles/aws/dev.tfvars"
```

## Best Practices

### 1. Use Consistent Naming

```
{provider}/{environment}.tfvars
  aws/dev.tfvars
  aws/staging.tfvars
  aws/prod.tfvars
```

### 2. Inherit from Common Defaults

```hcl
# profiles/common/defaults.tfvars
database_version       = "16.4"
enable_monitoring      = true
database_backup_retention_days = 7

# profiles/aws/dev.tfvars inherits these
```

### 3. Document Custom Values

```hcl
# profiles/aws/dev.tfvars

# Using t4g (ARM) for cost savings
architecture = "arm64"

# Reduced capacity for dev environment
database_min_capacity = 0.5  # vs 2.0 in prod
```

### 4. Version Control Profiles

```bash
# Commit profiles (without secrets)
git add profiles/aws/dev.tfvars
git add profiles/aws/prod.tfvars

# Ignore secrets
echo "profiles/**/secrets.tfvars" >> .gitignore
```

### 5. Use Profile for Each Environment

```
profiles/
├── aws/
│   ├── dev.tfvars        ← John's development
│   ├── dev-jane.tfvars   ← Jane's development
│   ├── staging.tfvars    ← Shared staging
│   └── prod.tfvars       ← Production
```

## Migrating from CUDly CLI Profiles

Convert `~/.cudly/deployment.yaml` to Terraform profile:

```bash
# CLI profile
cat ~/.cudly/deployment.yaml
# active_profile: aws-dev
# profiles:
#   aws-dev:
#     provider: aws
#     compute_platform: lambda
#     region: us-east-1

# Convert to Terraform profile
cat > profiles/aws/dev.tfvars <<EOF
provider         = "aws"
compute_platform = "lambda"
region           = "us-east-1"
environment      = "dev"
project_name     = "cudly"
EOF
```

## Helper Scripts

### tf-deploy.sh

```bash
#!/bin/bash
# Deploy using profile

PROVIDER=$1
PROFILE=$2
ACTION=${3:-apply}

PROFILE_FILE="profiles/${PROVIDER}/${PROFILE}.tfvars"

if [ ! -f "$PROFILE_FILE" ]; then
  echo "Error: Profile not found: $PROFILE_FILE"
  exit 1
fi

cd "terraform/environments/${PROVIDER}/${PROFILE}"

terraform init
terraform $ACTION -var-file="../../../../${PROFILE_FILE}"
```

### generate-profile.sh

```bash
#!/bin/bash
# Interactive profile generator

echo "Creating new Terraform profile..."
read -p "Cloud provider (aws/azure/gcp): " provider
read -p "Profile name: " profile_name
read -p "Region: " region
read -p "Compute platform: " compute_platform

cat > "profiles/${provider}/${profile_name}.tfvars" <<EOF
# Auto-generated profile
provider         = "${provider}"
environment      = "${profile_name}"
region           = "${region}"
compute_platform = "${compute_platform}"
project_name     = "cudly"

# Add more settings as needed
EOF

echo "✅ Profile created: profiles/${provider}/${profile_name}.tfvars"
```

## Related Documentation

- [Docker Build Module](../modules/build/README.md)
- [Environment Setup](../environments/README.md)
- [Deployment Guide](../../DEPLOYMENT_GUIDE.md)
