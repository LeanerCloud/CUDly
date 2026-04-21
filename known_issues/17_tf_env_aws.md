# Known Issues: Terraform AWS Environment

> **Audit status (2026-04-21):** `0 still valid · 7 resolved · 1 not applicable · 0 partially fixed · 0 moved · 1 needs triage (Lambda first-invocation RDS connect timeout — found in production logs)`

## ~~CRITICAL: Fargate compute platform has no multi-account support~~ — RESOLVED

**File**: `terraform/environments/aws/compute.tf:154-163`
**Description**: The Lambda path injected `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN`, `CUDLY_MAX_ACCOUNT_PARALLELISM`, and multi-account IAM permissions (`enable_cross_account_sts`, `enable_org_discovery`). Fargate only had the env-var pair; the module had no corresponding variables or IAM policy statements, so cross-account STS and org discovery were completely absent.
**Status:** ✔️ Resolved

**Resolved by:** Added five variables to `terraform/modules/compute/aws/fargate/variables.tf` mirroring the Lambda module: `credential_encryption_key_secret_arn`, `enable_cross_account_sts`, `cross_account_role_name_prefix`, `enable_org_discovery`, `email_from_domain`. Added three IAM role-policy statements in `main.tf`: extended `task_secrets` with the credential-encryption-key ARN, added opt-in `cross_account_sts` and `org_discovery` policies (count-gated on the flags). `ses_access` now honours `email_from_domain` just like Lambda — deployments without email notifications no longer get a broad SES `*` grant. The env-layer `compute.tf` wires all five values at parity with the Lambda branch (`enable_cross_account_sts = true`, `enable_org_discovery = true`, `credential_encryption_key_secret_arn = module.secrets.credential_encryption_key_secret_arn`, `email_from_domain = var.subdomain_zone_name`).

### Original implementation plan

**Goal:** Bring the Fargate compute path to feature parity with Lambda so multi-account credential encryption, org discovery, and cross-account STS work by default.

**Files to modify:**

- `terraform/environments/aws/compute.tf:154-163` — pass `credential_encryption_key_secret_arn`, `enable_cross_account_sts`, `enable_org_discovery`, and the matching env vars into the Fargate module.
- `terraform/modules/compute/aws/fargate/variables.tf` — declare the three new variables.
- `terraform/modules/compute/aws/fargate/main.tf` — inject the env vars into the container definition and extend the task role with `secretsmanager:GetSecretValue` + `sts:AssumeRole` + `organizations:ListAccounts` where applicable.
- `terraform/modules/compute/aws/fargate/iam.tf` (or equivalent) — attach the new policy statements.
- `terraform/environments/aws/variables.tf` — re-export toggles so per-env tfvars can override.

**Steps:**

1. Mirror the Lambda-path variables and defaults (e.g. `enable_cross_account_sts = true`, `enable_org_discovery = false`) in the Fargate module.
2. Add an `aws_iam_policy_document` (or inline statements) that grants `secretsmanager:GetSecretValue` on the credential key ARN plus `sts:AssumeRole` on the CUDly role ARN pattern; attach to the Fargate task role.
3. Extend the task definition with the matching env vars (`CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN`, `CUDLY_MAX_ACCOUNT_PARALLELISM`, `ENABLE_ORG_DISCOVERY`, etc.).
4. Update `terraform/environments/aws/compute.tf` to pass the same values already used by the Lambda branch.
5. Update module README(s) and the AWS environment deployment guide.

**Edge cases the fix must handle:**

- Fargate tasks running without a credential-encryption key (local dev) — variable must allow empty and skip env-var injection rather than crash.
- Cross-partition deployments (GovCloud) — STS endpoint override must still honour region.
- Scheduled tasks spawned by the Fargate variant — they must receive the same env vars.

**Test plan:**

- `terraform plan -var compute_platform=fargate` — expect new IAM statements and env vars.
- `terraform plan -var compute_platform=lambda` — expect no change.
- Integration: deploy Fargate to a scratch account, run CUDly's `/health` + a cross-account operation.

**Verification:**

- `terraform validate` in `terraform/environments/aws/`
- End-to-end smoke test using a staging account pair (ambient + target).

**Related issues:** `17_tf_env_aws#critical-secret-rotation`, `17_tf_env_aws#high-wildcard-cors`

**Effort:** `medium`

## ~~CRITICAL: Secret rotation hardcoded off with no override path~~ — RESOLVED

