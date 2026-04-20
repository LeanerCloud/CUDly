# Known Issues: Terraform GCP Environment

> **Audit status (2026-04-20):** `2 still valid · 3 resolved · 0 partially fixed · 0 moved · 2 needs triage`

## ~~CRITICAL: GKE path missing credential_encryption_key and core env vars~~ — RESOLVED

**File**: `terraform/environments/gcp/compute.tf:121-128`
**Description**: GKE `additional_env_vars` only has `STATIC_DIR`, `DASHBOARD_URL`, `CORS_ALLOWED_ORIGIN`. Missing: `CREDENTIAL_ENCRYPTION_KEY_SECRET_ID`, `SENDGRID_API_KEY_SECRET`, `FROM_EMAIL`, `SCHEDULED_TASK_SECRET`. Cloud Run path has all of these.
**Impact**: `compute_platform = "gke"` silently breaks credential decryption and email. No Terraform error.
**Status:** ✔️ Resolved

**Resolved by:** The GKE `additional_env_vars` block at `terraform/environments/gcp/compute.tf:123-136` now mirrors the Cloud Run branch — `CREDENTIAL_ENCRYPTION_KEY_SECRET_ID`, `SENDGRID_API_KEY_SECRET`, `FROM_EMAIL`, and `SCHEDULED_TASK_SECRET` are all injected into the workload.

## HIGH: Secret Manager IAM bindings for credential-encryption-key never applied

**File**: `terraform/environments/gcp/secrets.tf:25`
**Description**: `cloud_run_service_account_email = null` means per-secret IAM bindings are never created. Access works only because Cloud Run gets project-level `roles/secretmanager.secretAccessor` (overly broad). GKE workload SA has no access at all.
**Impact**: GKE pods cannot access `credential-encryption-key` via Secret Manager. Cloud Run has overly broad access to every secret in the project.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Grant each compute path least-privilege access to exactly the secrets it needs, eliminating both the GKE outage and the Cloud Run over-grant.

**Files to modify:**

- `terraform/environments/gcp/secrets.tf:25` — pass both `cloud_run_service_account_email` and `gke_service_account_email` to the secrets module.
- `terraform/modules/secrets/gcp/variables.tf` — accept one or both service account emails.
- `terraform/modules/secrets/gcp/main.tf` — create per-secret `google_secret_manager_secret_iam_member` bindings for each identity that needs access.
- `terraform/environments/gcp/compute.tf` — surface the compute-platform-specific SA email from the compute module output.
- `terraform/environments/gcp/variables.tf` — no new variables expected; rely on `compute_platform` to pick which SA to bind.
- `terraform/environments/gcp/README.md` — document the binding model.

**Steps:**

1. Add a module output in each compute variant (`cloud_run_service_account_email`, `gke_workload_service_account_email`).
2. Feed the right email into the secrets module based on `var.compute_platform` (can accept both for dual deployments).
3. Replace the project-wide `roles/secretmanager.secretAccessor` grant with per-secret `roles/secretmanager.secretAccessor` members.
4. Ensure the secrets module creates bindings for all relevant secrets (`credential-encryption-key`, `sendgrid-api-key`, `scheduled-task-secret`, any future additions).
5. Clean up the legacy project-level binding with a targeted `terraform state rm` / recreate step documented in release notes.

**Edge cases the fix must handle:**

- Dual-platform deployments (running both Cloud Run and GKE simultaneously) — accept both SA emails.
- Bootstrap order — APIs (Secret Manager) must be enabled before bindings; use `depends_on`.
- Legacy project-level binding — document the removal procedure to avoid double-grants.

**Test plan:**

- `terraform plan` — expect new per-secret bindings and removal of the broad project grant.
- Runtime: GKE pod reads `credential-encryption-key` successfully; Cloud Run still works; neither can read unrelated secrets.

**Verification:**

- `terraform validate` in `terraform/environments/gcp/` and the secrets module.
- `gcloud secrets get-iam-policy credential-encryption-key` shows only the intended members.

**Related issues:** `19_tf_env_gcp#high-cred-key-sensitivity`

**Effort:** `medium`

## HIGH: credential_encryption_key stored without sensitive masking in `additional_secrets` (Needs Triage)

**File**: `terraform/modules/secrets/gcp/variables.tf:67-72`
**Description**: `additional_secrets` declared without `sensitive = true` (removed to allow `for_each`). The credential encryption key flows through plan output and state in plaintext.
**Impact**: Key visible in CI/CD logs and unencrypted Terraform state files.
**Status:** ❓ Needs triage — confirm the auditor's claim that `sensitive = true` was removed solely to enable `for_each`. Terraform's current behaviour usually permits `for_each` over maps containing sensitive values with a `nonsensitive(keys(...))` unwrap on the key set only.

### Implementation plan

**Goal:** Treat `credential_encryption_key` as a first-class sensitive input so it never appears in plan output or unencrypted state.

**Files to modify:**

