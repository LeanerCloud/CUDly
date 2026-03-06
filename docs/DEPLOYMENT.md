# CUDly Deployment Guide

CUDly supports deployment via Terraform across three cloud providers (AWS, Azure, GCP). A helper script (`scripts/tf-deploy.sh`) simplifies common operations.

## Table of Contents

- [Quick Start](#quick-start)
- [Platform Comparison](#platform-comparison)
- [Terraform Deployment](#terraform-deployment)
- [AWS Deployment Details](#aws-deployment-details)
- [Azure Deployment Details](#azure-deployment-details)
- [GCP Deployment Details](#gcp-deployment-details)
- [Deploying Code Updates](#deploying-code-updates)
- [Fargate (Side-by-Side with Lambda)](#fargate-side-by-side-with-lambda)
- [CDN Architecture](#cdn-architecture)
- [Cost Estimates](#cost-estimates)
- [Monitoring](#monitoring)
- [Maintenance](#maintenance)
- [Troubleshooting](#troubleshooting)

---

## Quick Start

### Using the Deploy Script

```bash
# Deploy to AWS dev environment
./scripts/tf-deploy.sh aws dev

# Plan only (dry run)
./scripts/tf-deploy.sh aws dev plan

# Deploy to other providers/environments
./scripts/tf-deploy.sh azure dev
./scripts/tf-deploy.sh gcp dev
./scripts/tf-deploy.sh aws prod
```

The script uses profile-based tfvars from `terraform/profiles/<provider>/<profile>.tfvars`.

### Manual Terraform

```bash
cd terraform/environments/aws
cp dev.tfvars.example dev.tfvars  # edit with your values

terraform init -backend-config=backends/dev.tfbackend
terraform plan -var-file=dev.tfvars
terraform apply -var-file=dev.tfvars
```

Terraform automatically handles: Docker image build/push (via build module), frontend build/deploy to CDN, CDN cache invalidation, and admin user creation.

### Prerequisites

- Docker with buildx support
- Terraform >= 1.6.0
- Go 1.25+
- Cloud CLI configured: `aws`, `az`, or `gcloud`

---

## Platform Comparison

| Provider | Serverless | Containers/Kubernetes | Status |
|----------|-----------|----------------------|--------|
| **AWS** | Lambda | Fargate (ECS) | Fully implemented |
| **Azure** | Container Apps | AKS | Container Apps implemented |
| **GCP** | Cloud Run | GKE | Cloud Run implemented |

| | Lambda | Fargate | Container Apps | Cloud Run |
|---|---|---|---|---|
| **Timeout** | 15 min max | Unlimited | Unlimited | 60 min max |
| **Memory** | 128-10240 MB | 512-30720 MB | 0.5-4 GB | 128 MB-32 GB |
| **CPU** | Tied to memory | 256-4096 units | 0.25-2 vCPU | 1-8 vCPU |
| **Scaling** | Auto (1000 concurrent) | Auto (1-N tasks) | Auto (0-N) | Auto (0-1000) |
| **Cold Start** | ~1-2s | No (always warm) | ~1-2s | ~0.5-1s |
| **Cost (idle)** | $0 | ~$30/mo (1 task) | $0 (scale to zero) | $0 (scale to zero) |
| **Load Balancer** | Not needed | Required (ALB) | Built-in | Built-in |

---

## Terraform Deployment

### Directory Structure

```text
terraform/
├── environments/
│   ├── aws/           # main.tf, variables.tf, outputs.tf, backend.tf,
│   │                  # networking.tf, database.tf, compute.tf, frontend.tf,
│   │                  # secrets.tf, build.tf, ses.tf, route53.tf, acm.tf,
│   │                  # dev.tfvars.example, backends/
│   ├── azure/         # similar structure
│   └── gcp/           # similar structure
├── modules/
│   ├── build/          # Docker build (docker-build.tf)
│   ├── compute/
│   │   ├── aws/lambda/
│   │   ├── aws/fargate/
│   │   ├── aws/cleanup-lambda/
│   │   ├── azure/container-apps/
│   │   └── gcp/cloud-run/
│   ├── database/       # aws/ (Aurora), azure/ (Flexible Server), gcp/ (Cloud SQL)
│   ├── frontend/       # aws/ (CloudFront+S3), azure/ (CDN+Blob), gcp/ (Cloud CDN+GCS)
│   ├── monitoring/     # aws/, azure/, gcp/
│   ├── networking/     # aws/ (VPC), azure/ (VNet), gcp/ (VPC)
│   ├── registry/       # aws/ (ECR), azure/ (ACR), gcp/ (Artifact Registry)
│   └── secrets/        # aws/ (Secrets Manager), azure/ (Key Vault), gcp/ (Secret Manager)
└── profiles/
    ├── aws/            # dev.tfvars, prod.tfvars, fargate-dev.tfvars, *.example
    ├── azure/          # dev.tfvars.example
    └── gcp/            # dev.tfvars.example
```

### Manual Terraform Operations

```bash
cd terraform/environments/aws

terraform init -backend-config=backends/dev.tfbackend
terraform plan -var-file=../../profiles/aws/dev.tfvars
terraform apply -var-file=../../profiles/aws/dev.tfvars
terraform output
terraform destroy -var-file=../../profiles/aws/dev.tfvars
```

### Example dev.tfvars (AWS)

See `terraform/environments/aws/dev.tfvars.example` for a complete reference. Key variables:

```hcl
project_name = "cudly"
environment  = "dev"
stack_name   = "cudly-dev"
region       = "us-east-1"
aws_profile  = "default"

# Compute platform: "lambda" or "fargate"
compute_platform = "lambda"

# Lambda configuration
lambda_architecture        = "arm64"
lambda_memory_size         = 512
lambda_timeout             = 60

# Database configuration (Aurora Serverless v2)
database_engine_version       = "16.4"
database_name                 = "cudly"
database_username             = "cudly"
database_min_capacity         = 0.5
database_max_capacity         = 2.0
database_backup_retention_days = 7

# Admin user
admin_email = "admin@example.com"

# Frontend (optional)
enable_frontend_build       = true
frontend_price_class        = "PriceClass_100"
```

### State Management

**Local (dev):** State stored in `terraform.tfstate` (default).

**Remote (production):** Configure backend using `backends/*.tfbackend` files:

```bash
# Initialize with a specific backend config
terraform init -backend-config=backends/prod.tfbackend
```

Example backend config (`backends/prod.tfbackend`):

```hcl
bucket         = "cudly-terraform-state-prod"
key            = "prod/terraform.tfstate"
region         = "us-east-1"
encrypt        = true
use_lockfile   = true
```

---

## AWS Deployment Details

### Infrastructure Created

- **Compute:** Lambda (ARM64, container image) with Function URL, or Fargate (ECS) with ALB
- **Database:** Aurora Serverless v2 PostgreSQL 16.4 (0.5-2.0 ACU)
- **Network:** VPC (10.0.0.0/16) with IPv6 dual-stack, private/public subnets, no NAT Gateway
- **Proxy:** RDS Proxy for Lambda connection pooling
- **Frontend:** CloudFront (dual-origin: S3 for static, Lambda/ALB for API) + S3
- **Secrets:** Secrets Manager (DB password, JWT secret, session secret)
- **Monitoring:** CloudWatch log groups, alarms, EventBridge scheduled tasks

### Deploy with Script

```bash
# Deploy to dev
./scripts/tf-deploy.sh aws dev

# Plan only
./scripts/tf-deploy.sh aws dev plan

# Deploy to staging/prod
./scripts/tf-deploy.sh aws prod
```

### Verify Deployment

```bash
cd terraform/environments/aws

FUNCTION_URL=$(terraform output -raw lambda_function_url)
curl "$FUNCTION_URL/health"
# Expected: {"status":"healthy","version":"...","timestamp":"...","checks":{"config_store":{"status":"healthy"},"auth_store":{"status":"healthy"}}}

# Monitor logs
aws logs tail /aws/lambda/cudly-dev-api --follow
aws logs tail /aws/lambda/cudly-dev-api --filter-pattern "ERROR"
```

### Deploy Frontend

```bash
cd terraform/environments/aws
S3_BUCKET=$(terraform output -raw frontend_bucket)
CF_DIST_ID=$(terraform output -raw cloudfront_distribution_id)

# Build and upload
cd ../../../../frontend && npm install && npm run build

aws s3 sync dist/ "s3://${S3_BUCKET}/" --delete \
  --cache-control "public,max-age=3600"

aws cloudfront create-invalidation --distribution-id "$CF_DIST_ID" \
  --paths "/*"
```

---

## Azure Deployment Details

### Infrastructure Created

- **Compute:** Azure Container Apps (serverless containers)
- **Database:** Azure PostgreSQL Flexible Server
- **Frontend:** Azure Blob Storage (static website) + Azure CDN
- **Secrets:** Azure Key Vault
- **Monitoring:** Azure Monitor alerts

```bash
# Deploy with script
./scripts/tf-deploy.sh azure dev
```

### Deploy Frontend

```bash
az storage blob upload-batch \
  --account-name cudlyfrontendprod \
  --destination '$web' --source dist/ --overwrite

az cdn endpoint purge \
  --resource-group cudly-prod-rg \
  --profile-name cudly-cdn-profile \
  --name cudly-cdn-endpoint --content-paths "/*"
```

---

## GCP Deployment Details

### Infrastructure Created

- **Compute:** Cloud Run service (serverless containers)
- **Database:** Cloud SQL PostgreSQL
- **Frontend:** Cloud Storage + Global HTTPS Load Balancer + Cloud CDN
- **Secrets:** Secret Manager
- **Monitoring:** Cloud Monitoring alerts
- **Optional:** Cloud Armor (WAF/DDoS protection)

```bash
# Deploy with script
./scripts/tf-deploy.sh gcp dev
```

### Deploy Frontend

```bash
gsutil -m rsync -r -d -x ".*\.map$" dist/ gs://cudly-frontend-prod/

gsutil -m setmeta -h "Cache-Control:public, max-age=31536000, immutable" \
  gs://cudly-frontend-prod/js/**

gcloud compute url-maps invalidate-cdn-cache cudly-url-map --path "/*"
```

---

## Deploying Code Updates

When infrastructure already exists and you only need to update application code:

### Using the Deploy Script

```bash
./scripts/tf-deploy.sh aws dev                  # dev
./scripts/tf-deploy.sh aws prod                  # production
./scripts/tf-deploy.sh aws dev plan              # dry run
```

The script: initializes Terraform if needed, runs `terraform apply` with the profile-specific tfvars, and shows outputs on success.

### Manual Steps

```bash
# 1. Build image
GIT_COMMIT=$(git rev-parse --short HEAD)
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
IMAGE_URI="${AWS_ACCOUNT_ID}.dkr.ecr.us-east-1.amazonaws.com/cudly:${GIT_COMMIT}"

docker build --platform linux/arm64 --build-arg VERSION="$GIT_COMMIT" -t "$IMAGE_URI" .

# 2. Push to ECR
aws ecr get-login-password --region us-east-1 | \
  docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.us-east-1.amazonaws.com
docker push "$IMAGE_URI"

# 3. Force replace Lambda
cd terraform/environments/aws
terraform apply -replace="module.compute_lambda[0].aws_lambda_function.main" -auto-approve
```

### CI/CD Integration

```yaml
# .github/workflows/deploy.yml
name: Deploy to AWS
on:
  push:
    branches: [main]
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
          aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
          aws-region: us-east-1
      - run: ./scripts/tf-deploy.sh aws dev
```

---

## Fargate (Side-by-Side with Lambda)

You can run Fargate alongside an existing Lambda deployment for comparison testing. Both connect to the same Aurora database. The terraform setup uses a single directory with different `.tfvars` and `.tfbackend` files per environment.

### Architecture

| | Lambda (`dev`) | Fargate (`fargate-dev`) |
|---|---|---|
| **VPC** | 10.0.0.0/16 | 10.1.0.0/16 (separate) |
| **Compute** | Lambda + Function URL | ECS Fargate + ALB |
| **Database** | Aurora via RDS Proxy | Aurora via direct endpoint |
| **Frontend** | (none yet) | CloudFront + S3 |

### Deploy Fargate

```bash
cd terraform/environments/aws

# Initialize with fargate-dev backend
terraform init -backend-config=backends/fargate-dev.tfbackend

# Plan and apply with fargate-dev vars
terraform plan -var-file=fargate-dev.tfvars
terraform apply -var-file=fargate-dev.tfvars    # ~10-15 minutes

# Get URLs
terraform output fargate_api_url
terraform output frontend_url
```

### Compare Performance

```bash
cd terraform/environments/aws

# Lambda (1-2s cold start, then fast)
time curl $(terraform output -raw lambda_function_url)/health

# Fargate (consistent ~100-200ms)
time curl $(terraform output -raw fargate_api_url)/health
```

### Fargate Configuration (tfvars)

```hcl
compute_platform = "fargate"

fargate_cpu                    = 512     # 256, 512, 1024, 2048, 4096
fargate_memory                 = 1024
fargate_desired_count          = 2
fargate_min_capacity           = 1
fargate_max_capacity           = 10
fargate_enable_https           = false
fargate_enable_execute_command = false   # ECS Exec for debugging
```

### Switching Between Platforms

Change `compute_platform` in your tfvars and redeploy. The frontend module automatically adapts the API endpoint (Function URL vs ALB DNS). Update custom domain DNS if applicable.

### Cleanup

```bash
# Destroy only Fargate (leaves Lambda and database intact)
cd terraform/environments/aws
terraform destroy -var-file=fargate-dev.tfvars
```

---

## CDN Architecture

The frontend is served through CDN with dual-origin routing:

```text
User Request
    |
CDN (CloudFront / Azure CDN / Cloud CDN)
    |
    +-- /api/*  --> Backend (Lambda / Container Apps / Cloud Run)
    +-- /*      --> Static Files (S3 / Blob Storage / Cloud Storage)
```

**Static assets** (JS, CSS, images): Cached 1 year (content-hashed filenames). Compression enabled.
**HTML files**: No cache (`no-cache, no-store, must-revalidate`).
**API requests** (`/api/*`): No caching. All headers, cookies, and query strings forwarded.

The frontend uses relative paths (`/api`) by default. Since the CDN proxies `/api/*` to the backend, requests are same-origin and CORS is not needed.

### Per-Provider CDN Features

**AWS CloudFront:** Origin Access Control (OAC) for S3, CloudFront Function for security headers (HSTS, X-Frame-Options), custom error pages for SPA routing, optional WAF, `X-CloudFront-Secret` header for origin verification.

**Azure CDN:** Static website hosting via Blob Storage, Standard CDN or Front Door, managed SSL certificates, delivery rules for URL rewriting.

**GCP Cloud CDN:** Global HTTPS Load Balancer, Cloud Armor for WAF/DDoS protection, automatic managed SSL certificates.

### Verify CloudFront

```bash
CF_DIST_ID=$(terraform output -raw cloudfront_distribution_id)

# Check origins (should show S3 + Lambda/ALB)
aws cloudfront get-distribution --id "$CF_DIST_ID" \
  --query 'Distribution.DistributionConfig.Origins' --output json

# Test path routing
CF_URL=$(terraform output -raw frontend_url)
curl -I "$CF_URL/"           # X-Cache: Hit from cloudfront (static)
curl "$CF_URL/api/health"    # X-Cache: Miss from cloudfront (API)
```

---

## Cost Estimates

### AWS Lambda (low traffic, ~100K requests/month)

| Resource | Monthly Cost |
|----------|-------------|
| Lambda | ~$0.20 |
| Aurora Serverless v2 (0.5 ACU) | ~$43.80 |
| RDS Proxy | ~$10.95 |
| CloudFront + S3 | ~$1 |
| Secrets Manager | ~$1 |
| **Total** | **~$57/month** |

### AWS Fargate (2 tasks, 24/7)

| Resource | Monthly Cost |
|----------|-------------|
| Fargate (0.25 vCPU, 0.5GB x 2) | ~$21.90 |
| ALB | ~$16.20 |
| Aurora Serverless v2 (0.5 ACU) | ~$43.80 |
| CloudFront + S3 | ~$1 |
| **Total** | **~$83/month** |

### Multi-Cloud Comparison (Serverless, Dev)

| Platform | Estimated Monthly Cost |
|----------|----------------------|
| AWS Lambda | ~$57 |
| GCP Cloud Run | ~$13-27 |
| Azure Container Apps | ~$18-32 |

### Cost Optimization

- ARM64 Lambda/Fargate: 20% cheaper than x86
- Aurora scales to 0.5 ACU when idle
- Lambda/Cloud Run/Container Apps scale to zero
- IPv6 dual-stack eliminates NAT Gateway costs
- CloudFront PriceClass_100: US/Europe only for lower costs
- Fargate Spot: up to 70% discount for non-critical workloads

---

## Monitoring

### AWS Lambda

```bash
aws logs tail /aws/lambda/cudly-dev-api --follow
aws logs tail /aws/lambda/cudly-dev-api --filter-pattern "ERROR" --since 10m
```

### AWS Fargate

```bash
# Service status
aws ecs describe-services --cluster cudly-dev-fargate --services cudly-dev-fargate \
  --query 'services[0].{Status:status,Running:runningCount,Desired:desiredCount}'

# Logs
aws logs tail /ecs/cudly-dev-fargate --follow

# ECS Exec (if enabled)
aws ecs execute-command --cluster cudly-dev-fargate --task <task-id> \
  --container app --interactive --command "/bin/sh"
```

### Azure Container Apps

```bash
az containerapp logs show --name cudly-dev --resource-group cudly-rg
```

### GCP Cloud Run

```bash
gcloud run services logs read cudly-dev --region us-central1
```

---

## Maintenance

### Container Registry Cleanup

Each cloud provider has lifecycle policies configured via Terraform (`terraform/modules/registry/{aws,azure,gcp}/`):

- **AWS ECR:** Keep last 10 tagged images, delete untagged after 7 days, vulnerability scanning on push
- **GCP Artifact Registry:** Cleanup policies for untagged and old images
- **Azure ACR:** ACR tasks for automated cleanup

### Database Cleanup Jobs

Automated cleanup for expired sessions and completed purchase executions, running on a daily schedule:

- **AWS:** Lambda function triggered by EventBridge (`terraform/modules/compute/aws/cleanup-lambda/`)
- **Azure:** Azure Function App with timer trigger
- **GCP:** Cloud Function with Cloud Scheduler

Alternatively, use pg_cron for database-native scheduling.

---

## Troubleshooting

### Docker Build Fails

```bash
docker ps                   # is Docker running?
docker system df            # disk space
go build ./cmd/server       # syntax errors?
go mod tidy                 # dependency issues?
```

### Lambda Function Not Updating

Terraform may show "No changes" if the image tag didn't change. Force replace:

```bash
terraform apply -replace="module.compute_lambda[0].aws_lambda_function.main" -auto-approve
```

### Database Connection Failed

```bash
# Check Lambda is in VPC
aws lambda get-function-configuration --function-name cudly-dev-api --query 'VpcConfig'

# Check RDS Proxy
aws rds describe-db-proxies --db-proxy-name cudly-dev-proxy
# Check database cluster
aws rds describe-db-clusters --db-cluster-identifier cudly-dev-postgres
```

### Migrations Not Running

Check: `DB_AUTO_MIGRATE=true`, `DB_MIGRATIONS_PATH=/app/internal/database/postgres/migrations`, correct credentials. Run manually:

```bash
DB_PASSWORD=$(aws secretsmanager get-secret-value --secret-id cudly-dev-db-password-* --query SecretString --output text)
RDS_ENDPOINT=$(cd terraform/environments/aws && terraform output -raw database_proxy_endpoint)

migrate -path internal/database/postgres/migrations \
  -database "postgresql://cudly:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/cudly?sslmode=require" up
```

### Terraform State Lock

```bash
terraform force-unlock <LOCK_ID>
```

### CloudFront Returns 403

S3 bucket is empty. Deploy frontend or add a placeholder:

```bash
S3_BUCKET=$(terraform output -raw frontend_bucket)
echo "<h1>CUDly</h1>" | aws s3 cp - "s3://${S3_BUCKET}/index.html"
```

### Fargate Tasks Not Starting

```bash
aws ecs describe-services --cluster "$CLUSTER" --services "$SERVICE" --query 'services[0].events[:5]'
# Common: image pull errors, resource limits, health check failures
```

### ALB Returns 502/504 Through CloudFront

Test ALB directly to isolate:

```bash
ALB_DNS=$(terraform output -raw fargate_alb_dns_name)
curl "http://${ALB_DNS}/health"
# If ALB works but CloudFront doesn't: check origin settings, custom header, security groups
```

### Rollback

Deploy the previous git commit:

```bash
git log --oneline -5
git checkout <previous-commit>
./scripts/tf-deploy.sh aws dev
git checkout main
```
