#!/usr/bin/env bash
# setup.sh — Create the CUDly Azure AD service principal, deploy role assignments,
# and print the values needed to register the subscription in CUDly.
#
# Prerequisites:
#   az login (with an account that has Application Administrator + Owner/User Access
#   Administrator on the target subscription)
#
# Usage:
#   ./setup.sh [--subscription <subscription-id>] [--app-name <name>]
#
# The script is idempotent: re-running it reuses the existing App Registration
# if one with the same display name already exists.
#
# Output: prints all values needed to register the account in CUDly.

set -euo pipefail

APP_NAME="CUDly"
SUBSCRIPTION_ID=""

# ── Argument parsing ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --subscription) SUBSCRIPTION_ID="$2"; shift 2 ;;
    --app-name)     APP_NAME="$2";        shift 2 ;;
    *) echo "Unknown argument: $1"; exit 1 ;;
  esac
done

# ── Resolve subscription ───────────────────────────────────────────────────────
if [[ -z "$SUBSCRIPTION_ID" ]]; then
  SUBSCRIPTION_ID=$(az account show --query id -o tsv)
fi
az account set --subscription "$SUBSCRIPTION_ID"
TENANT_ID=$(az account show --query tenantId -o tsv)
echo "Subscription : $SUBSCRIPTION_ID"
echo "Tenant       : $TENANT_ID"
echo ""

# ── Create or reuse App Registration ──────────────────────────────────────────
echo "Looking for existing App Registration '${APP_NAME}'..."
APP_ID=$(az ad app list --display-name "$APP_NAME" --query "[0].appId" -o tsv 2>/dev/null || true)

if [[ -z "$APP_ID" || "$APP_ID" == "None" ]]; then
  echo "Creating App Registration '${APP_NAME}'..."
  APP_ID=$(az ad app create \
    --display-name "$APP_NAME" \
    --sign-in-audience AzureADMyOrg \
    --query appId -o tsv)
  echo "Created App: $APP_ID"
else
  echo "Reusing existing App: $APP_ID"
fi

# ── Create or reuse Service Principal ─────────────────────────────────────────
SP_OBJECT_ID=$(az ad sp show --id "$APP_ID" --query id -o tsv 2>/dev/null || true)
if [[ -z "$SP_OBJECT_ID" || "$SP_OBJECT_ID" == "None" ]]; then
  echo "Creating Service Principal..."
  SP_OBJECT_ID=$(az ad sp create --id "$APP_ID" --query id -o tsv)
fi
echo "SP Object ID : $SP_OBJECT_ID"
echo ""

# ── Create client secret ───────────────────────────────────────────────────────
echo "Creating client secret (valid 2 years)..."
CLIENT_SECRET=$(az ad app credential reset \
  --id "$APP_ID" \
  --display-name "CUDly-$(date +%Y%m%d)" \
  --years 2 \
  --query password -o tsv)

# ── Deploy ARM role assignments ────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
echo "Deploying role assignments to subscription ${SUBSCRIPTION_ID}..."
az deployment sub create \
  --location eastus \
  --template-file "${SCRIPT_DIR}/template.json" \
  --parameters servicePrincipalObjectId="$SP_OBJECT_ID" \
  --name "CUDly-CrossSubscription" \
  --no-prompt \
  --output none

echo ""
echo "══════════════════════════════════════════════════════════════"
echo "  CUDly Azure account registration values"
echo "══════════════════════════════════════════════════════════════"
echo "  provider           : azure"
echo "  azure_subscription_id : ${SUBSCRIPTION_ID}"
echo "  azure_tenant_id       : ${TENANT_ID}"
echo "  azure_client_id       : ${APP_ID}"
echo "  client_secret         : ${CLIENT_SECRET}"
echo ""
echo "  Save the client_secret now — it will not be shown again."
echo "══════════════════════════════════════════════════════════════"
