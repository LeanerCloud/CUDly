# GCP CI/CD Permissions

This Terraform module provisions a least-privilege service account (`cudly-terraform-deploy`) and
**Workload Identity Federation** pool that CUDly's CI/CD pipeline uses to deploy infrastructure on
GCP. GitHub Actions can impersonate the service account keylessly — no JSON key files needed.

## What this module creates

| Resource | Purpose |
| --- | --- |
| `google_service_account.cudly_deploy` | Deploy service account |
| `google_project_iam_member.*` | IAM roles bound to the SA (see below) |
| `google_iam_workload_identity_pool.github` | WIF pool for GitHub Actions OIDC |
| `google_iam_workload_identity_pool_provider.github` | OIDC provider inside the pool |
| `google_service_account_iam_member.github_actions` | Allows the WIF pool to impersonate the SA |

### Roles granted to the service account

`roles/run.admin`, `roles/artifactregistry.admin`, `roles/cloudsql.admin`,
`roles/redis.admin`, `roles/vpcaccess.admin`, `roles/compute.networkAdmin`,
`roles/dns.admin`, `roles/secretmanager.admin`, `roles/storage.admin`,
`roles/iam.serviceAccountUser`, `roles/iam.workloadIdentityPoolAdmin`

## Prerequisites

- Terraform >= 1.6
- `gcloud` CLI authenticated (`gcloud auth application-default login`) with an account that has **Project IAM Admin** on the target project
- A GCS bucket for Terraform state (see [Backend setup](#backend-setup))

## Backend setup

Create the GCS bucket once before the first `terraform init`:

```bash
PROJECT_ID=$(gcloud config get-value project)
BUCKET="cudly-terraform-state-${PROJECT_ID}"
REGION="us-central1"

gcloud storage buckets create "gs://${BUCKET}" \
  --project "$PROJECT_ID" \
  --location "$REGION" \
  --uniform-bucket-level-access

# Enable versioning so you can recover from accidental state corruption
gcloud storage buckets update "gs://${BUCKET}" --versioning
```

A `backend.hcl.example` is provided; copy it to `backend.hcl` and fill in your values, then pass it
to `terraform init -backend-config=backend.hcl`.

## Usage

```bash
# 1. Copy example configs
cp terraform.tfvars.example terraform.tfvars
cp backend.hcl.example backend.hcl

# 2. Fill in terraform.tfvars (project_id is required)
# 3. Fill in backend.hcl with your GCS bucket name

# 4. Initialise
terraform init -backend-config=backend.hcl

# 5. Plan
terraform plan

# 6. Apply
terraform apply
```

After applying, retrieve the outputs used in GitHub Actions:

```bash
terraform output service_account_email       # → GCP_SERVICE_ACCOUNT
terraform output workload_identity_provider  # → GCP_WORKLOAD_IDENTITY_PROVIDER
```

## Importing an existing service account

If the `cudly-terraform-deploy` SA was created manually before this module was added:

```bash
PROJECT_ID=$(gcloud config get-value project)

# The import ID format is: projects/<project>/serviceAccounts/<email>
SA_EMAIL="cudly-terraform-deploy@${PROJECT_ID}.iam.gserviceaccount.com"

terraform import google_service_account.cudly_deploy \
  "projects/${PROJECT_ID}/serviceAccounts/${SA_EMAIL}"
```

## GitHub Actions configuration

### Repository secrets / variables

Set these in **Settings → Secrets and variables → Actions** on the `LeanerCloud/CUDly` GitHub
repository (or in a GitHub Actions Environment for per-environment control):

| Name | Value | How to get it |
| --- | --- | --- |
| `GCP_WORKLOAD_IDENTITY_PROVIDER` | Full WIF provider resource name | `terraform output workload_identity_provider` |
| `GCP_SERVICE_ACCOUNT` | SA email | `terraform output service_account_email` |

> No JSON key files are needed — authentication is keyless via Workload Identity Federation.

### Example workflow step

```yaml
- name: Authenticate to GCP
  uses: google-github-actions/auth@v2
  with:
    workload_identity_provider: ${{ secrets.GCP_WORKLOAD_IDENTITY_PROVIDER }}
    service_account: ${{ secrets.GCP_SERVICE_ACCOUNT }}
```

The `google-github-actions/auth` action requests an OIDC token from GitHub, exchanges it at the
Workload Identity Federation endpoint for a short-lived SA access token, and sets
`GOOGLE_APPLICATION_CREDENTIALS` (and `GCLOUD_AUTH`) for all subsequent steps.

### Attribute condition

The WIF provider enforces **two** conditions simultaneously:

```text
assertion.repository == '<github_repo>' && assertion.ref == '<deploy_ref>'
```

Only workflows running from the exact repository **and** the exact ref (branch) configured in
`terraform.tfvars` can obtain a GCP token. Any other branch, PR, fork, tag, or
`workflow_dispatch` from a different ref is rejected with:

> `unauthorized_client: The given credential is rejected by the attribute condition.`

**Keeping `deploy_ref` in sync with the active deploy branch is critical.** When the base
deploy branch changes (e.g. a long-running feature branch is merged into `main`), the operator
must update `deploy_ref` in `terraform.tfvars` and re-apply this module before the next deploy.

The default value of `deploy_ref` is `refs/heads/main`. A plain `terraform apply` with no
override in `terraform.tfvars` restores the correct condition for deploys from `main`.

To verify the currently-applied condition without applying:

```bash
gcloud iam workload-identity-pools providers describe github-actions \
  --workload-identity-pool=github-actions \
  --location=global \
  --project=<PROJECT_ID> \
  --format="value(attributeCondition)"
```

To change the allowed repo, update `github_repo` in `terraform.tfvars` and re-apply.
