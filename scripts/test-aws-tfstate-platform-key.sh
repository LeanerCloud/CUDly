#!/usr/bin/env bash
# test-aws-tfstate-platform-key.sh
#
# Asserts that every job which applies a `compute_platform` writes the Terraform
# state namespace that platform owns, and is serialized by the concurrency group
# that names the object it writes.
#
# The AWS environment is one Terraform root applied twice into two different
# state objects, one per compute platform:
#
#   compute_platform=lambda   s3://<bucket>/github-<env>/terraform.tfstate
#   compute_platform=fargate  s3://<bucket>/github-fargate-<env>/terraform.tfstate
#
# Nothing in Terraform ties those together. The backend key is a string a
# workflow step builds by hand, the platform is a `-var` passed several steps
# later, and a job that pairs them wrongly initialises fine, plans fine and
# applies fine. It just rewrites the other platform's stack and records the
# result in a state file that platform's own deploys then disagree with, while
# the state it was supposed to write is left describing resources nobody
# reconciles. That was #1811: `rollback.yml`'s `rollback-aws-fargate` built the
# key from the LAMBDA namespace and applied `compute_platform=fargate` into it,
# so the two rollback jobs wrote the same object.
#
# The `concurrency` group is checked on the same axis, in two ways. #1806 keyed
# every group on the state object its job locks, so a group naming a different
# namespace from the key means one of the two moved alone: the job then
# serializes against a state file it never touches while writing one unguarded.
# A job with NO group is a violation too, not an exemption: it applies shared
# state with nothing serializing it at all, which is the same hazard without
# even a wrong answer to notice. Gating that check on the group being present
# read absence as permission and let exactly that job through.
#
# Only groups in the `aws-tfstate-*` / `aws-fargate-tfstate-*` families are
# checked; the Azure and GCP groups guard state objects with no platform split.
#
# A green workflow run proves nothing about this, which is why the pairing is
# asserted here as text rather than left to a live rollback to discover.
#
# Both directions are asserted, and the positive one first: a sweep that
# recognizes no state-writing job at all has no violations either, and would
# pass while every pairing in the repository was wrong.
#
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
WORKFLOW_DIR="${REPO_ROOT}/.github/workflows"

# code_of(), plus build_swept_scripts() for the scripts/ half of the sweep.
# Shared with test-ecr-delete-selection.sh and
# test-rds-deletion-protection-scope.sh so the rule separating code that RUNS a
# command from prose that mentions it has one definition rather than three that
# drift.
# shellcheck source=scripts/lib/code-scan-awk.sh
. "${SCRIPT_DIR}/lib/code-scan-awk.sh"

pass=0
fail=0

note_pass() {
  echo "PASS: $1"
  ((pass++)) || true
}

note_fail() {
  echo "FAIL: $1"
  ((fail++)) || true
}

