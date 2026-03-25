# Azure CI/CD Permissions

This Terraform module provisions a least-privilege Azure custom role and service principal
(`cudly-terraform-deploy`) that CUDly's CI/CD pipeline uses to deploy infrastructure on Azure. It
sets up keyless authentication via **GitHub Actions Federated Identity Credentials** so no client
secrets ever need to be stored.

## What this module creates

| Resource | Purpose |
| --- | --- |
| `azuread_application.cudly_deploy` | Azure AD app registration |
| `azuread_service_principal.cudly_deploy` | Service principal (identity) |
| `azuread_application_federated_identity_credential.github_main` | Federated credential for main-branch deployments |
| `azuread_application_federated_identity_credential.github_pr` | Federated credential for pull-request plan checks |
| `azurerm_role_definition.cudly_deploy` | Custom role with minimum required permissions |
| `azurerm_role_assignment.cudly_deploy` | Assigns the custom role to the SP at subscription scope |

### Permissions granted

**Control-plane actions** (ARM): VNets, subnets, NSGs, Container Apps, Container Registries,
Storage accounts, Key Vault management, Log Analytics, DNS zones, and more.

**Data-plane actions**: Blob read/write (Terraform state), Key Vault secrets read/write.

## Prerequisites

- Terraform >= 1.6
- Azure CLI: `az login` with an account that has **Owner** (or User Access Administrator + Contributor) on the target subscription
- A Storage Account + container for Terraform state (see [Backend setup](#backend-setup))

## Backend setup

Create the Storage Account once before the first `terraform init`:

```bash
RESOURCE_GROUP="cudly-terraform-state-rg"
STORAGE_ACCOUNT="cudlytfstatedev"     # must be globally unique, 3-24 lowercase alphanumeric
CONTAINER="tfstate"
LOCATION="eastus2"
SUBSCRIPTION_ID=$(az account show --query id --output tsv)

# Resource group
az group create \
  --name "$RESOURCE_GROUP" \
  --location "$LOCATION"

# Storage account with versioning
az storage account create \
  --name "$STORAGE_ACCOUNT" \
  --resource-group "$RESOURCE_GROUP" \
  --location "$LOCATION" \
  --sku Standard_LRS \
  --kind StorageV2 \
  --min-tls-version TLS1_2 \
  --allow-blob-public-access false

az storage account blob-service-properties update \
  --account-name "$STORAGE_ACCOUNT" \
  --resource-group "$RESOURCE_GROUP" \
  --enable-versioning true

# Container
az storage container create \
  --name "$CONTAINER" \
  --account-name "$STORAGE_ACCOUNT"
```

A `backend.hcl.example` is provided; copy it to `backend.hcl` and fill in your values, then pass it
to `terraform init -backend-config=backend.hcl`.

## Usage

```bash
# 1. Copy example configs
cp terraform.tfvars.example terraform.tfvars
cp backend.hcl.example backend.hcl

# 2. Fill in terraform.tfvars (only subscription_id is required)
# 3. Fill in backend.hcl with your storage account details

# 4. Initialise
terraform init -backend-config=backend.hcl

# 5. Plan
terraform plan

# 6. Apply
terraform apply
```

After applying, retrieve the outputs used in GitHub Actions:

```bash
terraform output client_id        # → AZURE_CLIENT_ID
terraform output tenant_id        # → AZURE_TENANT_ID
# AZURE_SUBSCRIPTION_ID is your terraform.tfvars subscription_id value
```

## Importing an existing service principal

If a `cudly-terraform-deploy` SP was created manually before this module was added:

```bash
# Get the application object ID (not the client/app ID):
APP_ID=$(az ad sp show --display-name cudly-terraform-deploy --query appId --output tsv)
APP_OBJ_ID=$(az ad app show --id "$APP_ID" --query id --output tsv)
SP_OBJ_ID=$(az ad sp show --id "$APP_ID" --query id --output tsv)

terraform import azuread_application.cudly_deploy "$APP_OBJ_ID"
terraform import azuread_service_principal.cudly_deploy "$SP_OBJ_ID"
```

## GitHub Actions configuration

### Repository secrets / variables

Set these in **Settings → Secrets and variables → Actions** on the `LeanerCloud/CUDly` GitHub
repository (or in a GitHub Actions Environment for per-environment control):

| Name | Value | How to get it |
| --- | --- | --- |
| `AZURE_CLIENT_ID` | Client ID of the SP | `terraform output client_id` |
| `AZURE_TENANT_ID` | Azure AD tenant ID | `terraform output tenant_id` |
| `AZURE_SUBSCRIPTION_ID` | Subscription ID | `az account show --query id --output tsv` |

> No `AZURE_CLIENT_SECRET` is needed — authentication is keyless via federated identity.

### Example workflow step

```yaml
- name: Azure login
  uses: azure/login@v2
  with:
    client-id: ${{ secrets.AZURE_CLIENT_ID }}
    tenant-id: ${{ secrets.AZURE_TENANT_ID }}
    subscription-id: ${{ secrets.AZURE_SUBSCRIPTION_ID }}
```

The `azure/login` action requests an OIDC token from GitHub, exchanges it with Azure AD for a
short-lived access token via the federated credential, and sets `AZURE_*` environment variables and
the `az` CLI context for all subsequent steps.

### Federated credential subjects

Two subjects are registered:

| Credential | Subject | Use case |
| --- | --- | --- |
| `github-actions-main` | `repo:LeanerCloud/CUDly:ref:refs/heads/main` | Deployments from main |
| `github-actions-pr` | `repo:LeanerCloud/CUDly:pull_request` | Plan runs on PRs |

If you need to deploy from a different branch or repo, add additional
`azuread_application_federated_identity_credential` resources to `sp.tf`.
