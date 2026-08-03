#!/usr/bin/env bash
# test-azure-role-parity.sh
#
# Exercises check-azure-role-parity.sh against testdata fixtures.
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="${SCRIPT_DIR}/check-azure-role-parity.sh"
FIXTURES="${SCRIPT_DIR}/testdata/role-parity"

pass=0
fail=0

run_case() {
  local label="$1"
  local expected_exit="$2"
  shift 2

  actual_exit=0
  "$CHECK" "$@" >/dev/null 2>&1 || actual_exit=$?

  if [[ "$actual_exit" -eq "$expected_exit" ]]; then
    echo "PASS: $label"
    (( pass++ )) || true
  else
    echo "FAIL: $label (expected exit $expected_exit, got $actual_exit)"
    (( fail++ )) || true
  fi
}

# Case 1: matching fixtures -> should exit 0
run_case "matching lists exit 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/matching-arm.json"

# Case 2: drifted ARM (missing purchase/action) -> should exit 1
run_case "drifted ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/drifted-arm.json"

# Case 3 (issue #1545): actions match, but the role is assignable at (and
# assigned at) the tenant-wide /providers/Microsoft.Capacity scope. This is
# the exact shape that shipped in arm/CUDly-CrossSubscription/template.json:
# the actions check passes, so only the scope check can catch it.
run_case "tenant-scope ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/tenant-scope-arm.json"

# Case 4 (issue #1545): the same tenant-wide escape, but written as an ARM
# expression that also mentions /subscriptions/. A check that merely required
# the scope to look subscription-anchored would admit this while rejecting the
# blunt literal in case 3, accepting the fail-OPEN form and blocking only the
# cosmetically-bad one.
run_case "obfuscated tenant-scope ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/obfuscated-tenant-scope-arm.json"

# Case 5: the same tenant scope in lowercase. Azure provider namespaces are
# case-insensitive, so /providers/microsoft.capacity is a fully functional
# tenant scope; a case-sensitive check would pass it while failing case 3.
run_case "lowercase tenant-scope ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/lowercase-tenant-scope-arm.json"

# Case 6: a scope pointing at a DIFFERENT subscription via a parameter. It
# contains /subscriptions/ and names no escape provider, so only an exact-match
# allowlist rejects it. This is the cross-subscription grant the template's own
# name invites.
run_case "other-subscription ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/other-subscription-arm.json"

# Case 7: a tenant-scoped assignment hidden inside a nested deployment. This is
# the idiomatic ARM way to assign at a scope other than the deployment's own,
# so it is what an author with a legitimate cross-scope need would reach for.
# A check that walked only the top-level resources array would not see it.
run_case "nested-deployment ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/nested-deployment-arm.json"

# Case 8: repointing $schema at the management-group template. Every role
# assignment here inherits the deployment scope, so this widens all of them to
# cover every child subscription without altering one scope string.
run_case "management-group schema ARM exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/mgmt-group-schema-arm.json"

# Case 9: the default-false assertion must fail CLOSED when the TF module
# references include_capacity_provider_scope but its variables.tf is missing,
# rather than silently skipping the assertion.
TMP_TF_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_TF_DIR"' EXIT
cp "${FIXTURES}/matching-tf.tf.fixture" "${TMP_TF_DIR}/main.tf"
# A GENUINE HCL reference, not a comment mentioning the name. An earlier
# revision appended only `# assignable_scopes uses include_capacity_provider_scope`,
# which made this case prove nothing about a real reference: it passed solely
# because the checker's trigger is a plain `grep -q` that also matches comment
# text. The block below mirrors how the real module wires the flag
# (terraform/modules/iam/azure/cudly-reservation-role/main.tf:68-71).
#
# Deliberately a `locals` block rather than a second azurerm_role_definition:
# that resource requires a `permissions { ... }` block, whose actions would be
# picked up by extract_tf_list and red the actions axis, making this case fail
# for an unrelated reason instead of the missing variables.tf.
cat >> "${TMP_TF_DIR}/main.tf" <<'HCL'

