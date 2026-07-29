#!/usr/bin/env bash
# check-azure-role-parity.sh
#
# Asserts that the Azure custom role stays in parity across both sources of
# truth, on two axes:
#
#   1. ACTIONS: the permission lists (actions, notActions, dataActions,
#      notDataActions) are identical (case-insensitively).
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

if ! command -v jq &>/dev/null; then
  echo "ERROR: jq is required but not installed." >&2
  exit 2
fi

# --- normalize ARM property-key casing ---------------------------------------
# Azure Resource Manager's resource-provider JSON deserializers are documented
# as case-insensitive by default, so a resource typed correctly but with a
# miscased property key -- "Scope" instead of "scope", "RoleDefinitionId",
# "Properties", "AssignableScopes" -- was invisible to every jq query below:
# silence, not refusal. The first of those is issue #1545 byte-for-byte apart
# from one capital letter.
#
# Whether ARM itself is genuinely lenient about a specific miscased key (as
# opposed to accepting a template at all) was not independently verified
# against a live subscription -- that would require deploying a miscased
# template to a real tenant, which was not done. Fixing this fail-closed
# regardless is still correct either way: if ARM does accept the miscased
# key, this closes a real hole; if it does not, the guard is merely redundant
# with a deploy-time rejection, never wrong.
#
# Lowercase every object key once, into a scratch copy, and query that copy
# from here on. Every jq field access below that names a non-lowercase ARM
# property (roleDefinitionId, assignableScopes, notActions, dataActions,
# notDataActions) is written in lowercase to match; `type`, `properties`,
# `permissions`, `scope`, `resources`, and `actions` are already all-lowercase
# in correct ARM spelling, so normalizing them is a no-op either way.
ARM_FILE_NORM="$(mktemp)"
trap 'rm -f "$ARM_FILE_NORM"' EXIT
jq 'walk(if type == "object" then with_entries(.key |= ascii_downcase) else . end)' \
  "$ARM_FILE" > "$ARM_FILE_NORM"

# --- extract permission lists from TF -----------------------------------------
# Pulls each of actions / not_actions / data_actions / not_data_actions out of
# the `permissions { ... }` block of the azurerm_role_definition resource.
# Handles both the multi-line `attr = [ ... ]` form and the single-line
# `attr = []` empty form. `in_perms` stays set for the whole permissions block
# (reset only on the block's own closing brace), not on the first list's
# closing bracket, so an attribute appearing after `actions` in the same block
# is still seen. An attribute that never appears extracts as empty, which
# matches the azurerm provider's own default for data_actions/not_data_actions.
extract_tf_list() {
  local attr="$1" file="$2"
  awk -v attr="$attr" '
    /^[[:space:]]*permissions[[:space:]]*\{/ { in_perms=1; next }
    in_perms && $0 ~ ("^[[:space:]]*" attr "[[:space:]]*=[[:space:]]*\\[[[:space:]]*\\][[:space:]]*$") { next }
    in_perms && $0 ~ ("^[[:space:]]*" attr "[[:space:]]*=") { in_list=1; next }
    in_list && /^[[:space:]]*\]/ { in_list=0; next }
    in_list {
      gsub(/^[[:space:]"]+|[",[:space:]]+$/, "")
      if (length($0) > 0) print tolower($0)
    }
    in_perms && /^[[:space:]]*\}/ { in_perms=0 }
  ' "$file" | sort -u
}

TF_ACTIONS=$(extract_tf_list "actions" "$TF_FILE")
TF_NOT_ACTIONS=$(extract_tf_list "not_actions" "$TF_FILE")
TF_DATA_ACTIONS=$(extract_tf_list "data_actions" "$TF_FILE")
TF_NOT_DATA_ACTIONS=$(extract_tf_list "not_data_actions" "$TF_FILE")

if [[ -z "$TF_ACTIONS" ]]; then
  echo "ERROR: No actions extracted from TF module: $TF_FILE" >&2
  echo "       Check that the file contains a permissions { actions = [...] } block." >&2
  exit 1
fi

