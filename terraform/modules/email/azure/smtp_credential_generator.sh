#!/bin/bash
# Azure Communication Services SMTP Credential Generator
# This script generates SMTP credentials and stores them in Key Vault

set -e

# Parse JSON input from Terraform
eval "$(jq -r '@sh "
  EMAIL_SERVICE_NAME=\(.email_service_name)
  DOMAIN_NAME=\(.domain_name)
  RESOURCE_GROUP=\(.resource_group)
  KEY_VAULT_NAME=\(.key_vault_name)
"')"

# Function to generate random password
generate_password() {
  openssl rand -base64 32 | tr -d "=+/" | cut -c1-25
}

# Check if domain exists and is provisioned
echo "Checking domain status..." >&2
DOMAIN_STATUS=$(az communication email domain show \
  --email-service-name "$EMAIL_SERVICE_NAME" \
  --domain-name "$DOMAIN_NAME" \
  --resource-group "$RESOURCE_GROUP" \
  --query "provisioningState" -o tsv 2>/dev/null || echo "NotFound")

if [ "$DOMAIN_STATUS" != "Succeeded" ]; then
  echo "⚠️  Domain not yet provisioned (status: $DOMAIN_STATUS)" >&2
  echo "Using placeholder credentials. Domain must be fully provisioned first." >&2

  # Return placeholders
  jq -n \
    --arg username "WAITING_FOR_DOMAIN_PROVISIONING" \
    --arg password "PLACEHOLDER" \
    --arg sender "DoNotReply@pending.azurecomm.net" \
    '{"username":$username,"password":$password,"sender_address":$sender,"ready":"false"}'
  exit 0
fi

# Get Mail From sender domain
MAIL_FROM=$(az communication email domain show \
  --email-service-name "$EMAIL_SERVICE_NAME" \
  --domain-name "$DOMAIN_NAME" \
  --resource-group "$RESOURCE_GROUP" \
  --query "mailFromSenderDomain" -o tsv)

echo "✅ Domain provisioned: $MAIL_FROM" >&2

# Check if SMTP credentials already exist in Key Vault
EXISTING_USERNAME=$(az keyvault secret show \
  --vault-name "$KEY_VAULT_NAME" \
  --name "azure-smtp-username" \
  --query "value" -o tsv 2>/dev/null || echo "")

if [ -n "$EXISTING_USERNAME" ] && [ "$EXISTING_USERNAME" != "PLACEHOLDER_GENERATE_IN_AZURE_PORTAL" ]; then
  echo "✅ Using existing SMTP credentials from Key Vault" >&2

  EXISTING_PASSWORD=$(az keyvault secret show \
    --vault-name "$KEY_VAULT_NAME" \
    --name "azure-smtp-password" \
    --query "value" -o tsv)

  jq -n \
    --arg username "$EXISTING_USERNAME" \
    --arg password "$EXISTING_PASSWORD" \
    --arg sender "DoNotReply@$MAIL_FROM" \
    '{"username":$username,"password":$password,"sender_address":$sender,"ready":"true"}'
  exit 0
fi

# Generate new SMTP credentials
# Note: Azure Communication Services doesn't have an API to generate SMTP credentials
# The credentials must be generated manually via Portal or there's no official API
echo "⚠️  SMTP credentials not found in Key Vault" >&2
echo "⚠️  Azure Communication Services requires manual SMTP credential generation" >&2
echo "" >&2
echo "Please generate credentials manually:" >&2
echo "  1. Go to Azure Portal → Communication Services → $EMAIL_SERVICE_NAME" >&2
echo "  2. Navigate to 'Domains' → '$DOMAIN_NAME' → 'SMTP'" >&2
echo "  3. Click 'Generate credentials'" >&2
echo "  4. Run these commands to store credentials:" >&2
echo "" >&2
echo "     az keyvault secret set --vault-name $KEY_VAULT_NAME --name azure-smtp-username --value '<username>'" >&2
echo "     az keyvault secret set --vault-name $KEY_VAULT_NAME --name azure-smtp-password --value '<password>'" >&2
echo "" >&2
echo "Then run 'terraform apply' again." >&2

# Return instructions in JSON
jq -n \
  --arg username "MANUAL_GENERATION_REQUIRED" \
  --arg password "PLACEHOLDER" \
  --arg sender "DoNotReply@$MAIL_FROM" \
  --arg instructions "See terraform output for manual generation steps" \
  '{"username":$username,"password":$password,"sender_address":$sender,"ready":"false","instructions":$instructions}'
