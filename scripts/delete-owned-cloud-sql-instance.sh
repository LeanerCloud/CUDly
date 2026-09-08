#!/usr/bin/env bash
# delete-owned-cloud-sql-instance.sh
#
# Deletes the one Cloud SQL instance a Terraform state owns, and nothing else,
# then removes the three resources that instance owns from state.
#
# Usage: delete-owned-cloud-sql-instance.sh TERRAFORM_STATE_DIR GCP_PROJECT_ID
#
# Run before `terraform destroy` on the GCP staging state: destroying
# google_sql_user and google_sql_database through Terraform deadlocks on
# PostgreSQL object ownership, and github-staging.tfvars applies the instance
# with database_deletion_protection = true, so the destroy fails on it anyway
# unless it is gone first.
#
# The state directory and project id are required arguments rather than
# constants because they are the identity of what gets deleted. Every caller
# happens to pass terraform/environments/gcp today, but which state the name
# is read from is the whole safety property here, so it stays visible at each
# call site.
#
# The owned name is read from `terraform output` on the state the destroy is
# about to tear down and compared by exact equality against every instance in
# the project, by scripts/select-owned-name.sh -- the same selector
# force-delete-owned-ecr-repo.sh and disable-owned-rds-deletion-protection.sh
# use. The step this replaces selected by
# `gcloud sql instances list --filter="name:cudly-staging" ... | head -1`
# (#1971). Measured on Cloud SDK 456.0.0 with `gcloud config configurations
# list --filter=...` (a local-only evaluation, no API call, so no live account
# is needed to reproduce this): `name:cudly-staging` matched every hyphenated
# name sharing that substring, and gcloud printed "WARNING: --filter :
# operator evaluation is changing for consistency across Google APIs ...
# currently matches but will not match in the near future" -- per `gcloud
# topic filters`, the new `:` semantics are an anchored word match, under
# which this filter matches NOTHING, so a future SDK silently turns this
# selection into a no-op. `name=...` is case-folded (`CUDLY-STAGING-POSTGRES`
# matched too) and is also deprecated for the APIs where it behaves like `:`.
# No `--filter` form is a stable byte-equality test, so the listing below is
# passed UNFILTERED and the comparison happens in the shell, exactly as the
# ECR and RDS callers already do.
#
# The instance name is deterministic, not random-suffixed:
# terraform/modules/database/gcp/main.tf:36 sets
# name = "${var.service_name}-postgres", and service_name resolves to
# "${project_name}-${environment}" (terraform/environments/gcp/main.tf:91,
# github-staging.tfvars), so on staging it is the fixed string
# "cudly-staging-postgres". Equality still matters despite the fixed name: the
# same module creates "cudly-staging-postgres-replica" when
# enable_read_replica is set, every prefix of the real name also matches that
# sibling, and an operator-named "cudly-staging-*" instance or
# "backup-cudly-staging" would match a substring filter too.
#
# `gcloud sql instances delete` is synchronous (its `--async` flag is what
# opts out of waiting), so its exit status IS the deletion confirmation; no
# polling loop is needed, and none is run here.
#
# The three `terraform state rm` lines below are new in effect even though the
# step already ran three state rm commands: those addresses were root-level
# (`google_sql_database_instance.main`), but the resources live under
# `module.database` (terraform/environments/gcp/database.tf:5;
# scripts/gcp-import-dev-state.sh:273-288 imports them module-qualified).
# Measured on Terraform 1.14.7 against a state holding those three
# module-qualified resources: `terraform state rm
# google_sql_database_instance.main` exits 1 "No matching objects found" and
# leaves the state untouched; `terraform state rm
# module.database.google_sql_database_instance.main` removes it and exits 0.
# The old root-level addresses, combined with `2>/dev/null || true`, made
# every run of that step a silent no-op. Correcting the addresses makes the
# removal real for the first time, which is why it must run only after a
# successful delete and must not swallow a failure: an instance that is still
# present must stay in state, or a re-run would try to delete it again with no
# state entry to reconcile against, and `terraform destroy` would try to
# manage it too.
#
# Nothing is swallowed. The old step carried `|| true` on the delete and
# `2>/dev/null || true` on the state rm calls, so a failed delete or a failed
# state rm both reported success and the instance was left running (still
# billing) or the step moved on with the deletion undone. "Already gone" needs
# no swallowing: the selector prints nothing and this script exits 0 without
# touching state.
#
# Cites: #1971, #1592, #1820, #1821.
#
# Exit codes:
#   0  completed, including the "state already destroyed" and "instance
#      already gone" cases, which are normal outcomes and not errors
#   1  the owned instance name cannot be resolved from a state that has
#      outputs: the key is absent or null (state predates the output), or it
#      is present and empty (state or module defect). Distinct messages,
#      distinct remedies.
#   2  usage error (wrong arity, a state directory that does not exist, or a
#      malformed project id)
#   *  anything gcloud, terraform, jq or the selector fails with, unmasked

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ $# -ne 2 ]]; then
  echo "usage: $(basename "$0") TERRAFORM_STATE_DIR GCP_PROJECT_ID" >&2
  exit 2