# --- extract permission lists from ARM JSON ------------------------------------
# Walks every Microsoft.Authorization/roleDefinitions resource under the
# template's `resources` array (`.resources // [] | ..`, not just the
# top-level .resources[] array itself), matching the resource type
# case-insensitively since ARM resource types are. A prior revision matched
# only the top-level array with a case-sensitive `==`, so a second role
# definition typed "microsoft.authorization/roleDefinitions" (lowercase) was
# invisible to this axis even though the case-insensitive scope walk below saw
# it fine -- actions and scope disagreeing about how many role definitions
# exist is exactly the kind of drift this check exists to catch.
#
# The walk is rooted at `.resources`, not the whole document (`..` from `.`):
# a `..` from the root would also match a decorative object under `variables`
# or `outputs` that merely happens to carry `"type":
# "Microsoft.Authorization/roleDefinitions"` for documentation purposes but is
# never deployed, and red a template whose actions genuinely match. Rooting at
# `.resources` still finds a role definition nested inside a parent resource's
# own `resources` array (the same reason the scope walk below is recursive),
# it just never leaves the tree of things ARM actually deploys.
#
# Also unions every entry of permissions[], not just permissions[0]: ARM unions
# permissions across the whole array, so a second entry appended after the
# canonical one silently added grants that comparing only index 0 missed.
# actions/notActions/dataActions/notDataActions each default to [] when absent,
# matching ARM's own semantics for an omitted key.
extract_arm_list() {
  local key="$1" file="$2"
  jq -r --arg key "$key" '
    [.resources // [] | .. | objects | select(has("type")) | select((.type|type) == "string")
       | select((.type|ascii_downcase) == "microsoft.authorization/roledefinitions")]
    | map(.properties.permissions // [] | .[] | (.[$key|ascii_downcase] // [])[])
    | flatten
    | .[]
    | ascii_downcase
  ' "$file" | sort -u
}

ARM_ACTIONS=$(extract_arm_list "actions" "$ARM_FILE_NORM")
ARM_NOT_ACTIONS=$(extract_arm_list "notActions" "$ARM_FILE_NORM")
ARM_DATA_ACTIONS=$(extract_arm_list "dataActions" "$ARM_FILE_NORM")
ARM_NOT_DATA_ACTIONS=$(extract_arm_list "notDataActions" "$ARM_FILE_NORM")

if [[ -z "$ARM_ACTIONS" ]]; then
  echo "ERROR: No actions extracted from ARM template: $ARM_FILE" >&2
  echo "       Check that the file contains a Microsoft.Authorization/roleDefinitions resource." >&2
  exit 1
fi

# --- compare -------------------------------------------------------------------

compare_action_lists() {
  local label="$1" tf_list="$2" arm_list="$3"
  local d
  d=$(diff <(echo "$tf_list") <(echo "$arm_list") || true)
  if [[ -n "$d" ]]; then
    echo "ERROR: ARM template and TF module ${label} lists differ." >&2
    echo "" >&2
    echo "  TF source : $TF_FILE" >&2
    echo "  ARM source: $ARM_FILE" >&2
    echo "" >&2
    echo "Diff (< TF  > ARM):" >&2
    echo "$d" >&2
    echo "" >&2
    echo "Update the lagging file so both ${label} lists match." >&2
    exit 1
  fi
}

compare_action_lists "actions"        "$TF_ACTIONS"         "$ARM_ACTIONS"
compare_action_lists "notActions"     "$TF_NOT_ACTIONS"     "$ARM_NOT_ACTIONS"
compare_action_lists "dataActions"    "$TF_DATA_ACTIONS"    "$ARM_DATA_ACTIONS"
compare_action_lists "notDataActions" "$TF_NOT_DATA_ACTIONS" "$ARM_NOT_DATA_ACTIONS"

echo "OK: ARM and TF actions/notActions/dataActions/notDataActions lists match (case-insensitive)."

# --- scope invariant (issue #1545) -------------------------------------------
# Every scope the ARM template grants at must stay inside the subscription that
# `az deployment sub create` targets.
#
# This is a CI drift guard, not a security boundary: anyone who can edit the
# template can edit this script. It exists to stop the grant being widened by
# ACCIDENT, so it is tuned to catch the shapes a well-meaning author actually
# reaches for.
#
# It is NOT a general ARM evaluator, and does not refuse everything it cannot
# reason about: it recognizes a fixed set of resource types
# (Microsoft.Authorization/roleDefinitions, Microsoft.Authorization/
# roleAssignments) and separately refuses outright a second fixed set it
# knows are grant-bearing or opaque and cannot safely inspect (see
# REFUSED_TYPES below). Any OTHER resource type -- including ones nobody has
# thought to add to either list yet -- is not inspected at all and passes
# silently. Closing that gap in general means asserting the template's exact
# expected set of grants, not enumerating everything to refuse; that is
# tracked separately in issue #1681 rather than attempted here.
#
# The allowlist is EXACT-MATCH (after normalization, see normalize_scope_expr
# below), not substring-anchored. An earlier revision accepted any value
# containing "/subscriptions/", which is far weaker than it reads:
# "[concat('/subscriptions/', parameters('otherSubscriptionId'))]" satisfies it
# while granting in a subscription the customer never targeted, in a template
# named CrossSubscription.
#
# A later revision of THIS check also accepted a bare literal
# /subscriptions/<guid>, on the theory that the test fixtures needed one. That
# was never true and the claim was disproved directly: the fixtures were moved
# to the canonical expression below and the full self-test suite still passes.
# Constraining the literal to GUID *shape* accepts any GUID, including one
# hard-coding a foreign subscription -- exactly the cross-subscription
# escalation this check exists to catch (issue #1545), and it slips through
# silently because a foreign literal has the same shape as a legitimate one;
# this script has no way to know the deployment target's own subscription id
# at review time, so it cannot tell them apart. Only the canonical ARM
# expression -- or its `subscription().id` equivalent -- is accepted; a
# literal, however it is spelled, never is.
CANONICAL_SCOPE_EXPR="[concat('/subscriptions/', subscription().subscriptionId)]"
CANONICAL_SCOPE_EXPR_ALT="[subscription().id]"

# Byte-exact comparison against CANONICAL_SCOPE_EXPR is brittle: whitespace
# placement, quote style, and a redundant empty-string concat argument are all
# spellings a well-meaning author could reach for that denote the identical
# runtime value. Normalize both sides before comparing so the guard reasons
# about the expression's meaning, not its formatting -- a pure reformat must
# not be able to red CI, or the guard invites being deleted out of frustration.
#
# Whitespace is collapsed only where it is immediately adjacent to structural
# punctuation ([ ] ( ) ,), never between two ordinary characters. A blanket
# `tr -d '[:space:]'` over the whole expression would also strip whitespace
# INSIDE the '/subscriptions/' string literal, so a typo like
# "'/sub scriptions/'" would normalize to the same text as the real literal --
# tolerating a corrupted value as if it were a reformat of the canonical one.
# It happens not to be exploitable (the corrupted literal doesn't resolve to a
# different, wider scope; it just fails to deploy), but a normalizer's whole
# job is telling "different formatting" apart from "different value", and this
# blurred that line. Punctuation-adjacent collapsing can't reach inside a
# literal's content, because that content by construction contains no
# whitespace next to `[`, `]`, `(`, `)`, or `,`.
#
# Also lowercased as an explicit final step, not left to the ambient
# `shopt -s nocasematch` the comparison happens to run under: ARM function
# names, property accessors, and resource-path segments (subscription IDs,
# provider namespaces) are all documented case-insensitive, the same reason
# resource `type` values are matched via ascii_downcase elsewhere in this
# script. Folding case here explicitly, inside the function whose whole job
# is "normalize equivalent spellings," keeps that guarantee from silently
# depending on unrelated code (the roleDefinitionId loop below, or the
# ESCAPE_TOKENS match) staying inside the same shell-option scope. Safe to
# do unconditionally: unlike whitespace-stripping, lowercasing is a 1:1
# character transform that can't collapse two distinct values into one.
normalize_scope_expr() {
  local v="$1"
  v="$(printf '%s' "$v" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')"  # trim the whole value only
  v="$(printf '%s' "$v" | tr '"' "'")"                                     # quote style is not meaningful
  v="$(printf '%s' "$v" | sed -E '
    s/\[[[:space:]]+/[/g; s/[[:space:]]+\]/]/g;
    s/\([[:space:]]+/(/g; s/[[:space:]]+\)/)/g;
    s/,[[:space:]]+/,/g; s/[[:space:]]+,/,/g;
  ')"
  v="$(printf '%s' "$v" | sed -E "s/,''\)\]\$/)]/")"  # drop a redundant ,'' arg
  printf '%s' "$v" | tr '[:upper:]' '[:lower:]'
}
CANONICAL_SCOPE_NORM="$(normalize_scope_expr "$CANONICAL_SCOPE_EXPR")"
CANONICAL_SCOPE_ALT_NORM="$(normalize_scope_expr "$CANONICAL_SCOPE_EXPR_ALT")"

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