# --- The scanner -------------------------------------------------------------
#
# scan_platform_keys MODE DIR [FILE...]
#
# Sites are delimited by top-level YAML job headers -- a line at exactly two
# spaces of indent holding an identifier and nothing else. NOT by `- name:`
# steps: deploy-aws-fargate.yml writes the backend file in "Terraform Init" and
# passes `-var="compute_platform=fargate"` in "Terraform Plan", so a
# step-delimited scan would see a key with no platform and a platform with no
# key and pair neither. A job is the smallest unit that always holds both, and
# nothing inside a job body sits at two-space indent, so no job can be split in
# half by a spurious boundary. A shell script has no such header and is scanned
# as one site, reported as "whole file".
#
# MODE=violations prints one line per job whose key disagrees with its platform
# or with its concurrency group, plus a line of its own when the scanned set
# holds no platform-applying job at all. No output means the scanned set is
# clean.
#
# MODE=census prints `file|job|key-namespace|platform|group-namespace` for every
# job that applies a platform, whatever the verdict. It is what the positive
# assertions read, so "clean" cannot mean "recognized nothing".
#
# Kept as one awk program in two modes rather than two, because a census that
# recognized jobs differently from the sweep would assert the wrong thing is
# covered.
scan_platform_keys() {
  local mode="$1"
  local dir="$2"
  shift 2
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

  # Ternaries are parenthesized wherever they appear in an argument list or a
  # `return`: unparenthesized, BWK awk (macOS) rejects them at parse time while
  # mawk (CI) accepts them, so an unparenthesized one passes locally under mawk
  # and never runs on a developer machine, or the reverse.
  #
  # The key is matched on code_of() alone, with string literals left intact --
  # unlike invokes(), which empties them. The key IS a quoted literal
  # (`key = "github-fargate-%s/..."` inside a printf format), so emptying
  # literals would erase the only thing being asserted.
  #
  # A site is reported from site_file rather than FILENAME: a site running to
  # the end of its file is flushed by the next file's first line, by which point
  # FILENAME has advanced and the report would name an innocent file.
  awk -v SQ="'" -v mode="$mode" "$AWK_CODE_FUNCS"'
    function finish(   expected, shown) {
      if (platform != "") {
        total++
        shown = (ns == "" ? "NONE" : ns)
        if (mode == "census") {
          printf "%s|%s|%s|%s|%s\n", basename(site_file), site, shown, platform, (grp == "" ? "NONE" : grp)
        } else if (platform == "MIXED") {
          printf "%s: %s: more than one compute_platform value in one job\n", basename(site_file), label()
        } else if (ns == "") {
          printf "%s: %s: compute_platform=%s with no AWS github-* backend state key in the same job\n", basename(site_file), label(), platform
        } else if (ns == "MIXED") {
          printf "%s: %s: more than one AWS state namespace in one job\n", basename(site_file), label()
        } else {
          expected = (platform == "fargate") ? "fargate" : ((platform == "lambda") ? "lambda" : "")
          if (expected == "")
            printf "%s: %s: unrecognized compute_platform=%s\n", basename(site_file), label(), platform
          else if (ns != expected)
            printf "%s: %s: compute_platform=%s applies into the %s state namespace\n", basename(site_file), label(), platform, nsdesc(ns)
        }
      }
      # Two independent conditions, not an if/else on `grp != ""`. Gating the
      # whole check on a non-empty group made ABSENCE read as permission: a job
      # with the right key and the right platform but no group at all passed,
      # and then applies shared state with nothing serializing it, which is
      # #1806 with the guard watching. Empty is a missing requirement here, not
      # an exemption.
      #
      # The mismatch arm does not require a platform, so a read-only job that
      # locks the wrong object is still caught; the missing-group arm does,
      # because a job that only reads state needs no lock.
      if (mode != "census" && ns != "" && ns != "MIXED") {
        if (grp == "" && platform != "")
          printf "%s: %s: no AWS concurrency group serializes a job applying compute_platform=%s into the %s state namespace\n", basename(site_file), label(), platform, nsdesc(ns)
        else if (grp != "" && grp != ns)
          printf "%s: %s: concurrency group aws%s-tfstate-* guards a job writing the %s state namespace\n", basename(site_file), label(), (grp == "fargate" ? "-fargate" : ""), nsdesc(ns)
      }
      site = ""; ns = ""; platform = ""; grp = ""
    }
    function label() { return (site == "" ? "whole file" : ("job \"" site "\"")) }
    function nsdesc(n) { return (n == "fargate" ? "github-fargate-<env>/ (fargate)" : "github-<env>/ (lambda)") }
    function basename(p) { sub(/^.*\//, "", p); return p }
    function note_ns(n) { ns = ((ns == "" || ns == n) ? n : "MIXED") }
    function note_grp(n) { grp = ((grp == "" || grp == n) ? n : "MIXED") }

    FNR == 1 { if (NR > 1) finish(); site_file = FILENAME }

    {
      code = code_of($0)

      # The job boundary is tested on the comment-stripped line so a header
      # carrying a trailing comment still ends the previous job. Missed, the
      # key and platform of the job after it merge into the previous one.
      if (code ~ /^  [A-Za-z0-9_.-]+:[[:space:]]*$/) {
        finish()
        site = code
        sub(/^[[:space:]]*/, "", site)
        sub(/:[[:space:]]*$/, "", site)
        next
      }

      # AWS S3 keys end in `/terraform.tfstate`; the Azure blob is
      # `github-<env>.terraform.tfstate`, one namespace with no platform split.
      # Requiring the slash keeps the Azure jobs out of both checks below rather
      # than classifying them as the Lambda namespace and then demanding an
      # `aws-tfstate-*` group for them.
      if (code ~ /key[[:space:]]*=[[:space:]]*"github-fargate-[^"]*\/terraform\.tfstate/) note_ns("fargate")
      else if (code ~ /key[[:space:]]*=[[:space:]]*"github-[^"]*\/terraform\.tfstate/) note_ns("lambda")
      if (code ~ /^[[:space:]]*group:[[:space:]]*aws-fargate-tfstate-/) note_grp("fargate")
      else if (code ~ /^[[:space:]]*group:[[:space:]]*aws-tfstate-/) note_grp("lambda")
      if (match(code, /compute_platform=[A-Za-z0-9_-]+/)) {
        found = substr(code, RSTART + 17, RLENGTH - 17)
        platform = ((platform == "" || platform == found) ? found : "MIXED")
      }
    }

    END {
      finish()
      if (total == 0 && mode != "census")
        print "no job applying a compute_platform found at all"
    }
  ' "${files[@]}"
}

