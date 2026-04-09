#!/usr/bin/env bash
# setup-gcp-wif.sh — Configure a GCP Workload Identity Pool and Provider so that
# CUDly (running on AWS or with an OIDC token) can access GCP without a service
# account key file.  Outputs the external-account credential config JSON that
# should be stored as gcp_workload_identity_config in CUDly.
#
# Prerequisites:
#   gcloud auth login (with roles/iam.workloadIdentityPoolAdmin + roles/iam.serviceAccountAdmin
#   on the target project, and roles/iam.serviceAccountTokenCreator on the SA)
#
# Usage (AWS provider):
#   ./setup-gcp-wif.sh \
#       --project my-gcp-project \
#       --pool-id cudly-pool \
#       --provider-id cudly-aws \
#       --provider-type aws \
#       --aws-account-id 123456789012 \
#       --sa-email cudly@my-gcp-project.iam.gserviceaccount.com
#
# Usage (OIDC provider):
#   ./setup-gcp-wif.sh \
#       --project my-gcp-project \
#       --pool-id cudly-pool \
#       --provider-id cudly-oidc \
#       --provider-type oidc \
#       --issuer-uri https://token.actions.githubusercontent.com \
#       --sa-email cudly@my-gcp-project.iam.gserviceaccount.com

set -euo pipefail

PROJECT=""
POOL_ID="cudly-pool"
PROVIDER_ID="cudly-provider"
PROVIDER_TYPE=""   # "aws" or "oidc"
SA_EMAIL=""
AWS_ACCOUNT_ID=""
ISSUER_URI=""

# ── Argument parsing ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --project)        PROJECT="$2";        shift 2 ;;
    --pool-id)        POOL_ID="$2";        shift 2 ;;
    --provider-id)    PROVIDER_ID="$2";    shift 2 ;;
    --provider-type)  PROVIDER_TYPE="$2";  shift 2 ;;
    --sa-email)       SA_EMAIL="$2";       shift 2 ;;
    --aws-account-id) AWS_ACCOUNT_ID="$2"; shift 2 ;;
    --issuer-uri)     ISSUER_URI="$2";     shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

# ── Validate required args ─────────────────────────────────────────────────────
if [[ -z "$PROJECT" || -z "$SA_EMAIL" || -z "$PROVIDER_TYPE" ]]; then
  echo "Error: --project, --sa-email, and --provider-type are required" >&2
  exit 1
fi
if [[ "$PROVIDER_TYPE" == "aws" && -z "$AWS_ACCOUNT_ID" ]]; then
  echo "Error: --aws-account-id is required for --provider-type aws" >&2
  exit 1
fi
if [[ "$PROVIDER_TYPE" == "oidc" && -z "$ISSUER_URI" ]]; then
  echo "Error: --issuer-uri is required for --provider-type oidc" >&2
  exit 1
fi
if [[ "$PROVIDER_TYPE" != "aws" && "$PROVIDER_TYPE" != "oidc" ]]; then
  echo "Error: --provider-type must be 'aws' or 'oidc'" >&2
  exit 1
fi

PROJECT_NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
echo "Project       : $PROJECT (number: $PROJECT_NUMBER)"
echo "Pool          : $POOL_ID"
echo "Provider      : $PROVIDER_ID ($PROVIDER_TYPE)"
echo "Service Acct  : $SA_EMAIL"
echo ""

# ── Idempotent pool creation ───────────────────────────────────────────────────
if ! gcloud iam workload-identity-pools describe "$POOL_ID" \
     --project="$PROJECT" --location=global &>/dev/null; then
  echo "Creating workload identity pool '${POOL_ID}'..."
  gcloud iam workload-identity-pools create "$POOL_ID" \
    --project="$PROJECT" --location=global \
    --display-name="CUDly WIF pool" --quiet
else
  echo "Reusing existing pool '${POOL_ID}'"
fi

# ── Idempotent provider creation ───────────────────────────────────────────────
if ! gcloud iam workload-identity-pools providers describe "$PROVIDER_ID" \
     --project="$PROJECT" --location=global \
     --workload-identity-pool="$POOL_ID" &>/dev/null; then
  echo "Creating ${PROVIDER_TYPE} provider '${PROVIDER_ID}'..."
  if [[ "$PROVIDER_TYPE" == "aws" ]]; then
    gcloud iam workload-identity-pools providers create-aws "$PROVIDER_ID" \
      --project="$PROJECT" --location=global \
      --workload-identity-pool="$POOL_ID" \
      --account-id="$AWS_ACCOUNT_ID" --quiet
  else
    gcloud iam workload-identity-pools providers create-oidc "$PROVIDER_ID" \
      --project="$PROJECT" --location=global \
      --workload-identity-pool="$POOL_ID" \
      --issuer-uri="$ISSUER_URI" --quiet
  fi
else
  echo "Reusing existing provider '${PROVIDER_ID}'"
fi

# ── Grant service account impersonation to the pool ───────────────────────────
POOL_PRINCIPAL="principalSet://iam.googleapis.com/projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${POOL_ID}/*"
echo "Granting roles/iam.workloadIdentityUser to pool members on ${SA_EMAIL}..."
gcloud iam service-accounts add-iam-policy-binding "$SA_EMAIL" \
  --role=roles/iam.workloadIdentityUser \
  --member="$POOL_PRINCIPAL" \
  --project="$PROJECT" \
  --quiet

# ── Generate external account credential config (no secrets) ──────────────────
PROVIDER_RESOURCE="projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${POOL_ID}/providers/${PROVIDER_ID}"
echo ""
echo "══════════════════════════════════════════════════════════════"
echo "  External account credential config (store in CUDly as"
echo "  gcp_workload_identity_config — contains no secrets)"
echo "══════════════════════════════════════════════════════════════"
if [[ "$PROVIDER_TYPE" == "aws" ]]; then
  gcloud iam workload-identity-pools create-cred-config \
    "$PROVIDER_RESOURCE" \
    --service-account="$SA_EMAIL" \
    --aws \
    --output-file=/dev/stdout
else
  gcloud iam workload-identity-pools create-cred-config \
    "$PROVIDER_RESOURCE" \
    --service-account="$SA_EMAIL" \
    --output-file=/dev/stdout
fi
echo ""
echo "══════════════════════════════════════════════════════════════"
echo "  CUDly account registration values:"
echo "    provider       : gcp"
echo "    gcp_auth_mode  : workload_identity_federation"
echo "    gcp_project_id : ${PROJECT}"
echo "    gcp_client_email (optional) : ${SA_EMAIL}"
echo "══════════════════════════════════════════════════════════════"
