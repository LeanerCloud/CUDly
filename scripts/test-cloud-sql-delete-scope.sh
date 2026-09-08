#!/usr/bin/env bash
# test-cloud-sql-delete-scope.sh
#
# Asserts that the GCP staging destroy deletes the Cloud SQL instance each
# Terraform state owns, and nothing else, before removing it from state.
#
# Both directions matter, and the negative direction alone is worthless here: a
# selector that matches nothing passes every "no longer over-matches" assertion
# while silently leaving the owned instance behind, so `terraform destroy`
# fails on it (deletion_protection = true in github-staging.tfvars). A
# selector that over-matches deletes a Cloud SQL database this state never
# owned. So the owned instance is asserted to be selected out of a full,
# hostile listing BEFORE any absence is asserted (#1971).
#
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
WORKFLOW_DIR="${REPO_ROOT}/.github/workflows"

# code_of() / invokes() / pipes_to_selector(), shared with
# test-ecr-delete-selection.sh and test-rds-deletion-protection-scope.sh so the
# rule separating code that RUNS a command from prose that mentions it has one
# definition rather than one that drifts per platform.
# shellcheck source=scripts/lib/code-scan-awk.sh
. "${SCRIPT_DIR}/lib/code-scan-awk.sh"

SELECT="${SCRIPT_DIR}/select-owned-name.sh"
DELETE_SCRIPT="${SCRIPT_DIR}/delete-owned-cloud-sql-instance.sh"
DELETE_CMD_RE='gcloud[[:space:]]+sql[[:space:]]+instances[[:space:]]+delete'
OWNED="cudly-staging-postgres"   # ${project_name}-${environment}-postgres, modules/database/gcp/main.tf:36
STEP="Delete the Cloud SQL instance this state owns"

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

# --- Positive direction FIRST: the owned instance is still selected ----------
#
# Everything below this point asserts that some name is NOT selected, and
# every one of those assertions is satisfied by a selector that selects
# nothing at all. These run first and are counted, so the negative table can
# never be the only thing holding.
#
# The wrong candidate sorts FIRST, so `head -1` -- the pre-fix selection --
# would have picked it (the issue's scenario).
LISTING=$(
  cat <<'EOF'
cudly-staging-mirror
backup-cudly-staging
cudly-staging
cudly-staging-postgres
cudly-staging-postgres-replica
cudly-staging-prod-mirror
cudly-stagingx
cudly-dev-postgres
cudly-prod-postgres
CUDLY-STAGING-POSTGRES
EOF
)

selected="$("$SELECT" "$OWNED" <<<"$LISTING" 2>/dev/null)"

# Counted in the shell rather than with `grep -c`, which prints 0 and exits 1 on
# an empty selection: the exact case this assertion exists to catch, arriving
# as a non-zero exit that a `|| true` would then have to launder.
selected_count=0
while IFS= read -r line; do
  if [[ -n "$line" ]]; then
    ((selected_count++)) || true
  fi
done <<<"$selected"

if [[ "$selected_count" -eq 1 ]]; then
  echo "PASS: exactly one instance is selected out of the hostile project listing"
  ((pass++)) || true
else
  echo "FAIL: expected exactly 1 selected instance out of the project listing, got ${selected_count}"
  echo "      a selection of 0 leaves the owned instance behind and the destroy fails on"
  echo "      deletion_protection; a selection of >1 deletes a database this state does not own"
  ((fail++)) || true
fi

run_case "owned instance is selected out of the full project listing" \
  0 "$OWNED" "$LISTING" "$OWNED"

run_case "owned instance already gone selects nothing, exit 0" \
  0 "" \
  "$(printf 'cudly-dev-postgres\ncudly-prod-postgres\n')" \
  "$OWNED"

# The owned instance arriving as the last line of an unterminated stream is
# still selected. A plain `while read` drops it and reports an empty
# selection, which reads as "already gone" and leaves the protected instance
# behind for `terraform destroy` to trip over.
run_case_no_trailing_newline "owned instance on an unterminated final line is selected" \
  0 "$OWNED" \
  "$(printf 'cudly-staging-mirror\n%s' "$OWNED")" \
  "$OWNED"

# --- Negative direction: every near-miss is refused --------------------------
#
# Each on its own line so a failure names the database that would have been
# deleted. The trailing comment is the filter that selects it, measured on
# Cloud SDK 456.0.0 (see the script's header for the full probe).
while IFS='|' read -r instance caught_by; do
  [[ -n "$instance" ]] || continue
  run_case "refused: ${instance} (selected by ${caught_by})" \
    0 "" "$instance" "$OWNED"
