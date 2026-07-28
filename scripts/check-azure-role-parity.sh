#!/usr/bin/env bash
# check-azure-role-parity.sh
#
# Asserts that the Azure custom role stays in parity across both sources of
# truth, on two axes:
#
#   1. ACTIONS: the permission list is identical (case-insensitively).
#   2. SCOPE:   no grant escapes the subscription being onboarded.
#
#   TF module : terraform/modules/iam/azure/cudly-reservation-role/main.tf
#   ARM template: arm/CUDly-CrossSubscription/template.json
#
# The scope axis exists because the actions axis alone did not catch issue
# #1545: the ARM template assigned the (correct) actions at the tenant-wide
# "/providers/Microsoft.Capacity" scope, which covers every reservation order
# in the Azure AD tenant (including subscriptions the customer never
# onboarded), while the TF module deliberately granted subscription scope
# only. Both files agreed on actions, so the parity gate stayed green.
#
# Exit 0 = actions match and every scope is subscription-anchored.
# Exit 1 = drift; the offending values are printed to stderr.
#
# Usage:
#   scripts/check-azure-role-parity.sh [--tf-file <path>] [--arm-file <path>]
#
# The --tf-file / --arm-file flags let the test harness substitute fixture files
# without touching the real sources.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

TF_FILE="${REPO_ROOT}/terraform/modules/iam/azure/cudly-reservation-role/main.tf"
ARM_FILE="${REPO_ROOT}/arm/CUDly-CrossSubscription/template.json"

# Allow override via flags (used by the test harness).
while [[ $# -gt 0 ]]; do
  case "$1" in
    --tf-file)  TF_FILE="$2";  shift 2 ;;
    --arm-file) ARM_FILE="$2"; shift 2 ;;
    *) echo "Unknown flag: $1" >&2; exit 2 ;;
  esac
done

# --- validate inputs ---------------------------------------------------------

if [[ ! -f "$TF_FILE" ]]; then
  echo "ERROR: TF module not found: $TF_FILE" >&2
  echo "       Has terraform/modules/iam/azure/cudly-reservation-role/ been created?" >&2
  exit 1
fi

if [[ ! -f "$ARM_FILE" ]]; then
  echo "ERROR: ARM template not found: $ARM_FILE" >&2
  exit 1
fi

# --- extract actions from TF -------------------------------------------------
# Match lines inside the `actions = [ ... ]` block of the azurerm_role_definition
# resource and extract the quoted string values.

TF_ACTIONS=$(
  awk '
    /^[[:space:]]*permissions[[:space:]]*\{/ { in_perms=1 }
    in_perms && /^[[:space:]]*actions[[:space:]]*=/ { in_actions=1; next }
    in_actions && /^[[:space:]]*\]/ { in_actions=0; in_perms=0; next }
    in_actions {
      # Strip leading/trailing whitespace, quotes, and trailing commas.
      gsub(/^[[:space:]"]+|[",[:space:]]+$/, "")
      if (length($0) > 0) print tolower($0)
    }
  ' "$TF_FILE" | sort
)

if [[ -z "$TF_ACTIONS" ]]; then
  echo "ERROR: No actions extracted from TF module: $TF_FILE" >&2
  echo "       Check that the file contains a permissions { actions = [...] } block." >&2
  exit 1
fi

# --- extract actions from ARM JSON -------------------------------------------
# Pull .resources[] where .type == "Microsoft.Authorization/roleDefinitions",
# then walk into .properties.permissions[0].actions.

if ! command -v jq &>/dev/null; then
  echo "ERROR: jq is required but not installed." >&2
  exit 2
fi

ARM_ACTIONS=$(
  jq -r '
    .resources[]
    | select(.type == "Microsoft.Authorization/roleDefinitions")
    | .properties.permissions[0].actions[]
    | ascii_downcase
  ' "$ARM_FILE" | sort
)

if [[ -z "$ARM_ACTIONS" ]]; then
  echo "ERROR: No actions extracted from ARM template: $ARM_FILE" >&2
  echo "       Check that the file contains a Microsoft.Authorization/roleDefinitions resource." >&2
  exit 1
fi

# --- compare -----------------------------------------------------------------

DIFF=$(diff <(echo "$TF_ACTIONS") <(echo "$ARM_ACTIONS") || true)

if [[ -n "$DIFF" ]]; then
  echo "ERROR: ARM template and TF module actions lists differ." >&2
  echo "" >&2
  echo "  TF source : $TF_FILE" >&2
  echo "  ARM source: $ARM_FILE" >&2
  echo "" >&2
  echo "Diff (< TF  > ARM):" >&2
  echo "$DIFF" >&2
  echo "" >&2
  echo "Update the lagging file so both lists match." >&2
  exit 1
fi

echo "OK: ARM and TF actions lists match (${#TF_ACTIONS} bytes, case-insensitive)."

