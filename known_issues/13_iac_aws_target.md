# Known Issues: IaC AWS Target Federation

> **Audit status (2026-08-03):** `1 still valid · 10 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~CRITICAL: federation bundle generator never emits `OIDCSubjectClaim`~~ — RESOLVED

**File**:

- `internal/api/handler_federation.go` (`buildCFParamsJSON`, and the
  `format=cli` bundle selector at `:401`/`:415`)
- `internal/iacfiles/templates/aws-wif-cf-params.json.tmpl`
- `internal/iacfiles/templates/aws-wif-cli.sh.tmpl:17` (documents
  `OIDC_SUBJECT_CLAIM` as Optional) and `:57-70` (else branch builds the role
  with only the `:aud` condition and no `:sub`)
- `internal/iacfiles/templates/aws-cfn-deploy.sh.tmpl:34-38`, the
  `--parameter-overrides` list in the script that deploys the CloudFormation
  bundle; it does not pass `OIDCSubjectClaim`
- `internal/iacfiles/templates/aws-wif.tfvars.tmpl:14-15`, which emits
  `# oidc_subject_claim = ""` labelled Optional, contradicting the REQUIRED
  `oidc_subject_claim` variable in `terraform/variables.tf:28-42`

**Description**: The customer-facing onboarding bundle writes CloudFormation
parameter overrides for `OIDCIssuerURL`, `OIDCIssuerHost`, `OIDCAudience` and
`RoleName`, but never `OIDCSubjectClaim`. `aws-wif-cli.sh.tmpl` has the same
shape: it documents `OIDC_SUBJECT_CLAIM` as "Optional" and builds a trust
policy with no `:sub` condition when the variable is unset. Both paths
therefore produce the unrestricted trust policy that issue #1543 removed from
the template itself. The tfvars template repeats the claim that the subject is
optional, and the deploy script never forwards the parameter.
**Impact**: Now that `OIDCSubjectClaim` is a required template parameter, the
generated CloudFormation bundle fails at change-set creation with
`Parameters: [OIDCSubjectClaim] must have values` — fail-closed, but the
onboarding flow is broken until the generator and `aws-cfn-deploy.sh.tmpl` are
taught to emit the subject. The `aws-wif-cli.sh.tmpl` path does not go through
CloudFormation at all: `format=cli` is a first-class user-selectable download,
so a customer choosing the CLI bundle still gets exactly the subject-less
role #1543 describes. The tfvars path is fail-closed, but not for the reason
one might assume: with `oidc_subject_claim` commented out there is no value
for a `validation` block to inspect, so the validations never fire. What makes
it fail-closed is the **absent default** on the variable, which makes
Terraform prompt for the value interactively or hard-error under
`-input=false`. The validations only apply once a value exists. The security
property holds; the operator is still misled by the "Optional" label first.
**Status:** ✔️ Resolved

**Resolved by:** #TODO_PR_NUMBER (closes #1640) — `aws-wif-cli.sh.tmpl` drops the subject-less else
branch entirely and validates `OIDC_SUBJECT_CLAIM` (non-empty, no whitespace/
`$`/`*`) before making any AWS call; `federationIaCData` gains an
`OIDCSubjectClaim` field threaded through `shellEscapeData`, `buildCFParamsJSON`,
`aws-wif-cf-params.json.tmpl`, `aws-cfn-deploy.sh.tmpl`'s `--parameter-overrides`,
and `aws-wif.tfvars.tmpl` (uncommented, "Optional" label removed); the standalone
`scripts/generate-federation-iac.go` mirror gains the same field plus an
`--oidc-subject-claim` flag. CUDly's server still has no generic way to know
the calling workload's real subject (unlike `OIDCIssuerURL`/`OIDCAudience`,
which are derivable from target/source alone), so the value remains
operator-supplied — every artifact now requires it explicitly instead of
defaulting to a working-but-insecure empty value.

## LOW: `aws-wif-cli.sh.tmpl` hardcodes an unoverridable OIDC-provider thumbprint placeholder