**File**: `terraform/environments/aws/secrets.tf:28`
**Description**: `enable_secret_rotation = false` was hard-coded in the module call with no variable to flip it, so production deployments could never turn rotation on without forking the env.
**Impact**: Production ran permanently without rotation.
**Status:** ✔️ Resolved

**Resolved by:** Added two env-layer variables: `enable_secret_rotation` (default `false` for backward compatibility) and `rds_cluster_id_for_rotation` (default `null`). Both flow through to the existing module inputs. The module already supports these cleanly via `count = var.enable_secret_rotation ? 1 : 0` on every rotation resource, so no module-level changes were needed. Production tfvars flip both to enable rotation against the prod RDS cluster; dev/staging stay at the default and are unaffected.

### Original implementation plan

**Goal:** Allow per-environment control of Secrets Manager rotation so production can enable it without forking the module.

**Files to modify:**

- `terraform/environments/aws/variables.tf` — add `variable "enable_secret_rotation"` with `default = false` (keeps backwards compatibility).
- `terraform/environments/aws/secrets.tf:28` — wire the variable through to the secrets module call.
- `terraform/modules/secrets/aws/variables.tf` — ensure the module already exposes the same input; add if missing.
- `terraform/environments/aws/tfvars/prod.tfvars` — set `enable_secret_rotation = true`.
- `terraform/environments/aws/README.md` — document the flag and the rotation Lambda prerequisites.

**Steps:**

1. Declare the new variable with a validation that requires a Lambda ARN when rotation is true.
2. Replace the hardcoded `false` with `var.enable_secret_rotation`.
3. Add a `rotation_lambda_arn` variable and pass-through so operators can plug in the AWS Secrets Manager rotation template.
4. Update each environment's tfvars (dev stays `false`, prod `true`).
5. Document the requirement to pre-deploy the rotation Lambda (or reference the AWS serverless application repository).

**Edge cases the fix must handle:**

- Rotation enabled without a Lambda ARN — surface a validation error at plan time.
- Secrets that don't support rotation (e.g. KMS-backed static keys) — allow overriding per-secret.
- Existing deployments — no diff when the flag stays at its default.

**Test plan:**

- `terraform plan` with `enable_secret_rotation = true` and ARN set — expect `aws_secretsmanager_secret_rotation` resources.
- `terraform plan` with default — no change.

**Verification:**

- `terraform validate` in `terraform/environments/aws/`
- Manual rotation trigger (`aws secretsmanager rotate-secret`) in staging.

**Related issues:** `17_tf_env_aws#high-empty-key`

**Effort:** `small`

## ~~HIGH: Empty credential_encryption_key stored silently~~ — RESOLVED

**File**: `terraform/modules/secrets/aws/variables.tf:123` and `main.tf:168-176`
**Description**: The module's `credential_encryption_key` had no validation; combined with `ignore_changes = [secret_string]`, a typo'd or short key would be locked in to Secrets Manager and never re-converged by Terraform.
**Impact**: Credentials encrypted with a placeholder/empty key would have been trivially breakable.
**Status:** ✔️ Resolved

**Resolved by:** Added a validation block to the variable that accepts only the empty string (which triggers the existing auto-generation path) or a 64-character hex string. Anything else fails at plan time. Kept the `ignore_changes` lifecycle (operators rotate the key out-of-band via `aws secretsmanager put-secret-value` and Terraform must not revert), but added a comment explaining the rationale and noting that the variable-level validation closes the original "empty/short key locked in forever" risk at the input gate.

### Original implementation plan

**Goal:** Reject empty/short credential-encryption keys at plan time so Secrets Manager never stores a placeholder that cannot be decrypted.

**Files to modify:**

- `terraform/modules/secrets/aws/variables.tf:123` — add a `validation` block requiring a 64-character hex string.
- `terraform/modules/secrets/aws/main.tf:168-176` — drop the `ignore_changes = [secret_string]` lifecycle, or narrow it so rotations still propagate.
- `terraform/environments/aws/secrets.tf` — ensure the key is sourced from a secure input (SOPS, TF_VAR, or AWS KMS).
- `terraform/environments/aws/README.md` — document the generation command (`openssl rand -hex 32`).

**Steps:**

1. Add `validation { condition = can(regex("^[0-9a-fA-F]{64}$", var.credential_encryption_key)) }` with a clear error message.
2. Review the `ignore_changes` lifecycle; if the original intent was to avoid spurious diffs from Secrets Manager rotation metadata, scope the ignore to only those fields rather than the entire `secret_string`.
3. Add a `precondition` in the environment layer preventing `terraform apply` from running with an empty or placeholder key.
4. Document a bootstrap script that generates the key and writes it to the chosen secret store.

