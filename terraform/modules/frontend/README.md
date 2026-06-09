# Frontend Deployment Modules

These Terraform modules handle **complete frontend deployment** including:

1. Building the frontend (npm install + npm run build)
2. Uploading static files to cloud storage (S3/Blob/Cloud Storage)
3. Invalidating CDN cache (CloudFront/Azure CDN/Cloud CDN)

## How It Works

### Automatic Frontend Build and Deployment

When you run `terraform apply`, the frontend modules automatically:

1. **Detect Changes**: Hash package.json and source files to detect changes
2. **Build**: Run `npm install` and `npm run build` in `frontend/` directory
3. **Upload**: Upload all files from `frontend/dist/` to cloud storage
4. **Cache**: Set appropriate cache headers (long cache for assets, no-cache for HTML)
5. **Invalidate**: Clear CDN cache so users get the latest version

### Smart Rebuilding

Frontend only rebuilds when:

- `frontend/package.json` changes (dependencies updated)
- Source files in `frontend/src/` change
- Build output files are missing or changed

Infrastructure-only changes (scaling, IAM, etc.) won't trigger frontend rebuild.

## Provider-Specific Implementation

### AWS (CloudFront + S3)

**Resources Created:**

- `terraform_data.frontend_build` - Runs npm build
- `aws_s3_object.frontend_files` - Uploads each file individually to S3
- `terraform_data.cloudfront_invalidation` - Invalidates CloudFront cache

**File Upload Strategy:**

- Uses `aws_s3_object` resource for each file (tracked in state)
- Automatic MIME type detection
- Cache headers: 1 year for assets, no-cache for HTML
- Individual file etags for change detection

**Requirements:**

- AWS CLI configured (`aws configure` or environment variables)
- Permissions: `s3:PutObject`, `cloudfront:CreateInvalidation`

### Azure (Azure CDN + Blob Storage)

**Resources Created:**

- `terraform_data.frontend_build` - Runs npm build
- `terraform_data.frontend_upload` - Batch uploads to $web container
- `terraform_data.cdn_purge` - Purges Azure CDN cache

**File Upload Strategy:**

- Uses `az storage blob upload-batch` for efficient batch upload
- Separate batches for cached assets vs HTML files
- Uploads to `$web` container (automatically created by static website)

**Requirements:**

- Azure CLI installed and authenticated (`az login`)
- Permissions: Storage Blob Data Contributor, CDN Endpoint Contributor

### GCP (Cloud CDN + Cloud Storage)

**Resources Created:**

- `terraform_data.frontend_build` - Runs npm build
- `terraform_data.frontend_upload` - Syncs files with gsutil
- `terraform_data.cdn_invalidation` - Invalidates Cloud CDN

**File Upload Strategy:**

- Uses `gsutil rsync` for efficient sync (only uploads changed files)
- Separate `setmeta` commands for cache control headers
- Deletes removed files with `-d` flag

**Requirements:**

- gcloud CLI installed and authenticated (`gcloud auth login`)
- Permissions: Storage Object Admin, Compute Load Balancer Admin

## Usage

### Basic Usage (Frontend Enabled)

```hcl
module "frontend" {
  source = "../../modules/frontend/aws"

  project_name         = "cudly"
  environment          = "dev"
  bucket_name          = "cudly-dev-frontend"
  api_domain_name      = module.compute.lambda_function_url_domain
  cloudfront_secret    = random_password.cloudfront_secret.result

  # Frontend build is enabled by default
  # enable_frontend_build = true
}
```

### Disable Frontend Build (Infrastructure Only)

When making infrastructure-only changes (scaling, security, IAM), skip frontend build:

```hcl
module "frontend" {
  source = "../../modules/frontend/aws"

  # ... other configuration ...

  # Skip frontend build for faster terraform apply
  enable_frontend_build = false
}
```

Or via variable:

```bash
# In terraform.tfvars
enable_frontend_build = false

# Or via command line
terraform apply -var="enable_frontend_build=false"
```

### First-Time Setup

When deploying for the first time:

