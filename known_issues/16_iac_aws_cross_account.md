# Known Issues: IaC AWS Cross-Account

> **Audit status (2026-04-20):** `0 still valid · 7 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~CRITICAL: No ExternalID Condition on Trust Policy — Confused Deputy Vulnerability~~ — RESOLVED

**File**: `iac/federation/aws-cross-account/terraform/main.tf:17-25`
**Description**: No `sts:ExternalId` condition in the trust policy. Any compromised or malicious service within the source account can call `sts:AssumeRole` targeting this role. The role ARN is published in the `cudly_account_registration` output.
**Impact**: A malicious actor running any workload inside the source account can impersonate CUDly and assume this role, gaining purchase authority over all RI/SP commitment types in the target account.
**Status:** ✔️ Resolved

**Resolved by:** An `external_id` variable (auto-generated via `random_uuid` when empty) plus an `sts:ExternalId` `StringEquals` condition are now present on the trust policy at `iac/federation/aws-cross-account/terraform/main.tf:19-42`, closing the confused-deputy gap.

## ~~HIGH: `ec2:DescribeRegions` Missing from IAM Policy~~ — RESOLVED

**File**: `iac/federation/aws-cross-account/terraform/main.tf:42-57`
**Description**: The AWS provider's `GetRegions()` calls `ec2:DescribeRegions`. This action is absent from the policy.
**Impact**: Region enumeration fails with `AccessDenied` after assuming the role.
**Status:** ✔️ Resolved

**Resolved by:** `ec2:DescribeRegions` is now included in the inline action list at `iac/federation/aws-cross-account/terraform/main.tf:71`, so region discovery succeeds after assume-role.

## ~~HIGH: `source_account_id` Has No Format Validation~~ — RESOLVED

**File**: `iac/federation/aws-cross-account/terraform/variables.tf:1-4`
**Description**: No `validation` block. AWS account IDs are exactly 12 decimal digits. A typo produces an invalid ARN that makes the role unassumable. Error surfaces only at runtime.
**Status:** ✔️ Resolved

**Resolved by:** A `validation` block at `iac/federation/aws-cross-account/terraform/variables.tf:5-8` enforces `can(regex("^[0-9]{12}$", var.source_account_id))`, rejecting typos at plan time.

## ~~MEDIUM: `organizations:ListAccounts` Missing from IAM Policy~~ — RESOLVED

**File**: `iac/federation/aws-cross-account/terraform/main.tf:42-57`
**Description**: The AWS provider calls `organizations:ListAccounts` to discover member accounts; the policy did not include that action so the provider silently degraded to the current account only.
**Impact**: Multi-account discovery through a cross-account role silently returned an incomplete list.
**Status:** ✔️ Resolved

**Resolved by:** Added a `enable_org_discovery` boolean variable (default `false` so member-account deployments are unaffected) plus an opt-in `OrganizationsDiscovery` Sid statement granting `organizations:ListAccounts` and `organizations:DescribeOrganization`. Operators set the flag to `true` only when deploying the role into an Organizations management or delegated-administrator account.

### Original implementation plan

**Goal:** Let operators opt into multi-account discovery via this cross-account role so the provider returns the full org, not just the delegated account.

**Files to modify:**

- `iac/federation/aws-cross-account/terraform/main.tf:42-57` — add a new, opt-in `Organizations` statement.
- `iac/federation/aws-cross-account/terraform/variables.tf` — add `enable_org_discovery` boolean (default `false`) and document the required caller (payer/management account).
- `iac/federation/aws-cross-account/terraform/README.md` — explain when to enable the flag.

**Steps:**

1. Add `variable "enable_org_discovery" { default = false }` with description: "Grants organizations:ListAccounts/DescribeOrganization; set when this role is deployed in an AWS Organizations management/delegated account."
2. In `main.tf`, introduce a second inline policy statement (or `dynamic "statement"`) gated on `var.enable_org_discovery`, listing `organizations:ListAccounts` and `organizations:DescribeOrganization` with `Resource = "*"`.
3. Keep the default disabled so member-account deployments do not fail validation against the Organizations IAM boundary.
4. Surface the flag in the top-level `terraform.tfvars.example` with commentary.

**Edge cases the fix must handle:**

- Role deployed in a member (non-management) account — flag must default to off; enabling would fail.
- Delegated administrator for Organizations — document that the flag is sufficient, no extra SCP changes needed.
- Permission boundaries — note that the org account owner may still need to attach an SCP allowing the action.

**Test plan:**

- `terraform plan` with `enable_org_discovery = false` — no change in the policy.
- `terraform plan` with `enable_org_discovery = true` — shows the extra statement.
- Runtime check: `aws organizations list-accounts` via assumed role succeeds when enabled.

**Verification:**

- `terraform validate` in `iac/federation/aws-cross-account/terraform/`
- IAM Access Analyzer preview or `aws iam simulate-custom-policy` to confirm the new actions are grantable.

**Related issues:** `16_iac_aws_cross_account#single-statement`

