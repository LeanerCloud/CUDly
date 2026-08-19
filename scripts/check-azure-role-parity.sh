#!/usr/bin/env bash
# check-azure-role-parity.sh
#
# Asserts that the Azure custom role stays in parity across both sources of
# truth, on four axes:
#
#   1. ACTIONS:   the permission lists (actions, notActions, dataActions,
#                 notDataActions) are identical (case-insensitively).
#   2. SCOPE:     no grant escapes the subscription being onboarded.
#   3. PRINCIPAL: every grant goes to the service principal the customer
#                 supplies at deploy time, and to nothing else.
#   4. GRANT SET: the template contains EXACTLY the grants it is supposed to,
#                 no more and no fewer.
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
# The principal and grant-set axes exist because axes 1 and 2 constrain WHICH
# role is granted and WHERE, but never WHO it is granted to, and never how
# many grants there are (issue #1681). A fourth roleAssignments resource
# binding the allowed custom purchaser role -- the one carrying
# Microsoft.Capacity/reservationOrders/purchase/action, which spends the
# customer's money -- at the correctly inherited subscription scope, to a
# hardcoded foreign principalId, passed this script cleanly: valid ARM,
# actions in parity, canonical scope, allowed role. So did a duplicate of an
# existing grant, a grant deleted outright, a role assignment carrying no
# principalId at all, and a resource of a type this script has never heard of.
# Each of those simply changed the number in the final "OK: all N ..." line,
# which asserted that a check had happened rather than that anything in
# particular was true.
#
# Exit 0 = actions match, every scope is subscription-anchored, every grant
#          goes to the expected principal, and the grant set is exactly the
#          expected one.
# Exit 1 = drift, or a template shape these checks cannot iterate; the
#          offending values are printed to stderr.
# Exit 2 = the script could not run at all (missing jq, unknown flag).
#
# Usage:
#   scripts/check-azure-role-parity.sh [--tf-file <path>] [--arm-file <path>]
#                                      [--expected-grants <path>]
#
# The --tf-file / --arm-file / --expected-grants flags let the test harness
# substitute fixture files without touching the real sources.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ARM_DIR="${REPO_ROOT}/arm"
TF_FILE="${REPO_ROOT}/terraform/modules/iam/azure/cudly-reservation-role/main.tf"
ARM_FILE="${ARM_DIR}/CUDly-CrossSubscription/template.json"

# The expected grant set (axis 4) is a property of the real template, so it is
# spelled out below as EXPECTED_GRANTS rather than read from a file. The flag
# exists for the harness, whose fixtures are deliberately smaller templates.
EXPECTED_GRANTS_FILE=""
ARM_FILE_OVERRIDDEN=0

# Allow override via flags (used by the test harness).
while [[ $# -gt 0 ]]; do
  case "$1" in
    --tf-file|--arm-file|--expected-grants)
      if [[ $# -lt 2 ]]; then
        echo "Flag $1 requires a value." >&2
        exit 2
      fi
      case "$1" in
        --tf-file)  TF_FILE="$2" ;;
        --arm-file) ARM_FILE="$2"; ARM_FILE_OVERRIDDEN=1 ;;
        --expected-grants) EXPECTED_GRANTS_FILE="$2" ;;
      esac
      shift 2
      ;;
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

