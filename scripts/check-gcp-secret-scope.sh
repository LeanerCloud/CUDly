#!/usr/bin/env bash
# check-gcp-secret-scope.sh
#
# Fails when any Terraform file grants a Secret Manager role at project, folder
# or organization scope. Those scopes hand the member EVERY secret in the scope,
# so a workload that needs one secret ends up able to read the credential
# encryption key, the JWT/session secrets and the SendGrid API key alongside it.
#
# The supported pattern is a per-secret binding instead:
#
#   resource "google_secret_manager_secret_iam_member" "..." {
#     project   = var.project_id
#     secret_id = var.some_secret_id
#     role      = "roles/secretmanager.secretAccessor"
#     member    = "serviceAccount:${...}"
#   }
#
# This guard exists because compute/gcp/cloud-run was migrated to per-secret
# bindings while its sibling compute/gcp/cleanup-function was left on a
# project-wide grant for as long as it took someone to notice (issue #1614).
#
# Exit 0 = no DIRECTLY EXPRESSED scope-wide Secret Manager grant found. Read the
#          limitations below before treating this as "the tree is clean".
# Exit 1 = at least one found; each is printed to stderr.
# Exit 2 = usage error.
#
# LIMITATIONS. This is a textual guard, not a policy engine. It matches a literal
# `roles/secretmanager.*` appearing inside a scope-wide IAM resource block, and
# only that. Exit 0 means "nothing of that exact shape", NOT "no scope-wide grant
# exists". Known blind spots, each tracked:
#
#   - Roles reaching the resource through `locals` + `for_each` (issue #1686).
#     There is a live instance today:
#     terraform/environments/gcp/ci-cd-permissions/service_account.tf grants
#     project-scope `roles/secretmanager.admin` to the Terraform deploy service
#     account through `local.deploy_roles`, and this guard exits 0 on it. That
#     grant is believed legitimate (the deploy SA creates secrets); it is called
#     out here so nobody reads a green run as proof of its absence.
#   - `google_project_iam_policy` fed by a `data "google_iam_policy"` block, and
#     resource bodies not indented the way `terraform fmt` produces (issue #1687).
#   - The Terraform JSON encoding (`.tf.json`) is not parsed. Rather than report
#     such a file as clean, the guard exits 2 if it finds one. None exist today.
#   - Any role that is only knowable after variable resolution
#     (`role = var.some_role`, string interpolation).
#
# It is a ratchet against the #1614 regression being reintroduced by copy-paste,
# which is how it arrived the first time. It is not a proof of absence.
#
# Usage:
#   scripts/check-gcp-secret-scope.sh              # scan the default roots
#   scripts/check-gcp-secret-scope.sh FILE...      # scan exactly these files
#
# Passing explicit files is how the test harness points the check at fixtures
# without touching the real sources.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Every directory holding Terraform this guard should cover. `iac/` carries the
# customer-facing federation modules, which are exactly the kind of thing that
# must not ship a scope-wide grant; scanning only `terraform/` missed all 20 of
# its .tf files.
SCAN_ROOTS=(
  "${REPO_ROOT}/terraform"
  "${REPO_ROOT}/iac"
)

# Resources that bind an IAM role across a whole project/folder/organization.
# `_member` adds one principal, `_binding` replaces the whole principal list;
# both are scope-wide and both are wrong for Secret Manager here.
SCOPE_WIDE_RESOURCES='google_(project|folder|organization)_iam_(member|binding)'

files=()
if [[ $# -gt 0 ]]; then
  for arg in "$@"; do
    case "$arg" in
      -*) echo "Unknown flag: $arg" >&2; exit 2 ;;
    esac
    if [[ ! -f "$arg" ]]; then
      echo "ERROR: file not found: $arg" >&2
      exit 2
    fi
    files+=("$arg")
  done
else
  for root in "${SCAN_ROOTS[@]}"; do
    if [[ ! -d "$root" ]]; then
      echo "ERROR: scan root not found: $root" >&2
      exit 2
    fi
    # NUL-delimited so paths with spaces survive. `.tf.json` is collected so it
    # can be REJECTED below, not because it can be parsed: the scanner below
    # matches HCL block syntax, and silently returning "clean" for a file it
    # cannot read is the exact failure mode this guard exists to prevent.
    while IFS= read -r -d '' f; do
      files+=("$f")
    done < <(find "$root" -type f \( -name '*.tf' -o -name '*.tf.json' \) -print0 | sort -z)
  done
fi

# Fail closed on the JSON encoding rather than under-reporting it. None exist in
# the repo today; if one is ever added, this stops rather than passing it over.
json_files=()
for f in "${files[@]}"; do
  [[ "$f" == *.tf.json ]] && json_files+=("$f")
done
if [[ ${#json_files[@]} -gt 0 ]]; then
  {
    echo "ERROR: this guard cannot parse the Terraform JSON encoding, and will not"
    echo "       report a file it cannot read as clean. Found:"
    printf '         %s\n' "${json_files[@]}"
    echo "       Extend the scanner to handle .tf.json (see issue #1687) before adding one."
  } >&2
  exit 2
fi

if [[ ${#files[@]} -eq 0 ]]; then
  echo "ERROR: no Terraform files to scan" >&2
  exit 2
fi

# Walk each resource block. A block opens on a `resource "<type>" "<name>" {`
# line at column 0 and closes on a `}` at column 0, which `terraform fmt`
# guarantees for top-level blocks. Report a block only when it is BOTH
# scope-wide AND carries a roles/secretmanager.* role, so per-secret bindings
# and non-secret project grants (cloudsql.client, logging, etc.) stay quiet.
violations=$(
  awk -v pattern="^resource[[:space:]]+\"${SCOPE_WIDE_RESOURCES}\"" '
    $0 ~ pattern {
      in_block = 1
      header = $0
      header_line = FNR
      role = ""
      next
    }
    in_block && /roles\/secretmanager\./ {
      role = $0
      sub(/^[[:space:]]+/, "", role)
    }
    in_block && /^}/ {
      if (role != "") {
        printf "%s:%d: %s\n      %s\n", FILENAME, header_line, header, role
      }
      in_block = 0
      role = ""
    }
  ' "${files[@]}"
)

if [[ -n "$violations" ]]; then
  {
    echo "FAILED: scope-wide Secret Manager grant(s) found."
    echo ""
    echo "$violations"
    echo ""
    echo "A project/folder/organization-scoped Secret Manager role grants the member"
    echo "every secret in that scope. Replace it with a per-secret binding:"
    echo ""
    echo "  resource \"google_secret_manager_secret_iam_member\" \"<name>\" {"
    echo "    project   = var.project_id"
    echo "    secret_id = var.<the_one_secret_this_workload_reads>"
    echo "    role      = \"roles/secretmanager.secretAccessor\""
    echo "    member    = \"serviceAccount:\${...}\""
    echo "  }"
    echo ""
    echo "See terraform/modules/compute/gcp/cloud-run/main.tf for the reference shape."
  } >&2
  exit 1
fi

echo "OK: no directly expressed project/folder/organization-scoped Secret Manager" \
     "role found in ${#files[@]} Terraform file(s). See the LIMITATIONS header:" \
     "this does not cover roles reaching a resource via locals/for_each (#1686)" \
     "or via a google_iam_policy data block (#1687)."