# The three roleDefinitionId expressions this template ever assigns (issue
# #1545, finding F6): the custom purchaser role it defines, and the two
# built-in roles it looks up via the `roles` variable. A role assignment
# binding anything else -- e.g. the built-in Owner role -- while correctly
# inheriting the subscription-scope deployment (no explicit `scope`, so the
# check above finds nothing wrong) would still grant far more than the
# calculatePrice -> purchase flow needs. Compared as raw ARM expression text,
# the same way CANONICAL_SCOPE_EXPR is: this is a drift guard tied to this
# template's own variables, not a general ARM evaluator.
ALLOWED_ROLE_DEFINITION_IDS=(
  "[variables('customRoleDefinitionId')]"
  "[variables('roles').reader]"
  "[variables('roles').costManagementReader]"
)

# The deployment scope is an invariant of this template, not a detail: the
# three role assignments carry no `scope` property and therefore inherit it.
# Repointing $schema at the management-group template would silently land all
# of them at management-group scope, covering every child subscription, without
# changing a single scope string. Pin it explicitly.
if ! jq -e '.["$schema"] | test("subscriptionDeploymentTemplate")' "$ARM_FILE_NORM" >/dev/null 2>&1; then
  ACTUAL_SCHEMA=$(jq -r '.["$schema"] // "<absent>"' "$ARM_FILE_NORM")
  echo "ERROR: ARM template is not a subscription-scoped deployment." >&2
  echo "       \$schema: ${ACTUAL_SCHEMA}" >&2
  echo "       Role assignments here carry no explicit scope, so they inherit the" >&2
  echo "       deployment scope. A management-group or tenant schema would widen" >&2
  echo "       every one of them without changing a scope string (issue #1545)." >&2
  exit 1
