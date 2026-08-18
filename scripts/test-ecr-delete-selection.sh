#!/usr/bin/env bash
# test-ecr-delete-selection.sh
#
# Exercises select-owned-name.sh, as the ECR destroy path uses it, in BOTH
# directions over a table of
# real and adversarial repository names. Both directions matter because the
# consumer force-deletes what this selector prints: a selector that matches
# nothing passes every "no longer over-matches" assertion while silently
# leaving the dev repository behind, and a selector that over-matches deletes
# images it never owned.
#
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SELECT="${SCRIPT_DIR}/select-owned-name.sh"

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
# Every case above exercises the script standalone. Drop the selector stage from
# the code that deletes and all of them stay green while that code goes back to
# force-deleting whatever the replacement filter matches. The recurrence mode
# that produced #1592 and then #1820 was exactly that: the guard landed in one
# workflow and not in its sibling.
#
# The deletion body used to be inlined in each consumer step. It now lives once
# in scripts/force-delete-owned-ecr-repo.sh, which all three destroy steps call,
# so the wiring splits into two halves that are asserted separately:
#
#   assert_step_wiring    each named step still calls the shared script, against
#                         the state directory whose repository it may delete
#   assert_script_wiring  the shared script still derives the owned name from
#                         `terraform output`, still feeds it to the exact-match
#                         selector, and still deletes only what that pipeline
#                         yields
#
# Asserting only the first would pass a shared script that had quietly gone back
# to a prefix filter; asserting only the second would pass a workflow that had
# stopped calling it. The sweep further down is the backstop for delete sites
# neither names.
#
# `[|]` and `[$]` rather than `\|` and `\$`: escaping those is undefined in
# POSIX ERE, and CI's awk is mawk rather than the awk this was written on.
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
WORKFLOW_DIR="${REPO_ROOT}/.github/workflows"
CLEANUP_SCRIPT="${SCRIPT_DIR}/force-delete-owned-ecr-repo.sh"

# The assertions below have to tell code that RUNS `aws ecr delete-repository`
# apart from prose that only mentions it. ci.yml's comment describing this
# assertion names the command, and an `echo` may quote it; neither deletes
# anything. A guard that fired on those would constrain what may be written
# ABOUT the command, which is the same "the string is present somewhere" mistake
# the selector itself exists to remove, one level up.
#
# So the awk programs match the shell code on a line rather than the raw line,
# through code_of() / invokes() / pipes_to_selector() in
# scripts/lib/code-scan-awk.sh. That file holds the rule and the reasoning; it
# is shared with test-rds-deletion-protection-scope.sh, which guards the same
# shape on RDS and would otherwise carry a second copy of it to drift against.
# shellcheck source=scripts/lib/code-scan-awk.sh
. "${SCRIPT_DIR}/lib/code-scan-awk.sh"

# The command whose invocation makes a site dangerous here. Passed to invokes()
# rather than baked into it, because the RDS suite scans for a different one.
DELETE_CMD_RE='aws[[:space:]]+ecr[[:space:]]+delete-repository'

# assert_step_wiring WORKFLOW_FILE STEP_NAME EXPECTED_STEPS
#
# Asserts that WORKFLOW_FILE contains exactly EXPECTED_STEPS steps named
# STEP_NAME and that EVERY one of them runs the shared cleanup script against
# terraform/environments/aws. The count matters where a file holds more than one
# such step (cleanup-staging.yml destroys two AWS states): without it, a run
# where one step keeps the call and the other drops it satisfies per-file flags
# and passes.
#
# The state directory is pinned rather than accepted as any argument because it
# is what decides which repository the call may delete. A step that called the
# script against a different state would delete a different repository, and that
# is a change this assertion should make someone state out loud.
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
          if (!has_call) unwired++
        }
        in_step = 0; has_call = 0
      }
      $0 ~ ("^[[:space:]]*-[[:space:]]+name:[[:space:]]*" step "[[:space:]]*$") {
        finish(); in_step = 1; next
      }
      /^[[:space:]]*-[[:space:]]+name:/ { finish() }
      in_step && code_of($0) ~ /[[:space:]]\.\/scripts\/force-delete-owned-ecr-repo\.sh[[:space:]]+terraform\/environments\/aws[[:space:]]*$/ { has_call = 1 }
      END { finish(); exit !(steps == expected && unwired == 0) }
    ' "$workflow"; then
    echo "PASS: all ${expected} '${step}' step(s) in $(basename "$workflow") call the shared cleanup script"
    ((pass++)) || true
  else
    echo "FAIL: $(basename "$workflow") does not have exactly ${expected} step(s) named"
    echo "      '${step}' that each run"
    echo "      './scripts/force-delete-owned-ecr-repo.sh terraform/environments/aws'"
    echo "      (call removed, state directory changed, step renamed, or a step"
    echo "      added/deleted). The cases above only exercise the selector standalone,"
    echo "      so they stay green while the #1592/#1820 over-match returns"
    ((fail++)) || true
  fi
}

