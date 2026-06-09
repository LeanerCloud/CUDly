#!/usr/bin/env bash
# gcp-import-dev-state.sh
#
# Queries GCP for all existing cudly-dev resources and imports any that are
# missing from the Terraform github-dev state in GCS.
#
# Usage:
#   export GOOGLE_APPLICATION_CREDENTIALS=~/.config/gcloud/cudly-terraform-deploy.json
#   cd <repo-root>
#   bash scripts/gcp-import-dev-state.sh
#
# Prerequisites:
#   - gcloud CLI authenticated (or GOOGLE_APPLICATION_CREDENTIALS set)
#   - terraform >= 1.10
#   - Access to gs://cudly-terraform-state-cloudprowess

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
PROJECT="serene-bazaar-666"
REGION="us-central1"
SERVICE_NAME="cudly-dev"
TF_DIR="$(cd "$(dirname "$0")/.." && pwd)/terraform/environments/gcp"
BACKEND_CONFIG="bucket = \"cudly-terraform-state-cloudprowess\"\nprefix = \"github-dev\""
BACKEND_FILE="/tmp/gcp-dev-backend.tfbackend"

export GOOGLE_APPLICATION_CREDENTIALS="${GOOGLE_APPLICATION_CREDENTIALS:-$HOME/.config/gcloud/cudly-terraform-deploy.json}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Print a section header
section() { echo; echo "=== $* ==="; }

# Check if a resource is already in Terraform state
in_state() {
  terraform state show "$1" &>/dev/null
}

# Import a resource only if it is not already tracked in state
# Usage: import_if_missing <tf_address> <import_id>
import_if_missing() {
  local addr="$1"
  local id="$2"
  if in_state "$addr"; then
    echo "  ✓ already in state: $addr"
  else
    echo "  → importing: $addr"
    terraform import "$addr" "$id"
  fi
}

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------
section "Setup"
echo "TF_DIR: $TF_DIR"
echo "Credentials: $GOOGLE_APPLICATION_CREDENTIALS"
[ -f "$GOOGLE_APPLICATION_CREDENTIALS" ] || { echo "ERROR: credentials file not found"; exit 1; }

printf '%b\n' "$BACKEND_CONFIG" > "$BACKEND_FILE"
echo "Backend file written to $BACKEND_FILE"

cd "$TF_DIR"

echo "Running terraform init..."
terraform init -backend-config="$BACKEND_FILE" -reconfigure -input=false 2>&1 | tail -5

section "Current state"
terraform state list || true

# ---------------------------------------------------------------------------
# Gather GCP resource info
# ---------------------------------------------------------------------------
section "Querying GCP resources"

# Secrets — discover which ones actually exist
SECRETS_EXIST=()
SECRET_VERSIONS=()
for SECRET_SUFFIX in db-password admin-password jwt-secret session-secret sendgrid-api-key scheduled-task-secret; do
  SECRET_ID="${SERVICE_NAME}-${SECRET_SUFFIX}"
  if gcloud secrets describe "$SECRET_ID" --project="$PROJECT" &>/dev/null; then
    SECRETS_EXIST+=("$SECRET_ID")
    # Get the latest enabled version number
    VERSION=$(gcloud secrets versions list "$SECRET_ID" \
      --project="$PROJECT" \
      --filter="state=ENABLED" \
      --sort-by="~createTime" \
      --limit=1 \
      --format="value(name)" 2>/dev/null | awk -F/ '{print $NF}')
    SECRET_VERSIONS+=("${SECRET_ID}:${VERSION:-1}")
    echo "  secret $SECRET_ID — latest version: ${VERSION:-1}"
  else
    echo "  secret $SECRET_ID — NOT FOUND (will be created by Terraform)"
    SECRETS_EXIST+=("")
    SECRET_VERSIONS+=("${SECRET_ID}:")
  fi
done

# VPC
VPC_EXISTS=$(gcloud compute networks describe "${SERVICE_NAME}-vpc" \
  --project="$PROJECT" --format="value(name)" 2>/dev/null || true)

# Cloud SQL
SQL_INSTANCE="${SERVICE_NAME}-postgres"
SQL_EXISTS=$(gcloud sql instances describe "$SQL_INSTANCE" \
  --project="$PROJECT" --format="value(name)" 2>/dev/null || true)

