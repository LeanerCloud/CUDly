#!/usr/bin/env bash
# test-aws-iam-parity.sh
#
# Exercises check-aws-iam-parity.sh:
#   1. against the real repository (must pass post-reconciliation),
#   2. against fixture trees seeded from the real files with drift injected,
#      replicating the exact pre-fix INF-02 failure shapes (missing CE
#      Coverage actions in the Terraform Lambda module; missing org-discovery
#      statement in the federation CloudFormation template).
#
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
CHECK="${SCRIPT_DIR}/check-aws-iam-parity.sh"

pass=0
fail=0

run_case() {
  local label="$1"
  local expected_exit="$2"
  shift 2

  actual_exit=0
  bash "$CHECK" "$@" >/dev/null 2>&1 || actual_exit=$?

  if [[ "$actual_exit" -eq "$expected_exit" ]]; then
    echo "PASS: $label"
    (( pass++ )) || true
  else
    echo "FAIL: $label (expected exit $expected_exit, got $actual_exit)"
    (( fail++ )) || true
  fi
}

# seed_fixture <dest> copies the six compared files from the repo into a
# fixture tree that mirrors the repo layout.
seed_fixture() {
  local dest="$1"
  local files=(
    cloudformation/stacks/CUDly/template.yaml
    terraform/modules/compute/aws/lambda/main.tf
    terraform/modules/compute/aws/fargate/main.tf
    iac/federation/aws-cross-account/cloudformation/template.yaml
    iac/federation/aws-cross-account/terraform/main.tf
    iac/federation/aws-target/cloudformation/template.yaml
    iac/federation/aws-target/terraform/main.tf
    internal/iacfiles/templates/aws-cross-account-cli.sh.tmpl
    internal/iacfiles/templates/aws-wif-cli.sh.tmpl
  )
  local f
  for f in "${files[@]}"; do
    mkdir -p "$dest/$(dirname "$f")"
    cp "$REPO_ROOT/$f" "$dest/$f"
  done
}

TMP_BASE="$(mktemp -d)"
trap 'rm -rf "$TMP_BASE"' EXIT

# Case 1: the real repository must be in parity.
run_case "real repository is in parity" 0

# Case 2: pre-fix INF-02 shape - TF Lambda module missing the CE Coverage
# actions while the CFN stack grants them.
FIX2="$TMP_BASE/drift-runtime"
seed_fixture "$FIX2"
grep -v 'ce:GetReservationCoverage\|ce:GetSavingsPlansCoverage' \
  "$REPO_ROOT/terraform/modules/compute/aws/lambda/main.tf" \
  > "$FIX2/terraform/modules/compute/aws/lambda/main.tf"
run_case "runtime drift (TF lambda missing CE Coverage actions) exits 1" 1 --root "$FIX2"

# Case 3: pre-fix INF-02 shape - federation CFN template missing the
# org-discovery statement its Terraform sibling carries.
FIX3="$TMP_BASE/drift-federation"
seed_fixture "$FIX3"
grep -v 'organizations:ListAccounts\|organizations:DescribeOrganization' \
  "$REPO_ROOT/iac/federation/aws-cross-account/cloudformation/template.yaml" \
  > "$FIX3/iac/federation/aws-cross-account/cloudformation/template.yaml"
run_case "federation drift (CFN missing org-discovery statement) exits 1" 1 --root "$FIX3"

# Case 4: a missing source file must fail loud (exit 2), never pass silently.
FIX4="$TMP_BASE/missing-file"
seed_fixture "$FIX4"
rm "$FIX4/terraform/modules/compute/aws/fargate/main.tf"
run_case "missing compared file exits 2" 2 --root "$FIX4"

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
