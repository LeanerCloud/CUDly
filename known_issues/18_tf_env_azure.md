# Known Issues: Terraform Azure Environment

> **Audit status (2026-04-20):** `5 still valid · 2 resolved · 0 partially fixed · 0 moved · 1 needs triage`

## ~~CRITICAL: `nonsensitive()` strips sensitivity from `additional_secrets` before merge~~ — RESOLVED

**File**: `terraform/environments/azure/secrets.tf:24`
**Description**: `nonsensitive(var.additional_secrets)` ran before merging `credential-encryption-key`, leaking every key/value to `terraform plan` output and CI logs.
**Impact**: API keys, webhook tokens, and the credential encryption key appeared in plaintext in plan diff output.
**Status:** ✔️ Resolved

**Resolved by:** Dropped the `nonsensitive()` wrapper in `terraform/environments/azure/secrets.tf` so the merge preserves sensitivity end-to-end. Marked the module variable `additional_secrets` (`terraform/modules/secrets/azure/variables.tf`) as `sensitive = true`. The `azurerm_key_vault_secret.additional` `for_each` now iterates `toset(nonsensitive(keys(var.additional_secrets)))` so the key set is enumerable while the actual values stay redacted in plan output (looked up via `var.additional_secrets[each.key]` in the resource body).

### Original implementation plan

**Goal:** Preserve sensitivity when merging `additional_secrets` so secret values never appear in plan output or CI logs.

**Files to modify:**

- `terraform/environments/azure/secrets.tf:24` — remove the `nonsensitive()` wrapper.
- `terraform/modules/secrets/azure/variables.tf` — confirm `additional_secrets` is typed as `map(string)` with `sensitive = true` where Terraform permits (map-of-strings can still be sensitive).
- `terraform/modules/secrets/azure/main.tf` — ensure the `for_each` still iterates with sensitive values; restructure via `tomap()` or `sensitive` local if required.

**Steps:**

1. Replace `merge(nonsensitive(var.additional_secrets), { ... })` with `merge(var.additional_secrets, { ... })`.
2. If Terraform rejects `for_each` over a sensitive value, add a minimal unwrap using `nonsensitive(keys(var.additional_secrets))` only for the key set, and fetch the value via `var.additional_secrets[k]` inside the resource body.
3. Verify `terraform plan` renders `(sensitive value)` for every entry.
4. Add a regression test / example asserting none of the values leak into plan output.

**Edge cases the fix must handle:**

- Empty `additional_secrets` — the merge must still succeed.
- Large secret sets — `for_each` on a sensitive map still performs well.
- Providers that validate the value type — confirm `azurerm_key_vault_secret` accepts a sensitive-wrapped string.

**Test plan:**

- `terraform plan -detailed-exitcode` with a populated `additional_secrets` — exit 2, but stdout must not contain any real values.
- `grep` the plan output for known secret prefixes — expect no matches.

**Verification:**

- `terraform validate` in `terraform/environments/azure/`
- Manual review of CI log artifact post-apply.

**Related issues:** `18_tf_env_azure#high-scheduled-task`, `18_tf_env_azure#high-key-vault-acl`

**Effort:** `small`

## HIGH: Container App RBAC grants `Secrets Officer` instead of `Secrets User` (Needs Triage)

**File**: `terraform/modules/secrets/azure/main.tf:291-297`
**Description**: The optional RBAC assignment uses `"Key Vault Secrets Officer"` (create/update/delete) instead of `"Key Vault Secrets User"` (read-only).
**Impact**: Compromised runtime identity can overwrite or delete all Key Vault secrets including the credential encryption key.
**Status:** ❓ Needs triage — confirm whether the runtime identity actually needs write permissions (e.g., self-rotation) before downgrading.

### Implementation plan

**Goal:** Grant the Container App the least privilege that still satisfies CUDly's runtime flows, ideally `Key Vault Secrets User`.

**Files to modify:**

- `terraform/modules/secrets/azure/main.tf:291-297` — change the `role_definition_name` after confirming no runtime write path exists.
- `terraform/modules/secrets/azure/variables.tf` — optionally add a `writeable_secrets_role = false` toggle for flows that genuinely need write access.
- `terraform/environments/azure/secrets.tf` — pass the toggle explicitly for environments that perform self-rotation.

**Steps:**

1. Audit the backend code for calls to `SetSecret`/`UpdateSecret`/`DeleteSecret` on Key Vault. If any exist, scope them to a distinct managed identity rather than the container runtime identity.
2. If no writes are required, change the role assignment to `"Key Vault Secrets User"`.
3. If some writes are required (e.g., storing new cloud credentials), split the identity: a read-only identity for the main pod, a privileged identity for the admin path, so the blast radius stays small.
4. Document the decision in the secrets module README.

**Edge cases the fix must handle:**

- Existing deployments where the Container App has relied on write access — plan the migration to avoid breaking startup.
- Future runtime features (e.g., secret rotation Lambda-equivalent) — keep the variable escape hatch.
- Multi-env parity — ensure dev + prod get the same role unless explicitly overridden.

**Test plan:**

