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
# Every scope the ARM template grants at must stay inside the subscription that
# `az deployment sub create` targets.
#
# This is a CI drift guard, not a security boundary: anyone who can edit the
# template can edit this script. It exists to stop the grant being widened by
# ACCIDENT, so it is tuned to catch the shapes a well-meaning author actually
# reaches for, and it errs towards refusing anything it cannot reason about.
#
# The allowlist is EXACT-MATCH, not substring-anchored. An earlier revision
# accepted any value containing "/subscriptions/", which is far weaker than it
# reads: "[concat('/subscriptions/', parameters('otherSubscriptionId'))]"
# satisfies it while granting in a subscription the customer never targeted,
# in a template named CrossSubscription. Only two shapes are accepted:
#
#   1. the canonical ARM expression binding the scope to the deployment target
#   2. a bare literal /subscriptions/<guid>, used by the test fixtures
#
# Anything else is rejected, including an escape assembled from a `variables`
# block that this script never reads.
CANONICAL_SCOPE_EXPR="[concat('/subscriptions/', subscription().subscriptionId)]"
LITERAL_SUBSCRIPTION_RE='^/subscriptions/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'

# Retained as defence in depth. The exact-match allowlist above already rejects
# every one of these, but they name the specific scopes that motivated the
# check, and they keep failing loudly if the allowlist is ever loosened.
#
# Escape tokens, all of which denote a scope ABOVE a single subscription:
#   Microsoft.Capacity   -> /providers/Microsoft.Capacity, tenant-wide
#                           reservation orders
#   Microsoft.Management -> /providers/Microsoft.Management/managementGroups/*
#   managementGroups     -> same, matched independently of the provider spelling
#   Microsoft.Billing    -> /providers/Microsoft.Billing/billingAccounts/*
ESCAPE_TOKENS='Microsoft\.Capacity|Microsoft\.Management|managementGroups|Microsoft\.Billing'

# The deployment scope is an invariant of this template, not a detail: the
# three role assignments carry no `scope` property and therefore inherit it.
# Repointing $schema at the management-group template would silently land all
# of them at management-group scope, covering every child subscription, without
# changing a single scope string. Pin it explicitly.
if ! jq -e '.["$schema"] | test("subscriptionDeploymentTemplate")' "$ARM_FILE" >/dev/null 2>&1; then
  ACTUAL_SCHEMA=$(jq -r '.["$schema"] // "<absent>"' "$ARM_FILE")
  echo "ERROR: ARM template is not a subscription-scoped deployment." >&2
  echo "       \$schema: ${ACTUAL_SCHEMA}" >&2
  echo "       Role assignments here carry no explicit scope, so they inherit the" >&2
  echo "       deployment scope. A management-group or tenant schema would widen" >&2
  echo "       every one of them without changing a scope string (issue #1545)." >&2
  exit 1
fi

# A nested deployment can carry an inner template with its own role
# assignments, at its own scope. That is the idiomatic ARM way to assign at a
# different scope from a subscription deployment, so it is exactly what a
# future author with a legitimate cross-scope need would reach for. This script
# cannot reason about inner templates, so refuse rather than pass them silently.
NESTED_COUNT=$(
  jq '[.. | objects | select(has("type")) | select((.type|type) == "string")
       | select((.type|ascii_downcase) == "microsoft.resources/deployments")] | length' "$ARM_FILE"
)
if [[ "$NESTED_COUNT" != "0" ]]; then
  echo "ERROR: ARM template contains ${NESTED_COUNT} nested deployment(s)." >&2
  echo "       This check cannot inspect the scopes inside a nested template, so a" >&2
  echo "       tenant-scoped assignment could hide there (issue #1545). Either" >&2
  echo "       inline the resources, or extend this script to recurse into" >&2
  echo "       properties.template before adding one." >&2
  exit 1
fi

