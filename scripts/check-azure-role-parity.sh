#!/usr/bin/env bash
# check-azure-role-parity.sh
#
# Asserts that the Azure custom-role actions list is identical (case-insensitively)
# in both sources of truth:
#
#   TF module : terraform/modules/iam/azure/cudly-reservation-role/main.tf
#   ARM template: arm/CUDly-CrossSubscription/template.json
#
# Exit 0 = lists match.
# Exit 1 = lists differ; the diff is printed to stderr.
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

if [[ -z "$DIFF" ]]; then
  echo "OK: ARM and TF actions lists match (${#TF_ACTIONS} bytes, case-insensitive)."
  exit 0
fi

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
