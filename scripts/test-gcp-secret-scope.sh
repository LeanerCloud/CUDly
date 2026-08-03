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
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
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

run_case "project-scope _binding variant exits 1" 1 \
  "${FIXTURES}/project-binding.tf.fixture"

# Folder and organization scope are asserted separately. They used to share one
# fixture, which meant a guard that caught only one of them still passed.
run_case "folder-scope _member variant exits 1" 1 \
  "${FIXTURES}/folder-scope.tf.fixture"

run_case "organization-scope _binding variant exits 1" 1 \
  "${FIXTURES}/org-scope.tf.fixture"

# A violation must still be found when mixed in with clean files.
run_case "violation alongside a clean file exits 1" 1 \
  "${FIXTURES}/clean.tf.fixture" "${FIXTURES}/project-scope.tf.fixture"

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

# The regression half of issue #1614: the module that carried the over-broad
# grant must stay free of one. This is a claim the guard can actually check.
run_case "cleanup-function module has no scope-wide Secret Manager grant" 0 \
  "${REPO_ROOT}/terraform/modules/compute/gcp/cleanup-function/main.tf"

# The default scan roots (terraform/ + iac/) must hold no violation of the shape
# this guard detects.
#
# Deliberately NOT phrased as "the tree is clean". It is not:
# terraform/environments/gcp/ci-cd-permissions/service_account.tf grants
# project-scope roles/secretmanager.admin through `locals` + `for_each`, which
# this guard structurally cannot see (issue #1686). That grant is believed
# legitimate, but the distinction matters: this case asserts what the guard
# verifies, not a broader property it never inspects. An earlier version of this
# suite claimed the tree was clean, which was false as written.
run_case "default scan roots hold no directly expressed scope-wide grant" 0

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
