# Known Issues: IaC GCP Target Federation

> **Audit status (2026-04-20):** `0 still valid · 7 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~CRITICAL: Overly Broad Workload Identity Pool Trust (Wildcard Member)~~ — RESOLVED

**File**: `iac/federation/gcp-target/terraform/main.tf:47`
**Description**: The `google_service_account_iam_member` uses `principalSet://.../*` as the member. The trailing `/*` wildcard grants `roles/iam.workloadIdentityUser` to every identity in the pool, not just CUDly's specific role/subject.
**Impact**: Any IAM entity in the declared AWS account, or any token holder from the OIDC issuer, can impersonate the GCP service account.
**Status:** ✔️ Resolved

**Resolved by:** Scoped member binding to the specific AWS role (or OIDC subject) at `iac/federation/gcp-target/terraform/main.tf:106-110` — the wildcard `/*` principalSet has been replaced by an attribute-scoped `principal://`/`principalSet://` with the concrete role ARN or subject.

## ~~CRITICAL: Missing `attribute_condition` on the WIF Provider~~ — RESOLVED

**File**: `iac/federation/gcp-target/terraform/main.tf:21-41`
**Description**: No `attribute_condition` block. Every token that validates against the provider's issuer/account is accepted unconditionally.
**Impact**: Combined with the wildcard member binding, there is no provider-level gate and no binding-level gate.
**Status:** ✔️ Resolved

**Resolved by:** Added an `attribute_condition` expression on the WIF provider at `iac/federation/gcp-target/terraform/main.tf:66-70`, restricting acceptance to tokens whose mapped attributes match the expected role/subject.

## ~~HIGH: Missing `attribute_mapping` for AWS Provider Type~~ — RESOLVED

**File**: `iac/federation/gcp-target/terraform/main.tf:26`
**Description**: When `provider_type == "aws"`, `attribute_mapping` is `null`. Without explicit mapping of `attribute.aws_role`, it's impossible to write a role-scoped `attribute_condition` or binding.
**Impact**: Cannot scope trust to a specific AWS IAM role.
**Status:** ✔️ Resolved

**Resolved by:** Explicit AWS-specific `attribute_mapping` (including `attribute.aws_role`) is now defined at `iac/federation/gcp-target/terraform/main.tf:60-64`, enabling role-scoped conditions and bindings.

## ~~HIGH: Missing Required GCP APIs (No `google_project_service` Resources)~~ — RESOLVED

**File**: `iac/federation/gcp-target/terraform/main.tf` (entire file)
**Description**: Module does not enable `iam.googleapis.com`, `iamcredentials.googleapis.com`, or `sts.googleapis.com`. `terraform apply` succeeds but runtime token exchange fails.
**Impact**: CUDly fails to authenticate to GCP at runtime with opaque "403 API not enabled" error.
**Status:** ✔️ Resolved

**Resolved by:** Added `google_project_service` resources for `iam`, `iamcredentials`, and `sts` at `iac/federation/gcp-target/terraform/main.tf:25-41`, with `depends_on` wiring so downstream IAM resources wait for API enablement.

## ~~HIGH: No Cross-Variable Validation (provider_type vs companion variables)~~ — RESOLVED

**File**: `iac/federation/gcp-target/terraform/variables.tf:27-37`
**Description**: `aws_account_id` and `oidc_issuer_uri` both default to `""`. No validation enforces the required companion variable for a given `provider_type`.
**Impact**: `terraform plan` succeeds but `apply` fails with a confusing API error.
**Status:** ✔️ Resolved

**Resolved by:** `precondition` blocks at `iac/federation/gcp-target/terraform/main.tf:87-95` now fail at plan-time when `provider_type = "aws"` but `aws_account_id == ""`, and equivalently for the OIDC path.

## ~~MEDIUM: Incomplete `gcloud_command` Output for OIDC Flows~~ — RESOLVED

**File**: `iac/federation/gcp-target/terraform/outputs.tf:24`
**Description**: The OIDC branch of `gcloud_command` omitted the `--credential-source-*` flags that `gcloud iam workload-identity-pools create-cred-config` requires, so operators running it verbatim produced a broken credential config.
**Status:** ✔️ Resolved

**Resolved by:** Added three variables (`oidc_credential_source_type`, `oidc_credential_source`, `oidc_credential_source_format`) with strict validation on the enum fields. The `gcloud_command` output now emits a complete command for `provider_type = "oidc"` including `--credential-source-file` / `--credential-source-url` and `--credential-source-type`. When the operator has not yet supplied `oidc_credential_source`, the output is replaced with a short comment directing them to the new variables so they don't accidentally run a half-built command.

### Original implementation plan

