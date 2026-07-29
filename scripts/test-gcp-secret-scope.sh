#!/usr/bin/env bash
# test-gcp-secret-scope.sh
#
# Exercises check-gcp-secret-scope.sh against testdata fixtures, in BOTH
# directions: a clean input must pass and a violating input must fail. A guard
# that only ever returns "clean" is indistinguishable from a broken one, so the
# failing cases are the ones that give this check its value.
#
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="${SCRIPT_DIR}/check-gcp-secret-scope.sh"
FIXTURES="${SCRIPT_DIR}/testdata/gcp-secret-scope"

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

# Negative direction: the supported shapes must not be reported. This is what
# catches a guard so blunt it fires on every per-secret binding.
run_case "per-secret binding and non-secret project grant exit 0" 0 \
  "${FIXTURES}/clean.tf.fixture"

# Positive direction: the anti-pattern must be caught.
run_case "project-scope secretAccessor exits 1" 1 \
  "${FIXTURES}/project-scope.tf.fixture"

run_case "folder/org scope and _binding variant exit 1" 1 \
  "${FIXTURES}/org-scope.tf.fixture"

# A violation must still be found when mixed in with clean files.
run_case "violation alongside a clean file exits 1" 1 \
  "${FIXTURES}/clean.tf.fixture" "${FIXTURES}/project-scope.tf.fixture"

# Usage errors are exit 2, distinct from "found a violation" (exit 1), so CI can
# tell a broken invocation apart from a real finding.
run_case "missing file exits 2" 2 \
  "${FIXTURES}/does-not-exist.tf.fixture"

run_case "unknown flag exits 2" 2 --bogus-flag

# The real tree must be clean: this is the regression half of issue #1614.
run_case "repository terraform/ tree is clean" 0

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