done <<'EOF'
cudly-staging-mirror|name:cudly-staging (substring), sorts first so head -1 picked it
backup-cudly-staging|name:cudly-staging (substring)
cudly-staging|name:cudly-staging
cudly-staging-postgres-replica|name:cudly-staging, name:cudly-staging-postgres*, every prefix of the real name
cudly-staging-prod-mirror|name:cudly-staging
cudly-stagingx|name:cudly-staging
CUDLY-STAGING-POSTGRES|name=cudly-staging-postgres (gcloud = is case-folded, measured on SDK 456.0.0)
cudly-dev-postgres|no filter, regression guard
cudly-prod-postgres|no filter, regression guard
EOF

# A name that differs from the owned one only by surrounding whitespace is a
# different instance, and comparing it as equal would delete the wrong one.
run_case "excluded: owned name with a leading space" \
  0 "" " ${OWNED}" "$OWNED"

# The owned name is compared literally, not as a glob. An unquoted right-hand
# side in [[ ]] would make this select every instance below.
run_case "owned name is not expanded as a glob pattern" \
  0 "" \
  "$(printf 'cudly-staging-postgres\ncudly-staging-prod-mirror\n')" \
  'cudly-staging-*'

# A failed `terraform output` hands the selector an empty string. That must be
# a loud failure and not a silent empty selection, which looks identical to
# "the instance is already gone" and lets the destroy report success.
run_case "empty owned name exits 2" 2 "" "$LISTING" ""
run_case "whitespace-only owned name exits 2" 2 "" "$LISTING" "  "

# --- Terraform: the state actually publishes the instance name ---------------
#
# The script resolves the owned instance from `terraform output`, so the whole
# guard rests on that output existing and being the instance's real name.
# Delete the output and every case above stays green while the destroy fails
# at runtime on a state that cannot name what it owns.

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
assert_file_matches "the gcp environment publishes database_instance_name from the database module" \
  "${REPO_ROOT}/terraform/environments/gcp/outputs.tf" \
  'code_of($0) ~ /^output[[:space:]]+"database_instance_name"[[:space:]]*[{]/ { in_block = 1; next }
   in_block && code_of($0) ~ /^[}]/ { in_block = 0 }
   in_block && code_of($0) ~ /value[[:space:]]*=[[:space:]]*module\.database\.instance_name[[:space:]]*$/ { hits++ }'

assert_file_matches "the gcp database module publishes the real google_sql_database_instance name" \
  "${REPO_ROOT}/terraform/modules/database/gcp/outputs.tf" \
  'code_of($0) ~ /^output[[:space:]]+"instance_name"[[:space:]]*[{]/ { in_block = 1; next }
   in_block && code_of($0) ~ /^[}]/ { in_block = 0 }
   in_block && code_of($0) ~ /value[[:space:]]*=[[:space:]]*google_sql_database_instance\.main\.name[[:space:]]*$/ { hits++ }'

# --- Wiring: the destroy step routes through the shared script ---------------
#
# Every case above exercises the selector standalone. Revert the step to a
# `--filter="name:cudly-staging" | head -1` selection and all of them stay
# green while that step deletes whatever the filter matches. The recurrence
# mode that produced #1592, then #1820, then #1821 was exactly that: the guard
# landed on one resource or one platform and not its sibling.

# assert_step_wiring WORKFLOW_FILE STEP_NAME EXPECTED_STEPS
#
# The state directory and the project argument are pinned rather than accepted
# as any argument because they decide which instance the call may delete. The
# regex ends at end-of-line, so a `|| true` appended to the call fails this
# too.
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
      in_step && code_of($0) ~ /[[:space:]]\.\/scripts\/delete-owned-cloud-sql-instance\.sh[[:space:]]+terraform\/environments\/gcp[[:space:]]+"[$]PROJECT"[[:space:]]*$/ { has_call = 1 }
      END { finish(); exit !(steps == expected && unwired == 0) }
    ' "$workflow"; then
    echo "PASS: all ${expected} '${step}' step(s) in $(basename "$workflow") call the shared script"
    ((pass++)) || true
  else
    echo "FAIL: $(basename "$workflow") does not have exactly ${expected} step(s) named"
    echo "      '${step}' that each run"
    echo "      './scripts/delete-owned-cloud-sql-instance.sh terraform/environments/gcp \"\$PROJECT\"'"
    echo "      (call removed, state directory changed, project argument changed, a trailing"
    echo "      '|| true' appended, step renamed, or a step added/deleted)"
    ((fail++)) || true
  fi
}

assert_step_wiring "${WORKFLOW_DIR}/cleanup-staging.yml" "$STEP" 1

