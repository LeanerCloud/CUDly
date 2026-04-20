# Known Issues: IaC Azure Target Federation

> **Audit status (2026-04-20):** `1 still valid · 3 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## MEDIUM: `.terraform.lock.hcl` silently excluded from git by root `.gitignore`

**File**: `.gitignore:109-110`
**Description**: Root `.gitignore` uses bare patterns `.terraform/` and `.terraform.lock.hcl` that match recursively. The lock file will never be committed, so `terraform init` resolves versions from scratch within loose `>= X.Y` constraints.
**Impact**: Different engineers and CI runs can initialize with different provider versions. Breaks reproducibility.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Commit module-scoped `.terraform.lock.hcl` files so every engineer and CI run resolves the same provider versions.

**Files to modify:**

- `.gitignore:109-110` — change the two patterns to root-anchored form.
- Every Terraform module directory (e.g. `iac/federation/azure-target/terraform/`, `iac/federation/aws-target/terraform/`, `iac/federation/gcp-target/terraform/`, `iac/federation/aws-cross-account/terraform/`, `terraform/environments/*/`) — commit the freshly generated `.terraform.lock.hcl`.
- `docs/terraform.md` (or module READMEs) — note that lock files are now tracked.

**Steps:**

1. Replace `.terraform/` with `/.terraform/` and `.terraform.lock.hcl` with per-module negations, e.g. keep `.terraform/` as the broad ignore but add `!**/terraform/.terraform.lock.hcl` to re-include lock files under any module.
2. Run `terraform init` in each module to produce a lock file targeting the current provider versions.
3. `git add` the generated lock files and commit alongside the `.gitignore` change so CI stays reproducible.
4. Document the policy in the contributor guide: never edit lock files by hand; run `terraform init -upgrade` to regenerate.

**Edge cases the fix must handle:**

- Nested submodules (e.g. `pkg/`) that also produce `.terraform.lock.hcl` — confirm the negation path pattern covers them.
- Developer-local `.terraform/` cache directories — must remain ignored.
- CI caches that key on the lock file — make sure the first run after the change repopulates correctly.

**Test plan:**

- `git check-ignore iac/federation/azure-target/terraform/.terraform.lock.hcl` — expect no match (file is tracked).
- `git check-ignore iac/federation/azure-target/terraform/.terraform/` — expect match (still ignored).
- `terraform init` in a fresh clone — expect deterministic provider versions.

**Verification:**

- `git status` after `terraform init` shows only lock-file changes, not `.terraform/` contents.
- CI terraform job passes with cold cache.

**Related issues:** none

**Effort:** `small`

## ~~MEDIUM: No `end_date` on `azuread_application_certificate`~~ — RESOLVED

~~**File**: `iac/federation/azure-target/terraform/main.tf:37-41`~~
**Status:** ✔️ Resolved

**Resolved by:** `fab366871` — replaced the entire certificate-based approach with `azuread_application_federated_identity_credential` (OIDC federation via CUDly's KMS-backed issuer). No certificate is generated or uploaded, so the `end_date` concern no longer applies.

## ~~MEDIUM: Provider version constraints unbounded from above~~ — RESOLVED

~~**File**: `iac/federation/azure-target/terraform/main.tf:6-11`~~
**Status:** ✔️ Resolved

**Resolved by:** `fab366871` — constraints tightened to `~> 3.8` for `azuread` and `~> 4.0` for `azurerm`, preventing surprise major-version upgrades.

## ~~LOW: `cudly_account_registration` output not marked `sensitive`~~ — RESOLVED

**File**: `iac/federation/azure-target/terraform/outputs.tf:11-21`
**Description**: Output contained `subscription_id` and `tenant_id` in plaintext and was printed to stdout / captured in CI/CD logs.
**Status:** ✔️ Resolved

**Resolved by:** Added `sensitive = true` to the `cudly_account_registration` output. Description now points operators at `terraform output -raw cudly_account_registration` for retrieval. Plan/apply summaries display `(sensitive value)` instead of the real subscription/tenant IDs.

### Original implementation plan

**Goal:** Stop leaking Azure subscription/tenant identifiers into CI logs by marking the registration output `sensitive`.

**Files to modify:**

- `iac/federation/azure-target/terraform/outputs.tf:11-21` — add `sensitive = true` to the `cudly_account_registration` block.
- `iac/federation/azure-target/terraform/README.md` (if present) — update the section describing how to retrieve the value (`terraform output -json cudly_account_registration`).

**Steps:**

1. Add `sensitive = true` inside the `output "cudly_account_registration"` block.
2. Verify any consumer scripts extract the value with `terraform output -raw` or `-json` rather than relying on the plan/apply summary.
3. Update documentation to call out that the full JSON is retrievable via `terraform output -json`.

**Edge cases the fix must handle:**

- Wrapper scripts parsing `terraform apply` stdout — they will need to switch to `terraform output -json`.
- Downstream Terraform modules consuming the value via `remote_state` still receive the real contents (sensitive flag is advisory).

**Test plan:**

- `terraform plan` — confirm the output is displayed as `(sensitive value)`.
- `terraform output -raw cudly_account_registration` — confirm the real value is still retrievable.

**Verification:**

- `terraform validate` in `iac/federation/azure-target/terraform/`
- Manual spot-check of CI logs after the change shows no plaintext subscription/tenant IDs.

**Related issues:** none

**Effort:** `small`