# --- scope invariant (issue #1545) -------------------------------------------
# Every scope the ARM template grants at (both the role definition's
# assignableScopes and any explicit `scope` on a role assignment) must stay
# inside the subscription that `az deployment sub create` targets.
#
# The rule is allowlist-anchored rather than a denylist of bad strings: a value
# is accepted only if it visibly contains "/subscriptions/", so anything that is
# not plainly subscription-anchored ("/", a management-group path, a bare
# tenant-level provider path) is rejected by default. The extra token denylist
# then catches escape scopes that would otherwise smuggle "/subscriptions/"
# past the anchor, and catches ARM expressions that assemble an escape scope
# piecewise (e.g. "[concat('/providers/', 'Microsoft.Capacity')]") instead of
# writing it as one literal.
#
# Escape tokens, all of which denote a scope ABOVE a single subscription:
#   Microsoft.Capacity   -> /providers/Microsoft.Capacity, tenant-wide
#                           reservation orders
#   Microsoft.Management -> /providers/Microsoft.Management/managementGroups/*
#   managementGroups     -> same, matched independently of the provider spelling
#   Microsoft.Billing    -> /providers/Microsoft.Billing/billingAccounts/*
ESCAPE_TOKENS='Microsoft\.Capacity|Microsoft\.Management|managementGroups|Microsoft\.Billing'

# Collect every scope value the template grants at, one per line, tagged with
# where it came from so the error message points at the right JSON node.
SCOPES=$(
  jq -r '
    ( .resources[]
      | select(.type == "Microsoft.Authorization/roleDefinitions")
      | .properties.assignableScopes[]
      | "assignableScopes\t" + . ),
    ( .resources[]
      | select(.type == "Microsoft.Authorization/roleAssignments")
      | select(has("scope"))
      | "roleAssignment.scope\t" + .scope )
  ' "$ARM_FILE"
)

if [[ -z "$SCOPES" ]]; then
  echo "ERROR: No assignableScopes found in ARM template: $ARM_FILE" >&2
  echo "       Expected a Microsoft.Authorization/roleDefinitions resource with" >&2
  echo "       a properties.assignableScopes array." >&2
  exit 1
fi

SCOPE_VIOLATIONS=""
while IFS=$'\t' read -r origin value; do
  [[ -z "$origin" ]] && continue
  reason=""
  if [[ ! "$value" == */subscriptions/* ]]; then
    reason="not anchored to /subscriptions/"
  elif [[ "$value" =~ $ESCAPE_TOKENS ]]; then
    reason="contains a scope token above the subscription"
  fi
  if [[ -n "$reason" ]]; then
    SCOPE_VIOLATIONS+="  ${origin}: ${value}"$'\n'"      -> ${reason}"$'\n'
  fi
done <<< "$SCOPES"

if [[ -n "$SCOPE_VIOLATIONS" ]]; then
  echo "ERROR: ARM template grants outside the onboarded subscription." >&2
  echo "" >&2
  echo "  ARM source: $ARM_FILE" >&2
  echo "" >&2
  printf '%s' "$SCOPE_VIOLATIONS" >&2
  echo "" >&2
  echo "Every scope must be derived from subscription().subscriptionId. A grant at" >&2
  echo "a tenant, management-group or billing-account scope reaches subscriptions" >&2
  echo "the customer never onboarded (issue #1545). If a wider grant is genuinely" >&2
  echo "required it must be a separate, manually applied, explicitly consented" >&2
  echo "step, never part of this template. See known-issues.md." >&2
  exit 1
fi

# The TF module offers include_capacity_provider_scope as an opt-in escape
# hatch. It must stay default-false, otherwise the TF path silently reacquires
# the tenant-wide grant this check removes from the ARM path.
if grep -q 'include_capacity_provider_scope' "$TF_FILE"; then
  TF_VARS_FILE="$(dirname "$TF_FILE")/variables.tf"
  if [[ -f "$TF_VARS_FILE" ]]; then
    CAPACITY_DEFAULT=$(
      awk '
        /^variable[[:space:]]+"include_capacity_provider_scope"/ { in_var=1 }
        in_var && /^[[:space:]]*default[[:space:]]*=/ {
          gsub(/^[[:space:]]*default[[:space:]]*=[[:space:]]*|[[:space:]]*$/, "")
          print; exit
        }
        in_var && /^\}/ { exit }
      ' "$TF_VARS_FILE"
    )
    if [[ "$CAPACITY_DEFAULT" != "false" ]]; then
      echo "ERROR: include_capacity_provider_scope must default to false." >&2
      echo "       Found default: ${CAPACITY_DEFAULT:-<none>} in $TF_VARS_FILE" >&2
      echo "       A true default grants the tenant-wide /providers/Microsoft.Capacity" >&2
      echo "       scope to every consumer of the module (issue #1545)." >&2
      exit 1
    fi
  fi
fi

SCOPE_COUNT=$(echo "$SCOPES" | grep -c . || true)
echo "OK: all ${SCOPE_COUNT} ARM grant scopes are subscription-anchored."
exit 0
