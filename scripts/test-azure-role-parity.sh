#!/usr/bin/env bash
# test-azure-role-parity.sh
#
# Exercises check-azure-role-parity.sh against testdata fixtures.
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="${SCRIPT_DIR}/check-azure-role-parity.sh"
FIXTURES="${SCRIPT_DIR}/testdata/role-parity"

pass=0
fail=0

run_case() {
  local label="$1"
  local expected_exit="$2"
  shift 2

  actual_exit=0
  "$CHECK" "$@" >/dev/null 2>&1 || actual_exit=$?

  if [[ "$actual_exit" -eq "$expected_exit" ]]; then
    echo "PASS: $label"
    (( pass++ )) || true
  else
    echo "FAIL: $label (expected exit $expected_exit, got $actual_exit)"
    (( fail++ )) || true
  fi
}

# Case 1: matching fixtures -> should exit 0
run_case "matching lists exit 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/matching-arm.json"

# Case 2: drifted ARM (missing purchase/action) -> should exit 1
run_case "drifted ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/drifted-arm.json"

# Case 3 (issue #1545): actions match, but the role is assignable at (and
# assigned at) the tenant-wide /providers/Microsoft.Capacity scope. This is
# the exact shape that shipped in arm/CUDly-CrossSubscription/template.json:
# the actions check passes, so only the scope check can catch it.
run_case "tenant-scope ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/tenant-scope-arm.json"

# Case 4 (issue #1545): the same tenant-wide escape, but written as an ARM
# expression that also mentions /subscriptions/. A check that merely required
# the scope to look subscription-anchored would admit this while rejecting the
# blunt literal in case 3, accepting the fail-OPEN form and blocking only the
# cosmetically-bad one.
run_case "obfuscated tenant-scope ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/obfuscated-tenant-scope-arm.json"

# Case 5: the same tenant scope in lowercase. Azure provider namespaces are
# case-insensitive, so /providers/microsoft.capacity is a fully functional
# tenant scope; a case-sensitive check would pass it while failing case 3.
run_case "lowercase tenant-scope ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/lowercase-tenant-scope-arm.json"

# Case 6: a scope pointing at a DIFFERENT subscription via a parameter. It
# contains /subscriptions/ and names no escape provider, so only an exact-match
# allowlist rejects it. This is the cross-subscription grant the template's own
# name invites.
run_case "other-subscription ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/other-subscription-arm.json"

# Case 7: a tenant-scoped assignment hidden inside a nested deployment. This is
# the idiomatic ARM way to assign at a scope other than the deployment's own,
# so it is what an author with a legitimate cross-scope need would reach for.
# A check that walked only the top-level resources array would not see it.
run_case "nested-deployment ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/nested-deployment-arm.json"

# Case 8: repointing $schema at the management-group template. Every role
# assignment here inherits the deployment scope, so this widens all of them to
# cover every child subscription without altering one scope string.
run_case "management-group schema ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/mgmt-group-schema-arm.json"

# Case 9: the default-false assertion must fail CLOSED when the TF module
# references include_capacity_provider_scope but its variables.tf is missing,
# rather than silently skipping the assertion.
TMP_TF_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_TF_DIR"' EXIT
cp "${FIXTURES}/matching-tf.tf.fixture" "${TMP_TF_DIR}/main.tf"
printf '\n# assignable_scopes uses include_capacity_provider_scope\n' >> "${TMP_TF_DIR}/main.tf"
run_case "TF flag without variables.tf exits 1" 1 \
  --tf-file  "${TMP_TF_DIR}/main.tf" \
  --arm-file "${FIXTURES}/matching-arm.json"

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