- `terraform/modules/secrets/gcp/variables.tf:67-72` — introduce a dedicated `credential_encryption_key` variable with `sensitive = true`.
- `terraform/modules/secrets/gcp/main.tf` — create `google_secret_manager_secret_version` directly from the new variable rather than via `additional_secrets`.
- `terraform/environments/gcp/secrets.tf` — move the key out of `additional_secrets` and pass it via the new dedicated variable.
- `terraform/modules/secrets/gcp/variables.tf` (same block) — re-evaluate whether `additional_secrets` itself can become `sensitive = true` (Terraform ≥ 1.5 allows `for_each` with `nonsensitive(keys(...))`).

**Steps:**

1. Add `variable "credential_encryption_key" { type = string, sensitive = true }` with validation enforcing 64-char hex.
2. Update the module to create the Secret + Version from this dedicated input.
3. In the environment, pass the key via the new variable; remove the entry from `additional_secrets`.
4. If feasible, re-add `sensitive = true` to `additional_secrets` and iterate with `for_each = toset(nonsensitive(keys(var.additional_secrets)))`.
5. Add a precondition barring the legacy "key-in-additional-secrets" path to catch stale callers.

**Edge cases the fix must handle:**

- Existing state already containing the key in `additional_secrets` — a `terraform plan` will show a replace; document the safe migration (version bump, not secret recreation).
- Operators supplying the key from a file vs env var — preserve both flows; mark the variable `nullable = false`.
- Rotation — document the new rotation entry point on the dedicated variable.

**Test plan:**

- `terraform plan` — expect the key rendered as `(sensitive value)`.
- `grep` the plan output for the key prefix — no matches.
- State file inspection (with `terraform show -json | jq`) — attribute present but marked sensitive.

**Verification:**

- `terraform validate` in `terraform/modules/secrets/gcp/` and `terraform/environments/gcp/`.
- CI log review after the change — no plaintext key.

**Related issues:** `19_tf_env_gcp#high-sm-iam`

**Effort:** `medium` (contingent on triage)

## ~~MEDIUM: `database_deletion_protection` defaults to false~~ — TRIAGED & RESOLVED

**File**: `terraform/environments/gcp/variables.tf:166-169`
**Description**: Default was `false` so `terraform destroy` would permanently delete the Cloud SQL instance.
**Status:** ✔️ Triaged & resolved.

**Triage:** Verified that `dev.tfvars`, `dev.tfvars.example`, and `github-dev.tfvars` all explicitly set `database_deletion_protection = false`. Production and staging already explicitly set `true`. Flipping the default to `true` therefore affects no current caller.

**Resolved by:** Flipped the default to `true`. Variable description now documents that disabling requires two applies (one to flip the flag, one to destroy) per Cloud SQL provider semantics.

### Original implementation plan

**Goal:** Default production Cloud SQL instances to deletion-protected, while keeping ephemeral environments easy to tear down.

**Files to modify:**

- `terraform/environments/gcp/variables.tf:166-169` — change default to `true`.
- `terraform/environments/gcp/tfvars/dev.tfvars` — explicit `database_deletion_protection = false`.
- `terraform/environments/gcp/README.md` — document the default and the override.

**Steps:**

1. Flip the default.
2. Add a validation or description note reminding operators that disabling deletion protection requires two Terraform applies (one to flip the flag, one to destroy) by design of the Cloud SQL provider.
3. Update release notes.

**Edge cases the fix must handle:**

- CI smoke tests that create and tear down Cloud SQL instances — use the dev override.
- Forgotten deletion protection preventing legitimate destroy — document the two-apply flow.

**Test plan:**

- `terraform plan` with prod tfvars — expect deletion protection enabled.
- `terraform plan -var database_deletion_protection=false` — expect override.

**Verification:**

- `terraform validate` in `terraform/environments/gcp/`.
- `gcloud sql instances describe` shows `settings.deletionProtectionEnabled: true`.

**Related issues:** none

**Effort:** `small` (contingent on triage)

## MEDIUM: Cloud Run publicly reachable with no authentication default

**File**: `terraform/environments/gcp/variables.tf:223-227`
**Description**: `cloud_run_allow_unauthenticated = true` combined with `ingress = "INGRESS_TRAFFIC_ALL"`. Cloud Armor only applies when CDN is enabled (default: false).
**Impact**: API surface publicly accessible with no infrastructure-level authentication.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Make production Cloud Run services require infrastructure-level authentication by default, while leaving dev flexibility.

**Files to modify:**

- `terraform/environments/gcp/variables.tf:223-227` — change `cloud_run_allow_unauthenticated` default to `false` and `ingress` default to `"INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"`.
- `terraform/environments/gcp/tfvars/dev.tfvars` — override both values for easy local access.
- `terraform/environments/gcp/network.tf` — ensure the load balancer + Cloud Armor path is wired for production.
- `terraform/environments/gcp/README.md` — document the new defaults and how to test.

**Steps:**