# --- refuse an ARM template nothing checks -----------------------------------
# Every assertion below is specific to THIS template: the expected grant set,
# the allowed roleDefinitionIds and the canonical scope expression all name
# what arm/CUDly-CrossSubscription/template.json is supposed to contain. A
# SECOND template anywhere under arm/ would be onboarding customer
# subscriptions with nothing checking it at all, and naming the one file this
# guard already knows about is how a guard fails to reach its own sibling
# site. Discover the set and refuse anything outside it, rather than listing
# what is covered.
#
# `find -L`, because `-type f` alone reports a symlink as neither a file nor
# something to refuse, so a second template symlinked into arm/ is swept past
# rather than found.
#
# `find -print0` with `read -d ''` rather than a `**` glob: `globstar` is bash
# 4 and this runs on the bash 3.2 that ships with macOS. `sort -z` so the
# order is deterministic, since `find` order is not defined, and the loop runs
# in the current shell via process substitution, because a pipeline subshell
# would build the array and then discard it. Same idiom, same reasons, as
# build_swept_scripts in scripts/lib/code-scan-awk.sh.
#
# Every *.json is refused, with no attempt to tell a deployment template from
# a parameters file by its $schema: a shape test deciding which files are
# worth checking is a second thing to get wrong, and there is exactly one
# .json under arm/ today. Adding another is a deliberate act, and its author
# extends this guard first -- the rule the REFUSED_TYPES message below already
# states for a resource type this script does not recognize.
#
# An empty discovery is a failure, not a pass: a sweep that opened no file has
# no unchecked templates either, and would report clean while every template
# in the tree was unguarded.
#
# Skipped when --arm-file overrides the default, because the harness fixtures
# live outside arm/ and this assertion has nothing to say about them. The CI
# invocation passes no flags, so it always runs there.
if [[ "$ARM_FILE_OVERRIDDEN" -eq 0 ]]; then
  ARM_TEMPLATES=()
  while IFS= read -r -d '' candidate; do
    ARM_TEMPLATES+=("$candidate")
  done < <(find -L "$ARM_DIR" -type f -name '*.json' -print0 2>/dev/null | sort -z)

  UNCHECKED_TEMPLATES=""
  FOUND_ARM_FILE=0
  for candidate in ${ARM_TEMPLATES[@]+"${ARM_TEMPLATES[@]}"}; do
    if [[ "$candidate" == "$ARM_FILE" ]]; then
      FOUND_ARM_FILE=1
      continue
    fi
    UNCHECKED_TEMPLATES+="  ${candidate}"$'\n'
  done

  # The sweep must have found the file this script goes on to check. The
  # `-f "$ARM_FILE"` test above already refuses the case where that file is
  # missing, so what is left here is a sweep that could not read ${ARM_DIR} at
  # all and reported nothing to refuse: an empty result and a clean result look
  # identical to the loop above.
  if [[ "$FOUND_ARM_FILE" -eq 0 ]]; then
    echo "ERROR: the sweep of ${ARM_DIR} did not find ${ARM_FILE}," >&2
    echo "       which exists. Nothing was read, so nothing was refused." >&2
    exit 1
  fi

  if [[ -n "$UNCHECKED_TEMPLATES" ]]; then
    echo "ERROR: ${ARM_DIR} holds JSON this guard does not check:" >&2
    echo "" >&2
    printf '%s' "$UNCHECKED_TEMPLATES" >&2
    echo "" >&2
    echo "       Everything below is specific to ${ARM_FILE}: its expected grant" >&2
    echo "       set, its allowed roleDefinitionIds, its canonical scope. A second" >&2
    echo "       template deploys into customer subscriptions with none of that" >&2
    echo "       asserted about it (issue #1681). Extend this script to cover the" >&2
    echo "       new file before adding it." >&2
    exit 1
  fi
fi

# --- refuse a document jq cannot read at all ---------------------------------
# Both checks are ahead of every jq query below, including the collision scan,
# because those queries index the root: a document that is not JSON, or whose
# root is an array or a scalar, aborts the first of them and surfaces jq's own
# exit status through `set -e` instead of a verdict from this script.
if ! jq empty "$ARM_FILE" >/dev/null 2>&1; then
  echo "ERROR: ARM template is not valid JSON: $ARM_FILE" >&2
  echo "       jq says:" >&2
  jq empty "$ARM_FILE" 2>&1 | sed 's/^/         /' >&2 || true
  exit 1
fi

if ! jq -e 'type == "object"' "$ARM_FILE" >/dev/null 2>&1; then
  # An empty file parses without error and yields no value at all, so the type
  # is reported as absent rather than as an empty string.
  ROOT_TYPE="$(jq -r 'type' "$ARM_FILE" 2>/dev/null || true)"
  echo "ERROR: ARM template root has type ${ROOT_TYPE:-<no JSON value>}, expected an object." >&2
  echo "       ARM source: $ARM_FILE" >&2
  echo "       Every check below reads named keys off the root document." >&2
  exit 1
fi

# --- refuse exact-duplicate JSON keys, before jq resolves them ---------------
# The case-variant collision scan below reads `keys_unsorted`, which is what
# jq's PARSER produced: two entries spelled identically -- `"principalId": A`
# followed by `"principalId": B` in the same object -- have already been
# collapsed to one by then, keeping the last. The scan cannot see them, and
# every check in this script reads B while a reviewer skimming the file reads
# whichever comes first.
#
# That is the same ambiguity the scan below refuses, in its sharpest form:
# which of two identically-spelled keys ARM itself honors is not something this
# script can determine from the file, and a template ambiguous about its own
# grants must not be what decides whether CI is green.
#
# Detected with `--stream`, which reports every leaf as it is parsed rather
# than as a merged object, so a repeated path is a duplicate key.
#
# The path is compared as `tojson`, not joined with a separator: joining is not
# injective, so a key that itself contains the separator collides with a nested
# path -- `{"a.b": 1, "a": {"b": 2}}` joins to "a.b" twice and reds a template
# whose keys are all distinct. Dotted keys are ordinary in Azure tags.
DUPLICATE_KEY_PATHS=$(
  jq -rc --stream 'select(length == 2) | .[0] | tojson' "$ARM_FILE" | sort | uniq -d
)

if [[ -n "$DUPLICATE_KEY_PATHS" ]]; then
  echo "ERROR: ARM template declares the same JSON key twice in one object." >&2
  echo "" >&2
  echo "  ARM source: $ARM_FILE" >&2
  echo "" >&2
  printf '%s\n' "$DUPLICATE_KEY_PATHS" | sed 's/^/  /' >&2
  echo "" >&2
  echo "jq keeps the last of two identically-spelled keys, so every check in" >&2
  echo "this script reads that one while the file shows both. A foreign" >&2
  echo "principalId spelled ahead of the sanctioned one is invisible here for" >&2
  echo "exactly that reason (issue #1681). Keep one key per property." >&2
  exit 1
fi