**Edge cases the fix must handle:**

- Initial bootstrap when no key yet exists — rely on the env-level precondition to stop apply rather than silently seeding `""`.
- Key rotation — explicit operator action required; rotation flow documented separately.
- Existing deployments — operators must supply the real key before upgrading; include a migration note in the release notes.

**Test plan:**

- `terraform validate` with an empty key — expect failure.
- `terraform plan` with a real 64-hex key — no unexpected diff.

**Verification:**

- `terraform validate` in `terraform/modules/secrets/aws/` and `terraform/environments/aws/`.
- Post-apply: `aws secretsmanager get-secret-value` returns the 64-hex key.

**Related issues:** `17_tf_env_aws#critical-secret-rotation`

**Effort:** `small`

## ~~HIGH: Lambda Function URL auth type defaults to NONE~~ — NOT APPLICABLE

**File**: `terraform/environments/aws/variables.tf:219-222`
**Description**: `lambda_function_url_auth_type` defaults to `"NONE"` so the URL is publicly reachable without SigV4.
**Status:** 🚫 Not applicable — `NONE` is the correct default for this architecture.

**Rationale:** CUDly is a browser-served SPA that talks directly to the Lambda Function URL. The browser has no AWS identity and cannot SigV4-sign requests, so setting `AWS_IAM` would break the app entirely. Authentication is enforced at the application layer — session cookies, JWT, API keys, and CSRF — see `internal/api/handler_login.go` and middleware in `internal/api/router.go`. The variable now carries a long-form description explaining this plus a validation block restricting the value to `{AWS_IAM, NONE}`, but the default stays `NONE`. Operators fronting the URL with a SigV4-capable gateway (CloudFront+Lambda@Edge, API Gateway) can override to `AWS_IAM` explicitly.

### Original implementation plan

**Goal:** Default Lambda Function URL auth to `AWS_IAM` so new deployments are not publicly reachable without opt-in.

**Files to modify:**

- `terraform/environments/aws/variables.tf:219-222` — change `default = "NONE"` to `default = "AWS_IAM"` and add a `validation` that restricts to `["AWS_IAM", "NONE"]`.
- `terraform/environments/aws/compute.tf` — ensure the IAM policy for API consumers (frontend, scheduler, tests) covers `lambda:InvokeFunctionUrl`.
- `terraform/environments/aws/tfvars/dev.tfvars` — explicit override where anonymous access is desired for local testing.
- `docs/aws-deployment.md` / environment README — document the signed-request requirement.

**Steps:**

1. Flip the default and add the validation block.
2. Audit all callers (frontend sigv4 signer, scheduler, CI smoke tests) for sigv4 support.
3. Generate IAM policies for each caller identity and attach under a documented naming scheme.
4. Update local dev instructions to use `awscurl` or signed fetch.

**Edge cases the fix must handle:**

- Third-party webhook callers that cannot sigv4 — document the NONE override plus a WAF/API Gateway fronting alternative.
- CORS preflight requests — still need auth bypass; confirm the Function URL OPTIONS handling.
- Existing dev environments relying on `NONE` — call out the breaking default change in release notes.

**Test plan:**

- `terraform plan` without override — expect `authorization_type = "AWS_IAM"`.
- `terraform plan -var lambda_function_url_auth_type=NONE` — expect opt-in override.
- Runtime: unauthenticated `curl` returns 403, sigv4 `curl` returns 200.

**Verification:**

- `terraform validate` in `terraform/environments/aws/`
- Post-apply auth test via `awscurl` and plain `curl`.

**Related issues:** `17_tf_env_aws#high-wildcard-cors`

**Effort:** `small`

## ~~HIGH: Wildcard CORS fallback in production~~ — RESOLVED

**File**: `terraform/environments/aws/compute.tf:63, 158`
**Description**: Earlier revisions set `CORS_ALLOWED_ORIGIN = "*"` when `frontend_domain_names` was empty. That wildcard fallback has already been replaced with `"http://localhost:3000"` (safe for local dev, not useful for prod). The remaining risk is an operator supplying `"*"` as a domain name entry, which the current code would accept silently.
**Impact**: Historical CORS wildcard (`"*"`) allowed any origin to make credentialed cross-origin requests.
**Status:** ✔️ Resolved

