#!/usr/bin/env bash
# test-azure-role-parity.sh
#
# Exercises check-azure-role-parity.sh against testdata fixtures, and against
# the repository's own template and TF module with no flags at all, the way CI
# invokes it. The fixture cases prove the checker behaves correctly on inputs
# built to break it; only the flagless ones prove anything about the sources it
# actually guards.
# Exits 0 when all cases pass; exits 1 on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
CHECK="${SCRIPT_DIR}/check-azure-role-parity.sh"
FIXTURES="${SCRIPT_DIR}/testdata/role-parity"

# The checker both runners invoke. It is the repo's own copy for every case
# but the arm/ sweep at the end of this file, which needs a checker whose
# REPO_ROOT is a scratch tree rather than the repository.
CHECK_UNDER_TEST="$CHECK"

pass=0
fail=0

run_case() {
  local label="$1"
  local expected_exit="$2"
  shift 2

  actual_exit=0
  "$CHECK_UNDER_TEST" "$@" >/dev/null 2>&1 || actual_exit=$?

  if [[ "$actual_exit" -eq "$expected_exit" ]]; then
    echo "PASS: $label"
    (( pass++ )) || true
  else
    echo "FAIL: $label (expected exit $expected_exit, got $actual_exit)"
    (( fail++ )) || true
  fi
}

# run_case_saying LABEL EXPECTED_EXIT DIAGNOSTIC ARGS...
#
# run_case with the checker's own diagnostic asserted as well. A nonzero exit
# proves only that something went wrong: a typo in the script, jq aborting on
# an unexpected shape and `set -e` surfacing its status, or an unrelated check
# firing first all exit nonzero too, so a case that asserts the code alone can
# go on passing after the assertion it was written for stops running. The
# cases below that pin a specific finding use this instead.
#
# DIAGNOSTIC is matched as a fixed string (`grep -F`), not a pattern: a regex
# here would be one more thing that can match more than it reads as.
run_case_saying() {
  local label="$1"
  local expected_exit="$2"
  local diagnostic="$3"
  shift 3

  local output actual_exit=0
  output="$("$CHECK_UNDER_TEST" "$@" 2>&1)" || actual_exit=$?

  if [[ "$actual_exit" -ne "$expected_exit" ]]; then
    echo "FAIL: $label (expected exit $expected_exit, got $actual_exit)"
    (( fail++ )) || true
    return
  fi
  if ! printf '%s\n' "$output" | grep -qF -- "$diagnostic"; then
    echo "FAIL: $label (exit $actual_exit as expected, but never said:)"
    echo "        ${diagnostic}"
    echo "      what it said was:"
    printf '%s\n' "$output" | sed 's/^/        /'
    (( fail++ )) || true
    return
  fi
  echo "PASS: $label"
  (( pass++ )) || true
}

# The fixtures below are deliberately smaller than the real onboarding
# template, so the grant-set axis (issue #1681) is given the set each fixture
# is supposed to contain. The cases that assert the REAL expected set pass no
# --expected-grants at all and are grouped at the end of this file.
GRANTS_ROLE_DEF_ONLY=(--expected-grants "${FIXTURES}/expected/role-definition-only.grants")
GRANTS_ROLE_DEF_AND_READER=(--expected-grants "${FIXTURES}/expected/role-definition-and-reader.grants")

# Case 1: matching fixtures -> should exit 0
run_case "matching lists exit 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/matching-arm.json" \
  "${GRANTS_ROLE_DEF_ONLY[@]}"

# Case 2: drifted ARM (missing purchase/action) -> should exit 1
run_case_saying "drifted ARM exits 1" 1 \
  "TF module actions lists differ" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/drifted-arm.json"

# Case 3 (issue #1545): actions match, but the role is assignable at (and
# assigned at) the tenant-wide /providers/Microsoft.Capacity scope. This is
# the exact shape that shipped in arm/CUDly-CrossSubscription/template.json:
# the actions check passes, so only the scope check can catch it.
run_case_saying "tenant-scope ARM exits 1" 1 \
  "names a scope above the subscription" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/tenant-scope-arm.json"

# Case 4 (issue #1545): the same tenant-wide escape, but written as an ARM
# expression that also mentions /subscriptions/. A check that merely required
# the scope to look subscription-anchored would admit this while rejecting the
# blunt literal in case 3, accepting the fail-OPEN form and blocking only the
# cosmetically-bad one.
run_case_saying "obfuscated tenant-scope ARM exits 1" 1 \
  "names a scope above the subscription" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/obfuscated-tenant-scope-arm.json"

