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
MODE="client_secret"   # or "wif" for certificate-based workload identity federation

# ── Argument parsing ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --subscription) SUBSCRIPTION_ID="$2"; shift 2 ;;
    --app-name)     APP_NAME="$2";        shift 2 ;;
    --mode)         MODE="$2";            shift 2 ;;
    *) echo "Unknown argument: $1"; exit 1 ;;
  esac
done

if [[ "$MODE" != "client_secret" && "$MODE" != "wif" ]]; then
  echo "Error: --mode must be 'client_secret' or 'wif'" >&2
  exit 1
fi

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

# ── Credential setup ──────────────────────────────────────────────────────────
CLIENT_SECRET=""
if [[ "$MODE" == "wif" ]]; then
  # Use a per-run temp directory to avoid predictable /tmp paths.
  WORK_DIR=$(mktemp -d)
  trap 'command -v shred >/dev/null && shred -u "${WORK_DIR}/cudly-wif.key" 2>/dev/null; rm -rf "$WORK_DIR"' EXIT

  echo "Generating self-signed certificate for workload identity federation..."
  openssl genrsa -out "${WORK_DIR}/cudly-wif.key" 2048 2>/dev/null
  openssl req -new -x509 -key "${WORK_DIR}/cudly-wif.key" -out "${WORK_DIR}/cudly-wif.crt" \
    -days 730 -subj "/CN=CUDly-WIF" 2>/dev/null
  CERT_B64=$(base64 < "${WORK_DIR}/cudly-wif.crt" | tr -d '\n')
  az ad app credential reset --id "$APP_ID" --cert "$CERT_B64" --append --output none

  # WARNING: The key+cert below will appear in terminal scrollback and any session
  # recording. Do NOT run this script in CI/CD or any environment that captures stdout.
  # Both blocks are written to stderr so stdout redirects do not capture them.
  # Store the ENTIRE output (key PEM + certificate PEM) as azure_wif_private_key in CUDly.
  # The certificate block provides the x5t thumbprint required by Azure AD client assertions.
  printf '\n=== Key + Certificate PEM (store BOTH blocks as azure_wif_private_key in CUDly) ===\n' >&2
  cat "${WORK_DIR}/cudly-wif.key" >&2
  cat "${WORK_DIR}/cudly-wif.crt" >&2
  printf '=== end — copy both blocks now, they will be deleted from disk ===\n\n' >&2
  # shred (Linux) preferred; dd overwrite as fallback for macOS
  if command -v shred >/dev/null; then
    shred -u "${WORK_DIR}/cudly-wif.key" 2>/dev/null
  else
    KEY_SIZE=$(wc -c < "${WORK_DIR}/cudly-wif.key")
    dd if=/dev/zero of="${WORK_DIR}/cudly-wif.key" bs=1 count="$KEY_SIZE" conv=notrunc 2>/dev/null
    rm -f "${WORK_DIR}/cudly-wif.key"
  fi
  rm -f "${WORK_DIR}/cudly-wif.crt"
  trap - EXIT  # key already deleted; cancel trap
else
  echo "Creating client secret (valid 2 years)..."
  CLIENT_SECRET=$(az ad app credential reset \
    --id "$APP_ID" \
    --display-name "CUDly-$(date +%Y%m%d)" \
    --years 2 \
    --query password -o tsv)
fi

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
echo "  provider              : azure"
echo "  azure_auth_mode       : ${MODE}"
echo "  azure_subscription_id : ${SUBSCRIPTION_ID}"
echo "  azure_tenant_id       : ${TENANT_ID}"
echo "  azure_client_id       : ${APP_ID}"
if [[ "$MODE" == "client_secret" ]]; then
  echo "  client_secret         : ${CLIENT_SECRET}"
  echo ""
  echo "  Save the client_secret now — it will not be shown again."
else
  echo ""
  echo "  Store the key+certificate PEM printed above as azure_wif_private_key in CUDly."
  echo "  Both PEM blocks are required (certificate provides x5t thumbprint for Azure AD)."
  echo "  Both files were deleted from disk."
fi
echo "══════════════════════════════════════════════════════════════"
