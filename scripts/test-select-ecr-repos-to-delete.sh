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

# --- The staging pair: each state owns one repository, and only its own ------
#
# cleanup-staging.yml destroys two AWS states from the same
# terraform/environments/aws directory (github-staging and
# github-fargate-staging), and each creates its own
# `cudly-staging-<random_id.suffix.hex>` repository. The `cudly-staging*`
# prefix both jobs used to select by therefore matched the sibling job's
# repository as well as the adversarial names below, so either job could
# force-delete a repository the other's state still owned (#1820).
#
# Asserted in both directions per state: the owned repository is still selected
# out of a full staging listing, and every other name in that listing -- the
# sibling state's repository included -- is not.
STAGING_LAMBDA_OWNED="cudly-staging-9f8e7d6c"
STAGING_FARGATE_OWNED="cudly-staging-5e4d3c2b"

STAGING_LISTING=$(
  cat <<'EOF'
cudly
cudly-staging
cudly-staging-9f8e7d6c
cudly-staging-5e4d3c2b
cudly-staging-9f8e7d6c-backup
cudly-staging-prod-mirror
backup-cudly-staging
cudly-prod-0badc0de
EOF
)

run_case "staging lambda state selects its own repo out of the staging listing" \
  0 "$STAGING_LAMBDA_OWNED" "$STAGING_LISTING" "$STAGING_LAMBDA_OWNED"

run_case "staging fargate state selects its own repo out of the staging listing" \
  0 "$STAGING_FARGATE_OWNED" "$STAGING_LISTING" "$STAGING_FARGATE_OWNED"

run_case "staging lambda state does not select the fargate state's repo" \
  0 "" "$STAGING_FARGATE_OWNED" "$STAGING_LAMBDA_OWNED"

run_case "staging fargate state does not select the lambda state's repo" \
  0 "" "$STAGING_LAMBDA_OWNED" "$STAGING_FARGATE_OWNED"

while IFS='|' read -r repo caught_by; do
  [[ -n "$repo" ]] || continue
  run_case "excluded from staging cleanup: ${repo} (selected by ${caught_by})" \
    0 "" "$repo" "$STAGING_LAMBDA_OWNED"
done <<'EOF'
cudly-staging|starts_with cudly-staging
cudly-staging-prod-mirror|starts_with cudly-staging
cudly-staging-9f8e7d6c-backup|starts_with cudly-staging, prefix of the real repo
cudly-staging-5e4d3c2b|starts_with cudly-staging, the sibling state's repo
backup-cudly-staging|contains
cudly|no filter, regression guard
cudly-prod-0badc0de|no filter, regression guard
EOF

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

# --- Wiring: the consumers still route ECR deletion through this selector ----
#
# Every case above exercises the script standalone. Delete the
# `| ./scripts/select-ecr-repos-to-delete.sh "$OWNED_REPO"` stage from a
# consumer and all of them stay green while that workflow goes back to
# force-deleting whatever the replacement filter matches. The recurrence mode
# that produced #1592 and then #1820 was exactly that: the guard landed in one
# workflow and not in its sibling.
#
# Scoped to the steps that delete: the selector invocation, its "$OWNED_REPO"
# argument and `aws ecr delete-repository` must all appear inside one and the
# same step. Asserting them anywhere in the file would let an unrelated line
# keep this green after a delete step lost its selector stage or its argument
# -- the same "the string is present somewhere" mistake the selector itself
# exists to remove. Step names are variables so the regexes and the messages
# cannot drift apart.
#
# `[|]` and `[$]` rather than `\|` and `\$`: escaping those is undefined in
# POSIX ERE, and CI's awk is mawk rather than the awk this was written on.
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
WORKFLOW_DIR="${REPO_ROOT}/.github/workflows"

