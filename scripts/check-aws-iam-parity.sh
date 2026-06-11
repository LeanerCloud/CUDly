#!/usr/bin/env bash
# check-aws-iam-parity.sh
#
# Asserts that the AWS IAM action lists granted to the CUDly runtime and to the
# customer-deployed federation roles are identical across every IaC flavor that
# encodes them, so CloudFormation and Terraform deployments of the same code
# cannot silently drift apart (the AWS sibling of check-azure-role-parity.sh).
#
# Three comparisons:
#
#   1. runtime    : cloudformation/stacks/CUDly/template.yaml
#                   == terraform/modules/compute/aws/lambda/main.tf
#                   == terraform/modules/compute/aws/fargate/main.tf
#   2. federation cross-account pair (incl. the optional org-discovery
#      statement, which exists in both as an opt-in):
#                   iac/federation/aws-cross-account/cloudformation/template.yaml
#                   == iac/federation/aws-cross-account/terraform/main.tf
#   3. federation core (org discovery excluded: the CLI quick-onboarding
#      scripts and the aws-target WIF flavor intentionally do not offer it):
#                   both files from (2)
#                   == iac/federation/aws-target/cloudformation/template.yaml
#                   == iac/federation/aws-target/terraform/main.tf
#                   == internal/iacfiles/templates/aws-cross-account-cli.sh.tmpl
#                   == internal/iacfiles/templates/aws-wif-cli.sh.tmpl
#
# Only actions in the cloud-API namespaces the application code calls are
# compared (see ACTION_PREFIXES). Platform plumbing (logs, dynamodb, ses, sns,
# secretsmanager, sts, ssmmessages, lambda) legitimately differs per deployment
# flavor and is excluded.
#
# Exit 0 = all lists match.
# Exit 1 = drift found; the diff is printed to stderr.
# Exit 2 = usage / environment error.
#
# Usage:
#   scripts/check-aws-iam-parity.sh [--root <path>]
#
# --root lets the test harness point at a fixture tree that mirrors the repo
# layout without touching the real sources.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) REPO_ROOT="$2"; shift 2 ;;
    *) echo "Unknown flag: $1" >&2; exit 2 ;;
  esac
done

# IAM action namespaces owned by the application code. Keep in sync with the
# services the providers/aws code actually calls.
ACTION_PREFIXES='ce|ec2|rds|elasticache|es|redshift|memorydb|savingsplans|organizations'

# --- extraction ---------------------------------------------------------------
# Pull every token shaped like <prefix>:<CamelCaseAction> out of a file,
# regardless of whether it is YAML, HCL, or an embedded JSON heredoc. IAM
# actions always start with an uppercase letter after the colon, which keeps
# ARNs (region segments are lowercase) out of the match.

extract_actions() {
  local file="$1"
  local exclude_orgs="${2:-}"

  if [[ ! -f "$file" ]]; then
    echo "ERROR: file not found: $file" >&2
    exit 2
  fi

  local actions
  actions=$(grep -oE "(^|[^A-Za-z])(${ACTION_PREFIXES}):[A-Z][A-Za-z]+" "$file" \
    | sed 's/^[^a-zA-Z]//' \
    | sort -u)

  if [[ "$exclude_orgs" == "no-orgs" ]]; then
    actions=$(printf '%s\n' "$actions" | grep -v '^organizations:' || true)
  fi

  if [[ -z "$actions" ]]; then
    echo "ERROR: no IAM actions extracted from: $file" >&2
    echo "       Did the policy move or change format?" >&2
    exit 2
  fi

  printf '%s\n' "$actions"
}

# --- comparison ---------------------------------------------------------------

FAILURES=0

compare_pair() {
  local label="$1" ref_name="$2" ref_actions="$3" other_name="$4" other_actions="$5"

  local diff_out
  diff_out=$(diff <(printf '%s\n' "$ref_actions") <(printf '%s\n' "$other_actions") || true)

  if [[ -n "$diff_out" ]]; then
    {
      echo "ERROR [$label]: IAM action drift between:"
      echo "  < $ref_name"
      echo "  > $other_name"
      echo "$diff_out"
      echo ""
    } >&2
    FAILURES=$((FAILURES + 1))
  fi
}

# --- 1. runtime: CFN stack vs TF lambda vs TF fargate --------------------------

CFN_STACK="$REPO_ROOT/cloudformation/stacks/CUDly/template.yaml"
TF_LAMBDA="$REPO_ROOT/terraform/modules/compute/aws/lambda/main.tf"
TF_FARGATE="$REPO_ROOT/terraform/modules/compute/aws/fargate/main.tf"

cfn_stack_actions=$(extract_actions "$CFN_STACK")
tf_lambda_actions=$(extract_actions "$TF_LAMBDA")
tf_fargate_actions=$(extract_actions "$TF_FARGATE")

compare_pair "runtime" "$CFN_STACK" "$cfn_stack_actions" "$TF_LAMBDA" "$tf_lambda_actions"
compare_pair "runtime" "$TF_LAMBDA" "$tf_lambda_actions" "$TF_FARGATE" "$tf_fargate_actions"

# --- 2. federation cross-account pair (all namespaces) -------------------------

FED_XACC_CFN="$REPO_ROOT/iac/federation/aws-cross-account/cloudformation/template.yaml"
FED_XACC_TF="$REPO_ROOT/iac/federation/aws-cross-account/terraform/main.tf"

fed_cfn_actions=$(extract_actions "$FED_XACC_CFN")
fed_tf_actions=$(extract_actions "$FED_XACC_TF")

compare_pair "federation cross-account" "$FED_XACC_CFN" "$fed_cfn_actions" "$FED_XACC_TF" "$fed_tf_actions"

# --- 3. federation core across all flavors (org discovery excluded) ------------

FED_WIF_CFN="$REPO_ROOT/iac/federation/aws-target/cloudformation/template.yaml"
FED_WIF_TF="$REPO_ROOT/iac/federation/aws-target/terraform/main.tf"
FED_XACC_CLI="$REPO_ROOT/internal/iacfiles/templates/aws-cross-account-cli.sh.tmpl"
FED_WIF_CLI="$REPO_ROOT/internal/iacfiles/templates/aws-wif-cli.sh.tmpl"

fed_core_ref=$(extract_actions "$FED_XACC_CFN" no-orgs)

for f in "$FED_XACC_TF" "$FED_WIF_CFN" "$FED_WIF_TF" "$FED_XACC_CLI" "$FED_WIF_CLI"; do
  compare_pair "federation core" "$FED_XACC_CFN" "$fed_core_ref" "$f" "$(extract_actions "$f" no-orgs)"
done

# --- result --------------------------------------------------------------------

if [[ "$FAILURES" -gt 0 ]]; then
  echo "FAILED: $FAILURES IAM parity comparison(s) drifted. Update the lagging file(s) so all lists match." >&2
  exit 1
fi

echo "OK: AWS IAM action lists are in parity across CloudFormation, Terraform, and CLI onboarding templates."
exit 0