**Goal:** Make the `gcloud_command` output a copy-paste-valid command for both AWS and OIDC provider types.

**Files to modify:**

- `iac/federation/gcp-target/terraform/outputs.tf:24` — extend the OIDC branch of `gcloud_command` to include `--credential-source-file` / `--credential-source-url` / `--credential-source-type` as appropriate.
- `iac/federation/gcp-target/terraform/variables.tf` — introduce `oidc_credential_source_type` (file|url), `oidc_credential_source_path`, and optional `oidc_credential_source_headers` variables.
- `iac/federation/gcp-target/terraform/README.md` (if present) — document the new variables and the resulting command shape.

**Steps:**

1. Add the three new input variables with tight validation (`contains(["file","url"], var.oidc_credential_source_type)`).
2. In `outputs.tf`, branch on `var.provider_type`:
   - AWS: keep existing command.
   - OIDC: append `--credential-source-file=<path>` or `--credential-source-url=<url>` plus `--credential-source-type=<type>` (json/text).
3. Guard against empty values with `precondition` or `validation` so OIDC users must supply a source.
4. Document in the README that the emitted command must still be reviewed by the operator before execution.

**Edge cases the fix must handle:**

- Mixed provider types across repeated module invocations — each invocation's output is self-contained.
- URL sources requiring headers — allow an optional `--credential-source-headers` JSON value.
- Multi-line command rendering — keep a single line with `--` separators so shell copy-paste works.

**Test plan:**

- `terraform plan` with `provider_type = "oidc"` and the new variables set — expect a gcloud command containing `--credential-source-*`.
- `terraform plan` with `provider_type = "aws"` — expect the existing command unchanged.
- Lint the emitted command with `gcloud iam workload-identity-pools create-cred-config --help` as reference.

**Verification:**

- `terraform validate` in `iac/federation/gcp-target/terraform/`
- Manual `gcloud` dry-run of the emitted command against a scratch project.

**Related issues:** none

**Effort:** `small`

## ~~LOW: No Remote Backend Configured~~ — RESOLVED

**File**: `iac/federation/gcp-target/terraform/main.tf:1-9`
**Description**: Local state only, so multiple operators acting on the same target project could not converge.
**Status:** ✔️ Resolved

**Resolved by:** Shipped `backend.tf.example` and `backend-config.example.hcl` in the module. Teams that need shared state copy `backend.tf.example` to `backend.tf` and run `terraform init -backend-config=backend-config.example.hcl` after filling in the bucket. No arguments are hard-coded inside the `backend "gcs"` block so the module stays portable across orgs. Left as opt-in rather than forcing a backend block in the default file because CUDly's federation bundles are typically run one-off by a single operator per target, and mandating a GCS bucket would create a hard prerequisite that most users don't need.

### Original implementation plan

**Goal:** Move the module's state to a shared remote backend so multiple operators can safely converge on the same resources.

**Files to modify:**

- `iac/federation/gcp-target/terraform/backend.tf` (new) — declare a `backend "gcs"` block.
- `iac/federation/gcp-target/terraform/main.tf:1-9` — remove any inline `backend` references if present and leave only `required_providers`.
- `iac/federation/gcp-target/terraform/README.md` — document the required bucket, prefix, and IAM prerequisites.
- `docs/operations.md` (or the equivalent onboarding doc) — add a bootstrap step for creating the GCS backend bucket.

**Steps:**

1. Create `backend.tf` with `terraform { backend "gcs" { prefix = "federation/gcp-target" } }` (bucket left unset so operators supply via `-backend-config`).
2. Document the bootstrap flow: `gsutil mb gs://<bucket>; gsutil versioning set on gs://<bucket>; gsutil uniformbucketlevelaccess set on gs://<bucket>`.
3. Add a `backend-config.example.hcl` file showing the expected `bucket` and optional `impersonate_service_account` values.
4. Update CI to pass `-backend-config=backend-config.hcl` on `terraform init`.

**Edge cases the fix must handle:**

- Existing local state in contributor workspaces — document `terraform init -migrate-state` for migration.
- Parallel applies — rely on GCS object versioning + Terraform's state locking via GCS generation numbers.
- Rolling back to local state for dev — allow `-backend=false` on `terraform init` for test harnesses.

**Test plan:**

- `terraform init -backend-config=...` — succeeds.
- `terraform plan` against a freshly migrated backend — no unexpected diff.
- Deliberate concurrent `terraform apply` — second invocation waits/locks.

**Verification:**

- `terraform validate` in `iac/federation/gcp-target/terraform/`
- `terraform init` with supplied backend config succeeds cold and warm.

**Related issues:** none

**Effort:** `small`
