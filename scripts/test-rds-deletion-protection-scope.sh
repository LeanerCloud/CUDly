#!/usr/bin/env bash
# test-rds-deletion-protection-scope.sh
#
# Asserts that the destroy workflows strip RDS deletion protection from the
# instance each Terraform state owns and from nothing else.
#
# Both directions matter, and the negative direction alone is worthless here: a
# selector that matches nothing passes every "no longer over-matches" assertion
# while silently leaving the owned instance protected, so `terraform destroy`
# fails on it. So the owned instance is asserted to be selected out of a full,
# hostile account listing BEFORE any absence is asserted.
#
# Deletion protection is the last line of defence on a database. Stripping it
# from an instance the state does not own does not delete anything by itself --
# it leaves that database exposed to the next destroy that does match it, which
# is why #1821 is a security issue and not a tidiness one.
#
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
WORKFLOW_DIR="${REPO_ROOT}/.github/workflows"
SELECT="${SCRIPT_DIR}/select-owned-name.sh"
UNPROTECT_SCRIPT="${SCRIPT_DIR}/disable-owned-rds-deletion-protection.sh"

# code_of() / invokes() / pipes_to_selector(), shared with
# test-ecr-delete-selection.sh so the rule separating code that RUNS a command
# from prose that mentions it has one definition rather than two that drift.
# shellcheck source=scripts/lib/code-scan-awk.sh
. "${SCRIPT_DIR}/lib/code-scan-awk.sh"

# The command whose invocation makes a site dangerous here.
MODIFY_CMD_RE='aws[[:space:]]+rds[[:space:]]+modify-db-instance'

# The instance terraform/environments/aws creates:
# aws_db_instance.main.identifier = "${local.stack_name}-postgres", and
# local.stack_name = "${project_name}-${environment}-${random_id.suffix.hex}"
# (terraform/modules/database/aws/main.tf:121, environments/aws/main.tf:55).
# The random suffix is why no literal list can be hardcoded and no prefix
# describes the instance uniquely.
OWNED="cudly-dev-1a2b3c4d-postgres"

