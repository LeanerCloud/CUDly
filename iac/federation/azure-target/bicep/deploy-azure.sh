#!/usr/bin/env bash
# Deploy the CUDly Azure role assignment template.
#
# Prerequisites:
#   1. Run the azure-wif-cli.sh script first to create the CUDly App Registration,
#      service principal, and upload the certificate. Note the printed
#      servicePrincipalObjectId.
#   2. Authenticate the Azure CLI with `az login` and select the target subscription
#      with `az account set --subscription <id>`.
#
# Usage:
#   SP_OBJECT_ID=<object-id-from-step-1> bash deploy-azure.sh [--location <region>] [--template bicep|arm]
#
set -euo pipefail

: "${SP_OBJECT_ID:?set SP_OBJECT_ID to the service principal object ID from azure-wif-cli.sh}"

LOCATION="${AZURE_LOCATION:-eastus}"
TEMPLATE="bicep"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --location) LOCATION="$2"; shift 2 ;;
    --template) TEMPLATE="$2"; shift 2 ;;
    *) echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
done

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

case "$TEMPLATE" in
  bicep) TEMPLATE_FILE="$DIR/azure-wif.bicep" ;;
  arm)   TEMPLATE_FILE="$DIR/azure-wif.arm.json" ;;
  *) echo "--template must be bicep or arm" >&2; exit 1 ;;
esac

PARAMS_FILE="$DIR/azure-wif-bicep-params.json"
if [[ ! -f "$PARAMS_FILE" ]]; then
  echo "Missing parameters file: $PARAMS_FILE" >&2
  exit 1
fi

DEPLOYMENT_NAME="cudly-$(date +%Y%m%d-%H%M%S)"

echo "Deploying ${TEMPLATE} template to subscription at ${LOCATION}..."
az deployment sub create \
  --name "$DEPLOYMENT_NAME" \
  --location "$LOCATION" \
  --template-file "$TEMPLATE_FILE" \
  --parameters "@$PARAMS_FILE" \
  --parameters "servicePrincipalObjectId=$SP_OBJECT_ID" \
  --output table

echo ""
echo "=== Done ==="
echo "Set azure_auth_mode=workload_identity_federation in CUDly."