locals {
  capacity_assignable_scopes = compact([
    "/subscriptions/00000000-0000-0000-0000-000000000001",
    var.include_capacity_provider_scope ? "/providers/Microsoft.Capacity" : "",
  ])
}
HCL
run_case "TF flag without variables.tf exits 1" 1 \
  --tf-file  "${TMP_TF_DIR}/main.tf" \
  --arm-file "${FIXTURES}/matching-arm.json"

# --- adversarial-review bypass regressions (PR #1658) -----------------------
# The cases above passed 9/9 while three bypasses (F1, F3, F4) were still live.
# Each case below reproduces one adversarial-review finding and must fail for
# its own specific reason, not incidentally alongside an unrelated one.

# Case 10 (F1): the deployable shape of the literal-subscription bypass -- the
# canonical expression is retained (so in-subscription assignments keep
# working) with a foreign-subscription literal appended to the same array.
# A GUID-shape-only allowlist accepted this; only rejecting literals outright
# catches it.
run_case "foreign-subscription literal alongside canonical exits 1 (F1)" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/foreign-subscription-literal-with-canonical-arm.json"

# Case 11 (F1): an uppercase GUID literal. `nocasematch` was live across the
# old LITERAL_SUBSCRIPTION_RE match, so `[0-9a-f]` also matched uppercase;
# rejecting literals outright makes case sufficiency moot.
run_case "uppercase GUID literal exits 1 (F1)" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/uppercase-guid-literal-arm.json"

# Case 12 (F2): whitespace, quote-style, a redundant empty-string concat arg,
# and the `subscription().id` equivalent are all spellings of the same
# canonical scope. A byte-exact comparison rejected every one of them; this
# fixture carries all four in one assignableScopes array and must pass.
run_case "canonical scope spelling variants exit 0 (F2)" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/canonical-scope-variants-arm.json"

# Case 13 (F3): a second role definition typed
# "microsoft.authorization/roleDefinitions" (lowercase) granting actions:["*"].
# The actions extractor matched .resources[] at the top level with a
# case-sensitive `==`, so this role was invisible to the actions axis even
# though the case-insensitive scope walk counted it (the old "all 2 ARM grant
# scopes are subscription-anchored" message proved the scope axis saw what the
# actions axis did not).
run_case "lowercase-typed second role def with wildcard actions exits 1 (F3)" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/lowercase-type-wildcard-actions-arm.json"

# Case 14 (F4): a second permissions[] entry appended after the canonical one,
# granting actions:["*"]. ARM unions permissions across the whole array; only
# comparing permissions[0] missed the second entry entirely.
run_case "second permissions entry with wildcard actions exits 1 (F4)" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/second-permissions-entry-arm.json"

# Case 15 (F4): dataActions:["*"] on the (otherwise matching) first permissions
# entry. dataActions/notActions/notDataActions were never compared at all.
run_case "dataActions wildcard exits 1 (F4)" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/dataactions-wildcard-arm.json"

# Case 16 (F5): a Microsoft.Resources/deploymentScripts resource alongside the
# (matching, canonical-scope) role definition. Only "deployments" was refused;
# a deployment script's runtime az-cli commands can issue role assignments this
# check never sees as JSON at all.
run_case "deploymentScripts resource exits 1 (F5)" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/deploymentscript-scope-escape-arm.json"

# Case 17 (F5): the same refusal, for Microsoft.Resources/deploymentStacks.
run_case "deploymentStacks resource exits 1 (F5)" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/deploymentstack-scope-escape-arm.json"

# Case 18 (F6): a role assignment binding the built-in Owner role, carrying no
# explicit `scope` so it correctly inherits the subscription-scope deployment
# (the scope axis finds nothing wrong). Only an explicit `scope` was ever
# checked; roleDefinitionId itself was unconstrained.
run_case "unallowed roleDefinitionId (Owner) exits 1 (F6)" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/unallowed-roledefinitionid-arm.json"

