#!/usr/bin/env bash
# check-archera-parity.sh — verify that the Archera permission lists in
# CloudFormation, Bicep, and ARM templates match the canonical YAML scope files.
#
# Since the shared Terraform modules (iac/modules/archera/{aws,azure,gcp}/)
# load permissions directly from the scope YAML at apply time, Terraform itself
# always reflects the YAML — no separate Terraform parity check is needed.
# The non-Terraform deployment paths (CFN, Bicep, ARM) embed the permission
# lists as literals and MUST be kept in sync with the scope YAML manually;
# this script is the CI gate that enforces that.
#
# Additionally, when terraform/environments/{aws,azure,gcp}/archera.tf exists
# (after PR #310 is merged into the base branch), this script verifies that
# those environment files are thin module callers (i.e. they no longer contain
# inline permission lists — the module handles that).
#
# Usage (from repo root):
#   bash scripts/check-archera-parity.sh
#
# Exit codes:
#   0 — parity confirmed (or optional files not yet present — skipped)
#   1 — drift detected; see output for details
#
# Dependencies: yq (YAML parsing) or python3 with PyYAML.  Falls back to
# python3 when yq is not on PATH.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

AWS_SCOPE_YAML="$REPO_ROOT/iac/modules/archera/scope.aws.yaml"
AZURE_SCOPE_YAML="$REPO_ROOT/iac/modules/archera/scope.azure.yaml"
GCP_SCOPE_YAML="$REPO_ROOT/iac/modules/archera/scope.gcp.yaml"

# ── YAML helpers ───────────────────────────────────────────────────────────────

# Extract a YAML list at a given key, one item per line, sorted.
# Always uses python3 for consistent cross-platform behaviour.
yaml_list() {
  local file="$1" key="$2"
  python3 - "$file" "$key" <<'EOF'
import sys, yaml
data = yaml.safe_load(open(sys.argv[1]))
key = sys.argv[2]
items = data.get(key, [])
for item in sorted(set(items)):
    print(item)
EOF
}

# ── AWS checks ─────────────────────────────────────────────────────────────────

# Extract IAM action strings from a CloudFormation YAML template.
# Only match lines that are YAML list items (- prefix) containing IAM actions.
# Excludes sts:AssumeRole (trust policy, not a grant), sts:ExternalId (condition
# key, not an action), and arn: references.
extract_cfn_aws_actions() {
  local file="$1"
  # Match YAML list items: lines like "              - ec2:DescribeReservedInstances"
  # Service names may contain digits (ec2, cur, s3, etc.) — use [a-z][a-z0-9]*.
  # Exclude sts: (trust policy) and arn: (ARN references).
  /usr/bin/grep -oE '^[[:space:]]+-[[:space:]]+([a-z][a-z0-9]*:[A-Za-z]+)' "$file" \
    | /usr/bin/grep -oE '[a-z][a-z0-9]*:[A-Za-z]+' \
    | /usr/bin/grep -vE '^(sts|arn):' \
    | sort -u
}

check_aws_cfn_parity() {
  local label="$1"
  local cfn_file="$2"

  if [[ ! -f "$cfn_file" ]]; then
    echo "FAIL  AWS $label: CFN template not found at $cfn_file"
    return 1
  fi

  local yaml_read yaml_purchase yaml_all cfn_actions diff_output
  yaml_read=$(yaml_list "$AWS_SCOPE_YAML" "read_actions")
  yaml_purchase=$(yaml_list "$AWS_SCOPE_YAML" "purchase_actions")
  yaml_all=$(printf '%s\n%s\n' "$yaml_read" "$yaml_purchase" | sort -u)

  cfn_actions=$(extract_cfn_aws_actions "$cfn_file")
  diff_output=$(diff <(echo "$yaml_all") <(echo "$cfn_actions") || true)

  if [[ -z "$diff_output" ]]; then
    echo "OK    AWS $label CFN parity confirmed"
    return 0
  else
    echo "FAIL  AWS $label CFN template has permission drift versus scope.aws.yaml:"
    echo "$diff_output"
    return 1
  fi
}

# ── Azure checks ───────────────────────────────────────────────────────────────

extract_bicep_azure_actions() {
  local file="$1"
  # Match only permission action strings (not resource type references).
  # Azure RBAC actions follow the pattern Service/ResourceType/action
  # e.g. 'Microsoft.CostManagement/*/read' — but resource type declarations
  # like 'Microsoft.Authorization/roleDefinitions@2022-04-01' contain '@'.
  # Also exclude string-literal resource type uses (param names, type blocks).
  grep -oE "'Microsoft\.[^']+'" "$file" \
    | tr -d "'" \
    | grep -vE '@|roleDefinitions$|roleAssignments$' \
    | sort -u
}

extract_arm_azure_actions() {
  local file="$1"
  # Match only permission action strings in the actions/notActions arrays.
  # Exclude resource type references (contain '@' or are pure resource types).
  grep -oE '"Microsoft\.[^"]+"' "$file" \
    | tr -d '"' \
    | grep -vE '@|roleDefinitions$|roleAssignments$|roleDefinitions/|roleAssignments/' \
    | sort -u
}

