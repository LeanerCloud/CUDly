# Federation IaC Templates

This directory is the **single source of truth** for all CUDly federation IaC
generation.  Every output — whether downloaded through the CUDly Settings UI or
generated locally with the helper script — is produced by rendering one of these
[`text/template`][go-tmpl] files.

## Templates

| File | Target → Source | Description |
| --- | --- | --- |
| `aws-cross-account.tfvars.tmpl` | AWS → AWS | Cross-account IAM role (`sts:AssumeRole`). No OIDC/WIF needed. |
| `aws-wif.tfvars.tmpl` | AWS ← Azure/GCP | `.tfvars` for `iac/federation/aws-target/terraform` — IAM OIDC provider + role. |
| `aws-wif-cf-params.json.tmpl` | AWS ← Azure/GCP | CloudFormation parameters for `iac/federation/aws-target/cloudformation`. |
| `azure-wif.tfvars.tmpl` | Azure ← any | `.tfvars` for `iac/federation/azure-target/terraform` — App Registration + cert WIF. |
| `gcp-wif.tfvars.tmpl` | GCP ← AWS/Azure | `.tfvars` for `iac/federation/gcp-target/terraform` — WIF pool + provider. |
| `gcp-sa-impersonation.tfvars.tmpl` | GCP → GCP | `.tfvars` for `iac/federation/gcp-sa-impersonation/terraform` — SA impersonation. |

## Template variables

All templates receive a struct with these fields:

```text
AccountName         — display name of the target account
AccountExternalID   — provider account ID (AWS 12-digit, Azure sub ID, GCP project ID)
AccountSlug         — URL-safe slug derived from AccountName (used in filenames)
Source              — source cloud: aws | azure | gcp
OIDCIssuerURL       — OIDC issuer URL (AWS target only)
OIDCAudience        — OIDC audience (AWS target only)
SubscriptionID      — Azure subscription ID (azure target only)
TenantID            — Azure tenant ID (azure target or azure source)
ProjectID           — GCP project ID (gcp target only)
ServiceAccountEmail — GCP service account email (gcp target only)
OIDCIssuerURI       — OIDC issuer URI for GCP WIF (gcp target + azure source)
```

## How output is generated

### Via the CUDly UI (recommended)

Open **Settings → [Provider] → Federation Setup**, select the source cloud, then
click the download button next to the target account.  The backend calls
`GET /api/federation/iac?target=&source=&account_id=&format=`, which renders the
appropriate template with account data fetched from the database and returns the
file for download.

The handler lives in `internal/api/handler_federation.go`.  It reads templates
from this directory via the `//go:embed` directive in `internal/iacfiles/embed.go`.

### Locally from the cloned repo

Use `scripts/generate-federation-iac.go` — a self-contained Go script with no
external dependencies:

```bash
# Run from the repository root

# AWS target, Azure source
go run scripts/generate-federation-iac.go \
  --target aws --source azure \
  --account-name "prod-aws" --account-id "123456789012" \
  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

# AWS target, GCP source
go run scripts/generate-federation-iac.go \
  --target aws --source gcp \
  --account-name "prod-aws" --account-id "123456789012"

# AWS target, AWS source (cross-account role, no WIF)
go run scripts/generate-federation-iac.go \
  --target aws --source aws \
  --account-name "target-aws" --account-id "999888777666"

# AWS target, Azure source — CloudFormation parameters
go run scripts/generate-federation-iac.go \
  --target aws --source azure --format cf-params \
  --account-name "prod-aws" --account-id "123456789012" \
  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

# Azure target
go run scripts/generate-federation-iac.go \
  --target azure --source aws \
  --account-name "prod-azure" --account-id "sub-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" \
  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

# GCP target, AWS source
go run scripts/generate-federation-iac.go \
  --target gcp --source aws \
  --account-name "prod-gcp" --account-id "my-gcp-project"

# GCP target, GCP source (service-account impersonation)
go run scripts/generate-federation-iac.go \
  --target gcp --source gcp \
  --account-name "target-gcp" --account-id "target-project-id"

# Print to stdout instead of writing a file
go run scripts/generate-federation-iac.go \
  --target aws --source azure \
  --account-name "prod" --account-id "123456789012" \
  --tenant-id "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" \
  --output -
```

Run `go run scripts/generate-federation-iac.go --help` for all available flags.

## Modifying templates

Edit the `.tmpl` files in this directory.  Changes take effect:

- **In a running CUDly instance** — after rebuilding and redeploying (templates
  are embedded at compile time via `//go:embed`).
- **Locally** — immediately; `go run scripts/generate-federation-iac.go` reads
  the files from disk at runtime.

[go-tmpl]: https://pkg.go.dev/text/template