# --- self-review of the round-2 fix itself -----------------------------------
# A normalizer exists to make different strings equal, which is exactly what
# an attacker wants; a recursive JSON walk exists to see more of the
# template, which is exactly what a false positive comes from. Both cut
# points identified during self-review before any external reviewer got to
# them.

# Case 19: a typo'd '/sub scriptions/' (a space inside the string literal, not
# adjacent to any [ ] ( ) , ) must NOT normalize to the canonical scope. It
# doesn't grant anything wider -- the corrupted path just fails to deploy --
# but a normalizer that can't tell "reformatted" from "corrupted" is the
# textbook shape of the next bypass, so this is refused rather than tolerated.
run_case "whitespace inside scope string literal exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/space-inside-literal-arm.json"

# Case 20: a decorative object under `variables` that merely happens to carry
# "type": "Microsoft.Authorization/roleDefinitions" (e.g. left behind as
# documentation) and grants wildcard actions at a tenant scope. Never
# deployed, so it must not be visible to this check at all -- an otherwise
# clean, matching template must still exit 0. A guard that reds valid input
# invites being deleted, which is how issue #1545 shipped in the first place.
run_case "decorative variables object is invisible, exits 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/decorative-variables-arm.json"

# --- round-4: independent review, key-casing and unmatched-type bypasses ----
# ARM's resource-provider JSON deserializers are documented case-insensitive
# for property names; this script's jq queries were not, so a correctly
# recognized resource with one miscased property key was invisible to the
# specific check that key feeds -- silence, not refusal. Cases 21-24 are one
# per miscased key the independent review found live (each on its own,
# isolated from the others, with a second clean role definition/assignment
# alongside so the failure is attributable to the miscasing and nothing
# else). Cases 25-27 are grant-bearing resource types this check's
# roleAssignments-only type match never saw at all.

# Case 21: a roleAssignment with "Scope" (capital S) set to the tenant-wide
# Microsoft.Capacity path. has("scope") never matched "Scope", so the
# assignment's explicit-scope violation -- the exact shape issue #1545
# shipped as -- was invisible.
run_case "miscased 'Scope' key on tenant-wide assignment exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/miscased-scope-arm.json"

# Case 22: a roleAssignment with "RoleDefinitionId" (capitalized) binding
# built-in Owner, no explicit scope. has("roleDefinitionId") never matched
# "RoleDefinitionId", so the roleDefinitionId allowlist (F6) was invisible.
run_case "miscased 'RoleDefinitionId' key on Owner grant exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/miscased-roledefinitionid-arm.json"

# Case 23: a roleAssignment with "Properties" (capitalized) wrapping an Owner
# roleDefinitionId. has("properties") never matched "Properties", so nothing
# inside it -- roleDefinitionId included -- was ever reached.
run_case "miscased 'Properties' key on Owner grant exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/miscased-properties-arm.json"

# Case 24: a second, actions-matching role definition whose "AssignableScopes"
# (capitalized) is the tenant-wide Microsoft.Capacity path. The canonical
# first role definition kept SCOPES non-empty, so the miscased entry's
# absence didn't even trip the "no assignableScopes found" fallback -- it
# just silently contributed nothing, and the template passed.
run_case "miscased 'AssignableScopes' key exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/miscased-assignablescopes-arm.json"

# Case 25 (Fix B): Microsoft.Authorization/roleEligibilityScheduleRequests
# (Azure PIM) binding built-in Owner. Grants a role the same way a plain
# roleAssignment does, under a property shape this check's
# roleAssignments-only type match never saw.
run_case "PIM roleEligibilityScheduleRequests (Owner) exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/pim-roleeligibility-owner-arm.json"

# Case 26 (Fix B): Microsoft.Authorization/roleAssignmentScheduleRequests
# (Azure PIM), same reasoning.
run_case "PIM roleAssignmentScheduleRequests (Owner) exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/pim-roleassignment-schedule-owner-arm.json"

