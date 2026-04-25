#!/usr/bin/env bash
# e2e-federation-test.sh — End-to-end test of all CUDly federation IaC formats.
#
# Tests every format × provider combination:
#   1. AWS cross-account: CLI, Terraform, CloudFormation
#   2. Azure WIF:         CLI, Terraform, Bicep, ARM
#   3. GCP WIF:           CLI, Terraform
#
# Each test cycle: download → deploy → auto-register → approve → /test → cleanup.
#
# Prerequisites:
#   - AWS CLI v2 configured with profiles 'cristi-cloudprowess-prd' (CUDly host)
#     and 'personal' (target account)
#   - Azure CLI logged in (az login) to a subscription with AD admin rights
#   - gcloud CLI authenticated with a project that has IAM/STS/WIF APIs enabled
#   - jq, uuidgen, base64, curl, terraform, unzip
#
# Usage:
#   bash scripts/e2e-federation-test.sh [--provider aws|azure|gcp] [--format cli|bundle|cfn|bicep|arm]
#
# Without flags, runs ALL test cycles. With flags, runs only the matching subset.
set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration — edit these to match your environment
# ---------------------------------------------------------------------------
CUDLY_HOST_PROFILE="${CUDLY_HOST_PROFILE:-cristi-cloudprowess-prd}"
CUDLY_LAMBDA_NAME="${CUDLY_LAMBDA_NAME:-cudly-dev-426fc8af-api}"
CUDLY_ADMIN_EMAIL="${CUDLY_ADMIN_EMAIL:-cristi@leanercloud.com}"
AWS_TARGET_PROFILE="${AWS_TARGET_PROFILE:-personal}"
GCP_PROJECT="${GCP_PROJECT:-serene-bazaar-666}"
GCP_SA_EMAIL="${GCP_SA_EMAIL:-cudly-e2e@${GCP_PROJECT}.iam.gserviceaccount.com}"

WORKDIR=$(mktemp -d)
trap 'rm -rf "${WORKDIR}"' EXIT
CONTACT_EMAIL="e2e-test-$(date +%s)@example.com"

FILTER_PROVIDER=""
FILTER_FORMAT=""
NO_CLEANUP=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider)   FILTER_PROVIDER="$2"; shift 2 ;;
    --format)     FILTER_FORMAT="$2"; shift 2 ;;
    --no-cleanup) NO_CLEANUP=true; shift ;;
    *)            echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
done

# ---------------------------------------------------------------------------
# Counters
# ---------------------------------------------------------------------------
PASS=0
FAIL=0
SKIP=0
RESULTS=()

record() {
  local label="$1" status="$2"
  RESULTS+=("${status} ${label}")
  if [[ "${status}" == "PASS" ]]; then PASS=$((PASS + 1)); fi
  if [[ "${status}" == "FAIL" ]]; then FAIL=$((FAIL + 1)); fi
  if [[ "${status}" == "SKIP" ]]; then SKIP=$((SKIP + 1)); fi
}

should_run() {
  local provider="$1" format="$2"
  if [[ -n "${FILTER_PROVIDER}" && "${FILTER_PROVIDER}" != "${provider}" ]]; then return 1; fi
  if [[ -n "${FILTER_FORMAT}" && "${FILTER_FORMAT}" != "${format}" ]]; then return 1; fi
  return 0
}

# ---------------------------------------------------------------------------
# Resolve CUDly URL + admin session
# ---------------------------------------------------------------------------
echo "=== Pre-flight ==="

CUDLY_URL=$(aws lambda get-function-url-config \
  --function-name "${CUDLY_LAMBDA_NAME}" \
  --profile "${CUDLY_HOST_PROFILE}" \
  --query 'FunctionUrl' --output text)
CUDLY_URL="${CUDLY_URL%/}"
echo "CUDly URL: ${CUDLY_URL}"

# Allow pre-set tokens via env vars to avoid hitting the login rate limit
if [[ -n "${CUDLY_TOKEN:-}" && -n "${CUDLY_CSRF:-}" ]]; then
  TOKEN="${CUDLY_TOKEN}"
  CSRF="${CUDLY_CSRF}"
  echo "Using pre-set CUDLY_TOKEN (${#TOKEN} chars)"