**Effort:** `small`

## ~~LOW: `cudly_account_registration` Output Omits ExternalID Field~~ — RESOLVED

**File**: `iac/federation/aws-cross-account/terraform/outputs.tf:6-13`
**Description**: No `external_id` field in the output. The aws-target output includes it.
**Status:** ✔️ Resolved

**Resolved by:** The `cudly_account_registration` output at `iac/federation/aws-cross-account/terraform/outputs.tf:6-10` now exposes `external_id` alongside the role ARN, allowing operators to pass it to CUDly's registration flow directly.

## ~~LOW: Single-Statement Inline Policy Prevents Per-Service Sid Labeling~~ — RESOLVED

**File**: `iac/federation/aws-cross-account/terraform/main.tf:34-60`
**Description**: The original inline single-statement policy with no Sids made it impossible to label or audit per-service permissions independently of one another. The aws-target module already used the split layout.
**Status:** ✔️ Resolved

**Resolved by:** Module already uses a standalone `aws_iam_policy.cudly` (managed policy) attached via `aws_iam_role_policy_attachment.cudly`, with seven Sid-labelled statements (`EC2Reservations`, `RDSReservations`, `ElastiCacheReservations`, `RedshiftReservations`, `MemoryDBReservations`, `SavingsPlans`, `OpenSearchReservations`) plus the new opt-in `OrganizationsDiscovery` statement. Added a `policy_arn` output so downstream automation can reference the managed policy directly.

### Original implementation plan

**Goal:** Bring the cross-account module in line with the aws-target bundle: one `aws_iam_policy` (managed) with per-service `Sid`-labelled statements.

**Files to modify:**

- `iac/federation/aws-cross-account/terraform/main.tf:34-60` — replace the inline policy on the role with a standalone `aws_iam_policy` and an `aws_iam_role_policy_attachment`.
- `iac/federation/aws-cross-account/terraform/main.tf` (same file, adjacent) — refactor the action list into per-service statements (`SidEC2`, `SidEBS`, `SidRDS`, `SidElastiCache`, `SidOpenSearch`, `SidPricing`, `SidOrganizations`, `SidSavingsPlans`, etc.) mirroring aws-target.
- `iac/federation/aws-cross-account/terraform/outputs.tf` — add `policy_arn` alongside the existing outputs.
- `iac/federation/aws-cross-account/terraform/README.md` — document the new managed policy and the stable Sids.

**Steps:**

1. Extract the current action list into a local data structure keyed by Sid.
2. Define an `aws_iam_policy "cudly_cross_account"` resource referencing an `aws_iam_policy_document` data source with one `statement` per Sid.
3. Replace `aws_iam_role_policy` with `aws_iam_role_policy_attachment` bound to the new managed policy.
4. Import existing deployments with a state-migration note in the commit message.
5. Add the `policy_arn` output for downstream automation.

**Edge cases the fix must handle:**

- Existing deployments — `terraform apply` will delete the inline policy and attach the managed one; document that this is non-disruptive because the role session picks up the new policy within seconds.
- IAM size limits — a managed policy has a 6144-byte quota; confirm the split stays below.
- The opt-in `organizations:ListAccounts` statement from the MEDIUM issue above must slot into this Sid layout.

**Test plan:**

- `terraform plan` — expected diff: inline policy removed, managed policy created, attachment created, no trust-policy change.
- `aws iam simulate-custom-policy` for each Sid.
- Runtime smoke test: CUDly account registration + purchase dry-run.

**Verification:**

- `terraform validate` in `iac/federation/aws-cross-account/terraform/`
- `terraform plan` diff review.

**Related issues:** `16_iac_aws_cross_account#medium-orgs`, `13_iac_aws_target` (template alignment)

**Effort:** `medium`