**Resolved by:** Confirmed the `"*"` fallback is gone from `compute.tf` (both Lambda and Fargate branches fall back to `"http://localhost:3000"`). Added two validation blocks on `var.frontend_domain_names` so the list itself cannot contain a bare `"*"` or any whitespace-containing entry. Updated the variable description to document the CORS derivation, the `localhost:3000` fallback, and why wildcards are not safe on an authenticated API.

### Original implementation plan

**Goal:** Eliminate the silent `"*"` fallback so CORS is always pinned to explicit origins, or deliberately disabled for local dev only.

**Files to modify:**

- `terraform/environments/aws/compute.tf:63` (Lambda branch) and `:158` (Fargate branch) — remove the `"*"` fallback.
- `terraform/environments/aws/variables.tf` — change `frontend_domain_names` validation to require at least one value (or rename to `cors_allowed_origins` with strict validation).
- `terraform/environments/aws/tfvars/dev.tfvars` — explicitly set `"http://localhost:3000"` rather than relying on the wildcard.
- `docs/aws-deployment.md` — document the new requirement.

**Steps:**

1. Introduce a `cors_allowed_origins` list variable with a validation block that requires each entry to match an http(s) URL pattern and disallows bare `"*"`.
2. Build the env var by joining the list with commas; fall back to `"http://localhost:3000"` only when an explicit `dev = true` flag is set.
3. Thread the same value through both Lambda and Fargate branches.
4. Update existing tfvars to supply the appropriate origins.

**Edge cases the fix must handle:**

- Multi-tenant deployments with many frontend domains — validate each entry but allow lists of arbitrary size.
- Preview environments (Netlify/Vercel) — allow `https://*.preview.example.com` via explicit opt-in (validation disables bare `*`, but not suffix wildcards scoped to a domain).
- Zero origins configured — fail plan rather than silently open.

**Test plan:**

- `terraform plan` with empty list — expect validation failure.
- `terraform plan` with explicit domains — expect matching env var.

**Verification:**

- `terraform validate` in `terraform/environments/aws/`
- Browser preflight test from an allowed origin and a disallowed origin.

**Related issues:** `17_tf_env_aws#critical-fargate`, `17_tf_env_aws#high-function-url`

**Effort:** `small`

## ~~MEDIUM: database_skip_final_snapshot defaults to true~~ — RESOLVED

**File**: `terraform/environments/aws/variables.tf:162-165`
**Description**: Default was `true`, so `terraform destroy` in production would have deleted the RDS instance with no final snapshot.
**Status:** ✔️ Resolved

**Resolved by:** Flipped the default to `false`. Dev environments already set `true` explicitly in their tfvars (`dev.tfvars`, `dev.tfvars.example`, `fargate-dev.tfvars`, `github-dev.tfvars`, `github-staging.tfvars`), so ephemeral teardown ergonomics are preserved where desired.

### Original implementation plan

**Goal:** Make `terraform destroy` safe for production by defaulting `skip_final_snapshot` to `false`.

**Files to modify:**

- `terraform/environments/aws/variables.tf:162-165` — change default to `false`.
- `terraform/environments/aws/tfvars/dev.tfvars` — set `database_skip_final_snapshot = true` to preserve dev ergonomics.
- `terraform/environments/aws/README.md` — document the new default and the dev override.

**Steps:**

1. Flip the default.
2. Ensure `final_snapshot_identifier` is automatically generated (e.g. `"cudly-${var.environment}-final-${formatdate(...)}"`).
3. Add validation requiring the identifier when `skip_final_snapshot = false`.
4. Communicate the change in release notes so existing `terraform destroy` workflows remain predictable.

**Edge cases the fix must handle:**

- Duplicate snapshot identifiers on repeated destroy/apply cycles — timestamp-suffix the identifier.
- Snapshot quotas — document the 100-snapshot-per-region soft limit.
- Dev environments deliberately destroying state repeatedly — override in tfvars.

**Test plan:**

- `terraform plan -destroy` against production defaults — expect final snapshot creation.
- `terraform plan -destroy -var database_skip_final_snapshot=true` — expect skip.

**Verification:**

- `terraform validate` in `terraform/environments/aws/`
- Post-destroy snapshot visible in `aws rds describe-db-snapshots`.

**Related issues:** none

**Effort:** `small`

## ~~MEDIUM: VPC Flow Logs disabled by default~~ — RESOLVED

**File**: `terraform/environments/aws/variables.tf:73-76`
**Description**: `enable_flow_logs` defaulted to `false`, so environments without an explicit opt-in had no network-level audit trail.
**Status:** ✔️ Resolved