# A plausible `aws rds describe-db-instances` listing in the target account.
# Every entry other than $OWNED is an instance this workflow does not own.
ACCOUNT_LISTING=$(
  cat <<'EOF'
cudly-dev
cudly-dev-postgres
cudly-dev-1a2b3c4d-postgres
cudly-dev-1a2b3c4d-postgres-replica
cudly-dev-1a2b3c4d-replica
cudly-dev-prod-mirror
cudly-dev-dba-scratch
backup-cudly-dev-postgres
cudly-staging-9f8e7d6c-postgres
cudly-prod-0badc0de-postgres
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

# --- Positive direction FIRST: the owned instance is still selected ----------
#
# Everything below this point asserts that some identifier is NOT selected, and
# every one of those assertions is satisfied by a selector that selects nothing
# at all. These two run first and are counted, so the negative table can never
# be the only thing holding.

selected="$("$SELECT" "$OWNED" <<<"$ACCOUNT_LISTING" 2>/dev/null)"

# Counted in the shell rather than with `grep -c`, which prints 0 and exits 1 on
# an empty selection: the exact case this assertion exists to catch, arriving as
# a non-zero exit that a `|| true` would then have to launder.
selected_count=0
while IFS= read -r line; do
  if [[ -n "$line" ]]; then
    ((selected_count++)) || true
  fi
done <<<"$selected"

if [[ "$selected_count" -eq 1 ]]; then
  echo "PASS: exactly one instance is selected out of the hostile account listing"
  ((pass++)) || true
else
  echo "FAIL: expected exactly 1 selected instance out of the account listing, got ${selected_count}"
  echo "      a selection of 0 leaves the owned instance protected and the destroy fails;"
  echo "      a selection of >1 strips protection from a database this state does not own"
  ((fail++)) || true
fi

run_case "owned instance is selected out of the full account listing" \
  0 "$OWNED" "$ACCOUNT_LISTING" "$OWNED"

run_case "selection is not tied to one suffix (different random_id)" \
  0 "cudly-dev-deadbeef-postgres" \
  "$(printf 'cudly-dev\ncudly-dev-deadbeef-postgres\ncudly-dev-deadbeef-postgres-replica\n')" \
  "cudly-dev-deadbeef-postgres"

run_case "owned instance already destroyed selects nothing, exit 0" \
  0 "" \
  "$(printf 'cudly-staging-9f8e7d6c-postgres\ncudly-prod-0badc0de-postgres\n')" \
  "$OWNED"

# --- Negative direction: every prefix near-miss is refused -------------------
#
# Each on its own line so a failure names the database that would have been
# stripped of its protection. The trailing comment is the filter that selects
# it: `starts_with cudly-dev` is what shipped at all three sites, and
# `cudly-dev-1a2b3c4d*` is the tightest prefix that still describes the real
# instance.
while IFS='|' read -r instance caught_by; do
  [[ -n "$instance" ]] || continue
  run_case "refused: ${instance} (selected by ${caught_by})" \
    0 "" "$instance" "$OWNED"
done <<'EOF'
cudly-dev|starts_with cudly-dev
cudly-dev-postgres|starts_with cudly-dev
cudly-dev-prod-mirror|starts_with cudly-dev
cudly-dev-1a2b3c4d-replica|starts_with cudly-dev
cudly-dev-1a2b3c4d-postgres-replica|starts_with cudly-dev, prefix of the real instance
cudly-dev-dba-scratch|starts_with cudly-dev, an operator-named instance
backup-cudly-dev-postgres|contains cudly-dev
cudly-staging-9f8e7d6c-postgres|no filter, regression guard
cudly-prod-0badc0de-postgres|no filter, regression guard
EOF

# --- The staging pair: each state owns one instance, and only its own --------
#
# cleanup-staging.yml destroys two AWS states from the same
# terraform/environments/aws directory (github-staging and
# github-fargate-staging), and each creates its own
# `cudly-staging-<random_id.suffix.hex>-postgres` instance. The `cudly-staging`
# prefix both jobs selected by therefore matched the sibling job's database as
# well, so either job unprotected a database the other's state still owned.
STAGING_LAMBDA_OWNED="cudly-staging-9f8e7d6c-postgres"
STAGING_FARGATE_OWNED="cudly-staging-5e4d3c2b-postgres"

STAGING_LISTING=$(
  cat <<'EOF'
cudly-staging
cudly-staging-9f8e7d6c-postgres
cudly-staging-5e4d3c2b-postgres
cudly-staging-9f8e7d6c-postgres-replica
cudly-staging-prod-mirror
backup-cudly-staging-postgres
cudly-prod-0badc0de-postgres
EOF
)

run_case "staging lambda state selects its own instance out of the staging listing" \
  0 "$STAGING_LAMBDA_OWNED" "$STAGING_LISTING" "$STAGING_LAMBDA_OWNED"

run_case "staging fargate state selects its own instance out of the staging listing" \
  0 "$STAGING_FARGATE_OWNED" "$STAGING_LISTING" "$STAGING_FARGATE_OWNED"

run_case "staging lambda state does not unprotect the fargate state's database" \
  0 "" "$STAGING_FARGATE_OWNED" "$STAGING_LAMBDA_OWNED"

run_case "staging fargate state does not unprotect the lambda state's database" \
  0 "" "$STAGING_LAMBDA_OWNED" "$STAGING_FARGATE_OWNED"

# A failed `terraform output` hands the selector an empty string. That must be a
# loud failure and not a silent empty selection, which looks identical to "the
# instance is already gone" and lets the destroy report success.
run_case "empty owned identifier exits 2" 2 "" "$ACCOUNT_LISTING" ""

# --- Terraform: the state actually publishes the identifier ------------------
#
# The script resolves the owned instance from `terraform output`, so the whole
# guard rests on that output existing and being the instance's real identifier.
# Delete the output and every case above stays green while the destroy fails at
# runtime on a state that cannot name what it owns.

# assert_file_matches LABEL FILE AWK_CONDITION
assert_file_matches() {
  local label="$1"
  local file="$2"
  local condition="$3"

  if [[ ! -f "$file" ]]; then
    echo "FAIL: ${label} -- file not found at ${file}"
    ((fail++)) || true
    return
  fi

  if awk -v SQ="'" "$AWK_CODE_FUNCS"'
      '"$condition"'
      END { exit !(hits == 1) }
    ' "$file"; then
    echo "PASS: $label"
    ((pass++)) || true
  else
    echo "FAIL: $label"
    echo "      in $(basename "$file") -- expected exactly one match"
    ((fail++)) || true
  fi
}

# Matched across the whole `output` block rather than on one line: the value is
# on the line after the block header, so the two are correlated by remembering
# which block is open. `[{]` and `[}]` rather than bare braces, which start an
# interval expression in ERE -- CI's awk is mawk rather than the awk this was
# written on, the same reason the shared helpers avoid `\|` and `\$`.
assert_file_matches "the aws environment publishes database_instance_identifier from the database module" \
  "${REPO_ROOT}/terraform/environments/aws/outputs.tf" \
  'code_of($0) ~ /^output[[:space:]]+"database_instance_identifier"[[:space:]]*[{]/ { in_block = 1; next }
   in_block && code_of($0) ~ /^[}]/ { in_block = 0 }
   in_block && code_of($0) ~ /value[[:space:]]*=[[:space:]]*module\.database\.instance_identifier[[:space:]]*$/ { hits++ }'