# Cloud Run service
CR_EXISTS=$(gcloud run services describe "$SERVICE_NAME" \
  --region="$REGION" --project="$PROJECT" --format="value(metadata.name)" 2>/dev/null || true)

# Service accounts
SA_CLOUDRUN="${SERVICE_NAME}-cloudrun@${PROJECT}.iam.gserviceaccount.com"
SA_CLOUDRUN_EXISTS=$(gcloud iam service-accounts describe "$SA_CLOUDRUN" \
  --project="$PROJECT" --format="value(email)" 2>/dev/null || true)

SA_SCHEDULER="${SERVICE_NAME}-scheduler@${PROJECT}.iam.gserviceaccount.com"
SA_SCHEDULER_EXISTS=$(gcloud iam service-accounts describe "$SA_SCHEDULER" \
  --project="$PROJECT" --format="value(email)" 2>/dev/null || true)

# Artifact Registry
REGISTRY_EXISTS=$(gcloud artifacts repositories describe cudly \
  --location="$REGION" --project="$PROJECT" --format="value(name)" 2>/dev/null || true)

# Scheduler jobs
SCHED_JOB="${SERVICE_NAME}-recommendations"
SCHED_EXISTS=$(gcloud scheduler jobs describe "$SCHED_JOB" \
  --location="$REGION" --project="$PROJECT" --format="value(name)" 2>/dev/null || true)

# VPC Connector
CONNECTOR_EXISTS=$(gcloud compute networks vpc-access connectors describe "${SERVICE_NAME}-connector" \
  --region="$REGION" --project="$PROJECT" --format="value(name)" 2>/dev/null || true)

# Firewall rules
FW_INTERNAL_EXISTS=$(gcloud compute firewall-rules describe "${SERVICE_NAME}-allow-internal" \
  --project="$PROJECT" --format="value(name)" 2>/dev/null || true)

FW_HEALTH_EXISTS=$(gcloud compute firewall-rules describe "${SERVICE_NAME}-allow-health-checks" \
  --project="$PROJECT" --format="value(name)" 2>/dev/null || true)

# ---------------------------------------------------------------------------
# Import secrets
# ---------------------------------------------------------------------------
section "Importing secrets"

# Map: secret_suffix → terraform address suffix
declare -A SECRET_TF_ADDR
SECRET_TF_ADDR["db-password"]="database_password"
SECRET_TF_ADDR["admin-password"]='admin_password[0]'
SECRET_TF_ADDR["jwt-secret"]='jwt_secret[0]'
SECRET_TF_ADDR["session-secret"]='session_secret[0]'
SECRET_TF_ADDR["sendgrid-api-key"]='sendgrid_api_key[0]'
SECRET_TF_ADDR["scheduled-task-secret"]='scheduled_task_secret[0]'

for entry in "${SECRET_VERSIONS[@]}"; do
  SECRET_ID="${entry%%:*}"
  VERSION="${entry##*:}"
  SUFFIX="${SECRET_ID#${SERVICE_NAME}-}"
  TF_SUFFIX="${SECRET_TF_ADDR[$SUFFIX]:-}"

  if [ -z "$TF_SUFFIX" ]; then
    echo "  unknown secret suffix: $SUFFIX — skipping"
    continue
  fi
  if [ -z "$VERSION" ]; then
    echo "  $SECRET_ID — not in GCP, skipping"
    continue
  fi

  # Import the secret metadata
  import_if_missing \
    "module.secrets.google_secret_manager_secret.${TF_SUFFIX}" \
    "projects/${PROJECT}/secrets/${SECRET_ID}"

  # Import the latest version
  import_if_missing \
    "module.secrets.google_secret_manager_secret_version.${TF_SUFFIX}" \
    "projects/${PROJECT}/secrets/${SECRET_ID}/versions/${VERSION}"
done

# ---------------------------------------------------------------------------
# Import networking
# ---------------------------------------------------------------------------
section "Importing networking"