**Resolved by:** Flipped the default to `true`. Dev/cost-sensitive environments already set `false` explicitly in their tfvars (`dev.tfvars`, `dev.tfvars.example`, `fargate-dev.tfvars`, `github-dev.tfvars`). The variable description now documents the CloudWatch ingest cost implication so operators can pick the retention window that fits their volume.

### Original implementation plan

**Goal:** Enable VPC flow logs by default so all environments capture a network audit trail without manual opt-in.

**Files to modify:**

- `terraform/environments/aws/variables.tf:73-76` — change default to `true`.
- `terraform/environments/aws/network.tf` (or equivalent) — ensure the existing flow-log resource uses a retention-bounded log group.
- `terraform/environments/aws/tfvars/dev.tfvars` — optionally opt out for cost-sensitive dev.
- `terraform/environments/aws/README.md` — document the flag and cost implications.

**Steps:**

1. Flip the default.
2. Confirm the CloudWatch log group already has a `retention_in_days` input; default to 30 days.
3. Allow the destination to be `s3` as an alternative (via a `flow_logs_destination_type` variable) for cheaper long-term retention.
4. Document expected monthly cost for a representative traffic volume.

**Edge cases the fix must handle:**

- Accounts already running flow logs — enable/disable should converge idempotently.
- CloudWatch Logs ingestion cost — surface a `flow_logs_retention_days` variable and recommend 30 days.
- Accounts lacking the log-delivery IAM role — add a bootstrap statement.

**Test plan:**

- `terraform plan` with the new default — expect `aws_flow_log` resource creation.
- `terraform plan -var enable_flow_logs=false` — expect no resource.

**Verification:**

- `terraform validate` in `terraform/environments/aws/`
- `aws ec2 describe-flow-logs` shows an active log stream after apply.

**Related issues:** none

**Effort:** `small`

## ~~LOW: Lambda first-invocation RDS connect timeout (3-4 minutes)~~ — RESOLVED

**File**: `terraform/environments/aws/` (Lambda + RDS + NAT gateway configuration) — symptom surfaces via `internal/database/connection.go`

**Description**: After Lambda idle, the first invocation fails with `failed to write startup message: timeout: context deadline exceeded ... time:3m55s`. Observed twice in cudly-dev logs on 2026-04-21 (17:59:01Z and 18:02:04Z). Lines up with cold-start ENI attachment + NAT gateway warmup. The existing `createConnectionPoolWithRetry` runs 5 attempts × ~45s each = the observed 235s.

**Impact**: First user request after idle returns 500/timeout; subsequent requests work.

**Status:** ✔️ Resolved

**Resolved by:** `internal/database/connection.go::createConnectionPoolWithRetry` now extracts each individual attempt into a new `attemptOpenPool(ctx, cfg, perAttemptTimeout)` helper that derives `perAttemptCtx, cancel := context.WithTimeout(ctx, perAttemptConnectTimeout); defer cancel()` and passes that bounded ctx into BOTH `pgxpool.NewWithConfig` AND `pool.Ping`. The defer sits inside the helper (function body) so each attempt's ctx is correctly released — defer-in-loop would have leaked. The retry knobs (`maxConnectRetries=5`, `connectRetryBaseDelay=2s`, `connectRetryMaxDelay=30s`, new `perAttemptConnectTimeout=15s`) are now package-level `var`s so tests can shrink them. Worst-case retry budget drops from ~235s to ~135s; common cold-start resolves on the 2nd or 3rd attempt within a minute. New `TestCreateConnectionPoolWithRetry_PerAttemptDeadline` uses pgxpool's `ConnConfig.DialFunc` injection seam to drive a perma-hanging dialer with ms-scale knobs and asserts the wall-clock wait is bounded.

**Implementation plan (recommended):** add a per-attempt deadline to `createConnectionPoolWithRetry` in `internal/database/connection.go` — extract `attemptOpenPool(ctx, cfg, perAttemptTimeout)` with `context.WithTimeout(ctx, 15*time.Second)` wrapping BOTH `pgxpool.NewWithConfig` and `Ping`. Worst-case retry budget drops from ~235s to ~105s; common cold-start finishes in ~60s. Test: inject a hanging `pgxpool.Config.ConnConfig.DialFunc` and assert the wall-clock wait is bounded.

**Alternatives** (longer-term): VPC endpoints for RDS + Secrets Manager (removes NAT hop); provisioned concurrency (~$4/month per provisioned exec); EventBridge warm-up.