# Case 5: the same tenant scope in lowercase. Azure provider namespaces are
# case-insensitive, so /providers/microsoft.capacity is a fully functional
# tenant scope; a case-sensitive check would pass it while failing case 3.
run_case_saying "lowercase tenant-scope ARM exits 1" 1 \
  "names a scope above the subscription" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/lowercase-tenant-scope-arm.json"

# Case 6: a scope pointing at a DIFFERENT subscription via a parameter. It
# contains /subscriptions/ and names no escape provider, so only an exact-match
# allowlist rejects it. This is the cross-subscription grant the template's own
# name invites.
run_case_saying "other-subscription ARM exits 1" 1 \
  "not the canonical subscription scope" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/other-subscription-arm.json"

# Case 7: a tenant-scoped assignment hidden inside a nested deployment. This is
# the idiomatic ARM way to assign at a scope other than the deployment's own,
# so it is what an author with a legitimate cross-scope need would reach for.
# A check that walked only the top-level resources array would not see it.
run_case_saying "nested-deployment ARM exits 1" 1 \
  "resource(s) of a type this" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/nested-deployment-arm.json"

# Case 8: repointing $schema at the management-group template. Every role
# assignment here inherits the deployment scope, so this widens all of them to
# cover every child subscription without altering one scope string.
run_case_saying "management-group schema ARM exits 1" 1 \
  "not a subscription-scoped deployment" \
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
run_case_saying "TF flag without variables.tf exits 1" 1 \
  "does not exist, so its default cannot be checked" \
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
run_case_saying "foreign-subscription literal alongside canonical exits 1 (F1)" 1 \
  "not the canonical subscription scope" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/foreign-subscription-literal-with-canonical-arm.json"

# Case 11 (F1): an uppercase GUID literal. `nocasematch` was live across the
# old LITERAL_SUBSCRIPTION_RE match, so `[0-9a-f]` also matched uppercase;
# rejecting literals outright makes case sufficiency moot.
run_case_saying "uppercase GUID literal exits 1 (F1)" 1 \
  "not the canonical subscription scope" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/uppercase-guid-literal-arm.json"

# Case 12 (F2): whitespace, quote-style, a redundant empty-string concat arg,
# and the `subscription().id` equivalent are all spellings of the same
# canonical scope. A byte-exact comparison rejected every one of them; this
# fixture carries all four in one assignableScopes array and must pass.
run_case "canonical scope spelling variants exit 0 (F2)" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/canonical-scope-variants-arm.json" \
  "${GRANTS_ROLE_DEF_ONLY[@]}"

# Case 13 (F3): a second role definition typed
# "microsoft.authorization/roleDefinitions" (lowercase) granting actions:["*"].
# The actions extractor matched .resources[] at the top level with a
# case-sensitive `==`, so this role was invisible to the actions axis even
# though the case-insensitive scope walk counted it (the old "all 2 ARM grant
# scopes are subscription-anchored" message proved the scope axis saw what the
# actions axis did not).
run_case_saying "lowercase-typed second role def with wildcard actions exits 1 (F3)" 1 \
  "actions lists differ" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/lowercase-type-wildcard-actions-arm.json"

# Case 14 (F4): a second permissions[] entry appended after the canonical one,
# granting actions:["*"]. ARM unions permissions across the whole array; only
# comparing permissions[0] missed the second entry entirely.
run_case_saying "second permissions entry with wildcard actions exits 1 (F4)" 1 \
  "actions lists differ" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/second-permissions-entry-arm.json"

# Case 15 (F4): dataActions:["*"] on the (otherwise matching) first permissions
# entry. dataActions/notActions/notDataActions were never compared at all.
run_case_saying "dataActions wildcard exits 1 (F4)" 1 \
  "dataActions lists differ" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/dataactions-wildcard-arm.json"

# Case 16 (F5): a Microsoft.Resources/deploymentScripts resource alongside the
# (matching, canonical-scope) role definition. Only "deployments" was refused;
# a deployment script's runtime az-cli commands can issue role assignments this
# check never sees as JSON at all.
run_case_saying "deploymentScripts resource exits 1 (F5)" 1 \
  "resource(s) of a type this" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/deploymentscript-scope-escape-arm.json"

# Case 17 (F5): the same refusal, for Microsoft.Resources/deploymentStacks.
run_case_saying "deploymentStacks resource exits 1 (F5)" 1 \
  "resource(s) of a type this" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/deploymentstack-scope-escape-arm.json"

