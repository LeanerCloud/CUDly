#!/usr/bin/env bash
# select-owned-name.sh
#
# Selects which AWS resources a destroy workflow may act destructively on.
#
# Reads the account's resource names on stdin, one per line, and prints back
# only the ones that are byte-for-byte identical to the owned name passed as the
# single argument. Nothing else is ever printed, so the caller can pipe the
# output straight into the destructive command.
#
# The owned name comes from `terraform output` on the state the destroy is about
# to tear down, so it is the name this environment actually created rather than
# a pattern someone hopes only matches that name. Both current callers pass a
# name derived from `local.stack_name`
# (terraform/environments/aws/main.tf), which carries a random suffix, so no
# literal list can be hardcoded here and no prefix describes it uniquely:
# `cudly-dev-<hex>-backup` shares every prefix the real name has.
#
# The comparison is deliberately resource-agnostic, and shared rather than
# copied per resource: two copies of it would have to be hardened in lockstep,
# and a guard landing on one resource and not its sibling is precisely how #1592
# became #1820 and then #1821. The callers are:
#
#   force-delete-owned-ecr-repo.sh          `aws ecr delete-repository --force`
#   disable-owned-rds-deletion-protection.sh `aws rds modify-db-instance
#                                            --no-deletion-protection`
#
# The filters this replaced were `contains(repositoryName,'cudly-dev')` and
# `starts_with(DBInstanceIdentifier,'cudly-dev')` evaluated inside the destroy
# workflows. `contains` also selected `backup-cudly-dev` and
# `cudly-dev-prod-mirror`; `starts_with` still selects `cudly-dev-prod-mirror`
# and `cudly-dev-<hex>-replica`. Equality is the only comparison that does not.
#
# Exit codes:
#   0  selection completed (an empty selection is normal -- the resource may
#      already be gone, and the caller must not treat that as an error, so it
#      is reported on stderr rather than through the exit code)
#   2  usage error, including an empty or whitespace-bearing owned name, which
#      is what a failed `terraform output` looks like. Never degrades into
#      "select nothing" or "select everything".

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $(basename "$0") OWNED_NAME < candidate-names" >&2
  echo "  reads candidate resource names on stdin, one per line" >&2
  exit 2
fi

owned="$1"

# Neither an ECR repository name nor an RDS DBInstanceIdentifier may contain
# whitespace, so anything that does is not a name this script was handed on
# purpose -- most likely an empty or warning-polluted `terraform output`. Refuse
# rather than guess.
case "$owned" in
  '' | *[![:graph:]]*)
    echo "error: owned name must be non-empty and free of whitespace; got '${owned}'" >&2
    exit 2
    ;;
esac

found=0

# `|| [[ -n "$candidate" ]]` so a final line with no trailing newline is still
# compared. `read` returns non-zero on such a line, which would otherwise drop
# the one entry the caller is looking for and report an empty selection.
while IFS= read -r candidate || [[ -n "$candidate" ]]; do
  # Quoted right-hand side: [[ ]] would otherwise treat it as a glob pattern,
  # which is the same class of over-matching this script exists to remove.
  if [[ "$candidate" == "$owned" ]]; then
    printf '%s\n' "$candidate"
    found=1
  fi
done

# An empty selection is a normal outcome (exit 0 above), but it is also what a
# listing taken from the wrong region or account looks like. Say which name was
# looked for so the two are distinguishable in the caller's log.
if [[ "$found" -eq 0 ]]; then
  echo "note: '${owned}' is not present in the listing on stdin; nothing to act on (already deleted, or the listing came from a different region or account)" >&2
fi