assert_step_wiring "${WORKFLOW_DIR}/destroy-fargate-dev.yml" "Force-delete ECR repo" 1
assert_step_wiring "${WORKFLOW_DIR}/cleanup-staging.yml" "Force-delete the ECR repo this state owns" 2

# assert_script_wiring SCRIPT
#
# The other half: the shared script the steps above call must still delete only
# what the exact-match selector yields. Five counts, each of which must be
# exactly one, so a second unguarded delete added beside the guarded one is
# caught and so is a guard that has been removed entirely:
#
#   owned       the owned name is read from `terraform output`, not hardcoded
#               and not derived from a prefix
#   piped       that name reaches the selector
#   fed         the delete loop reads the selector's output, so the selector
#               cannot be reduced to a no-op stage beside a delete driven by
#               some other listing
#   deletes     there is exactly one `aws ecr delete-repository`
#   by_loop_var the repository it deletes is the one the loop read, not some
#               other name that happened to be in scope
#
# `deletes` is also what keeps the rest from passing vacuously: a script that
# deletes nothing at all satisfies every "is guarded" reading of them.
assert_script_wiring() {
  local script="$1"

  if [[ ! -f "$script" ]]; then
    echo "FAIL: shared cleanup script not found at ${script}"
    ((fail++)) || true
    return
  fi

  if awk -v SQ="'" -v cmdre="$DELETE_CMD_RE" "$AWK_CODE_FUNCS"'
      code_of($0) ~ /OWNED_REPO=.*jq[[:space:]]+-er[[:space:]]+.*\.ecr_repository_name\.value/ { owned++ }
      pipes_to_selector($0, "\"[$]OWNED_REPO\"") { piped++ }
      code_of($0) ~ /[|][[:space:]]*while[[:space:]]+IFS=[[:space:]]*read[[:space:]]+-r[[:space:]]+REPO/ {
        if (pipes_to_selector(prev, "\"[$]OWNED_REPO\"")) fed++
      }
      invokes($0, cmdre) { deletes++ }
      code_of($0) ~ /aws[[:space:]]+ecr[[:space:]]+delete-repository[[:space:]]+--repository-name[[:space:]]+"[$]REPO"/ { by_loop_var++ }
      { prev = $0 }
      END { exit !(owned == 1 && piped == 1 && fed == 1 && deletes == 1 && by_loop_var == 1) }
    ' "$script"; then
    echo "PASS: $(basename "$script") deletes only what the exact-match selector yields"
    ((pass++)) || true
  else
    echo "FAIL: $(basename "$script") no longer reads the owned name from 'terraform"
    echo "      output', pipes it to scripts/select-owned-name.sh, and"
    echo "      force-deletes exactly the repositories that pipeline yields -- expected"
    echo "      one of each. This is where the deletion body lives now, so a prefix"
    echo "      filter reintroduced here is the #1592/#1820 over-match, whatever the"
    echo "      call sites look like"
    ((fail++)) || true
  fi
}

assert_script_wiring "$CLEANUP_SCRIPT"

# The assertions above name the files and steps they know about, so a NEW delete
# site in a new step, a new workflow or a new script is invisible to them --
# which is how #1820 outlived #1592. This sweep is keyed on the dangerous call
# instead of on a name: everything anywhere in the swept set that runs `aws ecr
# delete-repository` must pipe through the selector, whatever it is called. The
# argument only has to be a quoted variable here; the named assertions pin it to
# "$OWNED_REPO".
#
# The swept set is .github/workflows plus the two production scripts. Extracting
# the deletion body out of the workflow steps moved the only real delete call
# into scripts/, so a sweep that still looked only at .github/workflows would
# report a clean result for a directory that no longer contains the thing it is
# looking for. scripts/ is named file by file rather than globbed because this
# suite itself lives there and quotes both the command and the selector, in
# fixtures and in awk programs, as data.
#
# It also reports when it finds no delete site at all, because that is what a
# wrong directory or an unmatched glob looks like, and an empty sweep would
# otherwise read as a clean result for files it never opened. Both workflow
# extensions GitHub accepts are swept, so a new `.yaml` file cannot slip past.