# Case 18 (F6): a role assignment binding the built-in Owner role, carrying no
# explicit `scope` so it correctly inherits the subscription-scope deployment
# (the scope axis finds nothing wrong). Only an explicit `scope` was ever
# checked; roleDefinitionId itself was unconstrained.
run_case_saying "unallowed roleDefinitionId (Owner) exits 1 (F6)" 1 \
  "roleDefinitionId is not one of the three roles CUDly assigns" \
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
run_case_saying "whitespace inside scope string literal exits 1" 1 \
  "not the canonical subscription scope" \
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
  --arm-file "${FIXTURES}/decorative-variables-arm.json" \
  "${GRANTS_ROLE_DEF_ONLY[@]}"

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
run_case_saying "miscased 'Scope' key on tenant-wide assignment exits 1" 1 \
  "role assignments must inherit the deployment scope" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/miscased-scope-arm.json"

# Case 22: a roleAssignment with "RoleDefinitionId" (capitalized) binding
# built-in Owner, no explicit scope. has("roleDefinitionId") never matched
# "RoleDefinitionId", so the roleDefinitionId allowlist (F6) was invisible.
run_case_saying "miscased 'RoleDefinitionId' key on Owner grant exits 1" 1 \
  "roleDefinitionId is not one of the three roles CUDly assigns" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/miscased-roledefinitionid-arm.json"

# Case 23: a roleAssignment with "Properties" (capitalized) wrapping an Owner
# roleDefinitionId. has("properties") never matched "Properties", so nothing
# inside it -- roleDefinitionId included -- was ever reached.
run_case_saying "miscased 'Properties' key on Owner grant exits 1" 1 \
  "roleDefinitionId is not one of the three roles CUDly assigns" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/miscased-properties-arm.json"

# Case 24: a second, actions-matching role definition whose "AssignableScopes"
# (capitalized) is the tenant-wide Microsoft.Capacity path. The canonical
# first role definition kept SCOPES non-empty, so the miscased entry's
# absence didn't even trip the "no assignableScopes found" fallback -- it
# just silently contributed nothing, and the template passed.
run_case_saying "miscased 'AssignableScopes' key exits 1" 1 \
  "names a scope above the subscription" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/miscased-assignablescopes-arm.json"

# Case 25 (Fix B): Microsoft.Authorization/roleEligibilityScheduleRequests
# (Azure PIM) binding built-in Owner. Grants a role the same way a plain
# roleAssignment does, under a property shape this check's
# roleAssignments-only type match never saw.
run_case_saying "PIM roleEligibilityScheduleRequests (Owner) exits 1" 1 \
  "resource(s) of a type this" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/pim-roleeligibility-owner-arm.json"

# Case 26 (Fix B): Microsoft.Authorization/roleAssignmentScheduleRequests
# (Azure PIM), same reasoning.
run_case_saying "PIM roleAssignmentScheduleRequests (Owner) exits 1" 1 \
  "resource(s) of a type this" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/pim-roleassignment-schedule-owner-arm.json"

# Case 27 (Fix B): the legacy ARM spelling for a role assignment as a child
# resource type path (Microsoft.Storage/storageAccounts/providers/
# roleAssignments) rather than a top-level roleAssignments resource with a
# scope property. Same grant, invisible to the same type-string match.
run_case_saying "legacy child-type roleAssignments (Owner) exits 1" 1 \
  "resource(s) of a type this" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/legacy-child-roleassignment-owner-arm.json"

# Case 28: the canonical scope expression written with uppercase ARM function
# and property names (CONCAT / SUBSCRIPTION / SUBSCRIPTIONID). ARM identifiers
# are case-insensitive the same way resource types are; normalize_scope_expr
# now folds case explicitly rather than relying on the ambient nocasematch
# scope, so this must still be accepted as canonical.
run_case "uppercase canonical scope expression exits 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/uppercase-canonical-scope-arm.json" \
  "${GRANTS_ROLE_DEF_ONLY[@]}"

# Case 29: an allowed roleDefinitionId ("[Variables('Roles').Reader]") written
# with different capitalization than ALLOWED_ROLE_DEFINITION_IDS's own
# spelling. Must still match -- same reasoning, made explicit rather than
# implicit in the roleDefinitionId comparison loop.
run_case "case-varied allowed roleDefinitionId exits 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/uppercase-allowed-roledefinitionid-arm.json" \
  "${GRANTS_ROLE_DEF_AND_READER[@]}"

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
run_case_saying "key-collision AssignableScopes/assignableScopes exits 1" 1 \
  "two case-variant spellings" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-assignablescopes-evil-first-arm.json"

# Case 31: RoleDefinitionId (evil, built-in Owner) then roleDefinitionId
# (benign, allowed Reader) on the same roleAssignment.properties object.
run_case_saying "key-collision RoleDefinitionId/roleDefinitionId exits 1" 1 \
  "two case-variant spellings" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-roledefinitionid-evil-first-arm.json"

