#!/usr/bin/env bash
#
# azure-smtp-setup.sh — print the exact steps + pre-filled commands
# an operator needs to generate Azure ACS SMTP credentials in the portal
# and store them in Key Vault.
#
# Azure Communication Services SMTP credentials cannot be generated via
# Terraform or the ARM API (Microsoft hasn't exposed an endpoint for
# this as of 2026). This script closes the ergonomic gap: it verifies
# the ACS resource exists, prints the direct Portal URL, and emits the
# two `az keyvault secret set` commands the operator should run with
# --vault-name and --name already filled in.
#
# Usage:
#   ./scripts/azure-smtp-setup.sh <acs-resource-group> <acs-domain-name> <key-vault-name>
#
# Example:
#   ./scripts/azure-smtp-setup.sh cudly-dev-rg cudly.communication.azure.com cudly-dev-kv
#
# Prerequisites:
#   - az login (with Contributor on the resource group + Key Vault Secrets
#     Officer on the target Key Vault)
#   - jq (optional, used for nicer parsing)

set -euo pipefail

if [[ $# -lt 3 ]]; then
    cat >&2 <<EOF
Usage: $0 <acs-resource-group> <acs-domain-name> <key-vault-name>

Example:
    $0 cudly-dev-rg cudly.communication.azure.com cudly-dev-kv
EOF
    exit 1
fi

RESOURCE_GROUP="$1"
DOMAIN_NAME="$2"
KEY_VAULT_NAME="$3"

SUBSCRIPTION_ID=$(az account show --query id -o tsv)
TENANT_ID=$(az account show --query tenantId -o tsv)

echo "Verifying ACS email domain '$DOMAIN_NAME' in resource group '$RESOURCE_GROUP'..."

# The email domain lives under an Azure Communication Service resource which
# has a child emailService with domains. We list email services in the RG and
# look for a matching domain.
MATCHING_DOMAIN=$(az resource list \
    --resource-group "$RESOURCE_GROUP" \
    --resource-type "Microsoft.Communication/emailServices/domains" \
    --query "[?name=='$DOMAIN_NAME' || ends_with(name, '/$DOMAIN_NAME')].id" \
    -o tsv 2>/dev/null || true)

if [[ -z "$MATCHING_DOMAIN" ]]; then
    echo "⚠ Could not find email domain '$DOMAIN_NAME' in resource group '$RESOURCE_GROUP'."
    echo "  Verify with: az resource list -g $RESOURCE_GROUP --resource-type Microsoft.Communication/emailServices/domains"
    echo "  Continuing anyway — the Portal link below will work if the domain exists."
    echo ""
fi

# Portal URL. Azure Portal routes by subscription + RG + resource path.
# Using the Communication Services blade so the operator sees all their
# domains and can navigate to the SMTP tab.
PORTAL_URL="https://portal.azure.com/#@${TENANT_ID}/resource/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${RESOURCE_GROUP}/providers/Microsoft.Communication/communicationServices"

cat <<EOF

══════════════════════════════════════════════════════════════════════
  Azure ACS SMTP Credential Setup — Manual Portal Step Required
══════════════════════════════════════════════════════════════════════

Microsoft doesn't expose a REST API for generating ACS SMTP credentials,
so the credential generation must happen in the Azure Portal. Follow
these steps in order:

  1. Open the Azure Portal at the Communication Services blade:

         $PORTAL_URL

  2. Select the Communication Service resource that owns '$DOMAIN_NAME'.

  3. In the left nav: Email → Connect email → select '$DOMAIN_NAME' → Connect.
     (Skip if already connected.)

  4. In the left nav: Email → go to the "Send email now" card → click
     "Configure SMTP auth". A blade opens with "Generate".

  5. Click "Generate" to create a username/password pair.
     — The username will look like: <app-name>.<resource-name>.<guid>
     — The password is shown ONCE; copy it immediately.

  6. Back in your terminal, run these two commands (values already
     filled in except for the credentials from step 5):

         az keyvault secret set \\
             --vault-name '$KEY_VAULT_NAME' \\
             --name azure-smtp-username \\
             --value '<username-from-portal>'

         az keyvault secret set \\
             --vault-name '$KEY_VAULT_NAME' \\
             --name azure-smtp-password \\
             --value '<password-from-portal>'

  7. Restart the Container App / AKS pods so they pick up the new
     credentials. For Container Apps:

         az containerapp revision restart \\
             -g '$RESOURCE_GROUP' \\
             -n <your-container-app-name>

══════════════════════════════════════════════════════════════════════

Troubleshooting:

  - "Domain not found" in step 2: confirm the domain is connected to
    a Communication Service via
        az resource list -g '$RESOURCE_GROUP' \\
            --resource-type Microsoft.Communication/emailServices/domains

  - "keyvault secret set" returns 403: the current az login doesn't
    have Key Vault Secrets Officer on '$KEY_VAULT_NAME'. Either grant
    the role or re-run as an account that has it.

  - "keyvault secret set" reports the secret already exists: that's
    normal on re-runs; the new value replaces the old. The container
    app still needs a restart (step 7) to pick it up.

Alternative (CI/CD): if credentials are pre-generated elsewhere, pass
them to terraform apply via -var 'smtp_username=...' -var
'smtp_password=...' and skip this script entirely.

══════════════════════════════════════════════════════════════════════
EOF