assert_file_matches "the aws database module publishes the real aws_db_instance identifier" \
  "${REPO_ROOT}/terraform/modules/database/aws/outputs.tf" \
  'code_of($0) ~ /^output[[:space:]]+"instance_identifier"[[:space:]]*[{]/ { in_block = 1; next }
   in_block && code_of($0) ~ /^[}]/ { in_block = 0 }
   in_block && code_of($0) ~ /value[[:space:]]*=[[:space:]]*aws_db_instance\.main\.identifier[[:space:]]*$/ { hits++ }'

# --- Wiring: the destroy steps route through the shared script ---------------
#
# Every case above exercises the selector standalone. Revert a step to a
# `starts_with` loop and all of them stay green while that step strips
# protection from whatever the prefix matches. The recurrence mode that produced
# #1592, then #1820, then #1821 was exactly that: the guard landed on one
# resource or one workflow and not its sibling.

# assert_step_wiring WORKFLOW_FILE STEP_NAME EXPECTED_STEPS
#
# Asserts WORKFLOW_FILE contains exactly EXPECTED_STEPS steps named STEP_NAME
# and that EVERY one runs the shared script against terraform/environments/aws.
# The count matters where a file holds more than one such step
# (cleanup-staging.yml destroys two AWS states): without it, a run where one
# step keeps the call and the other drops it satisfies a per-file flag.
#
# The state directory is pinned rather than accepted as any argument because it
# decides which instance the call may unprotect. The regex ends at end-of-line,
# so a `|| true` appended to the call fails this too.
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
      in_step && code_of($0) ~ /[[:space:]]\.\/scripts\/disable-owned-rds-deletion-protection\.sh[[:space:]]+terraform\/environments\/aws[[:space:]]*$/ { has_call = 1 }
      END { finish(); exit !(steps == expected && unwired == 0) }
    ' "$workflow"; then
    echo "PASS: all ${expected} '${step}' step(s) in $(basename "$workflow") call the shared script"
    ((pass++)) || true
  else
    echo "FAIL: $(basename "$workflow") does not have exactly ${expected} step(s) named"
    echo "      '${step}' that each run"
    echo "      './scripts/disable-owned-rds-deletion-protection.sh terraform/environments/aws'"
    echo "      (call removed, state directory changed, failure swallowed with a trailing"
    echo "      '|| true', step renamed, or a step added/deleted). The cases above only"
    echo "      exercise the selector standalone, so they stay green while the #1821"
    echo "      prefix over-match returns"
    ((fail++)) || true
  fi
}

RDS_STEP="Disable RDS deletion protection on the instance this state owns"
assert_step_wiring "${WORKFLOW_DIR}/destroy-fargate-dev.yml" "$RDS_STEP" 1
assert_step_wiring "${WORKFLOW_DIR}/cleanup-staging.yml" "$RDS_STEP" 2