# Case 32: Properties (evil, wraps Owner) then properties (benign, wraps
# allowed Reader) as two TOP-LEVEL keys on the same roleAssignment resource.
run_case_saying "key-collision Properties/properties exits 1" 1 \
  "two case-variant spellings" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-properties-evil-first-arm.json"

# Case 33: Type (evil, Microsoft.Resources/deployments) then type (benign,
# Microsoft.Authorization/roleAssignments) as two TOP-LEVEL keys. If the
# collision drops the "Type"/deployments evidence, the resource looks like an
# ordinary, fully-compliant roleAssignments grant -- invisible not just to
# REFUSED_TYPES but to every other check too, which is why this fixture
# carries an otherwise-allowed roleDefinitionId and no scope override.
run_case_saying "key-collision Type/type (evades REFUSED_TYPES too) exits 1" 1 \
  "two case-variant spellings" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-type-evil-first-arm.json"

# Case 34: benign-first control -- same collision as case 30, keys reversed.
run_case_saying "key-collision benign-first control still exits 1" 1 \
  "two case-variant spellings" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-assignablescopes-benign-first-arm.json"

# --- round-6: adversarial-review, root scope and refusal-list gaps ----------
# Two gaps CodeRabbit found in round 5's own fixes: the key-collision scan
# only looked inside `.resources`, and the legacy-child-roleAssignment
# refusal only named one parent resource type.

# Case 35: a root-level case-variant key collision -- "Resources" (evil,
# carries the tenant-wide grant from issue #1545) spelled first, "resources"
# (benign, canonical) spelled last. The round-5 collision scan was rooted at
# `.resources`, so it never treated the root document object itself as one of
# the objects being inspected; normalization then folds the two root keys the
# same last-entry-wins way as any other collision, silently keeping only the
# benign array and discarding the evil one before any downstream check runs.
run_case_saying "root-level key-collision Resources/resources exits 1" 1 \
  "two case-variant spellings" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-root-resources-evil-first-arm.json"

# Case 36 (Fix B follow-up): the same legacy child-scoped roleAssignment shape
# as case 27, under Microsoft.KeyVault/vaults instead of
# Microsoft.Storage/storageAccounts. REFUSED_TYPES named only the storage
# parent by exact string, so any other parent's
# "*/providers/roleAssignments" child -- granting the same way, invisible to
# the same roleAssignments-only type match -- passed silently. Matched by
# suffix now, the same way ESCAPE_TOKENS matches managementGroups independently
# of its provider spelling.
run_case_saying "legacy child-type roleAssignments under non-storage parent exits 1" 1 \
  "resource(s) of a type this" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/legacy-child-keyvault-roleassignment-owner-arm.json"

# --- issue #1681: who the grant goes to, and how many grants there are -------
# Every case above hands the checker a fixture smaller than the real template,
# which is what let both of these gaps live: a two-resource fixture exercises
# one grant, and the checker reported "OK: all 2 ..." while the template it
# guards has four. The fixtures below are the REAL template's grant set --
# generated from arm/CUDly-CrossSubscription/template.json with its prose-only
# metadata and outputs removed -- so they are checked against the checker's own
# built-in EXPECTED_GRANTS, with no --expected-grants override, exactly as CI
# checks the template itself.
#
# Case 37 is the baseline the rest are one edit away from. Without it the
# hostile cases below prove only that the checker refuses things, not that it
# accepts the template it is supposed to accept, and a checker that refuses
# everything passes every negative case ever written.
run_case "the real template's grant set exits 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/expected-set-baseline-arm.json"

# Case 38 (#1681 finding 1): the bypass itself. A fourth roleAssignments
# resource binding the ALLOWED custom purchaser role -- the one carrying
# Microsoft.Capacity/reservationOrders/purchase/action -- with no explicit
# scope, so it correctly inherits the subscription-scope deployment, to a
# hardcoded foreign principalId. Valid ARM, actions in parity, canonical
# scope, allowed role: every axis that existed before this case was written
# passes it, and it grants a stranger the ability to spend the customer's
# money.
run_case_saying "hardcoded foreign principalId on an allowed role exits 1 (#1681)" 1 \
  "principalId is not [parameters('servicePrincipalObjectId')]" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/foreign-principal-literal-arm.json"