if [ -n "$VPC_EXISTS" ]; then
  import_if_missing \
    "module.networking.google_compute_network.main" \
    "projects/${PROJECT}/global/networks/${SERVICE_NAME}-vpc"

  # Subnet
  if gcloud compute networks subnets describe "${SERVICE_NAME}-private-subnet" \
      --region="$REGION" --project="$PROJECT" &>/dev/null; then
    import_if_missing \
      "module.networking.google_compute_subnetwork.private" \
      "projects/${PROJECT}/regions/${REGION}/subnetworks/${SERVICE_NAME}-private-subnet"
  fi

  # Connector subnet
  if gcloud compute networks subnets describe "${SERVICE_NAME}-connector-subnet" \
      --region="$REGION" --project="$PROJECT" &>/dev/null; then
    import_if_missing \
      "module.networking.google_compute_subnetwork.connector" \
      "projects/${PROJECT}/regions/${REGION}/subnetworks/${SERVICE_NAME}-connector-subnet"
  fi

  # Router
  if gcloud compute routers describe "${SERVICE_NAME}-router" \
      --region="$REGION" --project="$PROJECT" &>/dev/null; then
    import_if_missing \
      "module.networking.google_compute_router.main" \
      "${PROJECT}/${REGION}/${SERVICE_NAME}-router"

    # NAT (import ID: project/region/router/nat_name)
    if gcloud compute routers nats describe "${SERVICE_NAME}-nat" \
        --router="${SERVICE_NAME}-router" --region="$REGION" --project="$PROJECT" &>/dev/null; then
      import_if_missing \
        "module.networking.google_compute_router_nat.main" \
        "${PROJECT}/${REGION}/${SERVICE_NAME}-router/${SERVICE_NAME}-nat"
    fi
  fi

  # Global address for private service connection
  if gcloud compute addresses describe "${SERVICE_NAME}-private-ip" \
      --global --project="$PROJECT" &>/dev/null; then
    import_if_missing \
      "module.networking.google_compute_global_address.private_ip_address" \
      "projects/${PROJECT}/global/addresses/${SERVICE_NAME}-private-ip"
  fi

  # Private service networking connection
  # Import ID: projects/{project}/global/networks/{network}:{service}
  NETWORK_LINK="projects/${PROJECT}/global/networks/${SERVICE_NAME}-vpc"
  if gcloud services vpc-peerings list --network="${SERVICE_NAME}-vpc" \
      --project="$PROJECT" 2>/dev/null | grep -q "servicenetworking"; then
    import_if_missing \
      "module.networking.google_service_networking_connection.private_vpc_connection" \
      "${NETWORK_LINK}:servicenetworking.googleapis.com"
  fi
fi

# VPC connector
if [ -n "$CONNECTOR_EXISTS" ]; then
  import_if_missing \
    "module.networking.google_vpc_access_connector.main" \
    "projects/${PROJECT}/locations/${REGION}/connectors/${SERVICE_NAME}-connector"
fi

# Firewall rules
if [ -n "$FW_INTERNAL_EXISTS" ]; then
  import_if_missing \
    "module.networking.google_compute_firewall.allow_internal" \
    "projects/${PROJECT}/global/firewalls/${SERVICE_NAME}-allow-internal"
fi

if [ -n "$FW_HEALTH_EXISTS" ]; then
  import_if_missing \
    "module.networking.google_compute_firewall.allow_health_checks" \
    "projects/${PROJECT}/global/firewalls/${SERVICE_NAME}-allow-health-checks"
fi

# ---------------------------------------------------------------------------
# Import database
# ---------------------------------------------------------------------------
section "Importing database"

if [ -n "$SQL_EXISTS" ]; then
  import_if_missing \
    "module.database.google_sql_database_instance.main" \
    "projects/${PROJECT}/instances/${SQL_INSTANCE}"

  # Database (schema)
  if gcloud sql databases describe cudly --instance="$SQL_INSTANCE" \
      --project="$PROJECT" &>/dev/null; then
    import_if_missing \
      "module.database.google_sql_database.main" \
      "projects/${PROJECT}/instances/${SQL_INSTANCE}/databases/cudly"
  fi

  # SQL user
  if gcloud sql users list --instance="$SQL_INSTANCE" --project="$PROJECT" \
      --format="value(name)" 2>/dev/null | grep -q "^cudly$"; then
    import_if_missing \
      "module.database.google_sql_user.main" \
      "projects/${PROJECT}/instances/${SQL_INSTANCE}/users/cudly"
  fi