**File**: `internal/iacfiles/templates/aws-wif-cli.sh.tmpl` — the
`--thumbprint-list` argument to `aws iam create-open-id-connect-provider`
(line number shifts as the file changes; grep for `thumbprint-list`), and the
provider-exists branch a few lines above it that skips re-checking it.
**Description**: The script hardcodes the all-zeros placeholder thumbprint
with no way for the operator to override it, and the `if` that creates the
OIDC provider only runs when one does not already exist — an existing
provider created with the placeholder is never corrected by re-running the
script. This is the CLI-bundle sibling of #1615, which #1678 fixed for the
CloudFormation and Terraform bundles but deliberately left this file alone
(noted on #1640 by the #1678 author, since `internal/iacfiles/` was this
issue's scope).
**Impact**: Not an authentication bypass — AWS only consults the configured
thumbprint on a fallback path (its JWKS certificate does not chain to a
trusted root, AWS cannot retrieve it, or the endpoint requires TLS 1.3), and
all-zeros is not the SHA-1 of any certificate, so it matches nothing on that
path and role assumption fails outright. The failure mode is availability and
misleading configuration, not takeover. `aws iam update-open-id-connect-provider-thumbprint`
is the out-of-band remedy for an already-created provider.
**Status:** ⚠️ Still valid — filed as #1689.

## ~~HIGH: `OIDCThumbprint` defaults to the all-zeros placeholder for any issuer~~ — RESOLVED

**File**: `iac/federation/aws-target/cloudformation/template.yaml` (`OIDCThumbprint`
parameter and the `ThumbprintList` property), `iac/federation/aws-target/terraform/variables.tf`
(`thumbprint_list`)
**Description**: `OIDCThumbprint` defaulted to
`0000000000000000000000000000000000000000` and was validated only by
`AllowedPattern: "^[0-9a-fA-F]{40}$"`, which that placeholder satisfies. The
Terraform sibling defaulted to the same value and permitted it for
`login.microsoftonline.com` and `accounts.google.com` issuers.

**Impact**: Not the authentication bypass it looks like (see below), but wrong
in both directions, and documented as if the value carried security meaning it
does not have.

AWS verifies the JWKS endpoint's TLS certificate against its own library of
trusted root CAs, and consults the configured thumbprint only when that
certificate does not chain to one of them, when AWS cannot retrieve the
certificate, or when the endpoint requires TLS 1.3
([IAM User Guide](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_providers_create_oidc_verify-thumbprint.html),
[CreateOpenIDConnectProvider](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateOpenIDConnectProvider.html)).
Consequences:

- For a publicly-trusted issuer, including both this template documents, the
  thumbprint is read only under the fallback conditions listed above, which
  normal operation does not hit. All-zeros is inert there, and so is a correct
  thumbprint. An attacker still needs a JWKS-host certificate signed by a CA in
  AWS's trusted-root library, and that bar does not move with this parameter.
- On the fallback path, all-zeros is not the SHA-1 of any certificate, so it
  matches nothing and every `AssumeRoleWithWebIdentity` call fails. It does not
  bypass chain verification; it nulls out the only verification that path has.
  The failure mode is availability, not takeover.

The parameter therefore had no value that fails open, and the guard the
Terraform module carried ("the all-zeros value bypasses the CA-chain check
entirely") was defending against a mechanism that does not exist, while its
issuer allowlist was beside the point in both directions.

**Status:** ✔️ Resolved

**Resolved by:** #1615. `ThumbprintList` is optional on
`AWS::IAM::OIDCProvider`; when omitted, IAM retrieves and uses the issuer's
real top intermediate CA thumbprint, which is what the IAM console does by
default. That is now the default on both paths.

- CloudFormation: `OIDCThumbprint` defaults to empty, a `HasThumbprint`
  condition omits `ThumbprintList` via `AWS::NoValue` when it is, and the
  placeholder is rejected by two independent layers: an `AllowedPattern`
  requiring at least one non-zero hex digit, and an unconditional `Rules`
  assertion naming the literal.
- Terraform: `thumbprint_list` defaults to `[]` and is passed as `null` when
  empty; the all-zeros entry is rejected unconditionally rather than for
  non-allowlisted issuers.
- Regression coverage in `internal/api/handler_federation_test.go` reads the
  `AllowedPattern`, `MaxLength` and `Default` back out of the committed
  template and evaluates them the way CloudFormation does.

**Upgrade note:** `ThumbprintList` updates with no interruption, so switching
forms never replaces the OIDC provider or changes its ARN. A CloudFormation
stack still holding the placeholder fails its next update until the parameter
is cleared or set to a real thumbprint. That is deliberate, and such a stack cannot be
authenticating on the fallback path today anyway. Terraform's `thumbprint_list`
is Optional+Computed, so clearing it does not clear a value already stored on
an existing provider; correct those with
`aws iam update-open-id-connect-provider-thumbprint` or replace the resource.

**Still open:** `internal/iacfiles/templates/aws-wif-cli.sh.tmpl:33` hardcodes
the same placeholder into `aws iam create-open-id-connect-provider`. That file
is owned by #1640 and was left alone here; noted on that issue.

## ~~CRITICAL: CloudFormation `:sub` restriction is optional and defaults to trusting every issuer identity~~ — RESOLVED

**File**: `iac/federation/aws-target/cloudformation/template.yaml:52-57`, `:64-66`, `:171-178`
**Description**: `OIDCSubjectClaim` defaulted to `""` and a `HasSubject`
condition selected a trust statement that omitted the `:sub` condition
entirely, gating only on `<issuer>:aud`. With `OIDCAudience` also left at its
default the audience collapsed to the literal `sts.amazonaws.com`, so any
identity the issuer could mint — for the documented `accounts.google.com`
issuer, any Google service account anywhere — could call
`sts:AssumeRoleWithWebIdentity` and obtain `ec2:PurchaseReservedInstancesOffering`,
`savingsplans:CreateSavingsPlan` and `rds:PurchaseReservedDBInstancesOffering`
in the customer's account.
**Impact**: Unauthenticated-in-practice takeover of the commitment-purchasing
role in every customer account deployed with the default parameters. The
purchases are irreversible multi-year spend.
**Status:** ✔️ Resolved

**Resolved by:** #1543 — removes the `HasSubject` condition and the
subject-less trust statement, and makes `OIDCSubjectClaim` a required
parameter (no `Default`, `MinLength: 1`, `AllowedPattern` rejecting
any whitespace, `*` or `$`), mirroring the Terraform module's
`oidc_subject_claim` validation.

`$` is rejected because `AssumeRolePolicyDocument` is an IAM policy document
and IAM expands `${...}` policy variables inside `Condition` values. A subject
of `${accounts.google.com:sub}` (a documented web-identity policy variable for
the `accounts.google.com` issuer this template targets) expands to the
presented token's own `sub` claim, making the condition a tautology that
matches every identity the issuer can mint. An operator could reach that value
by copy-pasting a placeholder or by running a templating layer that failed to
interpolate, reconstructing the #1543 hole through the guard added to close
it. The same guard is applied to `OIDCAudience` and to the Terraform
`oidc_subject_claim` / `oidc_audience` variables, since audience is the only
other control on this trust policy.

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

> **Follow-up:** the *conditional* form this entry describes was itself the
> CRITICAL hole filed as #1543 — the parameter defaulted to `""` and the
> condition then selected a subject-less trust statement. The `:sub` check is
> now unconditional; see the #1543 entry above.

## ~~HIGH: `OIDCThumbprint` parameter has no format validation in CloudFormation~~ — RESOLVED

**File**: `iac/federation/aws-target/cloudformation/template.yaml:33-40`
**Description**: Accepts any string. A valid SHA-1 thumbprint must be exactly 40 hex characters. No `AllowedPattern`.
**Impact**: Invalid thumbprint creates an OIDC provider that silently fails certificate validation at auth time.
**Status:** ✔️ Resolved

**Resolved by:** `a96fc719f` — adds `AllowedPattern: "^[0-9a-fA-F]{40}$"` on the `OIDCThumbprint` parameter.

## ~~HIGH: CF and Terraform derive the condition key host differently~~ — RESOLVED

**File**: `template.yaml:16-23` vs `main.tf:123`
**Description**: CF accepted any string for `OIDCIssuerHost`; Terraform built it via `trimprefix` only (without `trimsuffix`). A trailing slash in the issuer URL produced mismatched condition keys, and CF never validated the relationship between `OIDCIssuerHost` and `OIDCIssuerURL`.
**Impact**: Role assumption silently failed with `AccessDenied` when the issuer URL or host had a trailing slash.
**Status:** ✔️ Resolved

**Resolved by:**

- Terraform now derives `local.oidc_issuer_url_normalized` (`trimsuffix(..., "/")`) and `local.oidc_condition_host` (`trimsuffix(trimprefix(..., "https://"), "/")`) once and uses them for both the OIDC provider URL and the trust-policy condition keys, so trailing slashes are handled consistently.
- The `oidc_issuer_url` variable now has a strict regex validation rejecting trailing slashes and non-https URLs.
- `OIDCIssuerURL` and `OIDCIssuerHost` parameters in CloudFormation now have explicit `AllowedPattern` + `ConstraintDescription` forbidding trailing slashes; the description calls out that the operator must keep the two values in sync.

### Original implementation plan

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

**Resolved by:** Added `AllowedPattern` and `ConstraintDescription` to the `OIDCAudience` parameter. Empty strings remain allowed (HasAudience stays false); whitespace-only and leading/trailing-whitespace values are now rejected at change-set creation time. #1543 tightened the pattern to `^$|^[^\\s*$]+$`, additionally rejecting `*`, `$` and interior whitespace for the IAM policy-variable-expansion reason described in the #1543 entry above.

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

> **Superseded by #1615.** The reasoning above is wrong: AWS does not
> "auto-validate" a special set of well-known providers, it validates the JWKS
> endpoint's TLS certificate against its trusted-root CA library for *every*
> issuer and reads the thumbprint only as a fallback. Keeping the all-zeros
> default was therefore not safe-for-those-two, it was inert everywhere it was
> read and fail-closed everywhere it mattered. The empty-list rejection has
> also been removed, since an empty list is now the correct default: it makes
> IAM retrieve the real thumbprint. See the #1615 entry above.

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