# assert_script_wiring SCRIPT
#
# The other half: the script that step calls must still delete only what the
# exact-match selector yields, must read that name from `terraform output`
# rather than a filter, and must remove state only after a successful delete.
# Counts, each with a fixed expectation, so a second unguarded delete added
# beside the guarded one is caught, and so is a guard removed entirely:
#
#   owned            the instance name is read from `terraform output`, not
#                    hardcoded and not derived from a filter
#   piped            that name reaches the selector
#   fed              the SELECTED= assignment reads the selector's output, so
#                    the selector cannot be reduced to a no-op stage beside a
#                    delete driven by some other listing
#   unfiltered/      the listing command carries no `--filter`; the filter is
#     filtered       not the guard, and a filter reintroduced here is the
#                    #1971 shape with a case-folded or deprecated operator
#   deletes          there is exactly one `gcloud sql instances delete`
#   by_selected_var  the instance it deletes is the one the selector yielded,
#                    not some other name that happened to be in scope
#   state_rm         the module-qualified `state rm` line appears exactly
#                    once in code, strictly AFTER the delete line
#   root_addr        no root-level `state rm google_sql...` address remains
#                    (the #1971 shape: an address that never matched anything)
#   addr1/2/3        each of the three module-qualified addresses appears
#                    exactly once
assert_script_wiring() {
  local script="$1"

  if [[ ! -f "$script" ]]; then
    echo "FAIL: shared script not found at ${script}"
    ((fail++)) || true
    return
  fi

  if awk -v SQ="'" -v cmdre="$DELETE_CMD_RE" "$AWK_CODE_FUNCS"'
      code_of($0) ~ /OWNED_INSTANCE=.*jq[[:space:]]+-er[[:space:]]+.*\.database_instance_name\.value/ { owned++ }
      pipes_to_selector($0, "\"[$]OWNED_INSTANCE\"") {
        piped++
        if (code_of(prev) ~ /SELECTED="?[$][(]gcloud[[:space:]]+sql[[:space:]]+instances[[:space:]]+list/) fed++
      }
      code_of($0) ~ /gcloud[[:space:]]+sql[[:space:]]+instances[[:space:]]+list/ {
        if (code_of($0) ~ ("--format=" SQ "value\\(name\\)" SQ)) {
          if (code_of($0) !~ /--filter/) unfiltered++
          else filtered++
        }
      }
      invokes($0, cmdre) { deletes++; if (delete_line == 0) delete_line = FNR }
      code_of($0) ~ /gcloud[[:space:]]+sql[[:space:]]+instances[[:space:]]+delete[[:space:]]+"[$]SELECTED"/ { by_selected_var++ }
      code_of($0) ~ /terraform[[:space:]]+-chdir=.*[[:space:]]state[[:space:]]+rm[[:space:]]+"[$]ADDR"/ {
        state_rm++
        if (state_rm_line == 0) state_rm_line = FNR
      }
      code_of($0) ~ /state[[:space:]]+rm[[:space:]]+google_sql/ { root_addr++ }
      code_of($0) ~ /module\.database\.google_sql_database_instance\.main/ { addr1++ }
      code_of($0) ~ /module\.database\.google_sql_database\.main/ { addr2++ }
      code_of($0) ~ /module\.database\.google_sql_user\.main/ { addr3++ }
      { prev = $0 }
      END {
        exit !(owned == 1 && piped == 1 && fed == 1 && unfiltered == 1 && filtered == 0 && \
               deletes == 1 && by_selected_var == 1 && state_rm == 1 && \
               (state_rm_line > delete_line) && root_addr == 0 && \
               addr1 == 1 && addr2 == 1 && addr3 == 1)
      }
    ' "$script"; then
    echo "PASS: $(basename "$script") deletes only what the exact-match selector yields, unfiltered"
    ((pass++)) || true
  else
    echo "FAIL: $(basename "$script") no longer reads the owned instance name from 'terraform"
    echo "      output', pipes it unfiltered to scripts/select-owned-name.sh, deletes exactly the"
    echo "      instance that pipeline yields, and removes exactly the three module-qualified"
    echo "      state addresses only after that delete succeeds -- expected one of each. This is"
    echo "      where the body lives, so a --filter or a root-level state address reintroduced"
    echo "      here is the #1971 shape, whatever the call site looks like"
    ((fail++)) || true
  fi
}

assert_script_wiring "$DELETE_SCRIPT"

# assert_nothing_swallowed SCRIPT
#
# The old step carried `2>/dev/null` on the listing, `|| true` on the delete,
# and `2>/dev/null || echo "..."` on the peering delete in the same step
# (#1971). Asserted on code_of() so the header, which quotes these forms to
# explain why they are gone, is not itself a violation.
assert_nothing_swallowed() {
  local script="$1"

  if awk -v SQ="'" "$AWK_CODE_FUNCS"'
      code_of($0) ~ /2>[[:space:]]*\/dev\/null/ { print "  swallowed stderr: " FNR; bad++ }
      code_of($0) ~ /[|][|][[:space:]]*true/ { print "  swallowed exit status: " FNR; bad++ }
      code_of($0) ~ /[|][|][[:space:]]*echo/ { print "  swallowed exit status via echo: " FNR; bad++ }
      END { exit !(bad == 0) }
    ' "$script"; then
    echo "PASS: $(basename "$script") swallows neither a failed listing, a failed delete, nor a failed state rm"
    ((pass++)) || true
  else
    echo "FAIL: $(basename "$script") suppresses an error on the line(s) above. A partial or"
    echo "      failed delete must fail loudly (#1971), not report success and leave the"
    echo "      instance running while state is removed out from under it"
    ((fail++)) || true
  fi
}

assert_nothing_swallowed "$DELETE_SCRIPT"

# --- Behaviour: the script run end to end against stubbed gcloud and terraform
#
# Everything above is static. None of it can show that the pipeline actually
# deletes the right instance, that a failed call is really not swallowed, or
# that state is removed only after a successful delete -- a script can satisfy
# every text assertion and still do the wrong thing at runtime.
#
# `terraform` and `gcloud` are stubbed on PATH, and every invocation of either
# is logged to the SAME file, so the ordering assertion (state rm after
# delete) can be made from one call log rather than two that would have to be
# interleaved by wall-clock time. The gcloud stub exits 99 if any argument
# starts with `--filter`, so a filter reintroduced into the listing call fails
# as BEHAVIOUR, not only as text.
STUB_DIR="$(mktemp -d)"
STUB_STATE="$(mktemp -d)"
CALLS="$(mktemp)"
trap 'rm -rf "$STUB_DIR" "$STUB_STATE"; rm -f "$CALLS"' EXIT

cat >"${STUB_DIR}/terraform" <<'EOF'
#!/usr/bin/env bash
echo "terraform $*" >>"$CALLS"
case "$2" in
  output)
    printf '%s' "$TF_OUTPUT_JSON"
    ;;
  state)
    if [[ "$3" == "rm" ]]; then
      [[ "${STATE_RM_FAILS:-0}" == "1" ]] && exit 3
    fi
    ;;
