#!/usr/bin/env bash
# test-scan-shipped-image.sh
#
# Exercises the verdict half of scan-shipped-image.sh in BOTH directions,
# against recorded govulncheck output, with no docker and no built image.
#
# The scan itself only has value if it can go red. A scanner that always
# passes reads as coverage while providing none, which is the failure this
# whole check exists to close (issue #1836), so the failing cases here are the
# important ones.
#
# The fixtures under testdata/scan-shipped-image/ are real, unmodified
# `govulncheck -mode=binary -format json` finding records:
#
#   stale-toolchain-binary.jsonl  the prebuilt golang-migrate v4.19.1 release
#                                 (go1.25.4) that the image shipped before
#                                 #1833, narrowed to six advisories so the
#                                 fixture stays reviewable: four with a
#                                 published fix, two without.
#   unfixable-only.jsonl          /app/cudly as built on main: one advisory,
#                                 GO-2026-5932, with no fixed version.
#   no-findings.jsonl             /usr/local/bin/migrate as built on main:
#                                 nothing at all.
#
# malformed.jsonl is the one hand-written fixture: it stands in for output that
# exists but cannot be parsed, which must not read as a clean binary.
#
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURES="${SCRIPT_DIR}/testdata/scan-shipped-image"

# shellcheck source=scripts/scan-shipped-image.sh
source "${SCRIPT_DIR}/scan-shipped-image.sh"

pass=0
fail=0

# run_case LABEL EXPECTED_EXIT FIXTURE [EXPECTED_SUBSTRING...]
#
# The substrings matter as much as the exit code: a classifier that returns the
# right verdict from the wrong reading (every advisory lumped as fixable, say)
# would still exit 1 on the failing fixture.
run_case() {
  local label="$1" expected_exit="$2" fixture="$3"
  shift 3

  local actual_exit=0 output
  output="$(classify_findings "$fixture" "$label" 2>&1)" || actual_exit=$?

  local ok=1
  if [[ "$actual_exit" -ne "$expected_exit" ]]; then
    echo "FAIL: $label (expected exit $expected_exit, got $actual_exit)"
    ok=0
  fi
  local want
  for want in "$@"; do
    if [[ "$output" != *"$want"* ]]; then
      echo "FAIL: $label (output does not contain: $want)"
      ok=0
    fi
  done

  if [[ "$ok" -eq 1 ]]; then
    echo "PASS: $label"
    pass=$((pass + 1))
  else
    echo "----- output -----"
    echo "$output"
    echo "------------------"
    fail=$((fail + 1))
  fi
}

# Positive direction: a binary built on a superseded toolchain and carrying
# stale dependencies must fail, and the stdlib advisories must be among the
# reasons. This is the shape that shipped in #1833 and again in #1835.
run_case "stale-toolchain binary fails, and names its fixable advisories" 1 \
  "${FIXTURES}/stale-toolchain-binary.jsonl" \
  "4 fixable advisory/advisories, 2 without a fix" \
  "FIXABLE  GO-2026-6218  in stdlib  fixed in v1.25.13" \
  "FIXABLE  GO-2026-4771  in github.com/jackc/pgx/v5  fixed in v5.9.0"

# The same fixture's unfixable advisories must be reported as tolerated rather
# than counted towards the failure, or the "fail only on what is fixable"
# policy silently becomes "fail on everything".
run_case "unfixable advisories in a failing binary are tolerated, not counted" 1 \
  "${FIXTURES}/stale-toolchain-binary.jsonl" \
  "no fix   GO-2022-0635  in github.com/aws/aws-sdk-go  (tolerated: no published fix)" \
  "no fix   GO-2026-4518"

# Negative direction: today's /app/cudly. GO-2026-5932 has no fixed version now
# or ever, so gating on it would make this check permanently red with no
# available action. It must still be printed.
run_case "an advisory with no published fix does not fail the scan" 0 \
  "${FIXTURES}/unfixable-only.jsonl" \
  "clean (1 advisory/advisories without a published fix)" \
  "no fix   GO-2026-5932  in golang.org/x/crypto"

run_case "a binary with no findings passes" 0 \
  "${FIXTURES}/no-findings.jsonl" \
  "clean (0 advisory/advisories without a published fix)"

# A missing file is exit 2 (could not check), distinct from exit 0 (checked,
# nothing gating). govulncheck failing to write its output must never read as a
# clean binary.
run_case "a missing govulncheck output is exit 2, not a clean verdict" 2 \
  "${FIXTURES}/does-not-exist.jsonl"

# Same reasoning for output that exists but cannot be read. errexit is
# suppressed inside a function whose status the caller is testing, so an
# unparseable stream must be rejected explicitly rather than falling through to
# the "no fixable advisories" branch with empty counts.
run_case "unparseable govulncheck output is exit 2, not a clean verdict" 2 \
  "${FIXTURES}/malformed.jsonl"

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
