# Known Issues: Credentials Resolver

> **Audit status (2026-04-20):** `0 still valid · 5 resolved · 0 partially fixed · 0 moved · 0 needs triage`

## ~~HIGH: `jti` claim is a non-unique integer — JWT replay possible under concurrency~~ — RESOLVED

**File**: `internal/credentials/resolver.go:373-386`
**Description**: The `assertionFunc` closure in `buildAzureWIFCredential` previously set `jti` to `fmt.Sprintf("%d", now.UnixNano())`. Under concurrent token refreshes, two invocations in the same nanosecond produced identical `jti`/`exp`, letting Azure AD replay-detection either reject assertions or accept replays depending on timing.
**Impact**: Potential JWT replay within the 5-minute assertion window; intermittent auth failures in multi-goroutine callers.
**Status:** ✔️ Resolved

**Resolved by:** `13bd81462` — `jti` is now generated via `uuid.NewString()` (line 379), guaranteeing uniqueness per token.

## ~~MEDIUM: `parsePEMBlob` silently takes the last key/cert when multiple PEM blocks are present~~ — RESOLVED

**File**: `internal/credentials/resolver.go:294-345`
**Description**: The loop previously overwrote `rsaKey`/`cert` on each matching PEM block, so a blob with two `CERTIFICATE` or two `PRIVATE KEY` blocks would silently pick the last one — possibly mismatched pairs producing `AADSTS700027`.
**Impact**: Silent misconfiguration; hard-to-trace production auth failures.
**Status:** ✔️ Resolved

**Resolved by:** `13bd81462` — `processPEMBlock` (line 322) now returns `"multiple private key blocks"` / `"multiple certificate blocks"` errors on duplicates, failing loudly instead of silently overwriting.

## ~~MEDIUM: `AWSWebIdentityTokenFile` path is not sanitized before use~~ — RESOLVED

**File**: `internal/credentials/resolver.go:165-191`
**Description**: `account.AWSWebIdentityTokenFile` was previously passed to `stscreds.IdentityTokenFile(tokenFile)` without validation, allowing traversal paths like `../../../../etc/shadow` to be sent to STS as an OIDC token (visible in CloudTrail).
**Impact**: Path traversal enabling exfiltration of arbitrary process-readable files via failed STS calls.
**Status:** ✔️ Resolved

**Resolved by:** `13bd81462` — lines 177-179 now reject the token file path unless it is absolute and free of `..` segments: `if strings.Contains(tokenFile, "..") || !filepath.IsAbs(tokenFile) { return error }`.

## ~~MEDIUM: `resolveBastionProvider` has no enforcement that the STS client carries bastion credentials~~ — RESOLVED

**File**: `internal/credentials/resolver.go:145-159`
**Description**: `resolveBastionProvider` previously delegated straight to `resolveRoleARNProvider` with the caller-supplied `stsClient`, so it had no runtime check that the STS client actually carried bastion credentials.
**Impact**: Silent privilege escalation or incorrect audit trail if callers passed the wrong STS client.
**Status:** ✔️ Resolved

**Resolved by:** Added `AccountLookupFunc`, `STSClientFactory`, and `AWSResolveOptions` types in `internal/credentials/resolver.go`. New `ResolveAWSCredentialProviderWithOpts` accepts these options; the bastion path now self-loads the bastion `CloudAccount` via `opts.AccountLookup`, recursively resolves its credentials, builds an STS client via `opts.STSClientFactory`, and only then assumes the target role. Bastion-of-bastion chaining, missing accounts, and disabled accounts are rejected with clear errors. The legacy 4-arg `ResolveAWSCredentialProvider` remains as a back-compat wrapper that falls through to the old "trust caller's STS client" behaviour when neither option is wired (so existing callers in scheduler/purchase keep working until they're migrated). Locked down by `TestResolveBastionProvider_LoadsBastionCreds`, `_RejectsBastionChain`, `_BastionNotFound`, `_BastionDisabled`, `_LegacyFallback`.

### Original implementation plan

**Goal:** Make the bastion resolver self-contained: it should load and use the bastion account's credentials rather than trust a caller-supplied STS client.

**Files to modify:**

- `internal/credentials/resolver.go:145-159` — resolve bastion credentials internally
- `internal/config/interfaces.go` — ensure there is a `GetCloudAccount(ctx, id)` on the config interface accessible to the resolver (or pass an `AccountLookup` callback)
- `internal/purchase/execution.go:152-160` — update call site (stop passing the bastion STS client)
- `internal/credentials/resolver_test.go` — new tests

**Steps:**

1. Change `ResolveAWSCredentialProvider` signature (or add a new option) to accept an account lookup callback, e.g. `AccountLookupFunc func(ctx, id string) (*config.CloudAccount, error)`.
2. Inside `resolveBastionProvider`: load the bastion `CloudAccount`, recursively resolve its credentials (access_keys or role_arn — not bastion, guarded by a depth check to prevent loops), build an STS client with those credentials, then call `resolveRoleARNProvider` with that STS client.
3. Prevent infinite recursion (`bastion of bastion of…`): limit depth to 1 and return an error if the bastion itself has `AWSAuthMode == "bastion"`.
4. Update call sites in `internal/purchase/execution.go` so they no longer pre-resolve bastion creds; the resolver owns that now.

**Edge cases the fix must handle:**

- Bastion account has `AWSAuthMode == "bastion"` → reject (no chaining).
- Bastion account not found → clear error.
- Bastion ID field set but the referenced account is disabled → reject.

**Test plan:**

- `TestResolveBastionProvider_LoadsBastionCreds` — mocks the lookup and asserts STS client used for the target assume-role was built from the bastion's own credentials.
- `TestResolveBastionProvider_RejectsBastionChain` — asserts loop rejection.
- `TestResolveBastionProvider_BastionNotFound` — asserts clear error.

**Verification:**

- `go test ./internal/credentials/... ./internal/purchase/...`

**Effort:** `medium`

## ~~LOW: Nil store guard for `access_keys` mode is untested~~ — RESOLVED

**File**: `internal/credentials/resolver_extra_test.go:593-601`
**Description**: `resolveAccessKeyProvider` returns an error when `store == nil`, but no test previously exercised that path.
**Status:** ✔️ Resolved

**Resolved by:** `TestResolveAccessKeyProvider_NilStore` covers the path and asserts the `"credential store required"` error message.