esac
exit 0
EOF

cat >"${STUB_DIR}/gcloud" <<'EOF'
#!/usr/bin/env bash
echo "gcloud $*" >>"$CALLS"
case "$3" in
  list)
    for arg in "$@"; do
      case "$arg" in
        --filter*)
          echo "stub: --filter is not the guard" >&2
          exit 99
          ;;
      esac
    done
    [[ "${LIST_FAILS:-0}" == "1" ]] && { echo "list failed" >&2; exit 255; }
    printf '%s\n' "$LISTING"
    ;;
  delete)
    [[ "${DELETE_FAILS:-0}" == "1" ]] && { echo "delete failed" >&2; exit 254; }
    ;;
esac
exit 0
EOF
chmod +x "${STUB_DIR}/terraform" "${STUB_DIR}/gcloud"

export CALLS

# The hostile listing from the positive-direction section above, one per line,
# exactly what `gcloud sql instances list --format='value(name)'` prints.
STUB_LISTING="$LISTING"

PROJECT_OK="cudly-staging"

# run_script STATE_DIR PROJECT -> STUB_EXIT, STUB_ERR, and a call log in $CALLS
run_script() {
  : >"$CALLS"
  local errfile
  errfile="$(mktemp)"
  STUB_EXIT=0
  PATH="${STUB_DIR}:${PATH}" "$DELETE_SCRIPT" "$@" >/dev/null 2>"$errfile" || STUB_EXIT=$?
  STUB_ERR="$(cat "$errfile")"
  rm -f "$errfile"
}

# assert_behaviour LABEL CONDITION_RESULT DETAIL
assert_behaviour() {
  if [[ "$2" == "0" ]]; then
    echo "PASS: $1"
    ((pass++)) || true
  else
    echo "FAIL: $1"
    echo "      $3"
    ((fail++)) || true
  fi
}

count_calls() { grep -c "$1" "$CALLS" 2>/dev/null || true; }

export LISTING="$STUB_LISTING"

# A state that is already destroyed is a normal outcome, not an error, and
# must not reach gcloud at all.
export TF_OUTPUT_JSON='{}'
run_script "$STUB_STATE" "$PROJECT_OK"
assert_behaviour "behaviour: a state with no outputs exits 0 without calling gcloud or state rm" \
  "$([[ "$STUB_EXIT" -eq 0 && "$(count_calls '^gcloud')" -eq 0 && "$(count_calls 'state rm')" -eq 0 ]] && echo 0 || echo 1)" \
  "exit ${STUB_EXIT}, calls: $(cat "$CALLS")"

