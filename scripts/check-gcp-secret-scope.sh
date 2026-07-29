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
# Exit 0 = no scope-wide Secret Manager grants found.
# Exit 1 = at least one found; each is printed to stderr.
# Exit 2 = usage error.
#
# Limitation: this is a textual guard, not a policy engine. It matches a literal
# `roles/secretmanager.*` inside a scope-wide IAM resource block, so a role
# supplied indirectly (`role = var.some_role`, or built by string interpolation)
# is invisible to it. It is a ratchet against the specific regression in #1614
# being reintroduced by copy-paste, which is how it arrived the first time; it
# is not a proof that no scope-wide grant can exist.
#
# Usage:
#   scripts/check-gcp-secret-scope.sh              # scan terraform/
#   scripts/check-gcp-secret-scope.sh FILE...      # scan exactly these files
#
# Passing explicit files is how the test harness points the check at fixtures
# without touching the real sources.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCAN_ROOT="${REPO_ROOT}/terraform"

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
  if [[ ! -d "$SCAN_ROOT" ]]; then
    echo "ERROR: scan root not found: $SCAN_ROOT" >&2
    exit 2
  fi
  # NUL-delimited so paths with spaces survive.
  while IFS= read -r -d '' f; do
    files+=("$f")
  done < <(find "$SCAN_ROOT" -type f -name '*.tf' -print0 | sort -z)
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

echo "OK: no project/folder/organization-scoped Secret Manager grants in ${#files[@]} Terraform file(s)."