# assert_script_wiring SCRIPT
#
# The other half: the script those steps call must still unprotect only what the
# exact-match selector yields. Five counts, each exactly one, so a second
# unguarded modify added beside the guarded one is caught, and so is a guard
# removed entirely:
#
#   owned        the identifier is read from `terraform output`, not hardcoded
#                and not derived from a prefix
#   piped        that identifier reaches the selector
#   fed          the modify loop reads the selector's output, so the selector
#                cannot be reduced to a no-op stage beside a modify driven by
#                some other listing
#   modifies     there is exactly one `aws rds modify-db-instance`
#   by_loop_var  the instance it modifies is the one the loop read, not some
#                other identifier that happened to be in scope
#
# `modifies` is also what keeps the rest from passing vacuously: a script that
# modifies nothing at all satisfies every "is guarded" reading of them.
assert_script_wiring() {
  local script="$1"

  if [[ ! -f "$script" ]]; then
    echo "FAIL: shared script not found at ${script}"
    ((fail++)) || true
    return
  fi

  if awk -v SQ="'" -v cmdre="$MODIFY_CMD_RE" "$AWK_CODE_FUNCS"'
      code_of($0) ~ /OWNED_INSTANCE=.*jq[[:space:]]+-er[[:space:]]+.*\.database_instance_identifier\.value/ { owned++ }
      pipes_to_selector($0, "\"[$]OWNED_INSTANCE\"") { piped++ }
      code_of($0) ~ /[|][[:space:]]*while[[:space:]]+IFS=[[:space:]]*read[[:space:]]+-r[[:space:]]+INSTANCE_ID/ {
        if (pipes_to_selector(prev, "\"[$]OWNED_INSTANCE\"")) fed++
      }
      invokes($0, cmdre) { modifies++ }
      code_of($0) ~ /aws[[:space:]]+rds[[:space:]]+modify-db-instance[[:space:]]+--db-instance-identifier[[:space:]]+"[$]INSTANCE_ID"/ { by_loop_var++ }
      { prev = $0 }
      END { exit !(owned == 1 && piped == 1 && fed == 1 && modifies == 1 && by_loop_var == 1) }
    ' "$script"; then
    echo "PASS: $(basename "$script") unprotects only what the exact-match selector yields"
    ((pass++)) || true
  else
    echo "FAIL: $(basename "$script") no longer reads the owned identifier from 'terraform"
    echo "      output', pipes it to scripts/select-owned-name.sh, and modifies exactly the"
    echo "      instances that pipeline yields -- expected one of each. This is where the"
    echo "      body lives, so a prefix filter reintroduced here is the #1821 over-match,"
    echo "      whatever the call sites look like"
    ((fail++)) || true
  fi
}

assert_script_wiring "$UNPROTECT_SCRIPT"

# assert_nothing_swallowed SCRIPT
#
# The second half of #1821: the sites carried `2>/dev/null` on the listing and
# `|| true` on the modify, so a failed listing (indistinguishable from an
# account with no instances) and a failed modify both reported success, and
# `terraform destroy` then failed downstream on an instance that was still
# protected. Asserted on code_of() so the header, which quotes both to explain
# why they are gone, is not itself a violation.
assert_nothing_swallowed() {
  local script="$1"

  if awk -v SQ="'" "$AWK_CODE_FUNCS"'
      code_of($0) ~ /2>[[:space:]]*\/dev\/null/ { print "  swallowed stderr: " FNR; bad++ }
      code_of($0) ~ /[|][|][[:space:]]*true/ { print "  swallowed exit status: " FNR; bad++ }
      END { exit !(bad == 0) }
    ' "$script"; then
    echo "PASS: $(basename "$script") swallows neither a failed listing nor a failed modify"
    ((pass++)) || true
  else
    echo "FAIL: $(basename "$script") suppresses an error on the line(s) above. A partial or"
    echo "      failed strip must fail loudly (#1821), not report success and let"
    echo "      'terraform destroy' hit a confusing downstream error"
    ((fail++)) || true
  fi
}

assert_nothing_swallowed "$UNPROTECT_SCRIPT"