```bash
# 1. Ensure frontend dependencies are installed
cd frontend
npm install
cd ..

# 2. Run terraform apply (will build and deploy frontend)
cd terraform/environments/aws/dev
terraform apply
```

### Code Updates Only

For quick frontend-only updates (no infrastructure changes):

```bash
# 1. Make code changes in frontend/src/
# 2. Run terraform apply (only frontend resources will change)
terraform apply
```

Terraform will:

- Detect source file changes via hash
- Rebuild frontend
- Upload changed files
- Invalidate CDN cache

## Configuration Variables

### All Providers

| Variable | Description | Default |
|----------|-------------|---------|
| `enable_frontend_build` | Enable/disable frontend build and deployment | `true` |
| `project_name` | Project name for resource naming | Required |
| `environment` | Environment (dev/staging/prod) | Required |

### AWS-Specific

| Variable | Description |
|----------|-------------|
| `bucket_name` | S3 bucket name |
| `api_domain_name` | Lambda Function URL domain |
| `cloudfront_secret` | Secret for CloudFront origin verification |
| `domain_names` | Custom domains (optional) |
| `acm_certificate_arn` | ACM certificate ARN (optional) |

### Azure-Specific

| Variable | Description |
|----------|-------------|
| `resource_group_name` | Azure resource group |
| `storage_account_name` | Storage account name |
| `api_hostname` | Container App hostname |
| `cdn_sku` | CDN SKU (Standard_Microsoft, etc.) |

### GCP-Specific

| Variable | Description |
|----------|-------------|
| `project_id` | GCP project ID |
| `bucket_name` | Cloud Storage bucket name |
| `cloud_run_service_name` | Cloud Run service name |
| `domain_names` | Custom domains (optional) |

## Cache Strategy

### Static Assets (Long Cache)

Files: `*.js`, `*.css`, `*.png`, `*.jpg`, `*.svg`, `*.woff*`, `*.ttf`

- **Cache-Control**: `public, max-age=31536000, immutable`
- **Why**: Content-hashed filenames mean these never change
- **Benefit**: Reduced bandwidth, faster page loads

### HTML Files (No Cache)

Files: `*.html`

- **Cache-Control**: `no-cache, no-store, must-revalidate`
- **Why**: Entry points that reference hashed assets
- **Benefit**: Users always get latest version after deployment

## Troubleshooting

### Frontend Build Fails

**Error**: `npm: command not found`

**Solution**: Install Node.js and npm

```bash
# macOS
brew install node

# Ubuntu/Debian
sudo apt-get install nodejs npm

# Verify
node --version
npm --version
```

### Build Succeeds but Files Not Uploaded

**AWS**: Check AWS credentials and S3 permissions

```bash
aws sts get-caller-identity
aws s3 ls s3://your-bucket-name/
```

**Azure**: Check Azure CLI authentication

```bash
az account show
az storage account show --name your-storage-account
```

**GCP**: Check gcloud authentication

```bash
gcloud auth list
gsutil ls gs://your-bucket-name/
```

### CDN Invalidation Fails

**AWS CloudFront**: Check invalidation limit (1000 free per month)

```bash
aws cloudfront list-invalidations --distribution-id YOUR_DIST_ID
```

**Azure CDN**: May take 10-15 minutes to complete

```bash
az cdn endpoint show --name your-endpoint --profile-name your-profile
```

**GCP Cloud CDN**: Check URL map exists

```bash
gcloud compute url-maps list
```

### Source Hash Not Detecting Changes

If Terraform doesn't detect source file changes:

```bash
# Force rebuild by touching package.json
touch frontend/package.json

# Or manually taint the build resource
terraform taint 'module.frontend.terraform_data.frontend_build[0]'
```

## Performance Optimization

### Skip Frontend for Infrastructure Changes

Frontend rebuild takes 30-60 seconds. When making infrastructure-only changes:

```bash
# Set in terraform.tfvars
enable_frontend_build = false

# Faster applies (5-10 seconds vs 30-60 seconds)
terraform apply
```

### Parallel Builds (Advanced)

For multiple environments, build once and reuse:

