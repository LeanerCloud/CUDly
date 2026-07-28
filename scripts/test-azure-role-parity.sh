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
# cosmetically-bad one. The token denylist is what must reject it.
run_case "obfuscated tenant-scope ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/obfuscated-tenant-scope-arm.json"

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
