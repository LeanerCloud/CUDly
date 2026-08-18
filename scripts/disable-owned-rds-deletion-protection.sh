#!/usr/bin/env bash
# disable-owned-rds-deletion-protection.sh
#
# Removes deletion protection from the one RDS instance a Terraform state owns,
# and from nothing else.
#
# Usage: disable-owned-rds-deletion-protection.sh TERRAFORM_STATE_DIR
#
# Run before `terraform destroy` on a state whose instance was applied with
# deletion_protection = true (terraform/modules/database/aws/main.tf): the
# destroy otherwise fails on the protected instance.
#
# The state directory is a required argument rather than a constant because it
# is the identity of what gets modified. Every caller happens to pass
# terraform/environments/aws today, but which state the identifier is read from
# is the whole safety property here, so it stays visible at each call site.
#
# The owned identifier is read from `terraform output` on the state the destroy
# is about to tear down and compared by exact equality against every instance in
# the account, by scripts/select-owned-name.sh -- the same selector
# force-delete-owned-ecr-repo.sh uses, so the comparison is hardened in one
# place for both resources.
#
# The callers used to select by the `cudly-dev*` / `cudly-staging*` identifier
# prefix, which also matches `cudly-dev-prod-mirror`,
# `cudly-dev-<hex>-postgres-replica` and any operator-named instance sharing the
# prefix, and stripped deletion protection from every one of them (#1821).
# Deletion protection is the last line of defence on a database, so an instance
# this state does not own must never be touched: it would be left exposed to the
# next destroy that does match it. The `cudly-staging` prefix also spanned both
# staging states, whose instances are each
# `cudly-staging-<random_id.suffix.hex>-postgres`, so either cleanup job
# unprotected the other's database.
#
# Reads the identifier through `output -json`, not `output -raw`: on a state
# with no outputs at all, `output -raw <name>` exits 0 and writes its "No
# outputs found" warning to STDOUT, so the identifier would become that warning
# text.
#
# The five states `terraform output -json` can be in were measured rather than
# reasoned about, because they do not behave alike and only one of them is loud
# by default. Measured on terraform 1.10.0, the version TF_VERSION pins in the
# workflows that call this, and on 1.14.4; identical on both. `output -json`
# exits 0 in ALL five, so the exit code carries no information here and every
# branch below is driven by the payload:
#
#   state              | -json         | jq length | jq -er .value | handled as
#   -------------------|---------------|-----------|---------------|------------
#   no state file      | {}            | 0         | exit 1        | skip, exit 0
#   state, no outputs  | {}            | 0         | exit 1        | skip, exit 0
#   key absent         | {...} w/o key | >=1       | exit 1        | exit 1, remedy
#   key present, null  | {"value":null}| 1         | exit 1        | exit 1, remedy
#   key present, ""    | {"value":""}  | 1         | exit 0, ""    | exit 1, remedy
#
# The last row is the trap: an empty string is neither null nor false, so `jq
# -er` accepts it and hands the caller a valid-looking empty identifier. It is
# caught by its own check rather than left to the selector, for two reasons. The
# selector does refuse it (exit 2, so nothing is ever unprotected), but only
# after `describe-db-instances` has already run, and its message names the
# selector's contract rather than the operator's actual problem. The two causes
# also need different fixes, so they get different messages: "key absent" means
# the state predates the output and wants an apply, while "empty" means the
# state HAS the output and it resolved to nothing, which is a real defect in the
# state or the module and an apply will not fix it.
#
# Nothing is swallowed. The `2>/dev/null || true` the callers used to carry
# reported success after a failed listing or a failed modify, and a failed
# listing is indistinguishable from an account with no instances, so the step
# did nothing and `terraform destroy` then failed on an instance that was still
# protected. "Already gone" needs no swallowing: the instance is simply absent
# from the listing, the selector prints nothing and exits 0, and the loop body
# never runs.
#
# Exit codes:
#   0  completed, including the "state already destroyed" and "instance already
#      gone" cases, which are normal outcomes and not errors
#   1  the owned identifier cannot be resolved from a state that has outputs:
#      the key is absent or null (state predates the output), or it is present
#      and empty (state or module defect). Distinct messages, distinct remedies.
#   2  usage error (wrong arity, or a state directory that does not exist)
#   *  anything the AWS CLI, terraform, jq or the selector fails with, unmasked

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ $# -ne 1 ]]; then
  echo "usage: $(basename "$0") TERRAFORM_STATE_DIR" >&2
  exit 2
fi

STATE_DIR="$1"

if [[ ! -d "$STATE_DIR" ]]; then
  echo "error: terraform state directory '${STATE_DIR}' does not exist" >&2
  exit 2
fi

OUTPUTS_JSON="$(terraform -chdir="$STATE_DIR" output -json)"
if [[ "$(jq -r 'length' <<<"$OUTPUTS_JSON")" -eq 0 ]]; then
  echo "State has no outputs; the stack is already destroyed and there is no RDS instance to unprotect."
  exit 0
fi

# `database_instance_identifier` was added with #1821, so a state last applied
# before it does not publish it yet. That is a loud failure with a named remedy
# rather than a fallback: the only fallback available is the identifier prefix
# this script exists to remove.
if ! OWNED_INSTANCE="$(jq -er '.database_instance_identifier.value' <<<"$OUTPUTS_JSON")"; then
  echo "error: state '${STATE_DIR}' has outputs, but 'database_instance_identifier' is" >&2
  echo "       absent or null, so the instance this state owns cannot be identified." >&2
  echo "       This state was last applied before that output existed (#1821)." >&2
  echo "       Fix: re-apply this state to publish the output, or remove deletion" >&2
  echo "       protection on that one instance by hand, then re-run the destroy." >&2
  echo "       Refusing to fall back to an identifier prefix, which strips protection" >&2
  echo "       from instances this state does not own." >&2
  exit 1
fi

# An empty string is neither null nor false, so `jq -er` above accepts it. Its
# own check, before anything is echoed as owned and before any AWS call, because
# it means something different from the branch above and an apply will not fix
# it. Whitespace is refused for the same reason the selector refuses it: no
# DBInstanceIdentifier contains any, so it is not a value this was handed on
# purpose.
case "$OWNED_INSTANCE" in
  '' | *[![:graph:]]*)
    echo "error: state '${STATE_DIR}' publishes 'database_instance_identifier', but it" >&2
    echo "       resolved to '${OWNED_INSTANCE}', which is not an instance identifier." >&2
    echo "       Unlike an absent output, re-applying will NOT fix this: the output is" >&2
    echo "       there and empty, so either the state is corrupt or the module stopped" >&2
    echo "       populating it. Inspect 'terraform -chdir=${STATE_DIR} output -json'" >&2
    echo "       before destroying anything. Refusing to fall back to an identifier" >&2
    echo "       prefix, which strips protection from instances this state does not own." >&2
    exit 1
    ;;
esac

echo "This state owns RDS instance '$OWNED_INSTANCE'"

aws rds describe-db-instances --query 'DBInstances[].DBInstanceIdentifier' --output text \
  | tr '\t' '\n' \
  | "${SCRIPT_DIR}/select-owned-name.sh" "$OWNED_INSTANCE" \
  | while IFS= read -r INSTANCE_ID; do
      echo "Disabling deletion protection on $INSTANCE_ID..."
      aws rds modify-db-instance --db-instance-identifier "$INSTANCE_ID" \
        --no-deletion-protection --apply-immediately
    done