```bash
# 1. Build frontend once
cd frontend
npm run build

# 2. Deploy to multiple environments with pre-built frontend
cd ../terraform/environments/aws/dev
terraform apply -var="enable_frontend_build=false"

cd ../staging
terraform apply -var="enable_frontend_build=false"

cd ../prod
terraform apply -var="enable_frontend_build=false"
```

## CI/CD Integration

### GitHub Actions

```yaml
name: Deploy Frontend

on:
  push:
    branches: [main]
    paths:
      - 'frontend/**'

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Setup Node.js
        uses: actions/setup-node@v3
        with:
          node-version: '18'

      - name: Setup Terraform
        uses: hashicorp/setup-terraform@v2

      - name: Terraform Apply
        run: |
          cd terraform/environments/aws/prod
          terraform init
          terraform apply -auto-approve
        env:
          AWS_ACCESS_KEY_ID: ${{ secrets.AWS_ACCESS_KEY_ID }}
          AWS_SECRET_ACCESS_KEY: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
```

### GitLab CI

```yaml
deploy-frontend:
  image: hashicorp/terraform:latest
  before_script:
    - apk add --no-cache nodejs npm aws-cli
  script:
    - cd terraform/environments/aws/prod
    - terraform init
    - terraform apply -auto-approve
  only:
    changes:
      - frontend/**
```

## Migration from CLI Tool

If you were using the `cudly deploy` CLI tool:

**Before** (CLI tool):

```bash
cudly deploy --profile aws-dev
```

**After** (Pure Terraform):

```bash
cd terraform/environments/aws/dev
terraform apply
```

**Benefits of Terraform Approach:**

- ✅ Infrastructure and frontend in single command
- ✅ Full state tracking for all resources
- ✅ Rollback capability
- ✅ CI/CD friendly
- ✅ No external dependencies (just Terraform)

**CLI Tool Still Useful For:**

- ❌ Guided setup for beginners
- ❌ Profile management
- ❌ Multi-cloud abstraction

## Architecture Diagram

```text
Terraform Apply
    ↓
┌─────────────────────────────────────────┐
│ 1. terraform_data.frontend_build        │
│    - Runs: npm install                  │
│    - Runs: npm run build                │
│    - Output: frontend/dist/             │
└─────────────────────────────────────────┘
    ↓
┌─────────────────────────────────────────┐
│ 2. Upload Resources                      │
│    AWS: aws_s3_object (per file)        │
│    Azure: az storage blob upload-batch  │
│    GCP: gsutil rsync                    │
└─────────────────────────────────────────┘
    ↓
┌─────────────────────────────────────────┐
│ 3. CDN Invalidation                     │
│    AWS: cloudfront create-invalidation  │
│    Azure: az cdn endpoint purge         │
│    GCP: gcloud url-maps invalidate      │
└─────────────────────────────────────────┘
    ↓
✅ Frontend Deployed!
```

## FAQ

**Q: Do I need the cudly deploy CLI anymore?**
A: No, Terraform handles everything now. The CLI is optional for simplified workflows.

**Q: What if I only want to deploy infrastructure?**
A: Set `enable_frontend_build = false` in your terraform.tfvars.

**Q: How long does frontend deployment take?**
A: First time: 60-90 seconds. Subsequent updates: 30-45 seconds.

**Q: Can I deploy frontend separately from infrastructure?**
A: Yes, use `terraform apply -target=module.frontend`.

**Q: Does this work with Terraform Cloud/Enterprise?**
A: Yes, but ensure the runner has Node.js, npm, and cloud CLI tools installed.

**Q: What about monorepo setups?**
A: Adjust the `path.root/../../../frontend` paths in frontend-build.tf to match your structure.

## Related Documentation

- [AWS Lambda Deployment Guide](../../docs/terraform/aws-lambda.md)
- [Azure Container Apps Guide](../../docs/terraform/azure-container-apps.md)
- [GCP Cloud Run Guide](../../docs/terraform/gcp-cloud-run.md)
- [CLI Deployment Guide](../../docs/CLI_DEPLOYMENT.md)