# Case 39 (#1681 finding 1): the same grant with the object ID moved into a
# second parameter's default. It is no longer a literal anywhere in a
# principalId field, so a check that refused GUID-shaped literals rather than
# requiring the expected expression would pass it.
run_case_saying "principalId from another parameter exits 1 (#1681)" 1 \
  "principalId is not [parameters('servicePrincipalObjectId')]" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/foreign-principal-parameter-arm.json"

# Case 40 (#1681 finding 1): a roleAssignments resource with no principalId at
# all. Deleting the field must not be a way to be exempt from the check on its
# value -- "every principalId I found is correct" is satisfied by finding none.
run_case_saying "role assignment with no principalId exits 1 (#1681)" 1 \
  "roleAssignment.principalId: <absent>" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/absent-principal-arm.json"

# Case 41 (#1681 finding 2): a second copy of the Reader grant. Every field is
# one the checker allows, because it is a duplicate of a grant that is
# legitimate once, so no per-value check can object to it. Only asserting the
# expected set as a multiset catches a grant being made twice.
run_case_saying "a duplicated grant exits 1 (#1681)" 1 \
  "ARM template does not grant exactly the expected set" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/duplicate-grant-arm.json"

# Case 42 (#1681 finding 2): the Cost Management Reader grant deleted. Nothing
# about the remaining template is wrong, which is the point: before the
# expected set was asserted, removing a grant changed the count in the final
# "OK: all N ..." line and nothing else. The customer's cost data then stops
# being readable at the next deployment, with CI green.
run_case_saying "a removed grant exits 1 (#1681)" 1 \
  "ARM template does not grant exactly the expected set" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/missing-grant-arm.json"

# Case 43 (#1681 finding 2): a resource of a type no selector in the checker
# has ever heard of, and which is not on REFUSED_TYPES either. This is the
# whole reason the set is asserted rather than filtered: the refusal list is
# only ever as long as somebody's memory, and a type nobody has thought of is
# refused here without anyone having to think of it.
run_case_saying "an unrecognized resource type exits 1 (#1681)" 1 \
  "unrecognized resource type=microsoft.managedidentity/userassignedidentities" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/unrecognized-resource-type-arm.json"

# Cases 44-45: shapes the jq selectors cannot iterate. Both already failed
# closed, by aborting jq mid-pipeline and letting `set -e` surface jq's exit
# status (5) with jq's own message; neither is one of this script's documented
# outcomes, and neither tells the author what is wrong with the file.
run_case_saying "assignableScopes as a bare string exits 1, not 5" 1 \
  "properties.assignableScopes is a string, expected an array" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/assignablescopes-not-array-arm.json"

run_case_saying "resources as a bare string exits 1, not 5" 1 \
  ".resources is a string, expected an array" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/resources-not-array-arm.json"

# Case 46: an expected grant set with no entries in it. Every template on
# earth grants exactly the empty set of things beyond an empty set, so a
# checker that accepted this would report success on the strength of an
# assertion that says nothing.
EMPTY_GRANTS="$(mktemp)"
trap 'rm -rf "$TMP_TF_DIR"; rm -f "$EMPTY_GRANTS"' EXIT
printf '# no grants listed\n\n' > "$EMPTY_GRANTS"
run_case_saying "an empty expected grant set is refused" 1 \
  "the expected grant set is empty" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/expected-set-baseline-arm.json" \
  --expected-grants "$EMPTY_GRANTS"

# Case 47: the real sources, invoked with no flags at all, the way CI does.
# Every other case here hands the checker a fixture, so this is the only one
# that asserts the template and the TF module are in parity with each other
# right now, on every axis, rather than that the checker behaves correctly on
# something else.
run_case "the real sources pass with no flags" 0

# --- what an expression NAMES versus what it RESOLVES TO ---------------------
# Found by an independent adversarial review of the first three axes, each
# verified passing before the case was written. All three are the same defect:
# a check that compares the text of an ARM expression is asserting the name of
# a thing, and the binding between that name and the thing is somewhere else
# in the template, unasserted.

# Case 50: variables.roles.reader repointed at the built-in Owner definition.
# Every string this script compares is byte-identical afterwards -- the
# assignment still reads "[variables('roles').reader]", which is on the
# roleDefinitionId allowlist (#1658 F6) -- and the deployment grants Owner.
run_case_saying "a variable repointed at Owner exits 1 (#1681)" 1 \
  "ARM template does not grant exactly the expected set" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/variable-repointed-to-owner-arm.json"

# Case 51: the same, on customRoleDefinitionId, which is the grant that
# carries Microsoft.Capacity/reservationOrders/purchase/action.
run_case_saying "the custom role's variable repointed at Owner exits 1 (#1681)" 1 \
  "ARM template does not grant exactly the expected set" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/variable-customrole-repointed-arm.json"

