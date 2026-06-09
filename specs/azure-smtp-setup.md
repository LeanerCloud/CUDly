# Azure ACS SMTP credential setup — operator runbook

## Background

Azure Communication Services (ACS) SMTP credentials **cannot be generated
via the ARM API or Terraform.** Microsoft has not exposed a REST endpoint
for this as of 2026-04; it remains a manual Azure Portal step.

CUDly's `terraform/modules/secrets/azure` creates the Key Vault secrets
`azure-smtp-username` and `azure-smtp-password` with placeholder values
on first apply. They need to be replaced with real credentials generated
in the Portal before email delivery works.

The helper `scripts/azure-smtp-setup.sh` closes the ergonomic gap: it
prints the direct Portal URL (with your subscription + resource group
pre-filled) and emits the two `az keyvault secret set` commands with
`--vault-name` and `--name` already set — you only paste the values
from the Portal.

## Interactive flow

1. After `terraform apply` on the Azure module, look for the
   `smtp_setup_instructions` output. It prints the exact command:

```bash
   bash scripts/azure-smtp-setup.sh <rg> <acs-domain-name> <kv-name>
```

   where `<rg>` and `<kv-name>` are already substituted for your
   deployment. Only `<acs-domain-name>` needs filling in (the email
   domain you connected to ACS).

1. Run the command. The script:
   - verifies the ACS domain resource exists in the resource group
   - prints the Azure Portal URL to land on the Communication Services
     blade for that subscription + resource group
   - prints the two `az keyvault secret set` commands you'll need to
     run after generating credentials in the Portal

2. Open the printed Portal URL. In the Communication Services resource:
   - Email → Connect email → select your domain → Connect (skip if
     already connected)
   - Email → "Send email now" card → "Configure SMTP auth" → Generate
   - Copy the generated username and password (password is shown once
     — grab it before closing the blade)

3. Back in your terminal, run the two `az keyvault secret set` commands
   the script printed, pasting the values from the Portal:

```bash
   az keyvault secret set \
       --vault-name <kv-name> \
       --name azure-smtp-username \
       --value '<username-from-portal>'

   az keyvault secret set \
       --vault-name <kv-name> \
       --name azure-smtp-password \
       --value '<password-from-portal>'
```

1. Restart the Container App (or AKS pods) so the updated secrets are
   picked up:

```bash
   az containerapp revision restart -g <rg> -n <container-app-name>
```

## CI/CD alternative

If credentials are pre-generated elsewhere and stored in an external
vault, skip the portal step entirely. Pass the values during
`terraform apply`:

```bash
terraform apply \
    -var 'smtp_username=...' \
    -var 'smtp_password=...'
```

The module detects non-null values and writes them straight to Key
Vault; `smtp_setup_instructions` is then empty (no manual step needed).
This path is the recommended one for automated environments where a
human can't complete the Portal flow.

## Troubleshooting

- **"Domain not found" from the helper script.** The domain isn't
  connected under any Communication Service in the given resource
  group. Verify with:

```bash
  az resource list -g <rg> \
      --resource-type Microsoft.Communication/emailServices/domains
```

  If the list is empty, create a Communication Service + email service

- domain first (those ARE Terraform-manageable via `azurerm`), then
  re-run the helper.

- **`az keyvault secret set` returns 403.** The account you're logged
  into doesn't have `Key Vault Secrets Officer` on the target vault.
  Grant the role or re-auth as an identity that has it.

- **`az keyvault secret set` says the secret already exists.** Normal
  on re-runs — the new value replaces the old. The container app still
  needs a restart afterwards to pick up the change.

- **Emails still fail after credential update.** Restart the container
  app revision (step 5). The app caches the secret at boot; without a
  restart it continues using the placeholder.

## Why this can't be automated

Microsoft's Communication Services team hasn't exposed
`Microsoft.Communication/emailServices/domains/smtpCredentials/generate`
as an ARM action — credential generation goes through a different
code path internal to the Portal. The `azurerm` Terraform provider
doesn't have a resource for it either. Short of reverse-engineering
the Portal's API (which would break on any Microsoft change), Portal-
only is the stable path. Watch the
[azurerm provider's GitHub issues](https://github.com/hashicorp/terraform-provider-azurerm/issues)
for future ACS SMTP support.