fi

STATE_DIR="$1"
PROJECT="$2"

if [[ ! -d "$STATE_DIR" ]]; then
  echo "error: terraform state directory '${STATE_DIR}' does not exist" >&2
  exit 2
fi

# Validate the shape, not merely non-emptiness: a whitespace-only or malformed
# value would otherwise reach `gcloud --project=`, match no instance, and
# silently skip the deletion this script exists to perform.
if ! printf '%s' "$PROJECT" | grep -Eq '^[a-z][a-z0-9-]{5,29}$'; then
  echo "error: GCP project id is unset or malformed ('${PROJECT}'); refusing to run Cloud SQL cleanup" >&2
  exit 2
fi

OUTPUTS_JSON="$(terraform -chdir="$STATE_DIR" output -json)"
# On its own line, not inlined into the `if` test: a jq failure inside
# `"$(jq ...)"` there would substitute an empty string, and `[[ "" -eq 0 ]]`
# evaluates true, treating a broken `terraform output` (non-JSON on stdout)
# the same as zero outputs -- a silent "already destroyed" exit. Assigned to
# its own variable, the failing command substitution's exit status is the
# assignment statement's own exit status, so `set -e` catches it here.
OUTPUTS_LENGTH="$(jq -r 'length' <<<"$OUTPUTS_JSON")"
if [[ "$OUTPUTS_LENGTH" -eq 0 ]]; then
  echo "State has no outputs; the stack is already destroyed and there is no Cloud SQL instance to delete."
  exit 0
fi

if ! OWNED_INSTANCE="$(jq -er '.database_instance_name.value' <<<"$OUTPUTS_JSON")"; then
  echo "error: state '${STATE_DIR}' has outputs, but 'database_instance_name' is" >&2
  echo "       absent or null, so the instance this state owns cannot be identified." >&2
  echo "       Fix: re-apply this state to publish the output, or delete that one" >&2
  echo "       instance by hand, then re-run the destroy. Refusing to fall back to a" >&2
  echo "       name pattern, which selects instances this state does not own." >&2
  exit 1
fi

case "$OWNED_INSTANCE" in
  '' | *[![:graph:]]*)
    echo "error: state '${STATE_DIR}' publishes 'database_instance_name', but it" >&2
    echo "       resolved to '${OWNED_INSTANCE}', which is not an instance name." >&2
    echo "       Unlike an absent output, re-applying will NOT fix this: the output is" >&2
    echo "       there and empty. Inspect 'terraform -chdir=${STATE_DIR} output -json'" >&2
    echo "       before destroying anything. Refusing to fall back to a name pattern." >&2
    exit 1
    ;;
esac

echo "This state owns Cloud SQL instance '$OWNED_INSTANCE'"

# Unfiltered on purpose: the equality test is the selector's, not gcloud's.
SELECTED="$(gcloud sql instances list --project="$PROJECT" --format='value(name)' \
  | "${SCRIPT_DIR}/select-owned-name.sh" "$OWNED_INSTANCE")"

if [[ -z "$SELECTED" ]]; then
  echo "Cloud SQL instance '$OWNED_INSTANCE' is not present in project '$PROJECT'; nothing to delete, state left untouched."
  exit 0
fi

echo "Deleting Cloud SQL instance $SELECTED..."
gcloud sql instances delete "$SELECTED" --project="$PROJECT" --quiet
echo "Cloud SQL instance $SELECTED deleted."

# Only after the delete above returned 0, and only the resources it removed.
# Module-qualified: the root-level addresses the workflow used to carry matched
# nothing (#1971).
for ADDR in \
  module.database.google_sql_database_instance.main \
  module.database.google_sql_database.main \
  module.database.google_sql_user.main; do
  terraform -chdir="$STATE_DIR" state rm "$ADDR"
done