# assert_scan LABEL EXPECTED DIR [FILE...]
#
# EXPECTED empty asserts the sweep finds nothing; otherwise it asserts EXPECTED
# appears in the report, so a fixture pins WHICH job was flagged rather than
# only that something was. A syntax error in the awk above also produces a
# non-empty report, and matching the text is what tells the two apart.
assert_scan() {
  local label="$1"
  local expected="$2"
  local dir="$3"
  shift 3
  local report line

  report="$(scan_platform_keys violations "$dir" "$@")"

  if [[ -z "$expected" && -z "$report" ]] || [[ -n "$expected" && "$report" == *"$expected"* ]]; then
    note_pass "$label"
  else
    note_fail "$label"
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
  fi
}

# --- Positive direction FIRST: the real pairings are seen and are right -------
#
# Every AWS job that applies a compute_platform, with the namespace it must
# write. Named explicitly, and asserted BEFORE any absence: if the scanner stops
# recognizing these, the sweep below reports a clean result for a repository it
# no longer understands. The sweep is the half that covers jobs nobody named.
EXPECTED_PAIRS=$(
  cat <<'EOF'
deploy-aws-fargate.yml|deploy|fargate|fargate|fargate
deploy-aws-lambda.yml|build-and-deploy|lambda|lambda|lambda
cleanup-staging.yml|destroy-aws-fargate|fargate|fargate|fargate
cleanup-staging.yml|destroy-aws-lambda|lambda|lambda|lambda
destroy-fargate-dev.yml|destroy|fargate|fargate|fargate
rollback.yml|rollback-aws-fargate|fargate|fargate|fargate
rollback.yml|rollback-aws-lambda|lambda|lambda|lambda
EOF
)

CENSUS="$(scan_platform_keys census "$WORKFLOW_DIR")"

# Counted by the `file|job|namespace|platform` shape rather than by line, so the
# scanner's own error lines ("no workflow files found under ...") are not
# counted as jobs. A plain `wc -l` reported an emptied workflow directory as one
# recognized job, which is the "no violations by looking at nothing" reading
# this count exists to close.
CENSUS_COUNT=$(printf '%s\n' "$CENSUS" | grep -cE '^[^|]+\|[^|]*(\|[^|]+){3}$' || true)

