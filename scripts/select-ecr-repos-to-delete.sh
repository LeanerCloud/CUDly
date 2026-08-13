#!/usr/bin/env bash
# select-ecr-repos-to-delete.sh
#
# Selects which ECR repositories a destroy workflow may force-delete.
#
# Reads the account's repository names on stdin, one per line, and prints back
# only the ones that are byte-for-byte identical to the owned name passed as the
# single argument. Nothing else is ever printed, so the caller can pipe the
# output straight into `aws ecr delete-repository --force`.
#
# The owned name comes from `terraform output -raw ecr_repository_name`, i.e.
# from the state the destroy is about to tear down, so it is the name this
# environment actually created rather than a pattern someone hopes only matches
# that name. The repository is `local.stack_name`
# (terraform/environments/aws/main.tf), which carries a random suffix, so no
# literal list can be hardcoded here and no prefix describes it uniquely:
# `cudly-dev-<hex>-backup` shares every prefix the real repository has.
#
# The previous filter was `contains(repositoryName,'cudly-dev')` evaluated
# inside the destroy workflow, which also selected `backup-cudly-dev` and
# `cudly-dev-prod-mirror` and force-deleted every image in them. A
# `starts_with`/`case cudly-dev*` allow-list, the shape cleanup-staging.yml
# uses, still selects `cudly-dev-prod-mirror`; equality is the only comparison
# that does not.
#
# Exit codes:
#   0  selection completed (an empty selection is normal -- the repository may
#      already be gone, and the caller must not treat that as an error, so it
#      is reported on stderr rather than through the exit code)
#   2  usage error, including an empty or whitespace-bearing owned name, which
#      is what a failed `terraform output` looks like. Never degrades into
#      "select nothing" or "select everything".

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $(basename "$0") OWNED_REPOSITORY_NAME < repository-names" >&2
  echo "  reads candidate repository names on stdin, one per line" >&2
  exit 2
fi

owned="$1"

# ECR repository names contain no whitespace, so anything that does is not a
# name this script was handed on purpose -- most likely an empty or
# warning-polluted `terraform output`. Refuse rather than guess.
case "$owned" in
  '' | *[![:graph:]]*)
    echo "error: owned repository name must be non-empty and free of whitespace; got '${owned}'" >&2
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
  echo "note: '${owned}' is not present in the listing on stdin; nothing to delete (already deleted, or the listing came from a different region or account)" >&2
fi
