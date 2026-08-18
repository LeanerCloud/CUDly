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

# Both wiring assertions below have to tell a step that RUNS `aws ecr
# delete-repository` apart from one that only mentions it. ci.yml's own comment
# describing this assertion names the command in prose, and an `echo` may quote
# it; neither deletes anything. A guard that fired on those would constrain what
# may be written ABOUT the command, which is the same "the string is present
# somewhere" mistake the selector itself exists to remove, one level up.
#
# So the awk programs match the shell code on a line rather than the raw line:
#
#   code_of()        drops a trailing comment. A `#` that starts a word ends the
#                    line in both YAML and shell, and in neither is the `#` of
#                    `a#b` a comment, so the word-start rule is the one rule
#                    both languages already use.
#   invokes_delete() additionally empties quoted string literals, so a command
#                    named inside `"..."` or `'...'` is prose, not an invocation.
#
# The selector match runs on code_of() alone, because it asserts the literal
# text of the `"$OWNED_REPO"` argument and emptying literals would erase it.
# Running it on the raw line instead would let a commented-out selector stage
# mask an unguarded delete in the same step, which fails open.
#
# `^#` and ` #` as two subs rather than one `(^|[[:space:]])#` alternation:
# anchors inside a group are not portable across awk implementations, and CI's
# awk is mawk rather than the awk this was written on. The single quote reaches
# awk through -v because it cannot be written inside the single-quoted program.
AWK_CODE_FUNCS='
  function code_of(line) {
    sub(/^#.*$/, "", line)
    sub(/[[:space:]]#.*$/, "", line)
    return line
  }
  function invokes_delete(line) {
    line = code_of(line)
    gsub(/"[^"]*"/, "", line)
    gsub(SQ "[^" SQ "]*" SQ, "", line)
    return line ~ /aws[[:space:]]+ecr[[:space:]]+delete-repository/
  }
'

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

  if awk -v SQ="'" -v step="$step" -v expected="$expected" "$AWK_CODE_FUNCS"'
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
      in_step && code_of($0) ~ /[|][[:space:]]*\.\/scripts\/select-ecr-repos-to-delete\.sh[[:space:]]+"[$]OWNED_REPO"/ { has_selector = 1 }
      in_step && invokes_delete($0) { has_delete = 1 }
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
# It also reports when it finds no delete step at all, because that is what a
# wrong directory or an unmatched glob looks like, and an empty sweep would
# otherwise read as a clean result for files it never opened. Both workflow
# extensions GitHub accepts are swept, so a new `.yaml` file cannot slip past.

# sweep_unwired DIR
#
# Prints one line per delete step in DIR that does not pipe through the
# selector, plus a line of its own when DIR holds no delete step at all. No
# output means the directory is clean. Kept separate from the pass/fail
# reporting so the sweep itself can be exercised over fixtures below.
sweep_unwired() {
  local dir="$1"
  local files=()

  shopt -s nullglob
  files=("${dir}"/*.yml "${dir}"/*.yaml)
  shopt -u nullglob

  if [[ ${#files[@]} -eq 0 ]]; then
    echo "no workflow files found under ${dir}"
    return
  fi

  awk -v SQ="'" "$AWK_CODE_FUNCS"'
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
    code_of($0) ~ /[|][[:space:]]*\.\/scripts\/select-ecr-repos-to-delete\.sh[[:space:]]+"[$][A-Za-z_][A-Za-z0-9_]*"/ { has_selector = 1 }
    invokes_delete($0) { has_delete = 1 }
    END { finish(); if (total == 0) print "no `aws ecr delete-repository` step found at all" }
  ' "${files[@]}"
}

# assert_sweep LABEL DIR EXPECTED
#
# EXPECTED empty asserts the sweep finds nothing; otherwise it asserts EXPECTED
# appears in the report, so a fixture pins which step was flagged rather than
# only that something was.
assert_sweep() {
  local label="$1"
  local dir="$2"
  local expected="$3"
  local report

  report="$(sweep_unwired "$dir")"

  if [[ -z "$expected" && -z "$report" ]] || [[ -n "$expected" && "$report" == *"$expected"* ]]; then
    echo "PASS: $label"
    ((pass++)) || true
  else
    echo "FAIL: $label"
    if [[ -z "$expected" ]]; then
      echo "      expected no findings, got:"
    else
      echo "      expected a finding containing '${expected}', got:"
    fi
    if [[ -z "$report" ]]; then
      echo "        (no findings)"
    else
      while IFS= read -r line; do
        echo "        ${line}"
      done <<<"$report"
    fi
    ((fail++)) || true
  fi
}

assert_sweep "every 'aws ecr delete-repository' step in .github/workflows pipes through the selector" \
  "$WORKFLOW_DIR" ""

# --- The sweep itself, in both directions, over fixtures ---------------------
#
# The sweep is the only assertion that covers delete sites nobody has named, so
# a sweep that quietly stops recognizing them fails open. These fixtures pin
# both directions of that recognition, including the prose case: an earlier
# revision keyed on the string anywhere in a step and failed on ci.yml's own
# comment describing this assertion, which is a guard policing what may be
# written rather than what is run.
FIXTURE_DIR="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_DIR"' EXIT
mkdir -p "${FIXTURE_DIR}/prose" "${FIXTURE_DIR}/wired" "${FIXTURE_DIR}/unwired"

# Prose only, in the two forms that occur: a comment describing the command and
# a quoted string naming it. Neither deletes anything, so this directory holds
# no delete site and the sweep must say so rather than flag a step.
cat >"${FIXTURE_DIR}/prose/mentions.yml" <<'EOF'
      - name: Describes the command without running it
        run: |
          # asserts every `aws ecr delete-repository` step is wired
          echo "would run aws ecr delete-repository if it were wired"
          echo 'aws ecr delete-repository is named here too'
EOF

# The same prose alongside a real, wired delete: the prose must not mask the
# step, and the wired step must not be flagged.
cat >"${FIXTURE_DIR}/wired/deletes.yml" <<'EOF'
      - name: Describes the command without running it
        run: |
          # asserts every `aws ecr delete-repository` step is wired
          echo "would run aws ecr delete-repository if it were wired"

      - name: Force-delete the ECR repo this state owns
        run: |
          aws ecr describe-repositories --query 'repositories[].repositoryName' --output text \
            | ./scripts/select-ecr-repos-to-delete.sh "$OWNED_REPO" \
            | while IFS= read -r REPO; do
                aws ecr delete-repository --repository-name "$REPO" --force
              done
EOF

# The #1592/#1820 shape: a prefix filter, no selector. A commented-out selector
# stage in the same step must not satisfy the wiring, which is why the selector
# match runs on the comment-stripped line.
cat >"${FIXTURE_DIR}/unwired/deletes.yml" <<'EOF'
      - name: Force-delete all staging ECR repos
        run: |
          for REPO in $(aws ecr describe-repositories \
            --query "repositories[?starts_with(repositoryName,'cudly-staging')].repositoryName" \
            --output text); do
            # | ./scripts/select-ecr-repos-to-delete.sh "$OWNED_REPO"
            aws ecr delete-repository --repository-name "$REPO" --force
          done
EOF

assert_sweep "a step that only mentions the command is not a delete site" \
  "${FIXTURE_DIR}/prose" 'no `aws ecr delete-repository` step found at all'

assert_sweep "a wired delete step alongside prose mentions is not flagged" \
  "${FIXTURE_DIR}/wired" ""

assert_sweep "an unwired delete step is flagged, past a commented-out selector stage" \
  "${FIXTURE_DIR}/unwired" 'step "Force-delete all staging ECR repos"'

assert_sweep "a directory holding no workflow file is reported, not passed" \
  "${FIXTURE_DIR}/empty-does-not-exist" 'no workflow files found under'

echo
echo "passed: ${pass}, failed: ${fail}"
[[ "$fail" -eq 0 ]]
