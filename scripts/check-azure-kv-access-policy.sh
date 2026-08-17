#!/usr/bin/env bash
# check-azure-kv-access-policy.sh
#
# Fails when any Terraform file declares a Key Vault access-policy grant, in
# either of the two forms HCL offers: a top-level
# `azurerm_key_vault_access_policy` resource, or an `access_policy` block nested
# inside an `azurerm_key_vault` resource (including the `dynamic` form).
#
# WHY A BLANKET BAN IS CORRECT HERE. This repo provisions exactly one Key Vault
# (terraform/modules/secrets/azure/main.tf) and it hardcodes
# `enable_rbac_authorization = true` as a literal, not a variable. An
# RBAC-enabled vault NEVER consults its accessPolicies array: data-plane
# authorization comes from Azure RBAC role assignments and nothing else.
#
# The failure mode this guards is that Azure's control plane happily ACCEPTS the
# access-policy write, so `terraform apply` reports success with no warning and
# the grant is silently inert. It surfaces only as a runtime 403, which is the
# worst possible place to discover it. That is exactly what happened to the
# Azure cleanup-function and the AKS workload identity (issue #1621) while every
# other consumer in the tree already used `azurerm_role_assignment`.
#
# The supported pattern is:
#
#   resource "azurerm_role_assignment" "..." {
#     scope                = var.key_vault_id
#     role_definition_name = "Key Vault Secrets User"
#     principal_id         = <identity principal id>
#   }
#
# Pick the role by mapping the access-policy permissions you would have written:
#   secret_permissions Get/List          -> "Key Vault Secrets User"
#   secret_permissions incl. Set/Delete  -> "Key Vault Secrets Officer"
#   key_permissions Sign/Get             -> "Key Vault Crypto User"
# Verify the role exists before using it (`az role definition list --name ...`);
# a matching string in a sibling file is not evidence that a role or an action
# is real (issue #1794).
#
# Exit 0 = no access-policy grant declared, in either form.
# Exit 1 = at least one found; each is printed to stderr.
# Exit 2 = usage error, or a file this scanner cannot read.
#
# LIMITATIONS. This is a textual guard, not a policy engine. It matches block
# headers at any indentation: a `resource "azurerm_key_vault_access_policy"`
# header (the form the #1621 defect arrived in) and a nested `access_policy` or
# `dynamic "access_policy"` header (the second way to express the same inert
# grant). The nested header must carry its opening brace on the same line, which
# is what HCL requires of a real block but not of an inline comment wedged before
# it (`access_policy /* c */ {`); that contrived spelling is out of scope, since
# the defect this guards against is an accidental grant, not an obfuscated one.
# The Terraform JSON encoding (`.tf.json`) is not parsed; rather than
# report such a file as clean, the guard exits 2. None exist today.
#
# Usage:
#   scripts/check-azure-kv-access-policy.sh              # scan the default roots
#   scripts/check-azure-kv-access-policy.sh FILE...      # scan exactly these files
#
# Passing explicit files is how the test harness points the check at fixtures
# without touching the real sources.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Every directory holding Terraform this guard should cover. `iac/` carries the
# customer-facing federation modules; they do not provision a vault today, but
# scanning only `terraform/` would let one arrive there unnoticed.
SCAN_ROOTS=(
  "${REPO_ROOT}/terraform"
  "${REPO_ROOT}/iac"
)

BANNED_RESOURCE='azurerm_key_vault_access_policy'

# The nested form: an `access_policy { ... }` block inside an azurerm_key_vault
# resource, or its `dynamic "access_policy"` equivalent.
BANNED_BLOCK='access_policy'

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
    # can be REJECTED below, not because it can be parsed: silently returning
    # "clean" for a file it cannot read is the exact failure mode this guard
    # exists to prevent.
    while IFS= read -r -d '' f; do
      files+=("$f")
    done < <(find "$root" -type f \( -name '*.tf' -o -name '*.tf.json' \) -print0 | sort -z)
  done
fi

# Fail closed on the JSON encoding rather than under-reporting it.
json_files=()
for f in "${files[@]}"; do
  [[ "$f" == *.tf.json ]] && json_files+=("$f")
done
if [[ ${#json_files[@]} -gt 0 ]]; then
  {
    echo "ERROR: this guard cannot parse the Terraform JSON encoding, and will not"
    echo "       report a file it cannot read as clean. Found:"
    printf '         %s\n' "${json_files[@]}"
    echo "       Extend the scanner to handle .tf.json before adding one."
  } >&2
  exit 2
fi

if [[ ${#files[@]} -eq 0 ]]; then
  echo "ERROR: no Terraform files to scan" >&2
  exit 2
fi

# Match both block headers, tolerating leading whitespace and not requiring
# whitespace between tokens: `resource"azurerm_key_vault_access_policy""x"{` is
# valid HCL. The nested form is necessarily indented, and the top-level form is
# normalized to column 0 by the pre-commit `terraform_fmt` hook, which covers
# every .tf file in the repo (both scan roots) because CI runs `pre-commit run
# --all-files` in .github/workflows/pre-commit.yml. The guard does not lean on
# that gate: tolerating indentation means narrowing it cannot open a bypass.
# Anchoring on `resource` still means a mention of the type inside a comment
# (this file's own guidance, for one) does not trip the guard.
#
# The nested axis matches the COMPLETE block header, up to and including the
# opening brace, rather than the `access_policy` token alone. A bare prefix also
# fires on any longer identifier that starts with it (`access_policy_enabled`)
# and on an attribute assignment (`access_policy = "metadata"`, or the
# `access_policy.value` reference inside a dynamic block's content), none of
# which grant anything. Requiring the brace does not let a real block hide by
# wrapping: hclsyntax rejects a block whose body does not open on the header
# line ("Argument or block definition required"), so the header cannot be split
# across two lines to evade this. It does narrow the guard by the one spelling
# noted under LIMITATIONS above.
violations=$(
  awk \
    -v resource_pattern="^[[:space:]]*resource[[:space:]]*\"${BANNED_RESOURCE}\"" \
    -v block_pattern="^[[:space:]]*(${BANNED_BLOCK}|dynamic[[:space:]]*\"${BANNED_BLOCK}\")[[:space:]]*[{]" '
    $0 ~ resource_pattern || $0 ~ block_pattern {
      printf "%s:%d: %s\n", FILENAME, FNR, $0
    }
  ' "${files[@]}"
)

if [[ -n "$violations" ]]; then
  {
    echo "FAILED: Key Vault access-policy grant declared."
    echo ""
    echo "$violations"
    echo ""
    echo "This project's Key Vault sets enable_rbac_authorization = true"
    echo "(terraform/modules/secrets/azure/main.tf). An RBAC-enabled vault ignores"
    echo "access policies entirely, so the grant above applies cleanly and then"
    echo "does nothing at runtime. Use an RBAC role assignment instead:"
    echo ""
    echo "  resource \"azurerm_role_assignment\" \"<name>\" {"
    echo "    scope                = var.key_vault_id"
    echo "    role_definition_name = \"Key Vault Secrets User\"  # Get + List"
    echo "    principal_id         = <identity principal id>"
    echo "  }"
    echo ""
    echo "See terraform/modules/compute/azure/container-apps/scheduled-tasks.tf"
    echo "for the reference shape, and confirm any role name you pick actually"
    echo "exists with: az role definition list --name \"<role>\""
  } >&2
  exit 1
fi

echo "OK: no Key Vault access-policy grant declared in ${#files[@]} Terraform file(s)."
