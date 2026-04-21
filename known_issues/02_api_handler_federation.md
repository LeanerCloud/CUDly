# Known Issues: Federation Handler

> **Audit status (2026-04-20):** `0 still valid · 5 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~CRITICAL: Shell Injection in CloudFormation Deploy Script Template~~ — RESOLVED

**File**: `internal/api/handler_federation.go:481-513`, `internal/iacfiles/templates/aws-cfn-deploy.sh.tmpl`
**Description**: `buildFederationBundle` previously rendered `aws-cfn-deploy.sh.tmpl` using `text/template`, which performs no escaping. Account fields (`AccountSlug`, `OIDCIssuerURL`, `OIDCAudience`) were interpolated directly into double-quoted shell argument positions, enabling an admin with account-record write access to inject shell code that would execute on any machine running the generated `deploy-cfn.sh`.
**Impact**: Arbitrary shell code execution on downloader's machine; privilege escalation via AWS creds in the runner environment.
**Status:** ✔️ Resolved

**Resolved by:** `13bd81462` — adds `shellEscape` (line 269, escapes `\`, `"`, `` ` ``, `$`) and `shellEscapeData` (line 158) which are applied before rendering each shell script (`writeCFNFiles` calls `escapedData := shellEscapeData(data)` at line 504 before `renderTemplate`). All other CLI script emissions were audited to use `shellEscapeData` as well.

## ~~HIGH: Malformed JSON Output When Account Fields Contain Special Characters~~ — RESOLVED

**File**: `internal/api/handler_federation.go:495-498`, `621-626`
**Description**: `aws-wif-cf-params.json.tmpl` was previously rendered with `text/template`, so any `"` or `\` character in `OIDCIssuerURL`, `OIDCAudience`, or `AccountSlug` produced syntactically invalid JSON.
**Impact**: CF params files silently corrupted; CloudFormation deploys failed with cryptic parser errors.
**Status:** ✔️ Resolved

**Resolved by:** `13bd81462` — the JSON template was removed and replaced with `buildCFParamsJSON` (line 626), which builds the parameter list as Go structs and encodes via `encoding/json`. A dedicated regression test `TestGetFederationIaC_CFNZip_ParamsValidJSON` (line 259 in the test file) asserts the output parses.

## ~~MEDIUM: `source` Parameter Is Never Validated Against an Allowlist~~ — RESOLVED

**File**: `internal/api/handler_federation.go:236-265`
**Description**: `federationIaCParams` previously checked only that `source` was non-empty; its raw value was written verbatim into the bundle README, and the `switch` in `awsOIDCIssuer`/`awsOIDCAudience` silently produced empty strings for unrecognised source values.
**Impact**: Crafted `source` values (including newlines) could inject README content.
**Status:** ✔️ Resolved

**Resolved by:** `13bd81462` — adds `validFederationSources` allowlist (line 236) and checks it in `federationIaCParams` (line 255) returning 400 for any source other than `aws`/`azure`/`gcp`. `TestGetFederationIaC_InvalidSource` covers the path.

## ~~MEDIUM: `addDirToZip` Silently Drops Subdirectories~~ — RESOLVED

**File**: `internal/api/handler_federation.go:655-656`
**Description**: The prior implementation iterated `fs.ReadDir` and skipped any `entry.IsDir()` entry, so Terraform module subdirectories were silently absent from the generated zip.
**Impact**: Bundles could ship incomplete modules.
**Status:** ✔️ Resolved

**Resolved by:** Rewrite uses `fs.WalkDir` (line 656 of current file) which recurses into every subdirectory of the embedded module tree. Confirm via `grep 'fs.WalkDir' internal/api/handler_federation.go`.

## ~~LOW: Missing Test Coverage for Invalid `source` Parameter~~ — RESOLVED

**File**: `internal/api/handler_federation_test.go:245-257`
**Description**: No test previously exercised an invalid `source` value.
**Status:** ✔️ Resolved

**Resolved by:** `13bd81462` — `TestGetFederationIaC_InvalidSource` now asserts that `source=badcloud` returns a 400 with message "source must be…".
