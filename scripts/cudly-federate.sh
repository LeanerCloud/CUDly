#!/usr/bin/env bash
# cudly-federate.sh — one-shot federation setup for a target cloud account.
#
# Downloads the CLI script from CUDly, runs it against the target cloud,
# submits a registration, admin-approves it, and runs /test — all in one
# invocation.
#
# Usage:
#   scripts/cudly-federate.sh --provider aws|azure|gcp [options]
#
# Required env vars:
#   CUDLY_URL          — base URL of the CUDly instance
#   CUDLY_API_KEY      — admin API key (X-API-Key header)
#   CUDLY_CONTACT_EMAIL — contact email for the registration
#
# Provider-specific env vars / flags:
#   AWS:   --profile <aws-profile>  (optional, defaults to current creds)
#   Azure: --subscription <id>      (optional, auto-detected from az login)
#   GCP:   --project <id>           (optional, auto-detected from gcloud)
#
# Example:
#   CUDLY_URL=https://cudly.example.com \
#   CUDLY_API_KEY=secret \
#   CUDLY_CONTACT_EMAIL=ops@example.com \
#   scripts/cudly-federate.sh --provider aws --profile personal

set -euo pipefail

die() { echo "ERROR: $*" >&2; exit 1; }

PROVIDER=""
AWS_PROFILE_ARG=""
SUBSCRIPTION=""
PROJECT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider)   PROVIDER="$2"; shift 2 ;;
    --profile)    AWS_PROFILE_ARG="--profile $2"; shift 2 ;;
    --subscription) SUBSCRIPTION="$2"; shift 2 ;;
    --project)    PROJECT="$2"; shift 2 ;;
    *)            die "unknown flag: $1" ;;
  esac
done

[[ -n "${PROVIDER}" ]] || die "missing --provider (aws|azure|gcp)"
[[ -n "${CUDLY_URL:-}" ]] || die "CUDLY_URL not set"
[[ -n "${CUDLY_API_KEY:-}" ]] || die "CUDLY_API_KEY not set"
[[ -n "${CUDLY_CONTACT_EMAIL:-}" ]] || die "CUDLY_CONTACT_EMAIL not set"

WORKDIR=$(mktemp -d)
trap 'rm -rf "${WORKDIR}"' EXIT

echo "=== Step 1: Download CLI script (provider=${PROVIDER}) ==="

case "${PROVIDER}" in
  aws)   TARGET="aws"; SOURCE="aws" ;;
  azure) TARGET="azure"; SOURCE="aws" ;;
  gcp)   TARGET="gcp"; SOURCE="aws" ;;
  *)     die "unsupported provider: ${PROVIDER}" ;;
esac

DOWNLOAD_RESP="${WORKDIR}/download.json"
HTTP_CODE=$(curl -sS -o "${DOWNLOAD_RESP}" -w "%{http_code}" \
  "${CUDLY_URL}/api/federation/iac?target=${TARGET}&source=${SOURCE}&format=cli")
[[ "${HTTP_CODE}" == "200" ]] || die "download failed (HTTP ${HTTP_CODE}): $(cat "${DOWNLOAD_RESP}")"

SCRIPT_B64=$(jq -r '.content' "${DOWNLOAD_RESP}")
[[ -n "${SCRIPT_B64}" && "${SCRIPT_B64}" != "null" ]] || die "empty .content in download response"
echo "${SCRIPT_B64}" | base64 -d > "${WORKDIR}/setup.sh"
chmod +x "${WORKDIR}/setup.sh"
echo "Downloaded CLI script to ${WORKDIR}/setup.sh"

echo ""
echo "=== Step 2: Run the CLI script ==="

export CUDLY_CONTACT_EMAIL
case "${PROVIDER}" in
  aws)
    SOURCE_ACCOUNT_ID=$(curl -sS "${CUDLY_URL}/api/health" | jq -r '.source_account_id // empty')
    if [[ -z "${SOURCE_ACCOUNT_ID}" ]]; then
      echo "WARNING: could not detect source account ID from /api/health, script may prompt for it"
    fi
    # shellcheck disable=SC2086
    bash "${WORKDIR}/setup.sh" ${AWS_PROFILE_ARG}
    ;;
  azure)
    if [[ -n "${SUBSCRIPTION}" ]]; then
      export AZURE_SUBSCRIPTION_ID="${SUBSCRIPTION}"
    fi
    bash "${WORKDIR}/setup.sh"
    ;;
  gcp)
    if [[ -n "${PROJECT}" ]]; then
      export GOOGLE_CLOUD_PROJECT="${PROJECT}"
    fi
    bash "${WORKDIR}/setup.sh"
    ;;