# The assertions above name the files and steps they know about, so a NEW modify
# site in a new step, workflow or script is invisible to them -- which is how
# #1820 outlived #1592 and #1821 outlived both. This sweep is keyed on the
# dangerous call instead of on a name: everything anywhere in the swept set that
# runs `aws rds modify-db-instance` must pipe through the selector, whatever it
# is called.
#
# It also reports when it finds no modify site at all, because that is what a
# wrong directory or an unmatched glob looks like, and an empty sweep would
# otherwise read as a clean result for files it never opened. Both workflow
# extensions GitHub accepts are swept, so a new `.yaml` file cannot slip past.
# scripts/ is named file by file rather than globbed because this suite itself
# lives there and quotes both the command and the selector, as data.

# sweep_unwired DIR [FILE...]
#
# Prints one line per modify site in DIR (plus each named FILE) that does not
# pipe through the selector, plus a line of its own when the swept set holds no
# modify site at all. No output means the swept set is clean.
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
  awk -v SQ="'" -v cmdre="$MODIFY_CMD_RE" "$AWK_CODE_FUNCS"'
    function finish() {
      if (has_modify && !has_selector) {
        if (step_name == "") printf "%s: whole file\n", site_file
        else printf "%s: step \"%s\"\n", site_file, step_name
      }
      if (has_modify) total++
      has_modify = 0; has_selector = 0; step_name = ""
    }
    FNR == 1 { if (NR > 1) finish(); site_file = FILENAME }
    /^[[:space:]]*-[[:space:]]+name:/ {
      finish()
      step_name = $0
      sub(/^[[:space:]]*-[[:space:]]+name:[[:space:]]*/, "", step_name)
    }
    pipes_to_selector($0, "\"[$][A-Za-z_][A-Za-z0-9_]*\"") { has_selector = 1 }
    invokes($0, cmdre) { has_modify = 1 }
    END { finish(); if (total == 0) print "no `aws rds modify-db-instance` step found at all" }
  ' "${files[@]}"
}

# assert_sweep LABEL DIR EXPECTED [FILE...]
#
# EXPECTED empty asserts the sweep finds nothing; otherwise it asserts EXPECTED
# appears in the report, so a fixture pins WHICH site was flagged rather than
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

assert_sweep "every 'aws rds modify-db-instance' site in .github/workflows and scripts/ pipes through the selector" \
  "$WORKFLOW_DIR" "" "$UNPROTECT_SCRIPT" "$SELECT"

# --- The sweep itself, in both directions, over fixtures ---------------------
#
# The sweep is the only assertion covering modify sites nobody has named, so a
# sweep that quietly stops recognizing them fails open. These fixtures pin both
# directions of that recognition, including the prose case: a guard that fired
# on ci.yml's comment describing this assertion would police what may be written
# rather than what is run.
FIXTURE_DIR="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_DIR"' EXIT
mkdir -p "${FIXTURE_DIR}/prose" "${FIXTURE_DIR}/wired" "${FIXTURE_DIR}/unwired" "${FIXTURE_DIR}/scripts" "${FIXTURE_DIR}/misattrib"

cat >"${FIXTURE_DIR}/prose/mentions.yml" <<'EOF'
      - name: Describes the command without running it
        run: |
          # asserts every `aws rds modify-db-instance` step is wired
          echo "would run aws rds modify-db-instance if it were wired"
          echo 'aws rds modify-db-instance is named here too'
EOF

cat >"${FIXTURE_DIR}/wired/modifies.yml" <<'EOF'
      - name: Describes the command without running it
        run: |
          # asserts every `aws rds modify-db-instance` step is wired
          echo "would run aws rds modify-db-instance if it were wired"

      - name: Disable RDS deletion protection on the instance this state owns
        run: |
          aws rds describe-db-instances --query 'DBInstances[].DBInstanceIdentifier' --output text \
            | ./scripts/select-owned-name.sh "$OWNED_INSTANCE" \
            | while IFS= read -r INSTANCE_ID; do
                aws rds modify-db-instance --db-instance-identifier "$INSTANCE_ID" \
                  --no-deletion-protection --apply-immediately
              done
EOF