1. Flip both defaults.
2. Ensure the provisioned load balancer references the Cloud Run service as a Serverless NEG.
3. Document Cloud Armor attachment + recommended WAF rules.
4. Update the frontend to call the load balancer domain rather than the direct Cloud Run URL.

**Edge cases the fix must handle:**

- Callers that do not support IAM-signed requests — either route via the LB or opt in to unauthenticated via tfvars.
- Internal Cloud Scheduler jobs — grant `roles/run.invoker` to the scheduler SA.
- Blue/green deployments — ensure the LB health checks target the revision correctly.

**Test plan:**

- `terraform plan` defaults — expect `ingress` set and `allUsers` binding removed.
- Runtime: direct Cloud Run URL returns 403; LB domain with valid token returns 200.

**Verification:**

- `terraform validate` in `terraform/environments/gcp/`.
- `gcloud run services describe` shows `ingress: internal-and-cloud-load-balancing`.

**Related issues:** `19_tf_env_gcp#medium-cors`

**Effort:** `medium`

## ~~MEDIUM: CORS_ALLOWED_ORIGIN falls back to wildcard~~ — RESOLVED

**File**: `terraform/environments/gcp/compute.tf:64, 125`
**Description**: Earlier revisions set `CORS_ALLOWED_ORIGIN = "*"` when `frontend_domain_names` was empty. The tracked code in `compute.tf` already falls back to `"http://localhost:3000"` (safe for local dev). The remaining gap was the absence of a guardrail preventing an operator from supplying `"*"` directly.
**Status:** ✔️ Resolved

**Resolved by:** Confirmed the `"*"` fallback is gone from `compute.tf` (both Cloud Run and GKE branches use `"http://localhost:3000"`). Added two validation blocks on `var.frontend_domain_names` so the list itself cannot contain a bare `"*"` or any whitespace-containing entry. Mirrors the AWS env fix in commit `57a0459da`.

### Original implementation plan

**Goal:** Remove the wildcard CORS fallback so both Cloud Run and GKE deployments pin to explicit origins.

**Files to modify:**

- `terraform/environments/gcp/compute.tf:64` (Cloud Run branch) and `:125` (GKE branch) — drop the `"*"` fallback.
- `terraform/environments/gcp/variables.tf` — require `frontend_domain_names` to be non-empty or replace with `cors_allowed_origins` with strict validation.
- `terraform/environments/gcp/tfvars/dev.tfvars` — set `"http://localhost:3000"` (or `http://localhost:5173`) explicitly.
- `docs/gcp-deployment.md` — document the change.

**Steps:**

1. Introduce a `cors_allowed_origins` list variable with a validation block rejecting bare `"*"`.
2. Build the env var by joining with commas; `compute.tf` uses the same value on both branches.
3. Remove the `"*"` fallback; fail plan when the list is empty outside dev.
4. Update existing tfvars.

**Edge cases the fix must handle:**

- Multi-domain frontends — allow lists.
- Preview deployments — allow scoped suffix wildcards (`https://*.preview.example.com`) but reject bare `*`.
- Zero origins configured in prod — validation error.

**Test plan:**

- `terraform plan` with populated origins — expected env var string.
- `terraform plan` with empty list — expect validation error outside dev.

**Verification:**

- `terraform validate` in `terraform/environments/gcp/`.
- Browser preflight test from an allowed and a disallowed origin.

**Related issues:** `19_tf_env_gcp#medium-cloud-run-public`

**Effort:** `small`

## LOW: GKE Kubernetes secret stores Secret Manager name under misleading `password` key

**File**: `terraform/modules/compute/gcp/gke/main.tf:237-255`
**Description**: The Kubernetes secret's `password` key contains a Secret Manager secret name string, not the actual password.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Rename the Kubernetes secret key so its contents match its name and avoid confusing operators who expect an actual password.

**Files to modify:**

- `terraform/modules/compute/gcp/gke/main.tf:237-255` — rename the key from `password` to `password_secret_name` in the Kubernetes secret definition.
- `k8s/*.yaml` / Helm charts / Go loader code that references `password` — update every consumer to the new key.
- `terraform/modules/compute/gcp/gke/README.md` — document the rename.

**Steps:**

1. Rename the key in the Terraform-rendered manifest.
2. Update the Go (or init-container) code that reads the env var / volume mount to use the new name.
3. Stage a rollout that keeps both keys during the transition, then drops the old one in a second release.
4. Add release notes calling out the rename for anyone bypassing Terraform.

**Edge cases the fix must handle:**

- Existing pods still running with the old key — provide the dual-key transition.
- Helm/ArgoCD consumers of the secret — confirm their templates are updated.
- Dev environments using `kubectl create secret` manually — document the new key name.

**Test plan:**

- `terraform plan` — expect the Kubernetes secret to get a new key.
- Pod logs confirm the init container resolves the new key successfully.

**Verification:**

- `terraform validate` in `terraform/modules/compute/gcp/gke/`.
- `kubectl describe secret` shows `password_secret_name` only after the second rollout.

**Related issues:** none

**Effort:** `small`