else
  ADMIN_SECRET_ARN=$(aws lambda get-function-configuration \
    --function-name "${CUDLY_LAMBDA_NAME}" \
    --profile "${CUDLY_HOST_PROFILE}" \
    --query 'Environment.Variables.ADMIN_PASSWORD_SECRET' --output text)

  ADMIN_PASSWORD=$(aws secretsmanager get-secret-value \
    --secret-id "${ADMIN_SECRET_ARN}" \
    --profile "${CUDLY_HOST_PROFILE}" \
    --query SecretString --output text)
  ADMIN_PASSWORD_B64=$(echo -n "${ADMIN_PASSWORD}" | base64)

  LOGIN_RESP=$(curl -sS -X POST "${CUDLY_URL}/api/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${CUDLY_ADMIN_EMAIL}\",\"password\":\"${ADMIN_PASSWORD_B64}\"}")
  TOKEN=$(echo "${LOGIN_RESP}" | jq -r '.token // empty')
  CSRF=$(echo "${LOGIN_RESP}" | jq -r '.csrf_token // empty')
  if [[ -z "${TOKEN}" ]]; then
    echo "FATAL: login failed: ${LOGIN_RESP}"
    echo "Hint: set CUDLY_TOKEN and CUDLY_CSRF env vars to bypass login"
    exit 1
  fi
  echo "Authenticated (token ${#TOKEN} chars)"
  echo "To reuse: export CUDLY_TOKEN=${TOKEN} CUDLY_CSRF=${CSRF}"
fi

# Health check
HTTP=$(curl -sS -o /dev/null -w "%{http_code}" "${CUDLY_URL}/health")
if [[ "${HTTP}" != "200" ]]; then
  echo "FATAL: health check returned HTTP ${HTTP}"
  exit 1
fi
echo "Health: OK"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Auth headers for admin API calls
auth_headers() {
  echo -H "Authorization: Bearer ${TOKEN}" -H "X-CSRF-Token: ${CSRF}"
}

# Download an IaC artifact, unzip if zip, return the directory
download_iac() {
  local target="$1" format="$2" dest="$3"
  mkdir -p "${dest}"
  local resp="${dest}/download.json"
  local http_code
  http_code=$(curl -sS -o "${resp}" -w "%{http_code}" \
    "${CUDLY_URL}/api/federation/iac?target=${target}&source=aws&format=${format}")
  if [[ "${http_code}" != "200" ]]; then
    echo "Download failed (HTTP ${http_code}): $(cat "${resp}")" >&2
    return 1
  fi
  local content
  content=$(jq -r '.content' "${resp}")
  if [[ -z "${content}" || "${content}" == "null" ]]; then
    echo "Download returned empty content" >&2
    return 1
  fi
  # Check if it's a zip (base64 of zip) or plain text
  local encoding
  encoding=$(jq -r '.content_encoding // empty' "${resp}")
  if [[ "${encoding}" == "base64" ]]; then
    echo "${content}" | base64 -d > "${dest}/bundle.zip"
    (cd "${dest}" && unzip -o bundle.zip) > /dev/null
  else
    local filename
    filename=$(jq -r '.filename' "${resp}")
    echo "${content}" > "${dest}/${filename}"
    chmod +x "${dest}/${filename}"
  fi
  return 0
}

# Find the most recent pending registration matching our contact email
find_pending_registration() {
  local provider="$1"
  curl -sS -H "Authorization: Bearer ${TOKEN}" \
    "${CUDLY_URL}/api/registrations?status=pending" \
    | jq -c "[.[] | select(.provider == \"${provider}\" and .contact_email == \"${CONTACT_EMAIL}\")] | sort_by(.created_at) | last"
}

# Find an existing enabled account by provider + external_id
find_existing_account() {
  local provider="$1" external_id="$2"
  curl -sS -H "Authorization: Bearer ${TOKEN}" "${CUDLY_URL}/api/accounts" \
    | jq -r ".[] | select(.provider == \"${provider}\" and .external_id == \"${external_id}\") | .id"
}