# Collect every scope value the template grants at, one per line, tagged with
# where it came from so the error message points at the right JSON node.
#
# The walk is recursive (`..`) rather than over the top-level `resources` array
# only, so an assignment nested inside a parent resource's own `resources`
# array is still seen. Type matching is case-insensitive because ARM resource
# types are, while jq's `==` is not.
SCOPES=$(
  jq -r '
    [.. | objects | select(has("type")) | select((.type|type) == "string")] as $all
    | ( $all[]
        | select((.type|ascii_downcase) == "microsoft.authorization/roledefinitions")
        | (.properties.assignableScopes // [])[]
        | "assignableScopes\t" + . ),
      ( $all[]
        | select((.type|ascii_downcase) == "microsoft.authorization/roleassignments")
        | select(has("scope"))
        | "roleAssignment.scope\t" + (.scope|tostring) )
  ' "$ARM_FILE"
)

if [[ -z "$SCOPES" ]]; then
  echo "ERROR: No assignableScopes found in ARM template: $ARM_FILE" >&2
  echo "       Expected a Microsoft.Authorization/roleDefinitions resource with" >&2
  echo "       a properties.assignableScopes array." >&2
  exit 1
fi

# Azure provider namespaces and ARM function names are case-insensitive, so
# "/providers/microsoft.capacity" is a fully functional tenant scope. Match
# case-insensitively or lowercasing alone would defeat every check below.
SCOPE_VIOLATIONS=""
shopt -s nocasematch
while IFS=$'\t' read -r origin value; do
  [[ -z "$origin" ]] && continue
  reason=""
  if [[ "$origin" == "roleAssignment.scope" ]]; then
    # Every assignment in this template inherits the deployment scope. An
    # explicit `scope` is how #1545 shipped, and there is no legitimate use
    # for one here.
    reason="role assignments must inherit the deployment scope, not set one"
  elif [[ "$value" == "$CANONICAL_SCOPE_EXPR" ]]; then
    :
  elif [[ "$value" =~ $LITERAL_SUBSCRIPTION_RE ]]; then
    :
  elif [[ "$value" =~ $ESCAPE_TOKENS ]]; then
    reason="names a scope above the subscription"
  else
    reason="not the canonical subscription scope"
  fi
  if [[ -n "$reason" ]]; then
    SCOPE_VIOLATIONS+="  ${origin}: ${value}"$'\n'"      -> ${reason}"$'\n'
  fi
done <<< "$SCOPES"
shopt -u nocasematch

if [[ -n "$SCOPE_VIOLATIONS" ]]; then
  echo "ERROR: ARM template grants outside the onboarded subscription." >&2
  echo "" >&2
  echo "  ARM source: $ARM_FILE" >&2
  echo "" >&2
  printf '%s' "$SCOPE_VIOLATIONS" >&2
  echo "" >&2
  echo "assignableScopes must be exactly:" >&2
  echo "  ${CANONICAL_SCOPE_EXPR}" >&2
  echo "and role assignments must carry no explicit scope. A grant at a tenant," >&2
  echo "management-group or billing-account scope, or at another subscription," >&2
  echo "reaches subscriptions the customer never onboarded (issue #1545). If a" >&2
  echo "wider grant is genuinely required it must be a separate, manually" >&2
  echo "applied, explicitly consented step, never part of this template." >&2
  echo "See known-issues.md." >&2
  exit 1
fi

# The TF module offers include_capacity_provider_scope as an opt-in escape
# hatch. It must stay default-false, otherwise the TF path silently reacquires
# the tenant-wide grant this check removes from the ARM path.
#
# Fails closed: if the flag is referenced but its default cannot be read, that
# is a failure, not a skip. Gating this on `-f variables.tf` previously let the
# whole assertion vanish when the variable moved or the file was absent.
if grep -q 'include_capacity_provider_scope' "$TF_FILE"; then
  TF_VARS_FILE="$(dirname "$TF_FILE")/variables.tf"
  if [[ ! -f "$TF_VARS_FILE" ]]; then
    echo "ERROR: $TF_FILE references include_capacity_provider_scope but" >&2
    echo "       $TF_VARS_FILE does not exist, so its default cannot be checked." >&2
    echo "       Point this check at wherever the variable now lives; do not let" >&2
    echo "       the default-false assertion silently disappear (issue #1545)." >&2
    exit 1
  fi
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

SCOPE_COUNT=$(echo "$SCOPES" | grep -c . || true)
echo "OK: all ${SCOPE_COUNT} ARM grant scopes are subscription-anchored."
exit 0
