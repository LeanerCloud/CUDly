# Known Issues: IaC AWS Target Federation

> **Audit status (2026-04-20):** `1 still valid · 6 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~CRITICAL: Terraform trust policy silently drops `:aud` condition when `oidc_subject_claim` is set~~ — RESOLVED

**File**: `iac/federation/aws-target/terraform/main.tf:120-131`
**Description**: The `assume_role_policy` uses `merge()` to combine `:aud` and `:sub` conditions. Both maps share the top-level key `StringEquals`. Terraform's `merge()` does a shallow merge, so the second map's `StringEquals` silently overwrites the first. When `oidc_subject_claim` is non-empty, the audience check is completely absent.
**Impact**: Any token from the IdP with the correct subject claim can assume the role regardless of its `aud` claim. Bypasses audience restriction entirely.
**Status:** ✔️ Resolved

**Resolved by:** `a96fc719f` — merges both `:aud` and `:sub` under a single `StringEquals` map so the audience check is preserved when a subject claim is configured.

## ~~HIGH: CloudFormation template has no subject-claim restriction parameter~~ — RESOLVED

**File**: `iac/federation/aws-target/cloudformation/template.yaml:127-138`
**Description**: The trust policy only enforces `:aud`. There is no `OIDCSubjectClaim` parameter equivalent to Terraform's `oidc_subject_claim`. Any principal with the correct audience can assume the role.
**Impact**: For shared IdPs where audience alone is not sufficiently unique, any application in the tenant can assume the role.
**Status:** ✔️ Resolved

**Resolved by:** `a96fc719f` — adds an `OIDCSubjectClaim` parameter and conditional `:sub` `StringEquals` alongside the existing `:aud` check.

## ~~HIGH: `OIDCThumbprint` parameter has no format validation in CloudFormation~~ — RESOLVED

**File**: `iac/federation/aws-target/cloudformation/template.yaml:33-40`
**Description**: Accepts any string. A valid SHA-1 thumbprint must be exactly 40 hex characters. No `AllowedPattern`.
**Impact**: Invalid thumbprint creates an OIDC provider that silently fails certificate validation at auth time.
**Status:** ✔️ Resolved

**Resolved by:** `a96fc719f` — adds `AllowedPattern: "^[0-9a-fA-F]{40}$"` on the `OIDCThumbprint` parameter.

## HIGH: CF and Terraform derive the condition key host differently

**File**: `template.yaml:16-23` vs `main.tf:123`
**Description**: CF requires separate `OIDCIssuerHost` parameter; Terraform uses `trimprefix`. A trailing slash in the issuer URL produces mismatched condition keys. No validation that `OIDCIssuerHost` matches `OIDCIssuerURL`.
**Impact**: Role assumption silently fails with `AccessDenied`.
**Status:** ✅ Still valid

### Implementation plan

**Goal:** Derive the OIDC condition-key host identically in CF and Terraform so trailing slashes or mismatched `OIDCIssuerHost` values cannot produce an unusable trust policy.

**Files to modify:**

- `iac/federation/aws-target/terraform/main.tf:120-131` — normalize the issuer host with `trimsuffix(trimprefix(var.oidc_issuer_url, "https://"), "/")` before building the condition key.
- `iac/federation/aws-target/terraform/variables.tf` — add `validation` on `oidc_issuer_url` that requires `^https://[^/]+(/.*)?$`.
- `iac/federation/aws-target/cloudformation/template.yaml:16-31` — either derive `OIDCIssuerHost` via `Fn::Select`/`Fn::Split` on `OIDCIssuerURL` (preferred) or add an `AllowedPattern` + explicit doc forbidding trailing slash.
- `iac/federation/aws-target/cloudformation/template.yaml` (Parameters) — add `ConstraintDescription` explaining the no-trailing-slash rule if kept separate.

**Steps:**

1. In Terraform, wrap the existing `trimprefix` call with `trimsuffix(..., "/")` and reuse the normalized local in both the `OpenIDConnectProvider URL` argument and the condition-key construction.
2. Add a `validation` block to `variables.tf` enforcing an https URL with no trailing slash; include a clear error message.
3. In CloudFormation, prefer eliminating `OIDCIssuerHost` by deriving it from `OIDCIssuerURL` with `!Select [2, !Split ["/", !Ref OIDCIssuerURL]]`; if operators still need the override, document that `OIDCIssuerURL` must be scheme-prefixed and must not end in `/`.
4. Update the module README / `--help` snippets so the two bundles are documented as behaviourally identical.

**Edge cases the fix must handle:**

- `https://example.com/` (trailing slash) — should produce the same condition key as `https://example.com`.
- `https://example.com/path/` (path + trailing slash) — reject in Terraform validation, reject in CF `AllowedPattern`.
- Mixed-case hostnames — keep as-is (AWS is case-sensitive on condition keys).

**Test plan:**

- `terraform validate` and `terraform plan -var oidc_issuer_url=https://example.com/` on a throw-away plan — expect failure under new validation.
- `terraform plan` with the canonical CUDly issuer URL — expect no diff after the refactor.
- `aws cloudformation validate-template --template-body file://template.yaml` — should succeed with the new constraints.

**Verification:**