# A state that predates the output. Distinct from the empty case below,
# because the remedies differ: this one wants an apply.
export TF_OUTPUT_JSON='{"network_name":{"value":"cudly-staging-vpc"}}'
run_script "$STUB_STATE" "$PROJECT_OK"
assert_behaviour "behaviour: a state missing the output exits 1 without calling gcloud" \
  "$([[ "$STUB_EXIT" -eq 1 && "$(count_calls '^gcloud')" -eq 0 ]] && echo 0 || echo 1)" \
  "exit ${STUB_EXIT}, calls: $(cat "$CALLS")"
assert_behaviour "behaviour: the missing-output error tells the operator to re-apply" \
  "$([[ "$STUB_ERR" == *"absent or null"* && "$STUB_ERR" == *"re-apply this state"* ]] && echo 0 || echo 1)" \
  "stderr: ${STUB_ERR}"

# `jq -er` accepts an empty string, so without its own check this reaches gcloud.
export TF_OUTPUT_JSON='{"database_instance_name":{"value":""}}'
run_script "$STUB_STATE" "$PROJECT_OK"
assert_behaviour "behaviour: an empty instance name exits 1 without calling gcloud" \
  "$([[ "$STUB_EXIT" -eq 1 && "$(count_calls '^gcloud')" -eq 0 ]] && echo 0 || echo 1)" \
  "exit ${STUB_EXIT}, calls: $(cat "$CALLS")"
assert_behaviour "behaviour: the empty-name error is distinct and says an apply will not fix it" \
  "$([[ "$STUB_ERR" == *"will NOT fix this"* && "$STUB_ERR" != *"re-apply this state"* ]] && echo 0 || echo 1)" \
  "stderr: ${STUB_ERR}"

# A null value takes the jq branch, not the empty branch.
export TF_OUTPUT_JSON='{"database_instance_name":{"value":null}}'
run_script "$STUB_STATE" "$PROJECT_OK"
assert_behaviour "behaviour: a null instance name exits 1 without calling gcloud" \
  "$([[ "$STUB_EXIT" -eq 1 && "$(count_calls '^gcloud')" -eq 0 ]] && echo 0 || echo 1)" \
  "exit ${STUB_EXIT}, calls: $(cat "$CALLS")"

# Malformed project ids: validation precedes everything, including
# `terraform output`, so none of these reach terraform or gcloud. Each is a
# distinct malformation: empty, whitespace-only, wrong case, too short.
export TF_OUTPUT_JSON='{"database_instance_name":{"value":"cudly-staging-postgres"}}'
for bad_project in "" "  " "Cudly-Staging" "cudly"; do
  run_script "$STUB_STATE" "$bad_project"
  assert_behaviour "behaviour: malformed project '${bad_project}' exits 2 without calling terraform or gcloud" \
    "$([[ "$STUB_EXIT" -eq 2 && ! -s "$CALLS" ]] && echo 0 || echo 1)" \
    "exit ${STUB_EXIT}, calls: $(cat "$CALLS")"
done

# The golden path, against the hostile listing (mirror sorts first).
export LISTING="$STUB_LISTING"
run_script "$STUB_STATE" "$PROJECT_OK"
assert_behaviour "behaviour: the golden path exits 0" \
  "$([[ "$STUB_EXIT" -eq 0 ]] && echo 0 || echo 1)" "exit ${STUB_EXIT}"
assert_behaviour "behaviour: exactly one instance is deleted out of the hostile listing" \
  "$([[ "$(count_calls 'sql instances delete')" -eq 1 ]] && echo 0 || echo 1)" \
  "delete calls: $(count_calls 'sql instances delete')"
assert_behaviour "behaviour: the deleted instance is the one the state owns" \
  "$(grep -q 'sql instances delete cudly-staging-postgres --project=' "$CALLS" && echo 0 || echo 1)" \
  "calls: $(grep delete "$CALLS" || echo none)"
assert_behaviour "behaviour: exactly three state rm calls, each module-qualified" \
  "$([[ "$(count_calls 'state rm module\.database\.')" -eq 3 ]] && echo 0 || echo 1)" \
  "state rm calls: $(grep 'state rm' "$CALLS" || echo none)"
assert_behaviour "behaviour: the first state rm call comes after the delete call" \
  "$([[ "$(grep -n 'sql instances delete' "$CALLS" | head -1 | cut -d: -f1)" -lt \
        "$(grep -n 'state rm' "$CALLS" | head -1 | cut -d: -f1)" ]] && echo 0 || echo 1)" \
  "call log: $(cat -n "$CALLS")"