esac

echo ""
echo "=== Step 3: Find the pending registration ==="

REGISTRATIONS=$(curl -sS -H "X-API-Key: ${CUDLY_API_KEY}" \
  "${CUDLY_URL}/api/registrations?status=pending")
REG_COUNT=$(echo "${REGISTRATIONS}" | jq 'length')
echo "Found ${REG_COUNT} pending registration(s)"

case "${PROVIDER}" in
  aws)
    FILTER='.provider == "aws"'
    ;;
  azure)
    FILTER='.provider == "azure"'
    ;;
  gcp)
    FILTER='.provider == "gcp"'
    ;;
esac

REG=$(echo "${REGISTRATIONS}" | jq -c "[.[] | select(${FILTER})] | sort_by(.created_at) | last")
[[ "${REG}" != "null" && -n "${REG}" ]] || die "no pending ${PROVIDER} registration found"

REG_ID=$(echo "${REG}" | jq -r '.id')
echo "Registration ID: ${REG_ID}"
echo "Details: $(echo "${REG}" | jq -c '{external_id, account_name, provider}')"

echo ""
echo "=== Step 4: Approve the registration ==="

APPROVE_BODY=$(echo "${REG}" | jq -c '{
  name: .account_name,
  provider: .provider,
  external_id: .external_id,
  contact_email: .contact_email,
  aws_auth_mode: .aws_auth_mode,
  aws_role_arn: .aws_role_arn,
  aws_external_id: .aws_external_id,
  azure_subscription_id: .azure_subscription_id,
  azure_tenant_id: .azure_tenant_id,
  azure_client_id: .azure_client_id,
  azure_auth_mode: .azure_auth_mode,
  gcp_project_id: .gcp_project_id,
  gcp_client_email: .gcp_client_email,
  gcp_auth_mode: .gcp_auth_mode,
  gcp_wif_audience: .gcp_wif_audience
} | with_entries(select(.value != null and .value != ""))')

APPROVE_RESP="${WORKDIR}/approve.json"
HTTP_CODE=$(curl -sS -o "${APPROVE_RESP}" -w "%{http_code}" \
  -X POST "${CUDLY_URL}/api/registrations/${REG_ID}/approve" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ${CUDLY_API_KEY}" \
  -d "${APPROVE_BODY}")
[[ "${HTTP_CODE}" == "200" || "${HTTP_CODE}" == "201" ]] || die "approve failed (HTTP ${HTTP_CODE}): $(cat "${APPROVE_RESP}")"

ACCOUNT_ID=$(jq -r '.id' "${APPROVE_RESP}")
ENABLED=$(jq -r '.enabled' "${APPROVE_RESP}")
echo "Account created: ${ACCOUNT_ID} (enabled=${ENABLED})"

echo ""
echo "=== Step 5: Test credentials ==="

TEST_RESP="${WORKDIR}/test.json"
HTTP_CODE=$(curl -sS -o "${TEST_RESP}" -w "%{http_code}" \
  -X POST "${CUDLY_URL}/api/accounts/${ACCOUNT_ID}/test" \
  -H "X-API-Key: ${CUDLY_API_KEY}")

TEST_OK=$(jq -r '.ok' "${TEST_RESP}")
TEST_MSG=$(jq -r '.message' "${TEST_RESP}")
echo "Test result: ok=${TEST_OK} — ${TEST_MSG}"

if [[ "${TEST_OK}" == "true" ]]; then
  echo ""
  echo "=== DONE ==="
  echo "Account ${ACCOUNT_ID} is federated, enabled, and tested."
  echo "Provider: ${PROVIDER}"
  echo "External ID: $(jq -r '.external_id' "${APPROVE_RESP}")"
else
  echo ""
  echo "WARNING: /test returned ok=false. The IAM resources may need time to propagate."
  echo "Re-run: curl -sS -X POST '${CUDLY_URL}/api/accounts/${ACCOUNT_ID}/test' -H 'X-API-Key: \$CUDLY_API_KEY'"
  exit 1
fi
