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

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