# Case 52: a defaultValue on the principal parameter. Every principalId in the
# template still reads parameters('servicePrincipalObjectId') and passes the
# principal axis; the deployment prefills the object ID in the default.
run_case_saying "a defaultValue on the principal parameter exits 1 (#1681)" 1 \
  "has a defaultValue" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/principal-parameter-default-arm.json"

# Case 53: the parameter deleted outright. Deleting the declaration must not
# be the way out of the check on it.
run_case_saying "an undeclared principal parameter exits 1 (#1681)" 1 \
  "is never declared" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/principal-parameter-undeclared-arm.json"

# Case 54: the management-group schema with "#subscriptionDeploymentTemplate"
# appended as a URL fragment. ARM reads the path, which is the
# management-group template, widening every inherited grant to cover every
# child subscription; the pin was an unanchored substring test that read the
# fragment.
run_case_saying "the mgmt-group schema with a subscription fragment exits 1" 1 \
  "not a subscription-scoped deployment" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/schema-fragment-mgmt-group-arm.json"

# Case 55: a copy loop on a role assignment. One declaration is then N
# deployed grants whose properties can vary by copyIndex(), so the set
# asserted is not the set deployed.
run_case_saying "a copy loop on a role assignment exits 1" 1 \
  "carries a copy loop" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/copy-loop-arm.json"

# --- second review round: the same three defects, one step further out ------
# A second independent review of the fixes above found each of them still
# reachable by a neighbouring form, which is the shape this whole file keeps
# rediscovering: a guard written against the example is not a guard against the
# class.

# Case 56: the management-group schema again, with the sanctioned name as the
# tail of the URL FRAGMENT rather than appended to it. Anchoring on the file
# name with an optional fragment after it still read the fragment; ARM reads
# the path, so the path is what is matched now.
run_case_saying "the mgmt-group schema with the name in the fragment tail exits 1" 1 \
  "not a subscription-scoped deployment" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/schema-fragment-tail-arm.json"

# Case 57: condition:false on a grant. This is the copy loop's N=0 case: the
# declaration stays in the file for the grant set to count while the grant is
# not deployed, which is case 42's own invariant (a removed grant is refused)
# defeated by leaving the resource where it is.
run_case_saying "a condition on a role assignment exits 1" 1 \
  "carries a condition" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/condition-false-arm.json"

# Case 58: a case-variant key collision on a resource whose `type` is a
# number. The collision scan runs before every string-type guard in this
# script and concatenated that value into its message, so the scan that exists
# to refuse an ambiguous template aborted jq instead.
run_case_saying "a key collision on a non-string type exits 1, not 5" 1 \
  "two case-variant spellings" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/collision-non-string-type-arm.json"

# Case 59: a variable that resolves to an object rather than a string. The
# resolver filtered with `strings`, whose empty stream dropped the ENTIRE
# resource from the inventory -- a grant vanishing from the set that exists to
# count grants, in the one axis that would otherwise have caught it.
run_case_saying "a variable resolving to a non-string is recorded, not dropped" 1 \
  "unresolved:[variables('roles').reader]" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/variable-resolves-to-object-arm.json"

# Case 60: allowedValues on the principal parameter, the other half of case
# 52. A defaultValue answers for the customer; allowedValues constrains what
# the customer is allowed to answer.
run_case_saying "allowedValues on the principal parameter exits 1 (#1681)" 1 \
  "has allowedValues" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/principal-parameter-allowedvalues-arm.json"

# Case 61: a flag with no value is a usage error, which this script documents
# as exit 2. Reading "$2" unguarded made it a `set -u` abort instead.
run_case "a flag with no value exits 2" 2 --arm-file

# --- the rest of the uniterable shapes ---------------------------------------
# Cases 44-45 covered `resources` and `assignableScopes`; these are the
# siblings on the same selectors, each of which aborted jq mid-pipeline and
# surfaced exit 5. Issue #1681's own closing note asks for all of them.
run_case_saying "permissions[].actions as a bare string exits 1, not 5" 1 \
  "properties.permissions[0].actions is a string, expected an array" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/actions-not-array-arm.json"

run_case_saying "a non-object permissions[] element exits 1, not 5" 1 \
  "properties.permissions[1] is a string, expected an object" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/permissions-element-not-object-arm.json"

run_case_saying "a non-string assignableScopes[] element exits 1, not 5" 1 \
  "properties.assignableScopes[1] is a object, expected a string" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/assignablescopes-element-not-string-arm.json"

run_case_saying "a document whose root is an array exits 1, not 5" 1 \
  "root has type array, expected an object" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/root-not-object-arm.json"