# assert_step_wiring WORKFLOW_FILE STEP_NAME EXPECTED_STEPS
#
# Asserts that WORKFLOW_FILE contains exactly EXPECTED_STEPS steps named
# STEP_NAME and that EVERY one of them pipes through the selector alongside its
# `aws ecr delete-repository` call. The count matters where a file holds more
# than one such step (cleanup-staging.yml destroys two AWS states): without it,
# a run where one step keeps the selector and the other drops it satisfies
# per-file flags and passes.
assert_step_wiring() {
  local workflow="$1"
  local step="$2"
  local expected="$3"

  if [[ ! -f "$workflow" ]]; then
    echo "FAIL: consumer workflow not found at ${workflow}"
    ((fail++)) || true
    return
  fi

  if awk -v step="$step" -v expected="$expected" '
      function finish() {
        if (in_step) {
          steps++
          if (!(has_selector && has_delete)) unwired++
        }
        in_step = 0; has_selector = 0; has_delete = 0
      }
      $0 ~ ("^[[:space:]]*-[[:space:]]+name:[[:space:]]*" step "[[:space:]]*$") {
        finish(); in_step = 1; next
      }
      /^[[:space:]]*-[[:space:]]+name:/ { finish() }
      in_step && /[|][[:space:]]*\.\/scripts\/select-ecr-repos-to-delete\.sh[[:space:]]+"[$]OWNED_REPO"/ { has_selector = 1 }
      in_step && /aws ecr delete-repository/ { has_delete = 1 }
      END { finish(); exit !(steps == expected && unwired == 0) }
    ' "$workflow"; then
    echo "PASS: all ${expected} '${step}' step(s) in $(basename "$workflow") pipe ECR deletion through the selector"
    ((pass++)) || true
  else
    echo "FAIL: $(basename "$workflow") does not have exactly ${expected} step(s) named"
    echo "      '${step}' that each pipe ./scripts/select-ecr-repos-to-delete.sh"
    echo "      \"\$OWNED_REPO\" alongside their 'aws ecr delete-repository' call (stage"
    echo "      removed, argument dropped, step renamed, or a step added/deleted). The"
    echo "      cases above only exercise the script standalone, so they stay green"
    echo "      while the #1592/#1820 over-match returns"
    ((fail++)) || true
  fi
}

assert_step_wiring "${WORKFLOW_DIR}/destroy-fargate-dev.yml" "Force-delete ECR repo" 1
assert_step_wiring "${WORKFLOW_DIR}/cleanup-staging.yml" "Force-delete the ECR repo this state owns" 2

# The assertions above name the steps they know about, so a NEW delete site in
# a new step or a new workflow is invisible to them -- which is how #1820
# outlived #1592. This sweep is keyed on the dangerous call instead of on a
# name: every step anywhere in .github/workflows that runs `aws ecr
# delete-repository` must pipe through the selector, whatever it or its
# workflow is called. The argument only has to be a quoted variable here; the
# named assertions pin it to "$OWNED_REPO".
#
# It also fails when it finds no delete step at all, because that is what a
# wrong WORKFLOW_DIR or an unmatched glob looks like, and an empty sweep would
# otherwise report a clean result for files it never read. Both workflow
# extensions GitHub accepts are swept, so a new `.yaml` file cannot slip past.
shopt -s nullglob
WORKFLOW_FILES=("${WORKFLOW_DIR}"/*.yml "${WORKFLOW_DIR}"/*.yaml)
shopt -u nullglob

if [[ ${#WORKFLOW_FILES[@]} -eq 0 ]]; then
  echo "FAIL: no workflow files found under ${WORKFLOW_DIR}"
  ((fail++)) || true
  echo
  echo "passed: ${pass}, failed: ${fail}"
  exit 1
fi

unwired_report="$(
  awk '
    function finish() {
      if (has_delete && !has_selector) {
        printf "%s: step \"%s\"\n", FILENAME, step_name
      }
      if (has_delete) total++
      has_delete = 0; has_selector = 0; step_name = ""
    }
    FNR == 1 && NR > 1 { finish() }
    /^[[:space:]]*-[[:space:]]+name:/ {
      finish()
      step_name = $0
      sub(/^[[:space:]]*-[[:space:]]+name:[[:space:]]*/, "", step_name)
    }
    /[|][[:space:]]*\.\/scripts\/select-ecr-repos-to-delete\.sh[[:space:]]+"[$][A-Za-z_][A-Za-z0-9_]*"/ { has_selector = 1 }
    /aws ecr delete-repository/ { has_delete = 1 }
    END { finish(); if (total == 0) print "no `aws ecr delete-repository` step found at all" }
  ' "${WORKFLOW_FILES[@]}"
)"

if [[ -z "$unwired_report" ]]; then
  echo "PASS: every 'aws ecr delete-repository' step in .github/workflows pipes through the selector"
  ((pass++)) || true
else
  echo "FAIL: an 'aws ecr delete-repository' step does not pipe through"
  echo "      ./scripts/select-ecr-repos-to-delete.sh, so it deletes whatever its own"
  echo "      filter matches:"
  while IFS= read -r line; do
    echo "        ${line}"
  done <<<"$unwired_report"
  ((fail++)) || true
fi

echo
echo "passed: ${pass}, failed: ${fail}"
[[ "$fail" -eq 0 ]]