- `terraform plan` with the new role — expect exactly one `azurerm_role_assignment` diff.
- Runtime smoke test: CUDly reads `credential-encryption-key`; write attempts return 403.

**Verification:**

- `terraform validate` in `terraform/environments/azure/` and the module.
- `az role assignment list --scope <keyvault-id>` confirms the downgraded role.

**Related issues:** `18_tf_env_azure#critical-nonsensitive`

**Effort:** `small` (contingent on triage)

## HIGH: `SCHEDULED_TASK_SECRET` injected as plain-text environment variable

**File**: `terraform/environments/azure/compute.tf:42`
**Description**: Raw password value (not a Key Vault secret name) is passed as a direct env var. Visible in Azure Portal, ARM templates, and Terraform state.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Pass only the Key Vault secret name to the container and let the app resolve the real value at runtime.

**Files to modify:**

- `terraform/environments/azure/compute.tf:42` — replace the env var value with the secret name (e.g. `SCHEDULED_TASK_SECRET_NAME = azurerm_key_vault_secret.scheduled_task.name`).
- `terraform/modules/compute/azure/container-apps/main.tf` — ensure env vars support Key Vault references (`secret_ref`) and mount the secret accordingly.
- `backend/scheduler/...` (Go) — resolve `SCHEDULED_TASK_SECRET_NAME` via the Azure Identity + Key Vault SDK instead of reading the plain env var.
- `terraform/environments/azure/variables.tf` — remove any plaintext input variable that fed the old env var.

**Steps:**

1. Store the scheduled task secret in Key Vault (if not already).
2. Pass `SCHEDULED_TASK_SECRET_NAME` and (optionally) `KEY_VAULT_NAME` as env vars.
3. Refactor the scheduler to lazily fetch the secret on startup via the runtime managed identity.
4. Delete the plaintext env var path and guard against regression with a Terraform precondition.

**Edge cases the fix must handle:**

- Cold start latency — cache the resolved value in memory for the process lifetime.
- Key Vault throttling — implement retry with jitter.
- Dev environments without Key Vault — allow a `SCHEDULED_TASK_SECRET` plaintext fallback only when `ENVIRONMENT = "dev"`.

**Test plan:**

- `terraform plan` — expect the env var to reference the secret name, not the value.
- Runtime: `env | grep SCHEDULED_TASK_SECRET` inside the container shows the name; the scheduler still authenticates successfully.

**Verification:**

- `terraform validate` in `terraform/environments/azure/`.
- Post-apply: `az containerapp show` reveals only the secret name in env vars.

**Related issues:** `18_tf_env_azure#critical-nonsensitive`

**Effort:** `medium`

## ~~HIGH: AKS compute path missing `CREDENTIAL_ENCRYPTION_KEY_SECRET_NAME` and SMTP env vars~~ — RESOLVED

**File**: `terraform/environments/azure/compute.tf:107-114`
**Description**: AKS `additional_env_vars` omits `CREDENTIAL_ENCRYPTION_KEY_SECRET_NAME`, SMTP secrets, and scheduled task secret. Only the Container Apps path has them.
**Impact**: `compute_platform = "aks"` silently breaks credential decryption and email.
**Status:** ✔️ Resolved

**Resolved by:** The AKS `additional_env_vars` block at `terraform/environments/azure/compute.tf:111-125` now mirrors the Container Apps branch — credential encryption key name, SMTP secrets, and scheduled task secret are all injected.

## HIGH: Key Vault network ACL hardcoded to `"Allow"`

**File**: `terraform/environments/azure/secrets.tf:17`
**Description**: `default_network_acl_action = "Allow"` overrides the module's secure default of `"Deny"`. The Key Vault holds the credential encryption key and database passwords.
**Impact**: Any host on the internet can attempt to reach the Key Vault data plane.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Restore the module's `Deny`-by-default ACL so the Key Vault is not reachable from arbitrary internet hosts.

**Files to modify:**

- `terraform/environments/azure/secrets.tf:17` — remove the hardcoded `"Allow"`.
- `terraform/environments/azure/variables.tf` — add `ip_allowlist` (list of CIDRs) and `virtual_network_subnet_ids` inputs.
- `terraform/environments/azure/network.tf` — ensure the CI runner VNet subnet (or service endpoints) is declared.
- `terraform/environments/azure/tfvars/*.tfvars` — populate the allowlist for each environment.

**Steps:**

1. Let `default_network_acl_action` fall back to the module default (`"Deny"`).
2. Expose `ip_allowlist` and `virtual_network_subnet_ids` variables; pass them through to the secrets module.
3. Add CI runner CIDRs and the Container App VNet subnet ID to the allowlist.
4. Document how to add new operators' office IPs when needed (manual tfvars entry + PR).

**Edge cases the fix must handle:**

- Break-glass operator access — document a temporary `Allow` override that requires PR review.
- Diagnostic traffic (Azure Monitor) — ensure service endpoints remain permitted via `bypass = "AzureServices"`.
- Container Apps without a dedicated VNet — document the required networking change before flipping the default.

**Test plan:**

