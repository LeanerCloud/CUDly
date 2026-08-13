#!/usr/bin/env bash
# test-select-ecr-repos-to-delete.sh
#
# Exercises select-ecr-repos-to-delete.sh in BOTH directions over a table of
# real and adversarial repository names. Both directions matter because the
# consumer force-deletes what this selector prints: a selector that matches
# nothing passes every "no longer over-matches" assertion while silently
# leaving the dev repository behind, and a selector that over-matches deletes
# images it never owned.
#
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SELECT="${SCRIPT_DIR}/select-ecr-repos-to-delete.sh"

# The repository terraform/environments/aws creates for the dev stack:
# local.stack_name = "${project_name}-${environment}-${random_id.suffix.hex}".
OWNED="cudly-dev-1a2b3c4d"

# A plausible listing of `aws ecr describe-repositories` in the target account.
# Every entry other than $OWNED is a repository this workflow does not own; the
# annotated ones are selected by the filters that were considered and rejected.
ACCOUNT_LISTING=$(
  cat <<'EOF'
cudly
cudly-dev
cudly-dev-1a2b3c4d
cudly-dev-1a2b3c4d-backup
cudly-dev-prod-mirror
backup-cudly-dev
prod-cudly-dev-archive
my-cudly-dev-clone
cudly-staging-9f8e7d6c
cudly-prod-0badc0de
EOF
)

pass=0
fail=0

# assert_case LABEL EXPECTED_EXIT EXPECTED_STDOUT ACTUAL_EXIT ACTUAL_STDOUT
assert_case() {
  local label="$1"
  local expected_exit="$2"
  local expected_out="$3"
  local actual_exit="$4"
  local actual_out="$5"

  if [[ "$actual_exit" -eq "$expected_exit" && "$actual_out" == "$expected_out" ]]; then
    echo "PASS: $label"
    ((pass++)) || true
  else
    echo "FAIL: $label"
    echo "      expected exit $expected_exit, got $actual_exit"
    echo "      expected stdout: '${expected_out}'"
    echo "      actual stdout:   '${actual_out}'"
    ((fail++)) || true
  fi
}

# run_case LABEL EXPECTED_EXIT EXPECTED_STDOUT STDIN [ARGS...]
run_case() {
  local label="$1"
  local expected_exit="$2"
  local expected_out="$3"
  local stdin_data="$4"
  shift 4

  local actual_out actual_exit=0
  actual_out="$("$SELECT" "$@" <<<"$stdin_data" 2>/dev/null)" || actual_exit=$?

  assert_case "$label" "$expected_exit" "$expected_out" "$actual_exit" "$actual_out"
}

# run_case_no_trailing_newline LABEL EXPECTED_EXIT EXPECTED_STDOUT STDIN [ARGS...]
#
# Same assertions, but stdin has no trailing newline. `<<<` always appends one,
# so run_case structurally cannot reach the final-unterminated-line path where
# `read` returns non-zero with the line already in the variable.
run_case_no_trailing_newline() {
  local label="$1"
  local expected_exit="$2"
  local expected_out="$3"
  local stdin_data="$4"
  shift 4

  local actual_out actual_exit=0
  actual_out="$(printf '%s' "$stdin_data" | "$SELECT" "$@" 2>/dev/null)" || actual_exit=$?

  assert_case "$label" "$expected_exit" "$expected_out" "$actual_exit" "$actual_out"
}

# --- Positive direction: the repository that SHOULD be destroyed is selected --
#
# Asserted against the full account listing, not against a listing containing
# only the owned name, so a selector that drops entries under pressure from the
# neighbouring names is caught here.
run_case "owned repo is selected out of the full account listing" \
  0 "$OWNED" "$ACCOUNT_LISTING" "$OWNED"

run_case "selection is not tied to one suffix (different random_id)" \
  0 "cudly-dev-deadbeef" \
  "$(printf 'cudly\ncudly-dev-deadbeef\ncudly-dev-deadbeef-backup\n')" \
  "cudly-dev-deadbeef"

run_case "owned repo already deleted selects nothing, exit 0" \
  0 "" \
  "$(printf 'cudly\ncudly-staging-9f8e7d6c\n')" \
  "$OWNED"

run_case "empty account listing selects nothing, exit 0" \
  0 "" "" "$OWNED"