check_azure_bicep_parity() {
  local bicep_file="$REPO_ROOT/iac/federation/azure-target/bicep/archera.bicep"

  if [[ ! -f "$bicep_file" ]]; then
    echo "FAIL  Azure Bicep template not found at $bicep_file"
    return 1
  fi

  local yaml_read yaml_purchase yaml_all bicep_actions diff_output
  yaml_read=$(yaml_list "$AZURE_SCOPE_YAML" "read_actions")
  yaml_purchase=$(yaml_list "$AZURE_SCOPE_YAML" "purchase_actions")
  yaml_all=$(printf '%s\n%s\n' "$yaml_read" "$yaml_purchase" | sort -u)

  bicep_actions=$(extract_bicep_azure_actions "$bicep_file")
  diff_output=$(diff <(echo "$yaml_all") <(echo "$bicep_actions") || true)

  if [[ -z "$diff_output" ]]; then
    echo "OK    Azure Bicep parity confirmed"
    return 0
  else
    echo "FAIL  Azure Bicep template has permission drift versus scope.azure.yaml:"
    echo "$diff_output"
    return 1
  fi
}

check_azure_arm_parity() {
  local arm_file="$REPO_ROOT/iac/federation/azure-target/arm/archera.arm.json"

  if [[ ! -f "$arm_file" ]]; then
    echo "FAIL  Azure ARM template not found at $arm_file"
    return 1
  fi

  local yaml_read yaml_purchase yaml_all arm_actions diff_output
  yaml_read=$(yaml_list "$AZURE_SCOPE_YAML" "read_actions")
  yaml_purchase=$(yaml_list "$AZURE_SCOPE_YAML" "purchase_actions")
  yaml_all=$(printf '%s\n%s\n' "$yaml_read" "$yaml_purchase" | sort -u)

  arm_actions=$(extract_arm_azure_actions "$arm_file")
  diff_output=$(diff <(echo "$yaml_all") <(echo "$arm_actions") || true)

  if [[ -z "$diff_output" ]]; then
    echo "OK    Azure ARM parity confirmed"
    return 0
  else
    echo "FAIL  Azure ARM template has permission drift versus scope.azure.yaml:"
    echo "$diff_output"
    return 1
  fi
}

# ── Environment thin-caller checks ─────────────────────────────────────────────
# When terraform/environments/{aws,azure,gcp}/archera.tf exist (after PR #310
# is merged), verify they are thin module callers — they must NOT contain
# inline permission actions (those belong in the shared module).

check_env_thin_caller() {
  local cloud="$1"
  local env_file="$REPO_ROOT/terraform/environments/${cloud}/archera.tf"

  if [[ ! -f "$env_file" ]]; then
    echo "SKIP  ${cloud} environment archera.tf not found (PR #310 not yet merged) — skipping thin-caller check"
    return 0
  fi

  local inline_actions
  case "$cloud" in
    aws)   inline_actions=$(grep -oE '"[a-z]+:[A-Za-z]+"' "$env_file" || true) ;;
    azure) inline_actions=$(grep -oE '"Microsoft\.[^"]*"' "$env_file" || true) ;;
    gcp)   inline_actions=$(grep -oE '"[a-z]+\.[a-z]+\.[a-zA-Z]+"' "$env_file" || true) ;;
  esac

  if [[ -z "$inline_actions" ]]; then
    echo "OK    ${cloud} environment archera.tf is a thin module caller (no inline permissions)"
    return 0
  else
    echo "FAIL  ${cloud} environment archera.tf still has inline permissions — should be a thin module caller:"
    echo "$inline_actions"
    return 1
  fi
}

# ── Run all checks ─────────────────────────────────────────────────────────────

failures=0

# Scope YAML files must exist — they are the source of truth.
for yaml in "$AWS_SCOPE_YAML" "$AZURE_SCOPE_YAML" "$GCP_SCOPE_YAML"; do
  if [[ ! -f "$yaml" ]]; then
    echo "FAIL  Scope YAML not found: $yaml"
    failures=$((failures + 1))
  fi
done

if [[ "$failures" -gt 0 ]]; then
  echo ""
  echo "Archera parity check FAILED ($failures failure(s)) — scope YAML files are missing."
  exit 1
fi

# CFN parity (AWS only — GCP does not have a CloudFormation equivalent)
check_aws_cfn_parity "aws-target"       "$REPO_ROOT/iac/federation/aws-target/cloudformation/archera.cfn.yaml"      || failures=$((failures + 1))
check_aws_cfn_parity "aws-cross-account" "$REPO_ROOT/iac/federation/aws-cross-account/cloudformation/archera.cfn.yaml" || failures=$((failures + 1))

# Azure alternative format parity
check_azure_bicep_parity || failures=$((failures + 1))
check_azure_arm_parity   || failures=$((failures + 1))

# Environment thin-caller verification (runs once PR #310 is merged)
check_env_thin_caller aws   || failures=$((failures + 1))
check_env_thin_caller azure || failures=$((failures + 1))
check_env_thin_caller gcp   || failures=$((failures + 1))

if [[ "$failures" -gt 0 ]]; then
  echo ""
  echo "Archera parity check FAILED ($failures failure(s))."
  echo "Update CFN / Bicep / ARM templates to match iac/modules/archera/scope.*.yaml"
  echo "and re-run this script."
  exit 1
fi

echo ""
echo "Archera parity check passed."