# --- refuse ambiguous same-object key collisions, before normalization -------
# The normalization step just below folds every object key to lowercase with
# `with_entries(.key |= ascii_downcase)`. jq's `from_entries` (which
# `with_entries` is built on) keeps the LAST entry for a given key when two
# entries produce the same key, so an object that already carries two
# case-variant spellings of the same property in the SAME object -- e.g. both
# "AssignableScopes" (ARM's tenant-wide grant) and "assignableScopes" (the
# canonical one) -- collapses to whichever one is spelled LAST in the file.
# Every selector below runs against the normalized copy, so the discarded
# value is invisible to the entire rest of this script: not merely unmatched
# by a case-sensitive selector (that failure mode is what the normalization
# step above exists to close), but deleted before any selector runs. A
# template carrying the hostile value first and a correctly-spelled,
# canonical-looking value second passes clean.
#
# This is checked BEFORE $ARM_FILE is normalized, against the raw file: by
# the time normalization has run, the collision has already been resolved and
# there is nothing left to detect.
#
# Refuse rather than pick a winner. Which of two case-variant keys ARM itself
# honors for a duplicate property in the same JSON object is not verified
# here (doing so would require deploying a miscased template to a live
# tenant) and is not something this script can determine from the file
# alone -- unlike the single-miscased-key case above, where ARM's documented
# case-insensitive deserialization justifies treating the miscased spelling
# as equivalent to the correct one. If ARM takes the first entry, a hostile
# value ahead of a benign one is a live escalation this script would
# otherwise report as clean. If ARM takes the last entry the same way jq
# does, this script's current behavior is coincidentally right, not
# correctly reasoned about -- and either way, a template that is ambiguous
# about its own grants must not be the thing that decides whether CI is
# green.
#
# Scanned objects are the root document itself, plus everything under
# `.resources`, not `.resources` alone: the normalization step below is
# `walk(...)` from the true root, so a root-level collision -- e.g. both
# "resources" and "Resources", or both "$schema" and "$Schema" -- is folded
# by the SAME last-entry-wins rule and is just as invisible to every
# selector below once normalized. A hostile "Resources" array spelled ahead
# of a benign "resources" array at the top of the file is this same bypass
# one level higher in the document tree, and was unreachable by a scan
# rooted at `.resources` because that scan never treats the root object
# itself as one of the objects being inspected for collisions.
COLLISION_DETAIL=$(jq -r '
  [., (.resources // [] | ..) | objects
     | . as $obj
     | ($obj | keys_unsorted) as $keys
     | ($keys | group_by(ascii_downcase) | map(select(length > 1))) as $collisions
     | select($collisions | length > 0)
     | { type: ($obj.type // $obj.Type // "<no type field>" | tostring),
         name: ($obj.name // $obj.Name // "<no name field>" | tostring),
         colliding_key_groups: ($collisions | map(join(" / "))) }
  ]
  | .[]
  | "  resource type=" + .type + " name=" + .name + ": " + (.colliding_key_groups | join(", "))
' "$ARM_FILE")

if [[ -n "$COLLISION_DETAIL" ]]; then
  echo "ERROR: ARM template contains an object with two case-variant spellings of" >&2
  echo "       the same property key. Which one ARM itself honors is not something" >&2
  echo "       this script can determine, and this is exactly the shape of issue" >&2
  echo "       #1545: a hostile value spelled first and a benign, canonical-looking" >&2
  echo "       value spelled second in the same object is deleted by this script's" >&2
  echo "       own key-casing normalization before any check below ever sees it." >&2
  echo "" >&2
  echo "  ARM source: $ARM_FILE" >&2
  echo "" >&2
  echo "$COLLISION_DETAIL" >&2
  echo "" >&2
  echo "Remove the duplicate spelling and keep exactly one key per property." >&2
  exit 1
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

# --- refuse a document shape the selectors below cannot iterate --------------
# `resources`, `permissions` and `assignableScopes` are all iterated by jq
# below, and `properties` is indexed. When one of them is present but is not
# the type its selector assumes -- assignableScopes given as a bare JSON
# string is the shape that surfaced this -- jq aborts mid-pipeline and `set
# -e` propagates jq's own exit status (5) with jq's own message, which is
# neither of this script's documented outcomes. It still fails closed, so this
# is about diagnosis rather than about a hole: refuse the shape up front, with
# this script's own message and its own exit 1, so a malformed template is
# told what is wrong with it.
#
# Checked against the normalized copy, so a miscased key is caught here the
# same way it is everywhere else, and rooted at `.resources` like every other
# walk, so a decorative object under `variables` cannot trip it.
SHAPE_ERRORS=$(jq -r '
  def flat: tostring | gsub("[\\t\\r\\n]"; " ");
  [ (if has("resources") and ((.resources | type) != "array")
     then "  .resources is a " + (.resources | type) + ", expected an array"
     else empty end),
    ( .resources // [] | .. | objects | select(has("type"))
      | select((.type | type) == "string")
      | select((.type | ascii_downcase)
               | . == "microsoft.authorization/roledefinitions"
                 or . == "microsoft.authorization/roleassignments")
      | . as $r
      | ("  " + ($r.type | flat) + " " + (($r.name // "<unnamed>") | flat) + ": ") as $where
      | if (($r | has("properties")) and (($r.properties | type) != "object"))
        then $where + "properties is a " + ($r.properties | type) + ", expected an object"
        elif (($r.properties | type) == "object")
             and ($r.properties | has("permissions"))
             and (($r.properties.permissions | type) != "array")
        then $where + "properties.permissions is a " + ($r.properties.permissions | type)
             + ", expected an array"
        elif (($r.properties | type) == "object")
             and ($r.properties | has("assignablescopes"))
             and (($r.properties.assignablescopes | type) != "array")
        then $where + "properties.assignableScopes is a "
             + ($r.properties.assignablescopes | type) + ", expected an array"
        else
          ( ( if (($r.properties | type) == "object")
                 and (($r.properties.permissions | type) == "array")
              then ($r.properties.permissions | to_entries[]
                    | if (.value | type) != "object"
                      then $where + "properties.permissions[" + (.key | tostring) + "] is a "
                           + (.value | type) + ", expected an object"
                      else ( .key as $i | .value | to_entries[]
                             | select(.key | . == "actions" or . == "notactions"
                                             or . == "dataactions" or . == "notdataactions")
                             | select((.value | type) != "array")
                             | $where + "properties.permissions[" + ($i | tostring) + "]."
                               + .key + " is a " + (.value | type) + ", expected an array" )
                      end )
              else empty end ),
            ( if (($r.properties | type) == "object")
                 and (($r.properties.permissions | type) == "array")
              then ($r.properties.permissions | to_entries[]
                    | select((.value | type) == "object")
                    | .key as $i | .value | to_entries[]
                    | select(.key | . == "actions" or . == "notactions"
                                    or . == "dataactions" or . == "notdataactions")
                    | select((.value | type) == "array")
                    | .key as $list | .value | to_entries[]
                    | select((.value | type) != "string")
                    | $where + "properties.permissions[" + ($i | tostring) + "]." + $list
                      + "[" + (.key | tostring) + "] is a " + (.value | type)
                      + ", expected a string")
              else empty end ),
            ( if (($r.properties | type) == "object")
                 and (($r.properties.assignablescopes | type) == "array")
              then ($r.properties.assignablescopes | to_entries[]
                    | select((.value | type) != "string")
                    | $where + "properties.assignableScopes[" + (.key | tostring) + "] is a "
                      + (.value | type) + ", expected a string")
              else empty end ),
            # `copy` multiplies the resource it sits on into N deployed
            # resources whose properties can vary by copyIndex(); `condition`
            # is the same statement with N of 0 or 1, and a `condition` of
            # false deletes a grant while leaving its declaration in the file
            # for the set below to count. Either way one declaration is no
            # longer one grant and the set asserted is not the set deployed.
            # Refused rather than modelled: this template has neither, and
            # adding one changes what a grant even means here.
            ( if ($r | has("copy")) then $where + "carries a copy loop" else empty end ),
            ( if ($r | has("condition"))
              then $where + "carries a condition, so whether it deploys is not in the file"
              else empty end ) )
        end )
  ] | .[]
' "$ARM_FILE_NORM")

if [[ -n "$SHAPE_ERRORS" ]]; then
  echo "ERROR: ARM template has a shape these checks cannot inspect." >&2
  echo "" >&2
  echo "  ARM source: $ARM_FILE" >&2
  echo "" >&2
  echo "$SHAPE_ERRORS" >&2
  echo "" >&2
  echo "Every one of these is iterated or indexed below. A value of the wrong" >&2
  echo "type aborts the check partway through instead of producing a verdict." >&2
  exit 1
fi

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

# The only principal this template may ever grant to (issue #1681): the CUDly
# service principal whose object ID the customer supplies at deploy time. Any
# other value is a grant to somebody else, and a hardcoded GUID literal is one
# by construction -- the deploying customer cannot have written it, and this
# script cannot tell a foreign object ID from a legitimate one by its shape,
# the same reason a bare literal /subscriptions/<guid> is refused above rather
# than shape-matched. Compared as ARM expression text, normalized the same way
# the scope expressions are.
EXPECTED_PRINCIPAL_PARAM="servicePrincipalObjectId"
EXPECTED_PRINCIPAL_EXPR="[parameters('${EXPECTED_PRINCIPAL_PARAM}')]"


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
# normalize_scope_expr is named for its first caller but is a general ARM
# expression normalizer (trim, quote style, punctuation-adjacent whitespace,
# case); principalId is ARM expression text under the same case-insensitivity
# rules, so it is compared the same way.
EXPECTED_PRINCIPAL_NORM="$(normalize_scope_expr "$EXPECTED_PRINCIPAL_EXPR")"

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
#
# Matched on the schema URL's PATH, which is the part ARM resolves, with the
# fragment and query excluded from the match rather than merely allowed after
# it. Two revisions of this check got that wrong in the same way: an unanchored
# `test("subscriptionDeploymentTemplate")` accepted the management-group schema
# with "#subscriptionDeploymentTemplate" appended, and anchoring on the name
# with `(#.*)?$` still accepted it with "#/subscriptionDeploymentTemplate.json"
# appended, because the sanctioned name was then the tail of the fragment. The
# path is everything before the first `#` or `?`, so that is what is matched.
if ! jq -e '.["$schema"] | strings
            | test("^[^#?]*/subscriptiondeploymenttemplate\\.json([#?].*)?$"; "i")' \
     "$ARM_FILE_NORM" >/dev/null 2>&1; then
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
#   *providers/roleAssignments -- the legacy ARM spelling for a role
#   assignment as a child resource (a full `.../providers/...` type path)
#   rather than a separate top-level roleAssignments resource with a `scope`
#   property. Same grant, invisible to the same selectors for the same
#   reason, under ANY parent resource type -- storageAccounts is only the
#   example issue #1545 happened to use. Matched by suffix rather than
#   enumerated per parent type, the same way ESCAPE_TOKENS above matches
#   `managementGroups` independently of its provider spelling: an allowlist
#   naming one parent (e.g. only microsoft.storage/storageaccounts) leaves
#   every other parent's `.../providers/roleAssignments` child free to grant
#   silently, and there is no fixed set of parent types to enumerate here.
#
# Rooted at `.resources`, same reasoning as extract_arm_list above: a
# decorative object elsewhere in the template (variables, outputs) is never
# deployed, so it must not be able to trip this refusal on a template that is
# otherwise fine.
REFUSED_TYPES='["microsoft.resources/deployments",
  "microsoft.resources/deploymentscripts",
  "microsoft.resources/deploymentstacks",
  "microsoft.authorization/roleeligibilityschedulerequests",
  "microsoft.authorization/roleassignmentschedulerequests"]'
REFUSED_TYPE_COUNT=$(
  jq --argjson types "$REFUSED_TYPES" '
    [.resources // [] | .. | objects | select(has("type")) | select((.type|type) == "string")
       | select((.type|ascii_downcase) as $t
                | ($types | index($t) != null)
                or ($t | endswith("providers/roleassignments")))]
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

# Collect every scope value, roleDefinitionId and principalId the template
# grants, one per line, tagged with where it came from so the error message
# points at the right JSON node.
#
# The walk is rooted at `.resources` and recursive (`.resources // [] | ..`)
# from there, rather than over the top-level `resources` array's direct
# elements only, so an assignment nested inside a parent resource's own
# `resources` array is still seen -- but a decorative object under
# `variables` or `outputs` that is never actually deployed is not, so it
# cannot red an otherwise-correct template. Type matching is case-insensitive
# because ARM resource types are, while jq's `==` is not.
#
# principalId is collected by KEY PRESENCE on any object under `.resources`,
# not from roleAssignments resources only, and this asymmetry is deliberate.
# `principalId` names the recipient of a grant wherever it appears, and the
# shapes that carry one are not a list this script can finish writing: the PIM
# schedule requests and the legacy `.../providers/roleAssignments` child type
# refused above are two that were already found, and the refusal list is only
# ever as long as somebody's memory. The key itself is the invariant, so it is
# what is matched. It also means the value is read out of the object that
# actually holds it -- `properties` -- which carries no `type` of its own.
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
        | "roleAssignment.roleDefinitionId\t" + (.properties.roledefinitionid|tostring) ),
      ( [.resources // [] | .. | objects | select(has("principalid"))][]
        | "roleAssignment.principalId\t" + (.principalid|tostring) ),
      ( $all[]
        | select((.type|ascii_downcase) == "microsoft.authorization/roleassignments")
        | select([.properties | objects | select(has("principalid"))] | length == 0)
        | "roleAssignment.principalId\t<absent>" )
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
PRINCIPAL_SITES=0
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
  elif [[ "$origin" == "roleAssignment.principalId" ]]; then
    # `<absent>` is emitted for a roleAssignments resource carrying no
    # principalId at all, so omitting the field is not a way to be exempt
    # from the check on its value.
    PRINCIPAL_SITES=$((PRINCIPAL_SITES + 1))
    if [[ "$(normalize_scope_expr "$value")" != "$EXPECTED_PRINCIPAL_NORM" ]]; then
      reason="principalId is not ${EXPECTED_PRINCIPAL_EXPR}, the only principal this template grants to"
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
  echo "template. Role assignments must carry no explicit scope," >&2
  echo "roleDefinitionId must be one of the three roles CUDly assigns, and" >&2
  echo "principalId must be ${EXPECTED_PRINCIPAL_EXPR}: a" >&2
  echo "hardcoded object ID grants somebody other than the customer's own" >&2
  echo "CUDly service principal, and the custom purchaser role spends money" >&2
  echo "(issue #1681). A grant" >&2
  echo "at a tenant, management-group or billing-account scope, or at another" >&2
  echo "subscription, reaches subscriptions the customer never onboarded" >&2
  echo "(issue #1545). If a wider grant is genuinely required it must be a" >&2
  echo "separate, manually applied, explicitly consented step, never part of" >&2
  echo "this template. See known-issues.md." >&2
  exit 1
fi

# --- the principal parameter must stay the customer's answer -----------------
# Every principalId above reads parameters('servicePrincipalObjectId'), which
# is only worth anything while that parameter is one the customer is forced to
# answer. A defaultValue on it is the same escalation one hop out: the
# principalId fields are untouched and still pass, while `az deployment sub
# create` and the portal's own form both prefill the object ID the default
# names, so a template nobody edited past this line grants to it. Refused with
# allowedValues, which constrains the customer's answer from the other
# direction.
#
# Gated on having SEEN the parameter referenced, not on the parameter
# existing: PRINCIPAL_SITES counts the principalId sites the loop above read,
# and that loop has already refused any value other than this parameter, so a
# nonzero count means the template grants through it. A fixture with no role
# assignment at all references nothing and is asked for nothing. Reading the
# gate the other way round -- skipping when the parameter is missing -- would
# make deleting the declaration the way out.
if [[ "$PRINCIPAL_SITES" -gt 0 ]]; then
  PARAM_ERRORS=$(jq -r --arg p "$(printf '%s' "$EXPECTED_PRINCIPAL_PARAM" | tr '[:upper:]' '[:lower:]')" '
    (.parameters // {}) as $params
    | if ($params | type) != "object" then
        "  parameters is a " + ($params | type) + ", expected an object"
      elif ($params | has($p) | not) then
        "  parameters." + $p + " is never declared, so nothing forces the customer to supply it"
      elif (($params[$p] | type) != "object") then
        "  parameters." + $p + " is a " + ($params[$p] | type) + ", expected an object"
      elif ($params[$p] | has("defaultvalue")) then
        "  parameters." + $p + " has a defaultValue: " + ($params[$p].defaultvalue | tostring)
      elif ($params[$p] | has("allowedvalues")) then
        "  parameters." + $p + " has allowedValues: " + ($params[$p].allowedvalues | tostring)
      else empty
      end
  ' "$ARM_FILE_NORM")

  if [[ -n "$PARAM_ERRORS" ]]; then
    echo "ERROR: the principal parameter is not the customer's own answer." >&2
    echo "" >&2
    echo "  ARM source: $ARM_FILE" >&2
    echo "" >&2
    echo "$PARAM_ERRORS" >&2
    echo "" >&2
    echo "${PRINCIPAL_SITES} grant(s) in this template are made to" >&2
    echo "${EXPECTED_PRINCIPAL_EXPR}, and the custom role one of them binds can" >&2
    echo "spend the customer's money. That is only a grant to the customer's own" >&2
    echo "CUDly service principal while the deploying customer is the one who" >&2
    echo "supplies the value (issue #1681)." >&2
    exit 1
  fi
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
echo "OK: all ${SCOPE_COUNT} ARM grant scopes/roleDefinitionIds/principalIds are subscription-anchored"
echo "    and go to ${EXPECTED_PRINCIPAL_EXPR}."

# --- the expected grant set (issue #1681) ------------------------------------
# Everything above answers "is each thing I happened to find individually
# acceptable?", which is only ever as complete as the list of shapes somebody
# remembered to refuse. A resource type this script has never heard of is not
# recognized by any selector above and is not on the REFUSED_TYPES list, so it
# contributes nothing and passes in silence; so does a second copy of a grant
# that is legitimate once; so does deleting one.
#
# This axis asserts the other direction: the template grants EXACTLY the set
# below, as a multiset, and every resource it deploys is one of them. Whatever
# the next unforeseen shape turns out to be, it is either one of these tuples
# or it is not, and nobody has to have thought of it first.
#
# Each grant is described by the fields that decide what it can do -- what
# role, to whom, at what scope -- normalized the same way the checks above
# normalize theirs, so a pure reformat of the template cannot red CI while a
# changed value always does.
CANONICAL_SCOPE_TOKEN="<canonical-subscription-scope>"
EXPECTED_PRINCIPAL_TOKEN="<servicePrincipalObjectId-parameter>"

# The four grants arm/CUDly-CrossSubscription/template.json is supposed to
# make: the custom purchaser role definition, assignable in the subscription
# being onboarded, and the three assignments of it and of the two built-in
# read-only roles to the customer's CUDly service principal.
#
# The roles are named by what `variables` resolves them TO, not by the
# variable that points at them, so repointing a variable is a change to this
# list rather than a change nothing reads. The two GUIDs are Azure's own
# built-in Reader and Cost Management Reader definitions, which is the whole
# assertion: any other definition, built-in or custom, is a different role.
#
# Written in the template's own spelling and normalized here, rather than
# pre-normalized by hand, so this list stays diffable against the template it
# describes.
EXPECTED_GRANTS=(
  "roleDefinition assignableScopes=${CANONICAL_SCOPE_TOKEN}"
  "roleAssignment roleDefinitionId=$(normalize_scope_expr "[subscriptionResourceId('Microsoft.Authorization/roleDefinitions', guid(subscription().subscriptionId, 'cudly-reservation-purchaser'))]") principalId=${EXPECTED_PRINCIPAL_TOKEN} scope=<inherited>"
  "roleAssignment roleDefinitionId=$(normalize_scope_expr "/providers/Microsoft.Authorization/roleDefinitions/acdd72a7-3385-48ef-bd42-f606fba81ae7") principalId=${EXPECTED_PRINCIPAL_TOKEN} scope=<inherited>"
  "roleAssignment roleDefinitionId=$(normalize_scope_expr "/providers/Microsoft.Authorization/roleDefinitions/72fafb9e-0641-4937-9268-a91bfd8191a3") principalId=${EXPECTED_PRINCIPAL_TOKEN} scope=<inherited>"
)

# The harness substitutes its own set for the smaller fixture templates.
# Blank lines and `#` comments are ignored; anything else is an expected grant
# descriptor, in the exact form the loop below builds.
if [[ -n "$EXPECTED_GRANTS_FILE" ]]; then
  if [[ ! -f "$EXPECTED_GRANTS_FILE" ]]; then
    echo "ERROR: expected-grants file not found: $EXPECTED_GRANTS_FILE" >&2
    exit 1
  fi
  EXPECTED_GRANTS=()
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "${line// /}" || "$line" == \#* ]] && continue
    EXPECTED_GRANTS+=("$line")
  done < "$EXPECTED_GRANTS_FILE"
fi

if [[ ${#EXPECTED_GRANTS[@]} -eq 0 ]]; then
  echo "ERROR: the expected grant set is empty, so this axis would assert nothing." >&2
  echo "       Every template satisfies 'grants nothing beyond an empty set'." >&2
  exit 1
fi

# Walk the resources ARM actually deploys: the document's `resources` array
# and, recursively, each resource's own nested `resources` array. This is a
# structural walk rather than the `..` descent the checks above use, because
# this axis asks what the DEPLOYED set is, and `..` also reaches objects that
# merely sit inside a resource -- `properties` carries its own `"type":
# "CustomRole"`, which is not a resource of type CustomRole, and reporting it
# as an unrecognized resource type would red the real template.
#
# What that costs is precise: an object sitting at a position ARM does not
# deploy from is not in this inventory. The checks above are the ones that
# read those, and they run first and exit on their own -- every `principalId`
# anywhere under `.resources` by key, every roleAssignments and
# roleDefinitions resource anywhere under it by type, and REFUSED_TYPES by
# type and by `/providers/roleAssignments` suffix. Between them they cover
# the shapes that carry a grant; this axis covers the set that is deployed.
#
# Emitted as four tab-separated fields per resource, with tabs and newlines in
# any value flattened to spaces so one resource is always exactly one line.
#
# roleDefinitionId is resolved through the template's own `variables` table
# before being recorded, because the expression text alone names a pointer
# rather than a role: `[variables('roles').reader]` is on the allowlist, and
# repointing `variables.roles.reader` at the built-in Owner definition changes
# what every grant of it confers while every string this script compares stays
# byte-identical. Only the two forms this template uses are resolved,
# `[variables('x')]` and `[variables('x').y]`; anything else is recorded as
# `unresolved:<text>`, which is in no expected set and is therefore refused
# rather than quietly compared as text.
GRANT_RECORDS=$(
  jq -r '
    # `<empty>` rather than "": IFS=$'"'"'\t'"'"' treats tab as IFS whitespace, so a
    # run of tabs collapses and an empty field shifts every field after it,
    # which makes the diagnostic describe a grant the template does not have.
    def flat: tostring | gsub("[\\t\\r\\n]"; " ") | if . == "" then "<empty>" else . end;
    def resolve_variable($vars):
      ascii_downcase as $e
      | ($e | capture("^\\[variables\\('"'"'(?<n>[^'"'"']+)'"'"'\\)(\\.(?<f>[a-z0-9_]+))?\\]$")) as $m
      | if $m == null then null
        elif ($vars | type) != "object" then null
        elif ($m.f == null)
        then ($vars[$m.n] | if type == "string" then . else null end)
        elif (($vars[$m.n] | type) == "object")
        then ($vars[$m.n][$m.f] | if type == "string" then . else null end)
        else null
        end;
    def deployed: (if type == "object" and (.resources | type) == "array"
                   then .resources else [] end)[]
                  | ., deployed;
    (.variables // {}) as $vars
    | [deployed][]
    | . as $r
    | ((if ($r | type) == "object" and ($r.properties | type) == "object"
        then $r.properties else {} end)) as $p
    | if (($r | type) != "object") then
        "unrecognized\t<non-object resource>\t\t"
      elif (($r.type | type) != "string") then
        "unrecognized\t<non-string type>\t\t"
      elif ($r.type | ascii_downcase) == "microsoft.authorization/roledefinitions" then
        "roleDefinition\t"
        + ( if ($p | has("assignablescopes"))
            then ($p.assignablescopes | map(flat) | join("\u0001"))
            else "<absent>" end )
        + "\t\t"
      elif ($r.type | ascii_downcase) == "microsoft.authorization/roleassignments" then
        "roleAssignment\t"
        + ( if ($p | has("roledefinitionid") | not) then "<absent>"
            else ($p.roledefinitionid | flat) as $raw
                 | ($raw | resolve_variable($vars)) as $resolved
                 | (if $resolved == null then "unresolved:" + $raw else ($resolved | flat) end)
            end )
        + "\t"
        + (if ($p | has("principalid")) then ($p.principalid | flat) else "<absent>" end)
        + "\t"
        + (if ($r | has("scope")) then ($r.scope | flat) else "<inherited>" end)
      else
        "unrecognized\t" + ($r.type | ascii_downcase | flat) + "\t\t"
      end
  ' "$ARM_FILE_NORM"
)

# A scope or principal that means what it is supposed to mean is reported as a
# token, so the expected set names the invariant rather than one of the
# several spellings that satisfy it. Anything else passes through normalized,
# so it shows up in the diff as the value it actually is.
canonical_scope_token() {
  local n
  n="$(normalize_scope_expr "$1")"
  if [[ "$n" == "$CANONICAL_SCOPE_NORM" || "$n" == "$CANONICAL_SCOPE_ALT_NORM" ]]; then
    printf '%s' "$CANONICAL_SCOPE_TOKEN"
  else
    printf '%s' "$n"
  fi
}

expected_principal_token() {
  local n
  n="$(normalize_scope_expr "$1")"
  if [[ "$n" == "$EXPECTED_PRINCIPAL_NORM" ]]; then
    printf '%s' "$EXPECTED_PRINCIPAL_TOKEN"
  else
    printf '%s' "$n"
  fi
}

ACTUAL_GRANTS=()
RESOURCE_COUNT=0
while IFS=$'\t' read -r kind field_a field_b field_c; do
  [[ -z "$kind" ]] && continue
  RESOURCE_COUNT=$((RESOURCE_COUNT + 1))
  case "$kind" in
    roleDefinition)
      # Several spellings of the canonical scope in one assignableScopes array
      # are one grant, not several, so the tokens are deduplicated. They are
      # sorted for the same reason the grant list is: array order is not a
      # security property.
      scopes=""
      if [[ "$field_a" == "<absent>" ]]; then
        scopes="<absent>"
      else
        mapped=""
        # `|| [[ -n "$one_scope" ]]`: the split below emits no trailing
        # newline, and a bare `read` discards an unterminated final line --
        # which for a single-element assignableScopes array is the only line
        # there is.
        while IFS= read -r one_scope || [[ -n "$one_scope" ]]; do
          [[ -z "$one_scope" ]] && continue
          mapped+="$(canonical_scope_token "$one_scope")"$'\n'
        done < <(printf '%s' "$field_a" | tr '\001' '\n')
        scopes="$(printf '%s' "$mapped" | sort -u | paste -sd, -)"
        [[ -z "$scopes" ]] && scopes="<empty>"
      fi
      ACTUAL_GRANTS+=("roleDefinition assignableScopes=${scopes}")
      ;;
    roleAssignment)
      role="$(normalize_scope_expr "$field_a")"
      principal="$(expected_principal_token "$field_b")"
      # The scope field is recorded as emitted rather than mapped through
      # canonical_scope_token: a roleAssignment carrying an explicit `scope` at
      # all is refused by the scope axis above, which runs first and exits, so
      # the only value that reaches here is <inherited>. Recording the raw text
      # keeps that from being an assumption -- if the axis above ever stops
      # refusing explicit scopes, the value lands in this diff instead of being
      # normalized into looking expected.
      ACTUAL_GRANTS+=("roleAssignment roleDefinitionId=${role} principalId=${principal} scope=${field_c}")
      ;;
    *)
      ACTUAL_GRANTS+=("unrecognized resource type=${field_a}")
      ;;
  esac
done <<< "$GRANT_RECORDS"

# This axis cannot pass having read nothing: the expected set is refused when
# empty (above), and what follows is equality against it rather than an
# absence, so an empty inventory is reported as every expected grant missing.
# That is the floor; a separate "did I read anything" assertion here would be
# a branch no input reaches.
GRANT_DIFF=$(
  diff <(printf '%s\n' "${EXPECTED_GRANTS[@]}" | sort) \
       <(printf '%s\n' ${ACTUAL_GRANTS[@]+"${ACTUAL_GRANTS[@]}"} | sort) || true
)

if [[ -n "$GRANT_DIFF" ]]; then
  echo "ERROR: ARM template does not grant exactly the expected set." >&2
  echo "" >&2
  echo "  ARM source: $ARM_FILE" >&2
  echo "" >&2
  echo "Diff (< expected  > found):" >&2
  echo "$GRANT_DIFF" >&2
  echo "" >&2
  echo "This template deploys into a customer's subscription and the custom role" >&2
  echo "it defines can spend their money, so what it grants is enumerated rather" >&2
  echo "than filtered: a grant that is not on the list is refused whether or not" >&2
  echo "anyone has thought about that shape before (issue #1681). A '> found'" >&2
  echo "line reading 'unrecognized resource type=...' is a resource no check here" >&2
  echo "inspects at all." >&2
  echo "" >&2
  echo "If the template is meant to change, change EXPECTED_GRANTS in this script" >&2
  echo "in the same commit, so the new grant is reviewed as a grant." >&2
  exit 1
fi

echo "OK: ARM template grants exactly the ${#EXPECTED_GRANTS[@]} expected tuples across ${RESOURCE_COUNT} deployed resource(s)."
exit 0
