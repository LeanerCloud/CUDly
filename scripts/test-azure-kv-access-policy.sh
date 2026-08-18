#!/usr/bin/env bash
# test-azure-kv-access-policy.sh
#
# Exercises check-azure-kv-access-policy.sh against testdata fixtures, in BOTH
# directions: a clean input must pass and a violating input must fail. A guard
# that only ever returns "clean" is indistinguishable from a broken one, so the
# failing cases are the ones that give this check its value.
#
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
CHECK="${SCRIPT_DIR}/check-azure-kv-access-policy.sh"
FIXTURES="${SCRIPT_DIR}/testdata/azure-kv-access-policy"

pass=0
fail=0

run_case() {
  local label="$1"
  local expected_exit="$2"
  shift 2

  local actual_exit=0
  "$CHECK" "$@" >/dev/null 2>&1 || actual_exit=$?

  if [[ "$actual_exit" -eq "$expected_exit" ]]; then
    echo "PASS: $label"
    (( pass++ )) || true
  else
    echo "FAIL: $label (expected exit $expected_exit, got $actual_exit)"
    (( fail++ )) || true
  fi
}

# Negative direction: the supported shape must not be reported. The fixture also
# mentions the banned resource type inside a comment, so this case fails if the
# guard degrades into a bare substring grep.
run_case "role assignments and a prose mention exit 0" 0 \
  "${FIXTURES}/clean.tf.fixture"

# Positive direction: the anti-pattern must be caught.
run_case "secret_permissions access policy exits 1" 1 \
  "${FIXTURES}/access-policy.tf.fixture"

# Asserted separately from the secret_permissions case: a guard keyed on
# "Get"/"List" or on secret_permissions would pass the case above and still miss
# this one. The ban is on the resource type.
run_case "key_permissions access policy exits 1" 1 \
  "${FIXTURES}/key-permissions.tf.fixture"

# Indentation must not hide a declaration. The pre-commit `terraform_fmt` hook
# would normally normalize a top-level block back to column 0 across both scan
# roots, but the guard must not depend on that gate staying as wide as it is.
run_case "indented access policy exits 1" 1 \
  "${FIXTURES}/indented.tf.fixture"

# HCL does not require whitespace between the block header tokens, and
# `terraform fmt` treats the no-space form as a reformat rather than a parse
# error, so the guard must not require a separator after `resource`.
run_case "access policy with no inter-token whitespace exits 1" 1 \
  "${FIXTURES}/nospace.tf.fixture"

# A violation must still be found when mixed in with clean files.
run_case "violation alongside a clean file exits 1" 1 \
  "${FIXTURES}/clean.tf.fixture" "${FIXTURES}/access-policy.tf.fixture"

# Usage errors are exit 2, distinct from "found a violation" (exit 1), so CI can
# tell a broken invocation apart from a real finding.
run_case "missing file exits 2" 2 \
  "${FIXTURES}/does-not-exist.tf.fixture"

run_case "unknown flag exits 2" 2 --bogus-flag

# The JSON encoding is not parseable by this scanner, so it must be rejected
# rather than reported clean. Exit 2 (cannot check) rather than 0 (checked and
# clean) is the whole point.
run_case "tf.json is rejected rather than silently passed" 2 \
  "${FIXTURES}/json-encoding.tf.json"

# The regression half of issue #1621: the two modules that carried the inert
# access policy must stay free of one. This is a claim the guard can check
# directly, and it is the reason the guard exists.
run_case "cleanup-function module declares no access policy" 0 \
  "${REPO_ROOT}/terraform/modules/compute/azure/cleanup-function/main.tf"

run_case "aks module declares no access policy" 0 \
  "${REPO_ROOT}/terraform/modules/compute/azure/aks/main.tf"

# The default scan roots (terraform/ + iac/) must hold no violation. This also
# pins the guard's false-positive surface: it runs over every real Terraform
# file in the tree, none of which may trip it.
run_case "default scan roots declare no access-policy resource" 0

# Every run_case above compares exit codes only, so a guard that exited 1 with a
# blank or wrong message would pass all of them while telling a developer
# nothing. Pin the report itself: it must name the file and the line, which is
# what makes a CI failure actionable.
run_report_case() {
  local label="$1"
  local expected="$2"
  shift 2

  local report_stderr
  report_stderr="$("$CHECK" "$@" 2>&1 >/dev/null || true)"

  if [[ "$report_stderr" =~ $expected ]]; then
    echo "PASS: $label"
    (( pass++ )) || true
  else
    echo "FAIL: $label"
    printf '  expected to match: %s\n' "$expected"
    printf '  stderr was:\n%s\n' "$report_stderr"
    (( fail++ )) || true
  fi
}

run_report_case "violation report names the file and line" \
  'access-policy\.tf\.fixture:9: resource "azurerm_key_vault_access_policy"' \
  "${FIXTURES}/access-policy.tf.fixture"

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
