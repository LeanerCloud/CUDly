#!/usr/bin/env bash
set -euo pipefail

# Test the cudly-terraform-deploy IAM role by running full destroy+apply
# cycles for both Lambda and Fargate CUDly environments.
#
# WARNING: If destroy succeeds but apply fails, the environment is left
# destroyed. Manually run terraform apply with your own credentials to recover.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CUDLY_TF_DIR="${SCRIPT_DIR}/.."

# Read role ARN from Terraform output (run as user/cristi)
ROLE_ARN=$(cd "$SCRIPT_DIR" && terraform output -raw role_arn 2>/dev/null) || {
  echo "ERROR: Could not read role_arn from terraform output."
  echo "Have you run 'terraform apply' in ${SCRIPT_DIR} first?"
  exit 1
}

echo "Using role: ${ROLE_ARN}"
echo "CUDly TF dir: ${CUDLY_TF_DIR}"
echo ""

# Environment configs: name, backend file, tfvars file
declare -a ENVS=(
  "lambda:backends/dev.tfbackend:dev.tfvars"
  "fargate:backends/fargate-dev.tfbackend:fargate-dev.tfvars"
)

assume_role() {
  local session_name="$1"
  echo "Assuming role ${ROLE_ARN} (session: ${session_name})..."

  local creds
  creds=$(aws sts assume-role \
    --role-arn "${ROLE_ARN}" \
    --role-session-name "${session_name}" \
    --duration-seconds 3600 \
    --output json \
    --profile personal)

  export AWS_ACCESS_KEY_ID=$(echo "$creds" | jq -r '.Credentials.AccessKeyId')
  export AWS_SECRET_ACCESS_KEY=$(echo "$creds" | jq -r '.Credentials.SecretAccessKey')
  export AWS_SESSION_TOKEN=$(echo "$creds" | jq -r '.Credentials.SessionToken')

  # Assumed-role creds conflict with named profiles
  unset AWS_PROFILE
  export TF_VAR_aws_profile=""

  echo "Assumed role successfully. Caller identity:"
  aws sts get-caller-identity
  echo ""
}

run_env() {
  local env_name="$1"
  local backend_file="$2"
  local tfvars_file="$3"
  local start_time

  echo "============================================"
  echo "Environment: ${env_name}"
  echo "Backend:     ${backend_file}"
  echo "Vars:        ${tfvars_file}"
  echo "============================================"

  # Fresh assume-role session per environment
  assume_role "cudly-test-${env_name}"

  cd "$CUDLY_TF_DIR"

  echo "--- terraform init ---"
  terraform init -backend-config="${backend_file}" -reconfigure

  start_time=$(date +%s)

  echo ""
  echo "--- terraform destroy ---"
  if ! terraform destroy -var-file="${tfvars_file}" -auto-approve 2>&1 | tee /tmp/cudly-test-${env_name}-destroy.log; then
    echo ""
    echo "DESTROY FAILED for ${env_name}."
    parse_access_denied /tmp/cudly-test-${env_name}-destroy.log
    return 1
  fi

  echo ""
  echo "--- terraform apply ---"
  if ! terraform apply -var-file="${tfvars_file}" -auto-approve 2>&1 | tee /tmp/cudly-test-${env_name}-apply.log; then
    echo ""
    echo "APPLY FAILED for ${env_name}."
    echo "WARNING: Environment was destroyed but apply failed. Recover manually:"
    echo "  cd ${CUDLY_TF_DIR}"
    echo "  terraform init -backend-config=${backend_file} -reconfigure"
    echo "  terraform apply -var-file=${tfvars_file}"
    parse_access_denied /tmp/cudly-test-${env_name}-apply.log
    return 1
  fi

  local end_time=$(date +%s)
  local elapsed=$(( end_time - start_time ))
  echo ""
  echo "${env_name}: destroy+apply completed in ${elapsed}s"
  echo ""
}

parse_access_denied() {
  local logfile="$1"
  local denied_actions

  denied_actions=$(grep -oP '(?<=Action=)[a-zA-Z0-9:]+(?=.*AccessDenied)' "$logfile" 2>/dev/null || true)
  if [[ -z "$denied_actions" ]]; then
    # Try alternative AccessDeniedException pattern
    denied_actions=$(grep -i 'accessdeni' "$logfile" | grep -oP '[a-z0-9]+:[A-Z][a-zA-Z]+' 2>/dev/null | sort -u || true)
  fi

  if [[ -n "$denied_actions" ]]; then
    echo ""
    echo "ACCESS DENIED - Missing permissions:"
    echo "$denied_actions" | sort -u | while read -r action; do
      echo "  - ${action}"
    done
    echo ""
    echo "Add the missing action(s) to the appropriate policy_*.tf file,"
    echo "run 'terraform apply' in ${SCRIPT_DIR}, then re-run this script."
  fi
}

# --- Main ---
total_start=$(date +%s)
failed=0

for env_spec in "${ENVS[@]}"; do
  IFS=: read -r env_name backend_file tfvars_file <<< "$env_spec"
  if ! run_env "$env_name" "$backend_file" "$tfvars_file"; then
    failed=1
    echo "Stopping after ${env_name} failure."
    break
  fi
done

total_end=$(date +%s)
total_elapsed=$(( total_end - total_start ))

echo ""
echo "============================================"
if [[ $failed -eq 0 ]]; then
  echo "SUCCESS: All environments passed destroy+apply in ${total_elapsed}s"
else
  echo "FAILED: See errors above. Total time: ${total_elapsed}s"
  exit 1
fi