# Neither of these is named *.json: one is deliberately not JSON and the other
# is empty, and the repository's check-json pre-commit hook reds every .json
# that fails to parse.
run_case_saying "a file that is not JSON exits 1, not 5" 1 \
  "is not valid JSON" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/not-json-arm.fixture"

# An empty file parses clean and yields no JSON value at all, which is a
# different shape from a root of the wrong type and reads as one in the
# message.
run_case_saying "an empty file exits 1, naming what it found" 1 \
  "root has type <no JSON value>" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/empty-arm.fixture"

# --- third review round ------------------------------------------------------

# Case 62: the same principalId key spelled twice in one object, the foreign
# value first. jq's parser collapses the two before any query runs, so the
# case-variant collision scan -- whose whole purpose is refusing an ambiguous
# template -- could not see the sharpest form of the ambiguity it exists for.
# Every check read the second value; a reviewer skimming the file reads the
# first.
# Not named *.json: the repository's check-json pre-commit hook refuses a
# duplicate key outright, which is a second, independent guard on this exact
# shape for any .json in the tree and the reason this fixture has to sit
# outside its reach to be usable as one.
run_case_saying "a duplicate principalId key exits 1 (#1681)" 1 \
  "declares the same JSON key twice" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/duplicate-principalid-key-arm.fixture"

# Case 63: the legacy child-scoped role assignment in its RELATIVE spelling,
# "providers/roleAssignments" with no leading slash, which is the idiomatic
# form inside a parent resource's own resources array. The refusal matched the
# absolute suffix only, so the form an author would actually write was the one
# it missed, while its comment claimed coverage of any parent type.
run_case_saying "legacy child roleAssignments in relative spelling exits 1" 1 \
  "resource(s) of a type this" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/legacy-child-relative-type-arm.json"

# Case 64: a non-string element inside permissions[].actions. The shape gate
# checked that the four permission lists are arrays but not what is in them,
# and extract_arm_list calls ascii_downcase on each element, which aborts jq
# on a number and surfaces exit 5.
run_case_saying "a non-string actions[] element exits 1, not 5" 1 \
  "properties.permissions[0].actions[11] is a number, expected a string" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/actions-element-not-string-arm.json"

# Case 65: a principalId on the role DEFINITION's properties rather than on a
# role assignment. Nothing deploys a grant from there, and that is the point:
# the principal axis is keyed on the property name wherever it appears, not on
# the resource type, because the set of types that carry one is not a list this
# script can finish writing. Narrowing the collection to roleAssignments
# resources leaves every other case in this file green.
run_case_saying "a principalId outside a roleAssignment is still read" 1 \
  "principalId is not [parameters('servicePrincipalObjectId')]" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/principalid-on-roledefinition-arm.json"

# Case 66: include_capacity_provider_scope defaulting to true. The TF path then
# silently reacquires the tenant-wide /providers/Microsoft.Capacity grant this
# whole guard exists to keep out (issue #1545). The assertion predates this
# change and had no case: only its fail-closed branch, the missing
# variables.tf, was covered.
TRUE_DEFAULT_TF_DIR="$(mktemp -d)"
cp "${FIXTURES}/matching-tf.tf.fixture" "${TRUE_DEFAULT_TF_DIR}/main.tf"
cat >> "${TRUE_DEFAULT_TF_DIR}/main.tf" <<'HCL'

locals {
  capacity_assignable_scopes = compact([
    "/subscriptions/00000000-0000-0000-0000-000000000001",
    var.include_capacity_provider_scope ? "/providers/Microsoft.Capacity" : "",
  ])
}
HCL
cat > "${TRUE_DEFAULT_TF_DIR}/variables.tf" <<'HCL'
variable "include_capacity_provider_scope" {
  type    = bool
  default = true
}
HCL
run_case_saying "include_capacity_provider_scope defaulting to true exits 1" 1 \
  "must default to false" \
  --tf-file  "${TRUE_DEFAULT_TF_DIR}/main.tf" \
  --arm-file "${FIXTURES}/matching-arm.json" \
  "${GRANTS_ROLE_DEF_ONLY[@]}"

# Case 67: an unknown flag is a usage error, exit 2.
run_case "an unknown flag exits 2" 2 --bogus-flag

# Case 68: --expected-grants naming a file that does not exist. Letting `set
# -e` catch the failed read would exit with the shell's message rather than
# one that says which file and why it mattered.
run_case_saying "a missing expected-grants file exits 1" 1 \
  "expected-grants file not found" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/matching-arm.json" \
  --expected-grants "${FIXTURES}/expected/no-such-file.grants"

