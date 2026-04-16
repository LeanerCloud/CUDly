# Known Issues: IaC Azure Target Federation

## MEDIUM: `.terraform.lock.hcl` silently excluded from git by root `.gitignore`

**File**: `.gitignore:109-110`
**Description**: Root `.gitignore` uses bare patterns `.terraform/` and `.terraform.lock.hcl` that match recursively. The lock file will never be committed, so `terraform init` resolves versions from scratch within loose `>= X.Y` constraints.
**Impact**: Different engineers and CI runs can initialize with different provider versions. Breaks reproducibility.
**Fix**: Change to root-anchored patterns (`/.terraform/`, `/.terraform.lock.hcl`) and commit module-specific lock files.

## ~~MEDIUM: No `end_date` on `azuread_application_certificate`~~ — RESOLVED

~~**File**: `iac/federation/azure-target/terraform/main.tf:37-41`~~
**Status**: The entire certificate-based approach was replaced with `azuread_application_federated_identity_credential` (OIDC federation via CUDly's KMS-backed issuer). No certificate is generated or uploaded. This issue no longer applies.

## ~~MEDIUM: Provider version constraints unbounded from above~~ — RESOLVED

~~**File**: `iac/federation/azure-target/terraform/main.tf:6-11`~~
**Status**: Constraints were tightened to `~> 3.8` for azuread and `~> 4.0` for azurerm.

## LOW: `cudly_account_registration` output not marked `sensitive`

**File**: `iac/federation/azure-target/terraform/outputs.tf:11-21`
**Description**: Contains `subscription_id` and `tenant_id` in plaintext, printed to stdout and captured in CI/CD logs.
**Fix**: Add `sensitive = true` to the output block.