# Asserted per neighbour so a failure names the database that would have been
# deleted.
while IFS= read -r neighbour; do
  [[ -n "$neighbour" ]] || continue
  assert_behaviour "behaviour: neighbour ${neighbour} is not deleted" \
    "$(grep -q -- "sql instances delete ${neighbour} " "$CALLS" && echo 1 || echo 0)" \
    "calls: $(grep delete "$CALLS" || echo none)"
done <<'EOF'
cudly-staging-mirror
cudly-staging-postgres-replica
cudly-staging-prod-mirror
backup-cudly-staging
cudly-stagingx
EOF

# Re-running a cleanup after a completed one is normal, not an error.
export LISTING=$'cudly-dev-postgres\ncudly-prod-postgres'
run_script "$STUB_STATE" "$PROJECT_OK"
assert_behaviour "behaviour: an instance already gone exits 0, deletes nothing, removes no state" \
  "$([[ "$STUB_EXIT" -eq 0 && "$(count_calls 'sql instances delete')" -eq 0 && "$(count_calls 'state rm')" -eq 0 ]] && echo 0 || echo 1)" \
  "exit ${STUB_EXIT}, calls: $(cat "$CALLS")"

# The two defects #1971 found, as behaviour rather than as text. A failed
# delete used to be swallowed by `|| true` and the state rm lines ran anyway
# (root-level addresses that never matched, so this never showed up); here a
# failed delete must fail the step AND leave state untouched.
export LISTING="$STUB_LISTING" DELETE_FAILS=1
run_script "$STUB_STATE" "$PROJECT_OK"
assert_behaviour "behaviour: a failed delete fails the step and removes no state" \
  "$([[ "$STUB_EXIT" -ne 0 && "$(count_calls 'state rm')" -eq 0 ]] && echo 0 || echo 1)" \
  "exit ${STUB_EXIT}, calls: $(cat "$CALLS")"
unset DELETE_FAILS

export LIST_FAILS=1
run_script "$STUB_STATE" "$PROJECT_OK"
assert_behaviour "behaviour: a failed listing fails the step and deletes nothing" \
  "$([[ "$STUB_EXIT" -ne 0 && "$(count_calls 'sql instances delete')" -eq 0 ]] && echo 0 || echo 1)" \
  "exit ${STUB_EXIT}, calls: $(cat "$CALLS")"
unset LIST_FAILS

export STATE_RM_FAILS=1
run_script "$STUB_STATE" "$PROJECT_OK"
assert_behaviour "behaviour: a failed state rm fails the step after exactly one delete" \
  "$([[ "$STUB_EXIT" -ne 0 && "$(count_calls 'sql instances delete')" -eq 1 ]] && echo 0 || echo 1)" \
  "exit ${STUB_EXIT}, calls: $(cat "$CALLS")"
unset STATE_RM_FAILS

run_script
assert_behaviour "behaviour: no arguments exits 2" \
  "$([[ "$STUB_EXIT" -eq 2 ]] && echo 0 || echo 1)" "exit ${STUB_EXIT}"

run_script "$STUB_STATE"
assert_behaviour "behaviour: one argument exits 2" \
  "$([[ "$STUB_EXIT" -eq 2 ]] && echo 0 || echo 1)" "exit ${STUB_EXIT}"

run_script "$STUB_STATE" "$PROJECT_OK" "extra"
assert_behaviour "behaviour: three arguments exits 2" \
  "$([[ "$STUB_EXIT" -eq 2 ]] && echo 0 || echo 1)" "exit ${STUB_EXIT}"

run_script "${STUB_STATE}/does-not-exist" "$PROJECT_OK"
assert_behaviour "behaviour: a missing state directory exits 2" \
  "$([[ "$STUB_EXIT" -eq 2 ]] && echo 0 || echo 1)" "exit ${STUB_EXIT}"

# The assertions above name the one file and script they know about, so a NEW
# delete site in a new step, workflow or script is invisible to them -- which
# is how #1820 outlived #1592 and #1821 outlived both. This sweep is keyed on
# the dangerous call instead of on a name: everything anywhere in the swept
# set that runs `gcloud sql instances delete` must pipe through the selector,
# whatever it is called.
#
# scripts/ is GLOBBED via build_swept_scripts, not named file by file, for the
# same reason: a NEW script running the command without the selector must be
# caught even though nobody remembered to list it here.
#
# The four guard suites are excluded by name because they carry both the
# command and the selector as fixture data and in awk programs; sweeping them
# would report this file as a violation of itself.

# sweep_unwired DIR [FILE...]
#
# Prints one line per delete site in DIR (plus each named FILE) that does not
# pipe through the selector, plus a line of its own when the swept set holds no
# delete site at all. No output means the swept set is clean.
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
    END { finish(); if (total == 0) print "no `gcloud sql instances delete` step found at all" }
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