- `terraform plan` with populated allowlists — expect `default_action = "Deny"` and `ip_rules`.
- Runtime: `az keyvault secret show` from allowlisted runner succeeds; from a random host fails with 403.

**Verification:**

- `terraform validate` in `terraform/environments/azure/`.
- Post-apply: `az keyvault network-rule list` shows the expected allowlist.

**Related issues:** `18_tf_env_azure#medium-purge`, `18_tf_env_azure#medium-prevent-deletion`

**Effort:** `medium`

## MEDIUM: `purge_protection_enabled` defaults to `false`

**File**: `terraform/environments/azure/variables.tf:126`
**Description**: Combined with 7-day soft-delete retention (minimum). An operator can permanently delete the Key Vault.
**Impact**: Permanent loss of credential encryption key renders all stored cloud credentials unrecoverable.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Default production Key Vaults to purge-protected + long soft-delete retention so accidental deletion cannot erase the credential key.

**Files to modify:**

- `terraform/environments/azure/variables.tf:126` — change default to `true`.
- `terraform/environments/azure/variables.tf` — add `soft_delete_retention_days` variable with default `90`.
- `terraform/environments/azure/tfvars/dev.tfvars` — explicit `purge_protection_enabled = false` + `soft_delete_retention_days = 7` for ephemeral dev.
- `terraform/environments/azure/README.md` — document the one-way nature of purge protection.

**Steps:**

1. Flip the default.
2. Add the retention variable and thread it through the module.
3. Annotate the variable description with "irreversible — once enabled on a vault, it cannot be disabled".
4. Update release notes.

**Edge cases the fix must handle:**

- Existing vaults without purge protection — enabling via Terraform is fine; disabling later requires a full replace.
- Dev environments destroying state regularly — must be able to opt out.
- CI runs that create throwaway vaults — document the `-target` + `-var purge_protection_enabled=false` flow.

**Test plan:**

- `terraform plan` against prod tfvars — expect purge protection enabled.
- `terraform plan -var purge_protection_enabled=false` — expect current behaviour.

**Verification:**

- `terraform validate` in `terraform/environments/azure/`.
- `az keyvault show` reports `enablePurgeProtection = true`.

**Related issues:** `18_tf_env_azure#high-key-vault-acl`, `18_tf_env_azure#medium-prevent-deletion`

**Effort:** `small`

## MEDIUM: `prevent_deletion_if_contains_resources = false` on resource group

**File**: `terraform/environments/azure/main.tf:51-53`
**Description**: `terraform destroy` or accidental `az group delete` will not be blocked even when the group contains live resources.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Prevent accidental resource-group deletion for non-ephemeral environments.

**Files to modify:**

- `terraform/environments/azure/main.tf:51-53` — flip to `true`.
- `terraform/environments/azure/variables.tf` — introduce `prevent_resource_group_deletion` with default `true`.
- `terraform/environments/azure/tfvars/dev.tfvars` — override to `false` for dev.
- `terraform/environments/azure/README.md` — document the semantics.

**Steps:**

1. Add the variable and wire it into the provider feature block.
2. Ensure the dev tfvars explicitly override to keep `terraform destroy` ergonomic for ephemeral envs.
3. Note in documentation that Terraform will refuse destroy when the flag is on — operators must manually remove resources or flip the flag briefly.

**Edge cases the fix must handle:**

- Rollback of a failed deployment where operator wants to destroy — document the temporary override.
- Shared subscription hosting multiple environments — the provider feature is subscription-scoped; verify no collateral on other RGs.

**Test plan:**

- `terraform plan -destroy` in prod — expect an explicit error from the Azure provider.
- `terraform plan -destroy` in dev — succeeds.

**Verification:**

- `terraform validate` in `terraform/environments/azure/`.
- Manual `az group delete` attempt denied in prod.

**Related issues:** `18_tf_env_azure#medium-purge`

**Effort:** `small`

## LOW: `min_replicas` defaults to `0`

**File**: `terraform/environments/azure/variables.tf:274`
**Description**: Container scales to zero when idle. Every new request after idle incurs a cold start.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Default staging/production to warm replicas so users do not see cold-start latency on first request.

**Files to modify:**

- `terraform/environments/azure/variables.tf:274` — change default to `1`.
- `terraform/environments/azure/tfvars/dev.tfvars` — keep `min_replicas = 0` for cost savings.
- `terraform/environments/azure/README.md` — document the default + override.

**Steps:**

1. Flip the default to `1`.
2. Add validation requiring `min_replicas <= max_replicas`.
3. Document the cost implication (one always-on replica) and the dev override.

**Edge cases the fix must handle:**

- Scale-to-zero cost optimisation for throwaway envs — preserved via dev tfvars.
- HPA disabling when min == max — allow explicit scale-in/out policies.

**Test plan:**

- `terraform plan` with defaults — expect `min_replicas = 1`.
- `terraform plan -var min_replicas=0` — expect override.

**Verification:**

- `terraform validate` in `terraform/environments/azure/`.
- Post-apply: `az containerapp show` shows one ready replica.

**Related issues:** none

**Effort:** `small`