if [[ "$CENSUS_COUNT" -eq 0 ]]; then
  note_fail "the scan recognizes no state-writing job under ${WORKFLOW_DIR}"
  echo "      a scan that recognizes nothing has no violations either, so every"
  echo "      assertion below would pass over a repository it never read"
else
  note_pass "the scan recognizes ${CENSUS_COUNT} job(s) that apply a compute_platform"
fi

while IFS= read -r expected_pair; do
  [[ -z "$expected_pair" ]] && continue
  if printf '%s\n' "$CENSUS" | grep -Fxq "$expected_pair"; then
    note_pass "${expected_pair} pairs as expected"
  else
    note_fail "expected pairing not found: ${expected_pair}"
    echo "      format is file|job|state-namespace|compute_platform|concurrency-namespace;"
    echo "      the census holds:"
    if [[ -z "$CENSUS" ]]; then
      echo "        (nothing)"
    else
      while IFS= read -r line; do
        echo "        ${line}"
      done <<<"$CENSUS"
    fi
  fi
done <<<"$EXPECTED_PAIRS"

# --- Negative direction: nothing anywhere pairs them wrongly ------------------
#
# The assertions above name the jobs they know about, so a NEW job in a new
# workflow is invisible to them, which is the mode that produced #1592, then
# #1820, then #1821: the guard landed on one site and not its sibling. This
# sweep is keyed on the pairing instead of on a name, over a GLOB of
# .github/workflows plus every script under scripts/, so a file added later is
# covered without anyone remembering to list it. Both workflow extensions GitHub
# accepts are swept, so a new `.yaml` cannot slip past.
build_swept_scripts "$SCRIPT_DIR"

