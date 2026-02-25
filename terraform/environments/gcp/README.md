# GCP Environment Configuration

This directory contains the Terraform configuration for CUDly on GCP with environment-specific values extracted into `.tfvars` files.

## Structure

```text
gcp/
├── main.tf                 # Main infrastructure, locals, providers
├── variables.tf            # Variable declarations
├── outputs.tf              # Output definitions
├── backend.tf              # Backend configuration (GCS)
├── build.tf                # Docker image build and push to GCR
├── compute.tf              # Cloud Run / GKE compute modules
├── dev.tfvars              # Development environment values
├── staging.tfvars          # Staging environment values (TBD)
├── prod.tfvars             # Production environment values (TBD)
└── backends/               # Backend configuration per environment
    ├── dev.tfbackend       # GCS backend for dev
    ├── staging.tfbackend   # GCS backend for staging
    └── prod.tfbackend      # GCS backend for prod
```

## Prerequisites

### 1. GCP Project and CLI

```bash
# Install gcloud CLI: https://cloud.google.com/sdk/docs/install
gcloud auth login
gcloud config set project YOUR_PROJECT_ID
```

### 2. Authentication

Use Application Default Credentials (ADC) for personal deployments:

```bash
gcloud auth application-default login
```

**Important**: If `GOOGLE_APPLICATION_CREDENTIALS` is set in your shell, it takes precedence over ADC. Unset it if you want to use personal credentials:

```bash
unset GOOGLE_APPLICATION_CREDENTIALS
```

### 3. Enable Required APIs

```bash
PROJECT_ID="your-project-id"

gcloud services enable \
  compute.googleapis.com \
  sqladmin.googleapis.com \
  run.googleapis.com \
  secretmanager.googleapis.com \
  cloudscheduler.googleapis.com \
  artifactregistry.googleapis.com \
  vpcaccess.googleapis.com \
  servicenetworking.googleapis.com \
  --project=$PROJECT_ID
```

### 4. Create Terraform State Bucket

```bash
BUCKET_NAME="cudly-terraform-state-dev"
gsutil mb -p $PROJECT_ID -l us-central1 gs://$BUCKET_NAME
gsutil versioning set on gs://$BUCKET_NAME
```

### 5. Configure Docker for GCR

```bash
gcloud auth configure-docker gcr.io --quiet
```

## First-Time Deployment

The first deployment requires **staged applies** because Terraform cannot resolve `count` expressions that depend on resources that don't exist yet. Subsequent applies work as a single `terraform apply`.

### Initialize

```bash
cd terraform/environments/gcp/
terraform init -backend-config=backends/dev.tfbackend
```

### Staged Apply (first time only)

```bash
# Stage 1: Networking + Secrets (~2 min)
terraform apply -var-file=dev.tfvars \
  -target=module.networking -target=module.secrets

# Stage 2: Database (~10-15 min for Cloud SQL creation)
terraform apply -var-file=dev.tfvars -target=module.database

# Stage 3: Docker Build + Push (~3-5 min)
terraform apply -var-file=dev.tfvars -target=module.build

# Stage 4: Compute (Cloud Run) (~1 min)
terraform apply -var-file=dev.tfvars -target=module.compute_cloud_run

# Stage 5: Full reconciliation (should be near no-op)
terraform apply -var-file=dev.tfvars
```

## Subsequent Deployments

After the first deployment, a single apply works:

```bash
terraform apply -var-file=dev.tfvars
```

## Verification

```bash
# Get the service URL
SERVICE_URL=$(terraform output -raw cloud_run_service_url)

# Health check (first request may show "degraded" due to lazy DB init)
curl $SERVICE_URL/health

# Login test (triggers DB initialization)
curl -s -X POST "$SERVICE_URL/api/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"your-admin@email.com","password":"BASE64_ENCODED_PASSWORD"}'

# Health check should now show "healthy"
curl $SERVICE_URL/health
```

## Architecture

The GCP deployment creates:

- **VPC** with private subnet, VPC connector, Cloud NAT
- **Cloud SQL** PostgreSQL (db-f1-micro for dev, private IP only)
- **Secret Manager** secrets for DB password, JWT, session, SendGrid, scheduled tasks
- **Cloud Run** (Gen2) service with VPC connector for Cloud SQL access
- **Cloud Scheduler** job for periodic recommendation collection
- **GCR** container image (built from project Dockerfile)

## Cost Notes (Dev Environment)

The dev configuration uses minimal resources:

| Resource | Size | Estimated Monthly Cost |
|----------|------|----------------------|
| Cloud SQL PostgreSQL | db-f1-micro, 10GB | ~$8 |
| Cloud Run | 1 vCPU, 512Mi, scale-to-zero | ~$0 (pay per request) |
| VPC Connector | f1-micro (2 instances) | ~$7 |
| Cloud NAT | Per-usage pricing | ~$1 |
| Secret Manager | 6 secrets | ~$0 |
| **Total** | | **~$16/month** |

Cloud Run Gen2 requires minimum 512Mi memory.

## Teardown

```bash
terraform destroy -var-file=dev.tfvars
```

**Note**: Cloud SQL instance names are reserved for 7 days after deletion. If you destroy and recreate, you may need to change the instance name or wait.

## Troubleshooting

### "Invalid grant" or credential errors

Refresh Application Default Credentials:

```bash
gcloud auth application-default login
```

### Cloud SQL connection failures

- Cloud SQL uses **private IP only** (no public IP) - Cloud Run connects via VPC connector
- SSL client certificates are **not required** - the private VPC connection is secure
- If you see `connection requires a valid client certificate`, ensure `require_ssl = false` in the database module

### Cloud Run reserved env vars

Cloud Run reserves certain environment variable names (`PORT`, `K_SERVICE`, `K_REVISION`, etc.). Do not set these in the module's env vars.

### Docker build on Apple Silicon

Cross-compilation from ARM (M1/M2/M3) to linux/amd64 is slower than native builds. The build module uses `--platform linux/amd64` and `--load` to build, load into local daemon, then push to GCR.