# sweep_unwired DIR [FILE...]
#
# Prints one line per delete site in DIR (plus each named FILE) that does not
# pipe through the selector, plus a line of its own when the swept set holds no
# delete site at all. No output means the swept set is clean. Kept separate from
# the pass/fail reporting so the sweep itself can be exercised over fixtures
# below.
#
# Sites are delimited by workflow `- name:` lines. A shell script has none, so
# it is swept as a single site and reported as "whole file".
sweep_unwired() {
  local dir="$1"
  shift
  local files=()
  local extra

  shopt -s nullglob
  files=("${dir}"/*.yml "${dir}"/*.yaml)
  shopt -u nullglob

  if [[ ${#files[@]} -eq 0 ]]; then
    echo "no workflow files found under ${dir}"
    return
  fi

  for extra in "$@"; do
    if [[ ! -f "$extra" ]]; then
      echo "swept file not found: ${extra}"
      return
    fi
    files+=("$extra")
  done

  # The site is reported from site_file, not FILENAME: a site that ends at a
  # file boundary is flushed by the next file's first line, by which point
  # FILENAME has already advanced and the report would send the reader to an
  # innocent file. Pinned by the two-file fixture below.
  awk -v SQ="'" -v cmdre="$DELETE_CMD_RE" "$AWK_CODE_FUNCS"'
    function finish() {
      if (has_delete && !has_selector) {
        if (step_name == "") printf "%s: whole file\n", site_file
        else printf "%s: step \"%s\"\n", site_file, step_name
      }
      if (has_delete) total++
      has_delete = 0; has_selector = 0; step_name = ""
    }
    FNR == 1 { if (NR > 1) finish(); site_file = FILENAME }
    /^[[:space:]]*-[[:space:]]+name:/ {
      finish()
      step_name = $0
      sub(/^[[:space:]]*-[[:space:]]+name:[[:space:]]*/, "", step_name)
    }
    pipes_to_selector($0, "\"[$][A-Za-z_][A-Za-z0-9_]*\"") { has_selector = 1 }
    invokes($0, cmdre) { has_delete = 1 }
    END { finish(); if (total == 0) print "no `aws ecr delete-repository` step found at all" }
  ' "${files[@]}"
}

# assert_sweep LABEL DIR EXPECTED [FILE...]
#
# EXPECTED empty asserts the sweep finds nothing; otherwise it asserts EXPECTED
# appears in the report, so a fixture pins which site was flagged rather than
# only that something was.
assert_sweep() {
  local label="$1"
  local dir="$2"
  local expected="$3"
  shift 3
  local report

  report="$(sweep_unwired "$dir" "$@")"

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

assert_sweep "every 'aws ecr delete-repository' site in .github/workflows and scripts/ pipes through the selector" \
  "$WORKFLOW_DIR" "" "$CLEANUP_SCRIPT" "$SELECT"

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
            | ./scripts/select-owned-name.sh "$OWNED_REPO" \
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
            # | ./scripts/select-owned-name.sh "$OWNED_REPO"
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

# The deletion body now lives in a shell script rather than a workflow step, so
# the sweep has to recognise a delete site in a file with no `- name:` lines at
# all, and has to accept the sibling-script call form that resolves the selector
# from BASH_SOURCE instead of from the caller's working directory. Both
# directions, over a file swept by name the way the real one is. The `prose` dir
# supplies the workflow half of the swept set and contributes no delete site.
mkdir -p "${FIXTURE_DIR}/scripts"

cat >"${FIXTURE_DIR}/scripts/wired.sh" <<'EOF'
aws ecr describe-repositories --query 'repositories[].repositoryName' --output text \
  | tr '\t' '\n' \
  | "${SCRIPT_DIR}/select-owned-name.sh" "$OWNED_REPO" \
  | while IFS= read -r REPO; do
      aws ecr delete-repository --repository-name "$REPO" --force
    done
EOF

cat >"${FIXTURE_DIR}/scripts/unwired.sh" <<'EOF'
aws ecr describe-repositories \
  --query "repositories[?starts_with(repositoryName,'cudly-staging')].repositoryName" \
  --output text \
  | while IFS= read -r REPO; do
      aws ecr delete-repository --repository-name "$REPO" --force
    done
EOF

assert_sweep "a script calling the selector through \${SCRIPT_DIR} is not flagged" \
  "${FIXTURE_DIR}/prose" "" "${FIXTURE_DIR}/scripts/wired.sh"

assert_sweep "an unwired script is flagged as a whole-file delete site" \
  "${FIXTURE_DIR}/prose" 'unwired.sh: whole file' "${FIXTURE_DIR}/scripts/unwired.sh"

assert_sweep "a swept file that does not exist is reported, not passed" \
  "${FIXTURE_DIR}/prose" 'swept file not found' "${FIXTURE_DIR}/scripts/does-not-exist.sh"

# A site that runs to the end of its file is only flushed once the next file
# starts, so the report has to remember which file the site came from. Reported
# from FILENAME it named the innocent file that happened to be swept next, which
# points whoever reads the failure at the wrong place -- and every fixture above
# sweeps one file at a time, so none of them can catch it. Two files, the unwired
# one first.
mkdir -p "${FIXTURE_DIR}/misattrib"

cat >"${FIXTURE_DIR}/misattrib/a-unwired.yml" <<'EOF'
      - name: Force-delete all staging ECR repos
        run: |
          aws ecr delete-repository --repository-name "$REPO" --force
EOF

cat >"${FIXTURE_DIR}/misattrib/b-innocent.yml" <<'EOF'
      - name: Deletes nothing
        run: |
          echo "clean"
EOF

assert_sweep "a finding names the file it came from, not the file swept after it" \
  "${FIXTURE_DIR}/misattrib" 'a-unwired.yml: step "Force-delete all staging ECR repos"'

echo
echo "passed: ${pass}, failed: ${fail}"
[[ "$fail" -eq 0 ]]