# The scripts/ half of the swept set is globbed, so a script added later is
# swept without anyone remembering to name it here. Rationale and the nullglob
# reasoning: build_swept_scripts in scripts/lib/code-scan-awk.sh.
build_swept_scripts "$SCRIPT_DIR"

# "Found no violations" must not be reachable by looking at nothing, so the
# swept set is asserted non-empty and asserted to contain the one script that
# actually runs the command. Guarding the expansion too: under `set -u`, bash
# 3.2 treats "${arr[@]}" on an empty array as an unbound variable.
if [[ ${#SWEPT_SCRIPTS[@]} -eq 0 ]]; then
  echo "FAIL: the scripts/ half of the swept set is empty"
  echo "      ${SCRIPT_DIR}/*.sh matched nothing, so the sweep below would report a"
  echo "      clean result for files it never opened"
  ((fail++)) || true
else
  echo "PASS: the swept set holds ${#SWEPT_SCRIPTS[@]} script(s) under scripts/"
  ((pass++)) || true

  swept_has_guarded=0
  for swept_candidate in "${SWEPT_SCRIPTS[@]}"; do
    [[ "$swept_candidate" == "$DELETE_SCRIPT" ]] && swept_has_guarded=1
  done
  if [[ "$swept_has_guarded" -eq 1 ]]; then
    echo "PASS: the swept set includes $(basename "$DELETE_SCRIPT"), the script that runs the command"
    ((pass++)) || true
  else
    echo "FAIL: the swept set does not include $(basename "$DELETE_SCRIPT")"
    echo "      the sweep would then find no delete site at all and pass vacuously"
    ((fail++)) || true
  fi

  assert_sweep "every 'gcloud sql instances delete' site in .github/workflows and scripts/ pipes through the selector" \
    "$WORKFLOW_DIR" "" "${SWEPT_SCRIPTS[@]}"
fi

# --- The sweep itself, in both directions, over fixtures ---------------------
#
# The sweep is the only assertion covering delete sites nobody has named, so a
# sweep that quietly stops recognizing them fails open. These fixtures pin
# both directions of that recognition, including the prose case: a guard that
# fired on ci.yml's comment describing this assertion would police what may be
# written rather than what is run.
FIXTURE_DIR="$(mktemp -d)"
# Replaces the stub trap set above rather than adding to it, so it has to
# clean up both sets. A second `trap ... EXIT` silently discards the first.
trap 'rm -rf "$FIXTURE_DIR" "$STUB_DIR" "$STUB_STATE"; rm -f "$CALLS"' EXIT
mkdir -p "${FIXTURE_DIR}/prose" "${FIXTURE_DIR}/wired" "${FIXTURE_DIR}/unwired" "${FIXTURE_DIR}/scripts" "${FIXTURE_DIR}/misattrib"

cat >"${FIXTURE_DIR}/prose/mentions.yml" <<'EOF'
      - name: Describes the command without running it
        run: |
          # asserts every `gcloud sql instances delete` step is wired
          echo "would run gcloud sql instances delete if it were wired"
          echo 'gcloud sql instances delete is named here too'
EOF

cat >"${FIXTURE_DIR}/wired/deletes.yml" <<'EOF'
      - name: Describes the command without running it
        run: |
          # asserts every `gcloud sql instances delete` step is wired
          echo "would run gcloud sql instances delete if it were wired"

      - name: Delete the Cloud SQL instance this state owns
        run: |
          SELECTED="$(gcloud sql instances list --project="$PROJECT" --format='value(name)' \
            | ./scripts/select-owned-name.sh "$OWNED_INSTANCE")"
          gcloud sql instances delete "$SELECTED" --project="$PROJECT" --quiet
EOF

# The #1971 shape: a substring filter and `head -1`, no selector, `|| true` on
# the delete. A commented-out selector line inside the `if` must not satisfy
# the wiring, which is why the selector match runs on the comment-stripped
# line. Body is the pre-fix step from origin/main (lines 385-416) verbatim,
# plus that one commented-out line.
cat >"${FIXTURE_DIR}/unwired/deletes.yml" <<'EOF'
      - name: Delete Cloud SQL instance directly (avoids user/DB ordering deadlock)
        env:
          TF_VAR_project_id: ${{ vars.GCP_PROJECT_ID }}
          GCP_PROJECT_ID: ${{ vars.GCP_PROJECT_ID }}
        run: |
          # Routed via env: rather than interpolated into this script's source,
          # per the zero-expression-interpolation-in-run-blocks rule from #1641.
          PROJECT="$GCP_PROJECT_ID"
          # Validate the shape, not merely non-emptiness: a whitespace-only or
          # malformed value would otherwise reach `gcloud --project=`, match no
          # instance, and silently skip the deletion this step exists to perform.
          if ! printf '%s' "$PROJECT" | grep -Eq '^[a-z][a-z0-9-]{5,29}$'; then
            echo "::error::vars.GCP_PROJECT_ID is unset or malformed ('$PROJECT'); refusing to run Cloud SQL cleanup"
            exit 1
          fi
          # Find and delete the staging Cloud SQL instance to avoid PostgreSQL dependency errors
          INSTANCE=$(gcloud sql instances list --project="$PROJECT" \
            --filter="name:cudly-staging" --format="value(name)" 2>/dev/null | head -1)
          if [ -n "$INSTANCE" ]; then
            # | ./scripts/select-owned-name.sh "$OWNED_INSTANCE"
            echo "Deleting Cloud SQL instance $INSTANCE..."
            gcloud sql instances delete "$INSTANCE" --project="$PROJECT" --quiet || true
            # Wait for deletion to complete (Service Networking Connection can't be removed until Cloud SQL is gone)
            echo "Waiting for Cloud SQL instance to be fully deleted..."
            for i in $(seq 1 30); do
              if ! gcloud sql instances describe "$INSTANCE" --project="$PROJECT" --quiet 2>/dev/null; then
                echo "Cloud SQL instance $INSTANCE deleted."
                break
              fi
              echo "Still deleting... ($i/30)"
              sleep 15
            done
          fi
EOF

assert_sweep "a step that only mentions the command is not a delete site" \
  "${FIXTURE_DIR}/prose" 'no `gcloud sql instances delete` step found at all'

assert_sweep "a wired delete step alongside prose mentions is not flagged" \
  "${FIXTURE_DIR}/wired" ""

assert_sweep "an unwired delete step is flagged, past a commented-out selector stage" \
  "${FIXTURE_DIR}/unwired" 'step "Delete Cloud SQL instance directly (avoids user/DB ordering deadlock)"'

assert_sweep "a directory holding no workflow file is reported, not passed" \
  "${FIXTURE_DIR}/empty-does-not-exist" 'no workflow files found under'

# The body lives in a shell script rather than a workflow step, so the sweep
# has to recognise a delete site in a file with no `- name:` lines at all, and
# has to accept the sibling-script call form that resolves the selector from
# BASH_SOURCE. Both directions, over a file swept by name the way the real one
# is. The `prose` dir supplies the workflow half and contributes no delete site.
cat >"${FIXTURE_DIR}/scripts/wired.sh" <<'EOF'
SELECTED="$(gcloud sql instances list --project="$PROJECT" --format='value(name)' \
  | "${SCRIPT_DIR}/select-owned-name.sh" "$OWNED_INSTANCE")"
gcloud sql instances delete "$SELECTED" --project="$PROJECT" --quiet
EOF

cat >"${FIXTURE_DIR}/scripts/unwired.sh" <<'EOF'
INSTANCE=$(gcloud sql instances list --project="$PROJECT" \
  --filter="name:cudly-staging" --format="value(name)" 2>/dev/null | head -1)
gcloud sql instances delete "$INSTANCE" --project="$PROJECT" --quiet || true
EOF

assert_sweep "a script calling the selector through \${SCRIPT_DIR} is not flagged" \
  "${FIXTURE_DIR}/prose" "" "${FIXTURE_DIR}/scripts/wired.sh"

assert_sweep "an unwired script is flagged as a whole-file delete site" \
  "${FIXTURE_DIR}/prose" 'unwired.sh: whole file' "${FIXTURE_DIR}/scripts/unwired.sh"

assert_sweep "a swept file that does not exist is reported, not passed" \
  "${FIXTURE_DIR}/prose" 'swept file not found' "${FIXTURE_DIR}/scripts/does-not-exist.sh"

# A site running to the end of its file is only flushed once the next file
# starts, so the report has to remember which file the site came from. Reported
# from FILENAME it named the innocent file swept next, and every fixture above
# sweeps one file at a time, so none of them can catch it.
cat >"${FIXTURE_DIR}/misattrib/a-unwired.yml" <<'EOF'
      - name: Delete Cloud SQL instance
        run: |
          gcloud sql instances delete "$INSTANCE" --project="$PROJECT" --quiet
EOF

cat >"${FIXTURE_DIR}/misattrib/b-innocent.yml" <<'EOF'
      - name: Deletes nothing
        run: |
          echo "clean"
EOF

assert_sweep "a finding names the file it came from, not the file swept after it" \
  "${FIXTURE_DIR}/misattrib" 'a-unwired.yml: step "Delete Cloud SQL instance"'

echo
echo "passed: ${pass}, failed: ${fail}"
[[ "$fail" -eq 0 ]]