fi

# Resource types this check knows it cannot safely inspect, refused outright
# rather than silently passed:
#
#   deployments / deploymentScripts / deploymentStacks -- a nested deployment
#   can carry an inner template with its own role assignments at its own
#   scope (the idiomatic ARM way to assign at a different scope from a
#   subscription deployment), and a deployment script's runtime `az`/az-cli
#   commands can issue role assignments neither one exposes as JSON this
#   check can walk.
#
#   roleEligibilityScheduleRequests / roleAssignmentScheduleRequests -- Azure
#   PIM (Privileged Identity Management) resources. These grant a role the
#   same way a plain roleAssignment does (confirmed: a PIM request binding
#   the built-in Owner role deploys and grants it) but under a completely
#   different property shape this check's roleAssignment selectors never
#   match, so the roleDefinitionId allowlist above is bypassed simply by
#   changing the resource type.
#
#   storageAccounts/providers/roleAssignments -- the legacy ARM spelling for
#   a role assignment as a child resource (a full `.../providers/...` type
#   path) rather than a separate top-level roleAssignments resource with a
#   `scope` property. Same grant, invisible to the same selectors for the
#   same reason.
#
# Rooted at `.resources`, same reasoning as extract_arm_list above: a
# decorative object elsewhere in the template (variables, outputs) is never
# deployed, so it must not be able to trip this refusal on a template that is
# otherwise fine.
REFUSED_TYPES='["microsoft.resources/deployments",
  "microsoft.resources/deploymentscripts",
  "microsoft.resources/deploymentstacks",
  "microsoft.authorization/roleeligibilityschedulerequests",
  "microsoft.authorization/roleassignmentschedulerequests",
  "microsoft.storage/storageaccounts/providers/roleassignments"]'
REFUSED_TYPE_COUNT=$(
  jq --argjson types "$REFUSED_TYPES" '
    [.resources // [] | .. | objects | select(has("type")) | select((.type|type) == "string")
       | select((.type|ascii_downcase) as $t | $types | index($t) != null)]
    | length
  ' "$ARM_FILE_NORM"
)
if [[ "$REFUSED_TYPE_COUNT" != "0" ]]; then
  echo "ERROR: ARM template contains ${REFUSED_TYPE_COUNT} resource(s) of a type this" >&2
  echo "       check cannot safely inspect: a nested deployment, deployment script or" >&2
  echo "       stack, a PIM role-eligibility/role-assignment schedule request, or a" >&2
  echo "       legacy child-scoped role assignment. Any of these can grant a role this" >&2
  echo "       check never sees as a plain Microsoft.Authorization/roleAssignments" >&2
  echo "       resource, so a tenant-scoped or otherwise unreviewed grant could hide" >&2
  echo "       there (issue #1545). Either inline the resources as a plain role" >&2
  echo "       assignment, or extend this script to recognize the new shape before" >&2
  echo "       adding one." >&2
  exit 1
