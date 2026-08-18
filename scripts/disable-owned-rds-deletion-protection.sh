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
# text. `output -json` returns `{}` for that state and the cases separate
# cleanly:
#   no outputs at all        -> already destroyed, skip
#   outputs but not this one -> fail loudly, with the remedy named
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
#   1  the state publishes outputs but not the owned identifier
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
  echo "error: state '${STATE_DIR}' publishes outputs but not 'database_instance_identifier'," >&2
  echo "       so the instance this state owns cannot be identified. Re-apply the state to" >&2
  echo "       publish the output, or remove deletion protection on that one instance by" >&2
  echo "       hand, then re-run the destroy. Refusing to fall back to an identifier" >&2
  echo "       prefix, which strips protection from instances this state does not own." >&2
  exit 1
fi

echo "This state owns RDS instance '$OWNED_INSTANCE'"

aws rds describe-db-instances --query 'DBInstances[].DBInstanceIdentifier' --output text \
  | tr '\t' '\n' \
  | "${SCRIPT_DIR}/select-owned-name.sh" "$OWNED_INSTANCE" \
  | while IFS= read -r INSTANCE_ID; do
      echo "Disabling deletion protection on $INSTANCE_ID..."
      aws rds modify-db-instance --db-instance-identifier "$INSTANCE_ID" \
        --no-deletion-protection --apply-immediately
    done