# --- fourth review round -----------------------------------------------------

# Case 72: a template with a dotted key beside a nested path of the same
# spelling, and no duplicate key anywhere. The duplicate-key detector compared
# `--stream` paths joined with a dot, which is not injective, so it reported a
# duplicate in a legal template. A guard that reds valid input invites being
# deleted, which is how issue #1545 shipped in the first place.
run_case "a dotted key beside a nested path of the same spelling exits 0" 0 \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/dotted-key-no-duplicate-arm.json"

# Case 73: a variable whose value carries a newline and a tab. It reaches the
# grant inventory through the roleDefinitionId allowlist untouched, because
# that allowlist compares the expression text and the expression is allowed;
# only the resolved value carries the newline. Unflattened it split one
# resource across two records, so the diagnostic described grants the template
# does not have.
run_case_saying "a newline in a resolved variable stays one record" 1 \
  "roledefinitions/acdd72a7-3385-48ef-bd42-f606fba81ae7 junk more principalId=" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/variable-value-with-newline-arm.json"

# Case 74: a variable resolving to the empty string, which reaches the
# inventory the same way. IFS=$'"'"'\t'"'"' treats tab as IFS whitespace, so an empty
# field would collapse and shift every field after it.
run_case_saying "an empty resolved variable is named, not collapsed" 1 \
  "roleDefinitionId=<empty> principalId=<servicePrincipalObjectId-parameter>" \
  --tf-file  "${FIXTURES}/matching-tf.tf.fixture" \
  --arm-file "${FIXTURES}/variable-value-empty-arm.json"

# --- the arm/ sweep ----------------------------------------------------------
# The sweep refuses any .json under arm/ that this checker does not check,
# because every assertion in it is specific to the one template it knows
# about. It runs only when no --arm-file is given, so exercising it means
# invoking the checker with no flags -- against a REPO ROOT that is a copy,
# since a test that writes a second template into the repo's own arm/ to see
# what happens is a test that can leave one there.
#
# The checker resolves its defaults from BASH_SOURCE, so a copy of it under
# ${SWEEP_ROOT}/scripts finds ${SWEEP_ROOT}/arm and ${SWEEP_ROOT}/terraform.
SWEEP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_TF_DIR" "$SWEEP_ROOT" "$TRUE_DEFAULT_TF_DIR"; rm -f "$EMPTY_GRANTS"' EXIT
mkdir -p "${SWEEP_ROOT}/scripts"
cp "$CHECK" "${SWEEP_ROOT}/scripts/"
cp -R "${REPO_ROOT}/arm" "${SWEEP_ROOT}/arm"
mkdir -p "${SWEEP_ROOT}/terraform/modules/iam/azure"
cp -R "${REPO_ROOT}/terraform/modules/iam/azure/cudly-reservation-role" \
      "${SWEEP_ROOT}/terraform/modules/iam/azure/cudly-reservation-role"

# Case 48: the control. The copy is the repo's own tree, so it must pass for
# the same reasons case 47 does -- otherwise case 49 below would be refusing
# the copy rather than the file added to it.
CHECK_UNDER_TEST="${SWEEP_ROOT}/scripts/check-azure-role-parity.sh"
run_case "an unmodified copy of the tree passes the sweep" 0

# Case 49 (#1681): a second .json under arm/. Nothing in this checker looks at
# it, so a template deploying into customer subscriptions would be onboarding
# them with no assertion made about what it grants. Naming the one file the
# checker already knows about is how a guard fails to reach its own sibling
# site, so the set is discovered and anything outside it is refused.
cp "${SWEEP_ROOT}/arm/CUDly-CrossSubscription/template.json" \
   "${SWEEP_ROOT}/arm/second-template.json"
run_case_saying "a second, unchecked template under arm/ exits 1 (#1681)" 1 \
  "holds JSON this guard does not check"
rm -f "${SWEEP_ROOT}/arm/second-template.json"

# Case 71: the same file as a SYMLINK. `find -type f` reports a symlink as
# neither a file nor anything to refuse, so the second template was swept past
# rather than found; `find -L` resolves it first.
ln -s "${SWEEP_ROOT}/arm/CUDly-CrossSubscription/template.json" \
      "${SWEEP_ROOT}/arm/symlinked-template.json"
run_case_saying "a symlinked second template under arm/ exits 1" 1 \
  "holds JSON this guard does not check"
rm -f "${SWEEP_ROOT}/arm/symlinked-template.json"
CHECK_UNDER_TEST="$CHECK"

echo ""
echo "Results: ${pass} passed, ${fail} failed."
[[ "$fail" -eq 0 ]]