- `terraform validate` in `iac/federation/aws-target/terraform/`.
- `aws cloudformation validate-template` on the CF bundle.
- Optional: `cfn-lint` on `template.yaml`.

**Related issues:** `13_iac_aws_target#medium-audience`, `13_iac_aws_target#medium-thumbprint`

**Effort:** `small`

## ~~MEDIUM: `OIDCAudience` permits empty/whitespace, making `HasAudience` fragile~~ — RESOLVED

**File**: `iac/federation/aws-target/cloudformation/template.yaml:25-31`
**Description**: A whitespace-only audience value previously created a trust policy that no token matches.
**Status:** ✔️ Resolved

**Resolved by:** Added `AllowedPattern: "^$|^\\S$|^\\S.*\\S$"` and `ConstraintDescription` to the `OIDCAudience` parameter. Empty strings remain allowed (HasAudience stays false); whitespace-only and leading/trailing-whitespace values are now rejected at change-set creation time.

### Original implementation plan

**Goal:** Reject empty or whitespace-only `OIDCAudience` values at template submission so the `HasAudience` condition cannot silently produce a trust policy that no token matches.

**Files to modify:**

- `iac/federation/aws-target/cloudformation/template.yaml:25-31` — add `AllowedPattern` and `ConstraintDescription` to the `OIDCAudience` parameter.

**Steps:**

1. Add `AllowedPattern: "^$|^\\S.*\\S$|^\\S$"` to `OIDCAudience` (allow empty — handled by `HasAudience` — or a non-whitespace-trimmed string).
2. Add `ConstraintDescription: "OIDCAudience must be either empty or a non-whitespace string."`.
3. Update module README to document the rule.

**Edge cases the fix must handle:**

- Empty string (`""`) — allowed, `HasAudience` stays false.
- Single-character audience (`"x"`) — allowed.
- Leading/trailing whitespace (`" sts.amazonaws.com "`) — rejected.
- Whitespace-only (`"   "`) — rejected.

**Test plan:**

- `aws cloudformation validate-template` — expect no schema error.
- `aws cloudformation create-change-set` with whitespace-only audience — expect parameter validation error.

**Verification:**

- `aws cloudformation validate-template --template-body file://template.yaml`
- `cfn-lint iac/federation/aws-target/cloudformation/template.yaml`

**Related issues:** `13_iac_aws_target#high-host-mismatch`

**Effort:** `small`

## ~~MEDIUM: Terraform `thumbprint_list` defaults to zeros with no validation~~ — RESOLVED

**File**: `iac/federation/aws-target/terraform/variables.tf:38-46`
**Description**: Default was an all-zeros placeholder with no validation; bogus or wrong-length thumbprints would silently produce a non-functional OIDC provider that fails at auth time.
**Status:** ✔️ Resolved

**Resolved by:** Added two `validation` blocks to `thumbprint_list`: one rejects empty lists, the other requires every entry to match `^[0-9a-fA-F]{40}$`. The all-zeros default is preserved (AWS auto-validates well-known providers like Azure AD/Google and accepts the placeholder for them); the validation prevents the typo'd / wrong-length cases that otherwise surface only at runtime. Custom issuers that need a real thumbprint are documented in the variable description.

### Original implementation plan

**Goal:** Prevent the all-zeros default from silently producing a non-functional OIDC provider by validating each thumbprint at `terraform plan` time.

**Files to modify:**

- `iac/federation/aws-target/terraform/variables.tf:38-46` — add a `validation` block on `thumbprint_list`.
- `iac/federation/aws-target/terraform/README.md` (if present) — document the hex-40 rule.

**Steps:**

1. Add a `validation` block: `condition = alltrue([for t in var.thumbprint_list : can(regex("^[0-9a-fA-F]{40}$", t))])` with an error message citing "must be a 40-character hex SHA-1 thumbprint".
2. Optionally keep the placeholder default but add a second validation disallowing the all-zeros string explicitly for clarity.
3. Surface the same rule in the CF template via `AllowedPattern` (already tracked in the HIGH issue above).

**Edge cases the fix must handle:**

- Empty list — reject (a provider with no thumbprints will not work).
- Mixed case hex (`"AbCdEf..."`) — allow.
- All-zeros placeholder — reject (operator forgot to override).
- Longer/shorter strings — reject.

**Test plan:**

- `terraform validate` with default (`"0000…"`) — expect failure.
- `terraform plan` with a real thumbprint — expect success and no diff vs. baseline.

**Verification:**

- `terraform validate` in `iac/federation/aws-target/terraform/`
- `terraform plan -var-file=example.tfvars`

**Related issues:** `13_iac_aws_target#high-host-mismatch`

**Effort:** `small`

## ~~LOW: `ec2:DescribeRegions` missing from both CF and Terraform IAM policies~~ — RESOLVED

**File**: `template.yaml:60-121` and `main.tf:21-103`
**Description**: The AWS provider calls `ec2:DescribeRegions` in `GetRegions()` but this action is absent from both policy definitions.
**Impact**: Region enumeration fails with `AccessDenied` after assuming the WIF role.
**Status:** ✔️ Resolved

**Resolved by:** `a96fc719f` — adds `ec2:DescribeRegions` to both the CloudFormation and Terraform managed-policy action lists.