fi

# ---------------------------------------------------------------------------
# Import Artifact Registry
# ---------------------------------------------------------------------------
section "Importing Artifact Registry"

if [ -n "$REGISTRY_EXISTS" ]; then
  import_if_missing \
    "module.registry.google_artifact_registry_repository.main" \
    "projects/${PROJECT}/locations/${REGION}/repositories/cudly"
fi

# ---------------------------------------------------------------------------
# Import compute (Cloud Run)
# ---------------------------------------------------------------------------
section "Importing compute (Cloud Run)"

# Service account for Cloud Run
if [ -n "$SA_CLOUDRUN_EXISTS" ]; then
  import_if_missing \
    "module.compute_cloud_run[0].google_service_account.cloud_run" \
    "projects/${PROJECT}/serviceAccounts/${SA_CLOUDRUN}"
fi

# Cloud Run service
if [ -n "$CR_EXISTS" ]; then
  import_if_missing \
    "module.compute_cloud_run[0].google_cloud_run_v2_service.main" \
    "projects/${PROJECT}/locations/${REGION}/services/${SERVICE_NAME}"

  # Public access IAM binding (allUsers → roles/run.invoker)
  # Import ID for google_cloud_run_service_iam_member: {project}/{location}/{service} {role} {member}
  import_if_missing \
    "module.compute_cloud_run[0].google_cloud_run_service_iam_member.public_access[0]" \
    "projects/${PROJECT}/locations/${REGION}/services/${SERVICE_NAME} roles/run.invoker allUsers"
fi

# Project-level IAM members for the Cloud Run SA
# These are idempotent but importing avoids spurious diffs on plan
if [ -n "$SA_CLOUDRUN_EXISTS" ]; then
  SA_MEMBER="serviceAccount:${SA_CLOUDRUN}"

  import_if_missing \
    "module.compute_cloud_run[0].google_project_iam_member.cloud_sql_client" \
    "${PROJECT} roles/cloudsql.client ${SA_MEMBER}"

  import_if_missing \
    "module.compute_cloud_run[0].google_project_iam_member.secret_accessor" \
    "${PROJECT} roles/secretmanager.secretAccessor ${SA_MEMBER}"

  import_if_missing \
    "module.compute_cloud_run[0].google_project_iam_member.compute_viewer" \
    "${PROJECT} roles/compute.viewer ${SA_MEMBER}"

  import_if_missing \
    "module.compute_cloud_run[0].google_project_iam_member.compute_commitment_admin" \
    "${PROJECT} roles/compute.admin ${SA_MEMBER}"

  import_if_missing \
    "module.compute_cloud_run[0].google_project_iam_member.recommender_viewer" \
    "${PROJECT} roles/recommender.viewer ${SA_MEMBER}"
fi

# Scheduler service account + job
if [ -n "$SA_SCHEDULER_EXISTS" ]; then
  import_if_missing \
    "module.compute_cloud_run[0].google_service_account.scheduler[0]" \
    "projects/${PROJECT}/serviceAccounts/${SA_SCHEDULER}"
fi

if [ -n "$SCHED_EXISTS" ]; then
  import_if_missing \
    "module.compute_cloud_run[0].google_cloud_scheduler_job.recommendations[0]" \
    "projects/${PROJECT}/locations/${REGION}/jobs/${SCHED_JOB}"
fi

# Scheduler Cloud Run IAM binding
if [ -n "$SA_SCHEDULER_EXISTS" ] && [ -n "$CR_EXISTS" ]; then
  import_if_missing \
    "module.compute_cloud_run[0].google_cloud_run_service_iam_member.scheduler_invoker[0]" \
    "projects/${PROJECT}/locations/${REGION}/services/${SERVICE_NAME} roles/run.invoker serviceAccount:${SA_SCHEDULER}"
fi

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
section "Final state summary"
terraform state list

echo
echo "Import complete. Run 'terraform plan' to verify — only the Docker build"
echo "and new/changed resources should appear as changes."