# The owned repository arriving as the last line of an unterminated stream is
# still selected. A plain `while read` drops it and reports an empty selection,
# which reads as "already deleted" and leaves the repository behind for
# `terraform destroy` to trip over.
run_case_no_trailing_newline "owned repo on an unterminated final line is selected" \
  0 "$OWNED" \
  "$(printf 'cudly\n%s' "$OWNED")" \
  "$OWNED"

run_case_no_trailing_newline "owned repo as the only, unterminated line is selected" \
  0 "$OWNED" "$OWNED" "$OWNED"

# --- Negative direction: every non-owned name is excluded -------------------
#
# Each is asserted on its own so a failure names the repository that would have
# been force-deleted. The trailing comment on each is the filter that selects
# it: `contains` is what shipped, `starts_with`/`case cudly-dev*` is the
# allow-list shape cleanup-staging.yml uses, and `cudly-dev-1a2b3c4d*` is the
# tightest prefix that still describes the real repository.
while IFS='|' read -r repo caught_by; do
  [[ -n "$repo" ]] || continue
  run_case "excluded: ${repo} (selected by ${caught_by})" \
    0 "" "$repo" "$OWNED"
done <<'EOF'
cudly-dev|contains
cudly-dev-prod-mirror|contains, starts_with cudly-dev
cudly-dev-1a2b3c4d-backup|contains, starts_with cudly-dev, prefix of the real repo
backup-cudly-dev|contains
prod-cudly-dev-archive|contains
my-cudly-dev-clone|contains
cudly|prefix of every name here
cudly-staging-9f8e7d6c|no filter, regression guard
cudly-prod-0badc0de|no filter, regression guard
CUDLY-DEV-1A2B3C4D|a case-folding comparison
EOF

# A name that differs from the owned one only by surrounding whitespace is a
# different repository, and comparing it as equal would delete the wrong one.
run_case "excluded: owned name with a leading space" \
  0 "" " ${OWNED}" "$OWNED"

# The owned name is compared literally, not as a glob. An unquoted right-hand
# side in [[ ]] would make this select both repositories.
run_case "owned name is not expanded as a glob pattern" \
  0 "" \
  "$(printf 'cudly-dev-1a2b3c4d\ncudly-dev-prod-mirror\n')" \
  'cudly-dev-*'

# --- Usage errors are exit 2, distinct from "selected nothing" (exit 0) ------
#
# A failed `terraform output` hands this script an empty string. That must be a
# loud failure and not a silent empty selection, which would look identical to
# "the repository is already gone" and let the destroy report success.
run_case "empty owned name exits 2" 2 "" "$ACCOUNT_LISTING" ""
run_case "whitespace-only owned name exits 2" 2 "" "$ACCOUNT_LISTING" "  "
run_case "owned name containing a space exits 2" 2 "" "$ACCOUNT_LISTING" "cudly dev"
run_case "no arguments exits 2" 2 "" "$ACCOUNT_LISTING"
run_case "more than one argument exits 2" 2 "" "$ACCOUNT_LISTING" "$OWNED" "cudly-dev-deadbeef"

# --- Wiring: the consumer still routes ECR deletion through this selector ----
#
# Every case above exercises the script standalone. Delete the
# `| ./scripts/select-ecr-repos-to-delete.sh "$OWNED_REPO"` stage from
# destroy-fargate-dev.yml and all of them stay green while the workflow goes
# back to force-deleting whatever the replacement filter matches. The
# recurrence mode that produced #1592 was exactly that: the guard landed in
# cleanup-staging.yml and not in its sibling.
#
# The pattern matches the pipe stage, not the script name: the workflow also
# names the script in a comment, so a bare name match would stay green after
# the stage was removed.
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
CONSUMER="${REPO_ROOT}/.github/workflows/destroy-fargate-dev.yml"

if [[ ! -f "$CONSUMER" ]]; then
  echo "FAIL: consumer workflow not found at ${CONSUMER}"
  ((fail++)) || true
elif grep -qE '\|[[:space:]]*\./scripts/select-ecr-repos-to-delete\.sh' "$CONSUMER"; then
  echo "PASS: destroy-fargate-dev.yml pipes ECR deletion through the selector"
  ((pass++)) || true
else
  echo "FAIL: destroy-fargate-dev.yml no longer pipes ECR deletion through the selector"
  echo "      the cases above only exercise the script standalone, so removing the"
  echo "      pipe stage leaves them green while the #1592 over-match returns"
  ((fail++)) || true
fi

echo
echo "passed: ${pass}, failed: ${fail}"
[[ "$fail" -eq 0 ]]