if [[ ${#SWEPT_SCRIPTS[@]} -eq 0 ]]; then
  note_fail "the scripts/ half of the swept set is empty"
  echo "      ${SCRIPT_DIR}/*.sh matched nothing, so the sweep below would report a"
  echo "      clean result for files it never opened"
  assert_scan "every compute_platform job writes the state namespace that platform owns" \
    "" "$WORKFLOW_DIR"
else
  note_pass "the swept set holds ${#SWEPT_SCRIPTS[@]} script(s) under scripts/"
  assert_scan "every compute_platform job writes the state namespace that platform owns" \
    "" "$WORKFLOW_DIR" "${SWEPT_SCRIPTS[@]}"
fi

# --- The scanner itself, in both directions, over fixtures -------------------
#
# The sweep is the only assertion covering jobs nobody has named, so a scanner
# that quietly stops recognizing them fails open. These fixtures pin both
# directions of that recognition, including the split-step case the real
# workflows rely on and the prose case, where a comment describing the defect
# must not be read as the defect.
FIXTURE_DIR="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_DIR"' EXIT
mkdir -p "${FIXTURE_DIR}/split" "${FIXTURE_DIR}/bug1811" "${FIXTURE_DIR}/prose" \
  "${FIXTURE_DIR}/unpaired" "${FIXTURE_DIR}/unknown" "${FIXTURE_DIR}/nostate" \
  "${FIXTURE_DIR}/misattrib"

# The shape deploy-aws-fargate.yml has: key and platform in different steps of
# one job, and a second job in the same file whose key must not leak into it.
cat >"${FIXTURE_DIR}/split/ok.yml" <<'EOF'
jobs:
  deploy-lambda:
    concurrency:
      group: aws-tfstate-${{ inputs.environment }}
      cancel-in-progress: false
    steps:
      - name: Terraform Init
        run: |
          printf '%s\nkey = "github-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
      - name: Terraform Plan
        run: |
          terraform plan -var="compute_platform=lambda"
  deploy-fargate:
    concurrency:
      group: aws-fargate-tfstate-${{ inputs.environment }}
      cancel-in-progress: false
    steps:
      - name: Terraform Init
        run: |
          printf '%s\nkey = "github-fargate-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
      - name: Terraform Plan
        run: |
          terraform plan -var="compute_platform=fargate"
EOF

# #1811 itself: the fargate rollback keyed on the lambda namespace. This is the
# exact text that shipped, so a revert of the fix reproduces this fixture.
cat >"${FIXTURE_DIR}/bug1811/rollback.yml" <<'EOF'
jobs:
  rollback-aws-fargate:
    steps:
      - name: Rollback with Terraform
        run: |
          printf '%s\nkey = "github-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
          terraform apply -auto-approve \
            -var="compute_platform=fargate"
EOF

# The mirror image, which is the same defect the other way round: a lambda apply
# into the fargate namespace.
cat >"${FIXTURE_DIR}/bug1811/inverse.yml" <<'EOF'
jobs:
  rollback-aws-lambda:
    steps:
      - name: Rollback with Terraform
        run: |
          printf '%s\nkey = "github-fargate-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
          terraform apply -auto-approve -var="compute_platform=lambda"
EOF

# A comment describing the mismatch is not the mismatch. A guard that fired on
# this would police what may be WRITTEN about the defect, including the comment
# on rollback.yml recording that #1811 was fixed.
cat >"${FIXTURE_DIR}/prose/mentions.yml" <<'EOF'
jobs:
  rollback-aws-fargate:
    # Until #1811 this job wrote key = "github-%s/terraform.tfstate" while
    # applying -var="compute_platform=fargate".
    concurrency:
      group: aws-fargate-tfstate-${{ inputs.environment }}
    steps:
      - name: Terraform Init
        run: |
          printf '%s\nkey = "github-fargate-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
          terraform apply -var="compute_platform=fargate"  # not key = "github-%s/x"
EOF

# Splitting the key out of the job is not a way to be unverifiable: a platform
# with no key in the same job is reported rather than skipped.
cat >"${FIXTURE_DIR}/unpaired/nokey.yml" <<'EOF'
jobs:
  rollback-aws-fargate:
    steps:
      - name: Rollback with Terraform
        run: |
          terraform apply -auto-approve -var="compute_platform=fargate"
EOF

# An unrecognized platform value is reported rather than passed: the namespace
# it should write is not known, so "no violation" would be a guess.
cat >"${FIXTURE_DIR}/unknown/newplatform.yml" <<'EOF'
jobs:
  deploy-eks:
    steps:
      - name: Terraform Init
        run: |
          printf '%s\nkey = "github-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
          terraform apply -var="compute_platform=eks"
EOF

assert_scan "key and platform in different steps of one job are paired" \
  "" "${FIXTURE_DIR}/split"

assert_scan "the #1811 pairing is flagged, naming the job" \
  'rollback.yml: job "rollback-aws-fargate": compute_platform=fargate applies into the github-<env>/ (lambda) state namespace' \
  "${FIXTURE_DIR}/bug1811"

assert_scan "the inverse pairing is flagged too" \
  'inverse.yml: job "rollback-aws-lambda": compute_platform=lambda applies into the github-fargate-<env>/ (fargate) state namespace' \
  "${FIXTURE_DIR}/bug1811"

assert_scan "a comment describing the mismatch is not the mismatch" \
  "" "${FIXTURE_DIR}/prose"

assert_scan "a platform with no state key in the same job is reported" \
  'nokey.yml: job "rollback-aws-fargate": compute_platform=fargate with no AWS github-* backend state key in the same job' \
  "${FIXTURE_DIR}/unpaired"

assert_scan "an unrecognized compute_platform value is reported" \
  'newplatform.yml: job "deploy-eks": unrecognized compute_platform=eks' \
  "${FIXTURE_DIR}/unknown"

# --- The concurrency axis ----------------------------------------------------
#
# The group and the key both name the state object, and #1811 was the state
# where they disagreed. A guard on the key alone lets the group drift back.
mkdir -p "${FIXTURE_DIR}/group" "${FIXTURE_DIR}/othercloud"

cat >"${FIXTURE_DIR}/group/drift.yml" <<'EOF'
jobs:
  rollback-aws-fargate:
    concurrency:
      group: aws-tfstate-${{ inputs.environment }}
      cancel-in-progress: false
    steps:
      - name: Rollback with Terraform
        run: |
          printf '%s\nkey = "github-fargate-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
          terraform apply -var="compute_platform=fargate"
EOF

# The Azure and GCP jobs pair a `github-<env>` key with a group in another
# family. Reading the Azure blob as the Lambda namespace would demand an
# `aws-tfstate-*` group for it, so the AWS key is recognized by its
# `/terraform.tfstate` suffix and the Azure `.terraform.tfstate` one is not.
cat >"${FIXTURE_DIR}/othercloud/azure.yml" <<'EOF'
jobs:
  destroy-azure:
    concurrency:
      group: azure-tfstate-staging
    steps:
      - name: Terraform Init
        run: |
          printf '%s\nkey = "github-%s.terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
  deploy-gcp:
    concurrency:
      group: gcp-tfstate-staging
    steps:
      - name: Terraform Init
        run: |
          printf '%s\nprefix = "github-%s"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
EOF

# The fail-open the group check originally had: key right, platform right, no
# group at all. Nothing about the pairing is wrong, so every other assertion in
# this suite stays green while that job applies shared state with nothing
# serializing it. Absence is a missing requirement, not an exemption.
cat >"${FIXTURE_DIR}/group/nogroup.yml" <<'EOF'
jobs:
  rollback-aws-fargate:
    steps:
      - name: Rollback with Terraform
        run: |
          printf '%s\nkey = "github-fargate-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
          terraform apply -auto-approve -var="compute_platform=fargate"
EOF

assert_scan "a concurrency group naming the other namespace is flagged" \
  'drift.yml: job "rollback-aws-fargate": concurrency group aws-tfstate-* guards a job writing the github-fargate-<env>/ (fargate) state namespace' \
  "${FIXTURE_DIR}/group"

assert_scan "a job with no concurrency group at all is flagged, not passed" \
  'nogroup.yml: job "rollback-aws-fargate": no AWS concurrency group serializes a job applying compute_platform=fargate into the github-fargate-<env>/ (fargate) state namespace' \
  "${FIXTURE_DIR}/group"

assert_scan "the Azure and GCP jobs are not read as AWS namespaces" \
  'no job applying a compute_platform found at all' "${FIXTURE_DIR}/othercloud"

# A workflow that writes a state key but applies no platform, which is what the
# Azure and GCP jobs and the fargate lock-clearing step look like. Those are not
# violations, but a directory holding only them is also not evidence of a clean
# repository, so the scan says it found nothing rather than staying silent.
cat >"${FIXTURE_DIR}/nostate/plain.yml" <<'EOF'
jobs:
  destroy-azure:
    steps:
      - name: Terraform Init
        run: |
          printf '%s\nkey = "github-%s.terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
          terraform destroy -auto-approve
EOF

assert_scan "a directory holding no compute_platform job says so rather than passing" \
  'no job applying a compute_platform found at all' "${FIXTURE_DIR}/nostate"

assert_scan "a directory that does not exist is reported, not passed" \
  'no workflow files found under' "${FIXTURE_DIR}/does-not-exist"

assert_scan "a swept file that does not exist is reported, not passed" \
  'swept file not found' "${FIXTURE_DIR}/split" "${FIXTURE_DIR}/scripts/does-not-exist.sh"

# A site running to the end of its file is only flushed once the next file
# starts, so the report has to remember which file the site came from. Reported
# from FILENAME it would name the innocent file scanned next, and every fixture
# above scans one offending file at a time, so none of them can catch it.
cat >"${FIXTURE_DIR}/misattrib/a-bad.yml" <<'EOF'
jobs:
  rollback-aws-fargate:
    steps:
      - name: Rollback with Terraform
        run: |
          printf '%s\nkey = "github-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
          terraform apply -var="compute_platform=fargate"
EOF

cat >"${FIXTURE_DIR}/misattrib/b-innocent.yml" <<'EOF'
jobs:
  summary:
    steps:
      - name: Applies nothing
        run: echo "clean"
EOF

assert_scan "a finding names the file it came from, not the file scanned after it" \
  'a-bad.yml: job "rollback-aws-fargate"' "${FIXTURE_DIR}/misattrib"

# A shell script has no job header, so it is scanned as one site. Nothing under
# scripts/ pairs a key with a platform today; this pins that the scan would see
# it if one did.
mkdir -p "${FIXTURE_DIR}/scripts"
cat >"${FIXTURE_DIR}/scripts/deploy.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\nkey = "github-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
terraform apply -var="compute_platform=fargate"
EOF

assert_scan "a script pairing them wrongly is flagged as a whole file" \
  'deploy.sh: whole file: compute_platform=fargate applies into the github-<env>/ (lambda) state namespace' \
  "${FIXTURE_DIR}/split" "${FIXTURE_DIR}/scripts/deploy.sh"

# --- build_swept_scripts reaches nested directories --------------------------
#
# The helper is shared with test-ecr-delete-selection.sh and
# test-rds-deletion-protection-scope.sh, and until this was fixed it globbed
# `$dir/*.sh` and `$dir/lib/*.sh` only. That names two directories the same way
# naming files would: a script at `scripts/aws/rollback.sh` was invisible to all
# three suites while each reported coverage of every script under `scripts/`.
#
# Asserted on a fixture tree rather than on the real `scripts/`, which has no
# nested `*.sh` today: the recursion has to be proven where a nested file
# actually exists, or the assertion passes for a directory that could not have
# failed it.
mkdir -p "${FIXTURE_DIR}/tree/lib" "${FIXTURE_DIR}/tree/aws/deep"
: >"${FIXTURE_DIR}/tree/top.sh"
: >"${FIXTURE_DIR}/tree/lib/helper.sh"
: >"${FIXTURE_DIR}/tree/aws/rollback.sh"
: >"${FIXTURE_DIR}/tree/aws/deep/nested.sh"
: >"${FIXTURE_DIR}/tree/aws/not-a-script.txt"
: >"${FIXTURE_DIR}/tree/test-ecr-delete-selection.sh"

build_swept_scripts "${FIXTURE_DIR}/tree"
swept_list="$(printf '%s\n' "${SWEPT_SCRIPTS[@]}" | sed "s#^${FIXTURE_DIR}/tree/##" | sort | tr '\n' ' ')"
expected_list="aws/deep/nested.sh aws/rollback.sh lib/helper.sh top.sh "

if [[ "$swept_list" == "$expected_list" ]]; then
  note_pass "build_swept_scripts reaches nested directories and still excludes the guard suites"
else
  note_fail "build_swept_scripts did not sweep the expected set"
  echo "      expected: ${expected_list}"
  echo "      actual:   ${swept_list}"
fi

# The nested script is not merely discovered, it is actually scanned: discovery
# that does not reach the scanner is the same gap one step later.
cat >"${FIXTURE_DIR}/tree/aws/rollback.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\nkey = "github-%s/terraform.tfstate"\n' "$TF_BACKEND" "$ENVIRONMENT" > /tmp/backend.tfbackend
terraform apply -var="compute_platform=fargate"
EOF

build_swept_scripts "${FIXTURE_DIR}/tree"
assert_scan "an invalid pairing in a NESTED script directory is scanned and flagged" \
  'rollback.sh: whole file: compute_platform=fargate applies into the github-<env>/ (lambda) state namespace' \
  "${FIXTURE_DIR}/split" "${SWEPT_SCRIPTS[@]}"

# Restore the real swept set: the assertions above reassigned the global.
build_swept_scripts "$SCRIPT_DIR"

echo
echo "passed: ${pass}, failed: ${fail}"
[[ "$fail" -eq 0 ]]