# Case 27 (Fix B): the legacy ARM spelling for a role assignment as a child
# resource type path (Microsoft.Storage/storageAccounts/providers/
# roleAssignments) rather than a top-level roleAssignments resource with a
# scope property. Same grant, invisible to the same type-string match.
run_case "legacy child-type roleAssignments (Owner) exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/legacy-child-roleassignment-owner-arm.json"

# Case 28: the canonical scope expression written with uppercase ARM function
# and property names (CONCAT / SUBSCRIPTION / SUBSCRIPTIONID). ARM identifiers
# are case-insensitive the same way resource types are; normalize_scope_expr
# now folds case explicitly rather than relying on the ambient nocasematch
# scope, so this must still be accepted as canonical.
run_case "uppercase canonical scope expression exits 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/uppercase-canonical-scope-arm.json"

# Case 29: an allowed roleDefinitionId ("[Variables('Roles').Reader]") written
# with different capitalization than ALLOWED_ROLE_DEFINITION_IDS's own
# spelling. Must still match -- same reasoning, made explicit rather than
# implicit in the roleDefinitionId comparison loop.
run_case "case-varied allowed roleDefinitionId exits 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/uppercase-allowed-roledefinitionid-arm.json"

# --- round-5: adversarial-review key-collision bypass -----------------------
# Round 4's key-casing normalization (`with_entries(.key |= ascii_downcase)`)
# closed the single-miscased-key bypass, but `from_entries` (which
# `with_entries` is built on) keeps the LAST entry when two entries produce
# the same key. An object that already carries BOTH case-variant spellings of
# a property in the same object -- not just one miscased key, but two keys --
# collapses to whichever is spelled last, and the discarded value is deleted
# before any selector runs: not unmatched, gone. A template with the hostile
# value first and a benign, canonical-looking value second was invisible.
#
# Cases 30-33 are one collision shape each, all evil-first (the hostile value
# spelled first, discarded on normalization), all against the fix added in
# this round; each must now be refused by name, not incidentally. Case 34 is
# the benign-first control: the same collision with the two keys reversed, so
# the hostile value survives normalization instead and the PRE-EXISTING
# tenant-scope check already caught it -- proving the bypass really is
# order-dependent (only evil-first was silently admitted) and that the new
# refusal does not regress an already-caught shape.

# Case 30: AssignableScopes (evil, tenant-wide) then assignableScopes (benign,
# canonical) on the same roleDefinitions.properties object -- issue #1545
# itself, reached via key collision instead of a single miscased key.
run_case "key-collision AssignableScopes/assignableScopes exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-assignablescopes-evil-first-arm.json"

# Case 31: RoleDefinitionId (evil, built-in Owner) then roleDefinitionId
# (benign, allowed Reader) on the same roleAssignment.properties object.
run_case "key-collision RoleDefinitionId/roleDefinitionId exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-roledefinitionid-evil-first-arm.json"

# Case 32: Properties (evil, wraps Owner) then properties (benign, wraps
# allowed Reader) as two TOP-LEVEL keys on the same roleAssignment resource.
run_case "key-collision Properties/properties exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-properties-evil-first-arm.json"

# Case 33: Type (evil, Microsoft.Resources/deployments) then type (benign,
# Microsoft.Authorization/roleAssignments) as two TOP-LEVEL keys. If the
# collision drops the "Type"/deployments evidence, the resource looks like an
# ordinary, fully-compliant roleAssignments grant -- invisible not just to
# REFUSED_TYPES but to every other check too, which is why this fixture
# carries an otherwise-allowed roleDefinitionId and no scope override.
run_case "key-collision Type/type (evades REFUSED_TYPES too) exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-type-evil-first-arm.json"

# Case 34: benign-first control -- same collision as case 30, keys reversed.
run_case "key-collision benign-first control still exits 1" 1 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-assignablescopes-benign-first-arm.json"

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