# The #1821 shape: a prefix filter, no selector. A commented-out selector stage
# in the same step must not satisfy the wiring, which is why the selector match
# runs on the comment-stripped line.
cat >"${FIXTURE_DIR}/unwired/modifies.yml" <<'EOF'
      - name: Disable RDS deletion protection
        run: |
          for INSTANCE_ID in $(aws rds describe-db-instances \
            --query "DBInstances[?starts_with(DBInstanceIdentifier,'cudly-staging')].DBInstanceIdentifier" \
            --output text 2>/dev/null); do
            # | ./scripts/select-owned-name.sh "$OWNED_INSTANCE"
            aws rds modify-db-instance --db-instance-identifier "$INSTANCE_ID" \
              --no-deletion-protection --apply-immediately 2>/dev/null || true
          done
EOF

assert_sweep "a step that only mentions the command is not a modify site" \
  "${FIXTURE_DIR}/prose" 'no `aws rds modify-db-instance` step found at all'

assert_sweep "a wired modify step alongside prose mentions is not flagged" \
  "${FIXTURE_DIR}/wired" ""

assert_sweep "an unwired modify step is flagged, past a commented-out selector stage" \
  "${FIXTURE_DIR}/unwired" 'step "Disable RDS deletion protection"'

assert_sweep "a directory holding no workflow file is reported, not passed" \
  "${FIXTURE_DIR}/empty-does-not-exist" 'no workflow files found under'

# The body lives in a shell script rather than a workflow step, so the sweep has
# to recognise a modify site in a file with no `- name:` lines at all, and has
# to accept the sibling-script call form that resolves the selector from
# BASH_SOURCE. Both directions, over a file swept by name the way the real one
# is. The `prose` dir supplies the workflow half and contributes no modify site.
cat >"${FIXTURE_DIR}/scripts/wired.sh" <<'EOF'
aws rds describe-db-instances --query 'DBInstances[].DBInstanceIdentifier' --output text \
  | tr '\t' '\n' \
  | "${SCRIPT_DIR}/select-owned-name.sh" "$OWNED_INSTANCE" \
  | while IFS= read -r INSTANCE_ID; do
      aws rds modify-db-instance --db-instance-identifier "$INSTANCE_ID" \
        --no-deletion-protection --apply-immediately
    done
EOF

cat >"${FIXTURE_DIR}/scripts/unwired.sh" <<'EOF'
aws rds describe-db-instances \
  --query "DBInstances[?starts_with(DBInstanceIdentifier,'cudly-staging')].DBInstanceIdentifier" \
  --output text \
  | while IFS= read -r INSTANCE_ID; do
      aws rds modify-db-instance --db-instance-identifier "$INSTANCE_ID" \
        --no-deletion-protection --apply-immediately
    done
EOF

assert_sweep "a script calling the selector through \${SCRIPT_DIR} is not flagged" \
  "${FIXTURE_DIR}/prose" "" "${FIXTURE_DIR}/scripts/wired.sh"

assert_sweep "an unwired script is flagged as a whole-file modify site" \
  "${FIXTURE_DIR}/prose" 'unwired.sh: whole file' "${FIXTURE_DIR}/scripts/unwired.sh"

assert_sweep "a swept file that does not exist is reported, not passed" \
  "${FIXTURE_DIR}/prose" 'swept file not found' "${FIXTURE_DIR}/scripts/does-not-exist.sh"

# A site running to the end of its file is only flushed once the next file
# starts, so the report has to remember which file the site came from. Reported
# from FILENAME it named the innocent file swept next, and every fixture above
# sweeps one file at a time, so none of them can catch it.
cat >"${FIXTURE_DIR}/misattrib/a-unwired.yml" <<'EOF'
      - name: Disable RDS deletion protection
        run: |
          aws rds modify-db-instance --db-instance-identifier "$INSTANCE_ID" \
            --no-deletion-protection --apply-immediately
EOF

cat >"${FIXTURE_DIR}/misattrib/b-innocent.yml" <<'EOF'
      - name: Modifies nothing
        run: |
          echo "clean"
EOF

assert_sweep "a finding names the file it came from, not the file swept after it" \
  "${FIXTURE_DIR}/misattrib" 'a-unwired.yml: step "Disable RDS deletion protection"'

echo
echo "passed: ${pass}, failed: ${fail}"
[[ "$fail" -eq 0 ]]