# Approve a registration → returns account ID
approve_registration() {
  local reg_id="$1" reg_json="$2"
  local body
  body=$(echo "${reg_json}" | jq -c '{
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

  local resp="${WORKDIR}/approve-resp.json"
  local http_code
  http_code=$(curl -sS -o "${resp}" -w "%{http_code}" \
    -X POST "${CUDLY_URL}/api/registrations/${reg_id}/approve" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "X-CSRF-Token: ${CSRF}" \
    -d "${body}")
  if [[ "${http_code}" != "200" && "${http_code}" != "201" ]]; then
    echo "Approve failed (HTTP ${http_code}): $(cat "${resp}")" >&2
    return 1
  fi
  jq -r '.id' "${resp}"
}

# Approve or reuse: if an account already exists for this provider+external_id,
# reuse it (just test, don't delete). Otherwise approve the pending registration.
# Prints: account_id|created  or  account_id|reused
approve_or_reuse() {
  local provider="$1" external_id="$2"
  local existing
  existing=$(find_existing_account "${provider}" "${external_id}")
  if [[ -n "${existing}" ]]; then
    echo "Reusing existing account ${existing} (provider=${provider}, external_id=${external_id})" >&2
    echo "${existing}|reused"
    return 0
  fi

  local reg
  reg=$(find_pending_registration "${provider}")
  if [[ "${reg}" == "null" || -z "${reg}" ]]; then
    echo "No pending registration found for ${provider}" >&2
    return 1
  fi

  local reg_id
  reg_id=$(echo "${reg}" | jq -r '.id')
  echo "Approving registration ${reg_id}..." >&2
  local acct_id
  acct_id=$(approve_registration "${reg_id}" "${reg}")
  if [[ -n "${acct_id}" && "${acct_id}" != "null" ]]; then
    echo "${acct_id}|created"
  else
    delete_registration "${reg_id}" 2>/dev/null || true
    return 1
  fi
}

# Test an account's credentials
test_account() {
  local account_id="$1"
  local resp
  resp=$(curl -sS -X POST "${CUDLY_URL}/api/accounts/${account_id}/test" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "X-CSRF-Token: ${CSRF}")
  local ok
  ok=$(echo "${resp}" | jq -r '.ok')
  local msg
  msg=$(echo "${resp}" | jq -r '.message')
  echo "${ok}|${msg}"
}

# Delete a CUDly account — only if it was created by this test run
delete_account_if_created() {
  local account_id="$1" origin="$2"
  if [[ "${NO_CLEANUP}" == "true" ]]; then
    echo "Keeping account ${account_id} (--no-cleanup)"
    return
  fi
  if [[ "${origin}" == "created" ]]; then
    echo "Deleting test account ${account_id}..."
    curl -sS -X DELETE "${CUDLY_URL}/api/accounts/${account_id}" \
      -H "Authorization: Bearer ${TOKEN}" \
      -H "X-CSRF-Token: ${CSRF}" > /dev/null
  else
    echo "Keeping existing account ${account_id} (not created by this test)"
  fi
}

# Delete a pending registration
delete_registration() {
  if [[ "${NO_CLEANUP}" == "true" ]]; then return; fi
  local reg_id="$1"
  curl -sS -X DELETE "${CUDLY_URL}/api/registrations/${reg_id}" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "X-CSRF-Token: ${CSRF}" > /dev/null
}

# Run cleanup command only if --no-cleanup is not set
cleanup() {
  if [[ "${NO_CLEANUP}" == "true" ]]; then
    echo "  (skipped cleanup: $*)"
    return 0
  fi
  "$@"
}

# ---------------------------------------------------------------------------
# Test: AWS cross-account CLI
# ---------------------------------------------------------------------------
if should_run aws cli; then
  echo ""
  echo "============================================================"
  echo "TEST: AWS cross-account — CLI"
  echo "============================================================"
  DEST="${WORKDIR}/aws-cli"
  if download_iac aws cli "${DEST}"; then
    SCRIPT=$(find "${DEST}" -name '*-cli.sh' -o -name '*cli*.sh' | grep -v cfn | head -1)
    if [[ -z "${SCRIPT}" ]]; then
      SCRIPT=$(find "${DEST}" -name '*.sh' | head -1)
    fi
    echo "Running: ${SCRIPT}"
    ROLE_NAME="CUDly-e2e-test-$$"
    AWS_TARGET_ACCT_ID=$(aws sts get-caller-identity --profile "${AWS_TARGET_PROFILE}" --query Account --output text)
    if ROLE_NAME="${ROLE_NAME}" \
       AWS_PROFILE="${AWS_TARGET_PROFILE}" \
       CUDLY_CONTACT_EMAIL="${CONTACT_EMAIL}" \
       bash "${SCRIPT}" 2>&1 | tee "${DEST}/output.log"; then

      APPROVE_RESULT=$(approve_or_reuse aws "${AWS_TARGET_ACCT_ID}") || true
      if [[ -n "${APPROVE_RESULT}" ]]; then
        ACCT_ID="${APPROVE_RESULT%%|*}"
        ORIGIN="${APPROVE_RESULT#*|}"
        RESULT=$(test_account "${ACCT_ID}")
        OK="${RESULT%%|*}"
        echo "Test: ok=${OK} — ${RESULT#*|}"
        if [[ "${OK}" == "true" ]]; then
          delete_account_if_created "${ACCT_ID}" "${ORIGIN}"
          record "AWS CLI" "PASS"
        else
          record "AWS CLI" "FAIL"
        fi
      else
        record "AWS CLI" "FAIL"
      fi

      # Cleanup AWS resources
      if [[ "${NO_CLEANUP}" != "true" ]]; then
        echo "Cleaning up IAM role ${ROLE_NAME}..."
        aws iam delete-role-policy --role-name "${ROLE_NAME}" --policy-name CUDlyPermissions \
          --profile "${AWS_TARGET_PROFILE}" 2>/dev/null || true
        aws iam delete-role --role-name "${ROLE_NAME}" \
          --profile "${AWS_TARGET_PROFILE}" 2>/dev/null || true
      else
        echo "Keeping IAM role ${ROLE_NAME} (--no-cleanup)"
      fi
    else
      record "AWS CLI" "FAIL"
    fi
  else
    record "AWS CLI" "FAIL"
  fi
else
  record "AWS CLI" "SKIP"
fi

# ---------------------------------------------------------------------------
# Test: AWS cross-account Terraform bundle
# ---------------------------------------------------------------------------
if should_run aws bundle; then
  echo ""
  echo "============================================================"
  echo "TEST: AWS cross-account — Terraform bundle"
  echo "============================================================"
  DEST="${WORKDIR}/aws-bundle"
  if download_iac aws bundle "${DEST}"; then
    cd "${DEST}/terraform"
    # Use the server-generated tfvars as base, override with test-specific values
    SERVER_TFVARS=$(find . -name '*.tfvars' ! -name 'e2e*' | head -1)
    cat > e2e.tfvars <<TFVARS
role_name         = "CUDly-e2e-tf-$$"
cudly_api_url     = "${CUDLY_URL}"
contact_email     = "${CONTACT_EMAIL}"
account_name      = "AWS TF E2E"
TFVARS
    export AWS_PROFILE="${AWS_TARGET_PROFILE}"
    AWS_TARGET_ACCT_ID=$(aws sts get-caller-identity --query Account --output text)
    if terraform init -no-color > /dev/null 2>&1 \
       && terraform validate -no-color \
       && terraform apply -var-file="${SERVER_TFVARS}" -var-file=e2e.tfvars -auto-approve -no-color 2>&1 | tail -5; then

      APPROVE_RESULT=$(approve_or_reuse aws "${AWS_TARGET_ACCT_ID}") || true
      if [[ -n "${APPROVE_RESULT}" ]]; then
        ACCT_ID="${APPROVE_RESULT%%|*}"
        ORIGIN="${APPROVE_RESULT#*|}"
        RESULT=$(test_account "${ACCT_ID}")
        OK="${RESULT%%|*}"
        echo "Test: ok=${OK} — ${RESULT#*|}"
        if [[ "${OK}" == "true" ]]; then
          delete_account_if_created "${ACCT_ID}" "${ORIGIN}"
          record "AWS TF" "PASS"
        else
          record "AWS TF" "FAIL"
        fi
      else
        record "AWS TF" "FAIL"
      fi

      if [[ "${NO_CLEANUP}" != "true" ]]; then
        terraform destroy -var-file="${SERVER_TFVARS}" -var-file=e2e.tfvars -auto-approve -no-color 2>&1 | tail -3
      else
        echo "Keeping TF resources (--no-cleanup)"
      fi
    else
      record "AWS TF" "FAIL"
      if [[ "${NO_CLEANUP}" != "true" ]]; then
        terraform destroy -var-file="${SERVER_TFVARS}" -var-file=e2e.tfvars -auto-approve -no-color 2>/dev/null || true
      fi
    fi
    cd - > /dev/null
  else
    record "AWS TF" "FAIL"
  fi
else
  record "AWS TF" "SKIP"
fi

# ---------------------------------------------------------------------------
# Test: AWS cross-account CloudFormation
# ---------------------------------------------------------------------------
if should_run aws cfn; then
  echo ""
  echo "============================================================"
  echo "TEST: AWS cross-account — CloudFormation"
  echo "============================================================"
  DEST="${WORKDIR}/aws-cfn"
  if download_iac aws cfn "${DEST}"; then
    export AWS_PROFILE="${AWS_TARGET_PROFILE}"
    STACK_NAME="CUDly-e2e-cfn-$$"
    ROLE_NAME="CUDly-e2e-cfn-$$"
    CFN_TEMPLATE=$(find "${DEST}" -name 'template.yaml' | head -1)

    CFN_EXTERNAL_ID=$(uuidgen)
    CFN_SOURCE_ACCT=$(aws sts get-caller-identity --profile "${CUDLY_HOST_PROFILE}" --query Account --output text)
    # CFN template auto-registers via Lambda custom resource when CUDlyAPIURL + ContactEmail are set
    if aws cloudformation deploy \
         --template-file "${CFN_TEMPLATE}" \
         --stack-name "${STACK_NAME}" \
         --parameter-overrides \
           "SourceAccountID=${CFN_SOURCE_ACCT}" \
           "ExternalID=${CFN_EXTERNAL_ID}" \
           "RoleName=${ROLE_NAME}" \
           "CUDlyAPIURL=${CUDLY_URL}" \
           "ContactEmail=${CONTACT_EMAIL}" \
         --capabilities CAPABILITY_NAMED_IAM \
         --no-fail-on-empty-changeset 2>&1 | tail -5; then

      ROLE_ARN=$(aws cloudformation describe-stacks \
        --stack-name "${STACK_NAME}" \
        --query "Stacks[0].Outputs[?OutputKey=='RoleARN'].OutputValue" --output text)
      echo "Role ARN: ${ROLE_ARN}"

      # CFN auto-registered — approve or reuse → test
      CFN_TARGET_ACCT=$(aws sts get-caller-identity --query Account --output text)
      APPROVE_RESULT=$(approve_or_reuse aws "${CFN_TARGET_ACCT}") || true
      if [[ -n "${APPROVE_RESULT}" ]]; then
        ACCT_ID="${APPROVE_RESULT%%|*}"
        ORIGIN="${APPROVE_RESULT#*|}"
        RESULT=$(test_account "${ACCT_ID}")
        OK="${RESULT%%|*}"
        echo "Test: ok=${OK} — ${RESULT#*|}"
        if [[ "${OK}" == "true" ]]; then
          delete_account_if_created "${ACCT_ID}" "${ORIGIN}"
          record "AWS CFN" "PASS"
        else
          record "AWS CFN" "FAIL"
        fi
      else
        echo "WARNING: no account found after CFN deploy"
        record "AWS CFN" "FAIL"
      fi

      if [[ "${NO_CLEANUP}" != "true" ]]; then
        echo "Deleting CFN stack ${STACK_NAME} (async)..."
        aws cloudformation delete-stack --stack-name "${STACK_NAME}"
      else
        echo "Keeping CFN stack ${STACK_NAME} (--no-cleanup)"
      fi
    else
      record "AWS CFN" "FAIL"
      if [[ "${NO_CLEANUP}" != "true" ]]; then
        aws cloudformation delete-stack --stack-name "${STACK_NAME}" 2>/dev/null || true
      fi
    fi
  else
    record "AWS CFN" "FAIL"
  fi
else
  record "AWS CFN" "SKIP"
fi

# ---------------------------------------------------------------------------
# Helper: Run a self-contained Azure deploy script (Bicep/ARM/CLI)
# deploy-azure.sh now creates identity + role assignment + auto-registers
# ---------------------------------------------------------------------------
run_azure_deploy_test() {
  local label="$1" format="$2" template_flag="$3"
  echo ""
  echo "============================================================"
  echo "TEST: Azure WIF — ${label}"
  echo "============================================================"
  local dest="${WORKDIR}/azure-${format}"
  if ! download_iac azure "${format}" "${dest}"; then
    record "${label}" "FAIL"
    return
  fi

  local deploy_script
  deploy_script=$(find "${dest}" -name 'deploy-azure.sh' -o -name '*-cli.sh' | head -1)
  echo "Running: ${deploy_script}"

  if APP_NAME="CUDly-e2e-${format}-$$" \
     CUDLY_CONTACT_EMAIL="${CONTACT_EMAIL}" \
     bash "${deploy_script}" ${template_flag} 2>&1 | tee "${dest}/output.log"; then

    # Script auto-registered — approve or reuse → test
    local az_sub_id
    az_sub_id=$(az account show --query id --output tsv)
    APPROVE_RESULT=$(approve_or_reuse azure "${az_sub_id}") || true
    if [[ -n "${APPROVE_RESULT}" ]]; then
      ACCT_ID="${APPROVE_RESULT%%|*}"
      ORIGIN="${APPROVE_RESULT#*|}"
      RESULT=$(test_account "${ACCT_ID}")
      OK="${RESULT%%|*}"
      echo "Test: ok=${OK} — ${RESULT#*|}"
      if [[ "${OK}" == "true" ]]; then
        delete_account_if_created "${ACCT_ID}" "${ORIGIN}"
        record "${label}" "PASS"
      else
        record "${label}" "FAIL"
      fi
    else
      echo "No Azure account found/created"
      record "${label}" "FAIL"
    fi
  else
    record "${label}" "FAIL"
  fi

  # Cleanup: find SP/app from output and delete
  if [[ "${NO_CLEANUP}" == "true" ]]; then
    echo "Keeping Azure resources (--no-cleanup)"
    return
  fi
  echo "Cleaning up Azure resources..."
  local sp_id app_id
  sp_id=$(grep -oP 'sp_object_id\s*:\s*\K\S+' "${dest}/output.log" 2>/dev/null \
    || grep -oP 'SP Object ID:\s*\K\S+' "${dest}/output.log" 2>/dev/null || true)
  app_id=$(grep -oP 'client_id\s*:\s*\K\S+' "${dest}/output.log" 2>/dev/null \
    || grep -oP 'App.*ID:\s*\K\S+' "${dest}/output.log" 2>/dev/null || true)
  if [[ -n "${sp_id}" ]]; then
    az role assignment delete --assignee "${sp_id}" --role "Reservation Purchaser" \
      --scope "/subscriptions/$(az account show --query id -o tsv)" 2>/dev/null || true
    az ad sp delete --id "${sp_id}" 2>/dev/null || true
  fi
  if [[ -n "${app_id}" ]]; then
    az ad app delete --id "${app_id}" 2>/dev/null || true
  fi
}

# ---------------------------------------------------------------------------
# Test: Azure WIF — CLI
# ---------------------------------------------------------------------------
if should_run azure cli; then
  run_azure_deploy_test "Azure CLI" "cli" ""
else
  record "Azure CLI" "SKIP"
fi

# ---------------------------------------------------------------------------
# Test: Azure WIF — Bicep (self-contained: identity + role + registration)
# ---------------------------------------------------------------------------
if should_run azure bicep; then
  run_azure_deploy_test "Azure Bicep" "bicep" "--template bicep"
else
  record "Azure Bicep" "SKIP"
fi

# ---------------------------------------------------------------------------
# Test: Azure WIF — ARM (self-contained: identity + role + registration)
# ---------------------------------------------------------------------------
if should_run azure arm; then
  run_azure_deploy_test "Azure ARM" "arm" "--template arm"
else
  record "Azure ARM" "SKIP"
fi

# ---------------------------------------------------------------------------
# Test: Azure WIF — Terraform bundle
# ---------------------------------------------------------------------------
if should_run azure bundle; then
  echo ""
  echo "============================================================"
  echo "TEST: Azure WIF — Terraform bundle"
  echo "============================================================"
  DEST="${WORKDIR}/azure-tf"
  if download_iac azure bundle "${DEST}"; then
    cd "${DEST}/terraform"
    cat > e2e.tfvars <<TFVARS
app_display_name = "CUDly-e2e-tf-$$"
cudly_api_url    = "${CUDLY_URL}"
contact_email    = "${CONTACT_EMAIL}"
account_name     = "Azure TF E2E"
TFVARS
    SERVER_TFVARS=$(find . -name '*.tfvars' ! -name 'e2e*' | head -1)

    if terraform init -no-color > /dev/null 2>&1 \
       && terraform apply -var-file="${SERVER_TFVARS}" -var-file=e2e.tfvars -auto-approve -no-color 2>&1 | tail -5; then

      AZ_SUB_ID=$(az account show --query id --output tsv)
      APPROVE_RESULT=$(approve_or_reuse azure "${AZ_SUB_ID}") || true
      if [[ -n "${APPROVE_RESULT}" ]]; then
        ACCT_ID="${APPROVE_RESULT%%|*}"
        ORIGIN="${APPROVE_RESULT#*|}"
        RESULT=$(test_account "${ACCT_ID}")
        OK="${RESULT%%|*}"
        echo "Test: ok=${OK} — ${RESULT#*|}"
        if [[ "${OK}" == "true" ]]; then
          delete_account_if_created "${ACCT_ID}" "${ORIGIN}"
          record "Azure TF" "PASS"
        else
          record "Azure TF" "FAIL"
        fi
      else
        record "Azure TF" "FAIL"
      fi

      if [[ "${NO_CLEANUP}" != "true" ]]; then
        terraform destroy -var-file="${SERVER_TFVARS}" -var-file=e2e.tfvars -auto-approve -no-color 2>&1 | tail -3
      else
        echo "Keeping TF resources (--no-cleanup)"
      fi
    else
      record "Azure TF" "FAIL"
      if [[ "${NO_CLEANUP}" != "true" ]]; then
        terraform destroy -var-file="${SERVER_TFVARS}" -var-file=e2e.tfvars -auto-approve -no-color 2>/dev/null || true
        REG=$(find_pending_registration azure)
        if [[ "${REG}" != "null" && -n "${REG}" ]]; then
          delete_registration "$(echo "${REG}" | jq -r '.id')" 2>/dev/null || true
        fi
      fi
    fi
    cd - > /dev/null
  else
    record "Azure TF" "FAIL"
  fi
else
  record "Azure TF" "SKIP"
fi

# ---------------------------------------------------------------------------
# Test: GCP WIF — CLI
# ---------------------------------------------------------------------------
if should_run gcp cli; then
  echo ""
  echo "============================================================"
  echo "TEST: GCP WIF — CLI"
  echo "============================================================"
  DEST="${WORKDIR}/gcp-cli"
  if download_iac gcp cli "${DEST}"; then
    SCRIPT=$(find "${DEST}" -name '*.sh' | head -1)
    echo "Running: ${SCRIPT}"
    GCP_POOL_ID="cudly-e2e-cli-$$"
    if PROJECT_ID="${GCP_PROJECT}" \
       SERVICE_ACCOUNT_EMAIL="${GCP_SA_EMAIL}" \
       POOL_ID="${GCP_POOL_ID}" \
       CUDLY_CONTACT_EMAIL="${CONTACT_EMAIL}" \
       bash "${SCRIPT}" 2>&1 | tee "${DEST}/output.log"; then

      APPROVE_RESULT=$(approve_or_reuse gcp "${GCP_PROJECT}") || true
      if [[ -n "${APPROVE_RESULT}" ]]; then
        ACCT_ID="${APPROVE_RESULT%%|*}"
        ORIGIN="${APPROVE_RESULT#*|}"
        echo "Waiting 15s for GCP IAM propagation..."
        sleep 15
        RESULT=$(test_account "${ACCT_ID}")
        OK="${RESULT%%|*}"
        echo "Test: ok=${OK} — ${RESULT#*|}"
        if [[ "${OK}" == "true" ]]; then
          delete_account_if_created "${ACCT_ID}" "${ORIGIN}"
          record "GCP CLI" "PASS"
        else
          record "GCP CLI" "FAIL"
        fi
      else
        record "GCP CLI" "FAIL"
      fi

      # Cleanup GCP resources
      if [[ "${NO_CLEANUP}" != "true" ]]; then
        echo "Cleaning up GCP WIF pool ${GCP_POOL_ID}..."
        gcloud iam workload-identity-pools delete "${GCP_POOL_ID}" \
          --location global --project "${GCP_PROJECT}" --quiet 2>/dev/null || true
      else
        echo "Keeping GCP WIF pool ${GCP_POOL_ID} (--no-cleanup)"
      fi
    else
      record "GCP CLI" "FAIL"
    fi
  else
    record "GCP CLI" "FAIL"
  fi
else
  record "GCP CLI" "SKIP"
fi

# ---------------------------------------------------------------------------
# Test: GCP WIF — Terraform bundle
# ---------------------------------------------------------------------------
if should_run gcp bundle; then
  echo ""
  echo "============================================================"
  echo "TEST: GCP WIF — Terraform bundle"
  echo "============================================================"
  DEST="${WORKDIR}/gcp-tf"
  if download_iac gcp bundle "${DEST}"; then
    cd "${DEST}/terraform"
    cat > e2e.tfvars <<TFVARS
project               = "${GCP_PROJECT}"
service_account_email = "${GCP_SA_EMAIL}"
pool_id               = "cudly-e2e-tf-$$"
provider_id           = "cudly-e2e-prov-$$"
provider_type         = "aws"
aws_account_id        = "$(aws sts get-caller-identity --profile "${CUDLY_HOST_PROFILE}" --query Account --output text)"
cudly_api_url         = "${CUDLY_URL}"
contact_email         = "${CONTACT_EMAIL}"
account_name          = "GCP TF E2E"
TFVARS
    SERVER_TFVARS=$(find . -name '*.tfvars' ! -name 'e2e*' | head -1)

    if terraform init -no-color > /dev/null 2>&1 \
       && terraform apply -var-file="${SERVER_TFVARS}" -var-file=e2e.tfvars -auto-approve -no-color 2>&1 | tail -5; then

      APPROVE_RESULT=$(approve_or_reuse gcp "${GCP_PROJECT}") || true
      if [[ -n "${APPROVE_RESULT}" ]]; then
        ACCT_ID="${APPROVE_RESULT%%|*}"
        ORIGIN="${APPROVE_RESULT#*|}"
        echo "Waiting 15s for GCP IAM propagation..."
        sleep 15
        RESULT=$(test_account "${ACCT_ID}")
        OK="${RESULT%%|*}"
        echo "Test: ok=${OK} — ${RESULT#*|}"
        if [[ "${OK}" == "true" ]]; then
          delete_account_if_created "${ACCT_ID}" "${ORIGIN}"
          record "GCP TF" "PASS"
        else
          record "GCP TF" "FAIL"
        fi
      else
        record "GCP TF" "FAIL"
      fi

      if [[ "${NO_CLEANUP}" != "true" ]]; then
        terraform destroy -var-file="${SERVER_TFVARS}" -var-file=e2e.tfvars -auto-approve -no-color 2>&1 | tail -3
      else
        echo "Keeping TF resources (--no-cleanup)"
      fi
    else
      record "GCP TF" "FAIL"
      if [[ "${NO_CLEANUP}" != "true" ]]; then
        terraform destroy -var-file="${SERVER_TFVARS}" -var-file=e2e.tfvars -auto-approve -no-color 2>/dev/null || true
        REG=$(find_pending_registration gcp)
        if [[ "${REG}" != "null" && -n "${REG}" ]]; then
          delete_registration "$(echo "${REG}" | jq -r '.id')" 2>/dev/null || true
        fi
      fi
    fi
    cd - > /dev/null
  else
    record "GCP TF" "FAIL"
  fi
else
  record "GCP TF" "SKIP"
fi

# ---------------------------------------------------------------------------
# Test: Existing persistent accounts — /test smoke
# ---------------------------------------------------------------------------
echo ""
echo "============================================================"
echo "TEST: Persistent accounts — /test smoke"
echo "============================================================"
ACCOUNTS=$(curl -sS -H "Authorization: Bearer ${TOKEN}" "${CUDLY_URL}/api/accounts")
ENABLED=$(echo "${ACCOUNTS}" | jq -c '[.[] | select(.enabled == true)]')
COUNT=$(echo "${ENABLED}" | jq 'length')
echo "Found ${COUNT} enabled account(s)"

for row in $(echo "${ENABLED}" | jq -r '.[] | @base64'); do
  ACCT=$(echo "${row}" | base64 -D 2>/dev/null || echo "${row}" | base64 -d)
  ID=$(echo "${ACCT}" | jq -r '.id')
  NAME=$(echo "${ACCT}" | jq -r '.name')
  PROVIDER=$(echo "${ACCT}" | jq -r '.provider')

  RESULT=$(test_account "${ID}")
  OK="${RESULT%%|*}"
  MSG="${RESULT#*|}"

  if [[ "${OK}" == "true" ]]; then
    echo "  PASS: ${NAME} (${PROVIDER}) — ${MSG}"
    record "Smoke: ${NAME}" "PASS"
  else
    echo "  FAIL: ${NAME} (${PROVIDER}) — ${MSG}"
    record "Smoke: ${NAME}" "FAIL"
  fi
done

# ---------------------------------------------------------------------------
# Final cleanup: delete any pending registrations created by this run
# ---------------------------------------------------------------------------
echo ""
if [[ "${NO_CLEANUP}" != "true" ]]; then
  echo "=== Cleaning up pending registrations ==="
  PENDING=$(curl -sS -H "Authorization: Bearer ${TOKEN}" "${CUDLY_URL}/api/registrations?status=pending")
  for REG_ID in $(echo "${PENDING}" | jq -r ".[] | select(.contact_email == \"${CONTACT_EMAIL}\") | .id"); do
    echo "Deleting pending registration ${REG_ID}..."
    delete_registration "${REG_ID}" 2>/dev/null || true
  done
else
  echo "=== Skipping cleanup (--no-cleanup) ==="
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "============================================================"
echo "E2E FEDERATION TEST SUMMARY"
echo "============================================================"
for r in "${RESULTS[@]}"; do
  status="${r%% *}"
  label="${r#* }"
  case "${status}" in
    PASS) printf "  \033[32mPASS\033[0m  %s\n" "${label}" ;;
    FAIL) printf "  \033[31mFAIL\033[0m  %s\n" "${label}" ;;
    SKIP) printf "  \033[33mSKIP\033[0m  %s\n" "${label}" ;;
  esac
done
echo ""
echo "Total: ${PASS} passed, ${FAIL} failed, ${SKIP} skipped"
echo "============================================================"

if [[ "${FAIL}" -gt 0 ]]; then
  exit 1
fi