fi

# Collect every scope value and roleDefinitionId the template grants,
# one per line, tagged with where it came from so the error message points at
# the right JSON node.
#
# The walk is rooted at `.resources` and recursive (`.resources // [] | ..`)
# from there, rather than over the top-level `resources` array's direct
# elements only, so an assignment nested inside a parent resource's own
# `resources` array is still seen -- but a decorative object under
# `variables` or `outputs` that is never actually deployed is not, so it
# cannot red an otherwise-correct template. Type matching is case-insensitive
# because ARM resource types are, while jq's `==` is not.
SCOPES=$(
  jq -r '
    [.resources // [] | .. | objects | select(has("type")) | select((.type|type) == "string")] as $all
    | ( $all[]
        | select((.type|ascii_downcase) == "microsoft.authorization/roledefinitions")
        | (.properties.assignablescopes // [])[]
        | "assignableScopes\t" + . ),
      ( $all[]
        | select((.type|ascii_downcase) == "microsoft.authorization/roleassignments")
        | select(has("scope"))
        | "roleAssignment.scope\t" + (.scope|tostring) ),
      ( $all[]
        | select((.type|ascii_downcase) == "microsoft.authorization/roleassignments")
        | select(has("properties") and (.properties|has("roledefinitionid")))
        | "roleAssignment.roleDefinitionId\t" + (.properties.roledefinitionid|tostring) )
  ' "$ARM_FILE_NORM"
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
  elif [[ "$origin" == "roleAssignment.roleDefinitionId" ]]; then
    # Compared explicitly lowercased, not left to the ambient nocasematch
    # this loop happens to run under: the same "ARM identifiers are
    # case-insensitive" reasoning as normalize_scope_expr above, made
    # explicit here too so it doesn't silently depend on this comparison
    # staying inside that shell-option scope.
    allowed_match=0
    value_lower="$(printf '%s' "$value" | tr '[:upper:]' '[:lower:]')"
    for allowed in "${ALLOWED_ROLE_DEFINITION_IDS[@]}"; do
      allowed_lower="$(printf '%s' "$allowed" | tr '[:upper:]' '[:lower:]')"
      if [[ "$value_lower" == "$allowed_lower" ]]; then
        allowed_match=1
        break
      fi
    done
    if [[ "$allowed_match" -eq 0 ]]; then
      reason="roleDefinitionId is not one of the three roles CUDly assigns (custom purchaser, Reader, Cost Management Reader)"
    fi
  elif [[ "$(normalize_scope_expr "$value")" == "$CANONICAL_SCOPE_NORM" ]]; then
    :
  elif [[ "$(normalize_scope_expr "$value")" == "$CANONICAL_SCOPE_ALT_NORM" ]]; then
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
  echo "assignableScopes must be the canonical deployment-scope expression" >&2
  echo "(whitespace and quote-style differences are tolerated):" >&2
  echo "  ${CANONICAL_SCOPE_EXPR}" >&2
  echo "  or equivalently: ${CANONICAL_SCOPE_EXPR_ALT}" >&2
  echo "A bare literal /subscriptions/<guid> is never accepted, even for the" >&2
  echo "onboarded subscription itself: accepting a literal by GUID shape alone" >&2
  echo "cannot distinguish it from a foreign subscription hard-coded into the" >&2
  echo "template. Role assignments must carry no explicit scope, and" >&2
  echo "roleDefinitionId must be one of the three roles CUDly assigns. A grant" >&2
  echo "at a tenant, management-group or billing-account scope, or at another" >&2
  echo "subscription, reaches subscriptions the customer never onboarded" >&2
  echo "(issue #1545). If a wider grant is genuinely required it must be a" >&2
  echo "separate, manually applied, explicitly consented step, never part of" >&2
  echo "this template. See known-issues.md." >&2
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
echo "OK: all ${SCOPE_COUNT} ARM grant scopes/roleDefinitionIds are subscription-anchored."
exit 0
