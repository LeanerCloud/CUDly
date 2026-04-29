<!-- markdownlint-disable MD040 MD060 -->
# Implementation Tasks

Tasks are ordered by dependency. Each phase can begin once the previous phase's tasks are complete. Within a phase, tasks can be parallelised where noted.

Commit strategy: one atomic commit per task (or per logical sub-task if a task is large). Each commit must pass `make full-test` and `npm test`.

---

## Phase 1 — Data Layer

These tasks establish the foundation that everything else depends on.

### Task 1.1: DB Migration

**Files to create:**

- `internal/database/postgres/migrations/000011_cloud_accounts.up.sql`
- `internal/database/postgres/migrations/000011_cloud_accounts.down.sql`

**What to do:** Write the migration as specified in `data-model.md`. Include all four new tables (`cloud_accounts`, `account_credentials`, `account_service_overrides`, `plan_accounts`), the FK column additions on existing tables, all indexes, and the `updated_at` triggers.

**Verification:** `make migrate-up` on a clean test DB; `make migrate-down` brings it back to state 000010; `make migrate-up` again leaves schema identical.

---

### Task 1.2: Go Type Definitions

**Files to modify:**

- `internal/config/types.go`

**What to do:** Add `CloudAccount`, `CloudAccountFilter`, `AccountServiceOverride` structs as specified in `data-model.md` (Go type additions section). Add `CloudAccountID *string` field to: `PurchaseHistoryRecord`, `PurchaseExecution`, `RIExchangeRecord`, and `RecommendationRecord` (needed so aggregated multi-account recommendations can be tagged with the originating account UUID). Note: existing structs carry `dynamodbav` struct tags — add `db:"cloud_account_id"` or omit tags since pgx uses positional scanning, not tag-based.

**Verification:** `go build ./...` passes with no new errors.

---

### Task 1.3: Store Interface Extension

**Files to modify:**

- `internal/config/interfaces.go`

**What to do:** Add new method signatures to `StoreInterface` as listed in `backend.md` (Store Interface Additions section). The mock is hand-written (no generator) — add the new mock methods to `internal/mocks/stores.go` following the existing `testify/mock` pattern in that file (e.g. `func (m *MockConfigStore) CreateCloudAccount(ctx context.Context, account *config.CloudAccount) error { ... }`).

**Verification:** `go build ./...` passes; existing mock still compiles (update mock if needed).

---

### Task 1.4: Credential Encryption Package

**Files to create:**

- `internal/credentials/cipher.go`
- `internal/credentials/cipher_test.go`
- `internal/credentials/store.go`
- `internal/credentials/resolver.go`
- `internal/credentials/resolver_test.go`

**What to do:** Implement AES-256-GCM encrypt/decrypt, the `CredentialStore` interface backed by `account_credentials` table, and the `ResolveAWSCredentialProvider` / `ResolveAzureCredentials` / `ResolveGCPCredentials` functions as specified in `backend.md`.

Ensure all structs have `String() string` returning `"[REDACTED]"`.

**Tests:** Round-trip encrypt/decrypt, resolver for each auth mode (mock STS client), dev key fallback behaviour, `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN` path (mock `secretsmanager.GetSecretValue`).

**Verification:** `go test ./internal/credentials/...` — all pass.

---

### Task 1.5: Service Config Resolver

**Files to create:**

- `internal/config/resolver.go`
- `internal/config/resolver_test.go`

**What to do:** Implement `ResolveServiceConfig(global, override)` as specified in `backend.md`. Table-driven tests covering: no override (returns global), partial override (sparse merge), full override.

**Verification:** `go test ./internal/config/... -run TestResolveServiceConfig`

---

### Task 1.6: Store Implementation (Cloud Accounts)

**Files to modify:**

- `internal/config/store_postgres.go`

**What to do:** Implement the new `StoreInterface` methods: `CreateCloudAccount`, `GetCloudAccount`, `UpdateCloudAccount`, `DeleteCloudAccount`, `ListCloudAccounts`, `GetAccountServiceOverride`, `SaveAccountServiceOverride`, `DeleteAccountServiceOverride`, `ListAccountServiceOverrides`, `SetPlanAccounts`, `GetPlanAccounts`.

Do NOT implement `CredentialStore` here — that lives in `internal/credentials/store.go` to avoid a circular import (`credentials` imports `config`; `config` must not import `credentials`).

Follow the existing CRUD pattern: parameterised queries, `nullStringFromString` helpers for nullable fields, helper `queryXxx` functions for row scanning.

**Verification:** `go test ./internal/config/... -tags integration` (requires test DB).

---

## Phase 2 — Backend API

Can begin once Phase 1 is complete.

### Task 2.1: Account HTTP Handlers

**Files to create:**

- `internal/api/handler_accounts.go`
- `internal/api/handler_accounts_test.go`

**Files to modify:**

- `internal/api/types.go` — add `CredentialStore credentials.CredentialStore` field to `HandlerConfig`
- `internal/api/handler.go` — add `credStore credentials.CredentialStore` field to `Handler` struct; assign from config in `NewHandler`
- `internal/server/app.go` — add `CredentialStore credentials.CredentialStore` to `Application`; initialise via `credentials.NewCredentialStore(pool, key)` in the DB connect path

**Context:** HTTP handlers in this project live in `internal/api/handler_*.go`, NOT in `internal/server/`. The `internal/server/handler.go` is for *scheduled task* dispatch only. All Lambda Function URL request handling goes through `internal/api/`.

**What to do:** Implement all handlers from `api.md`:

- `handleListAccounts`, `handleCreateAccount`, `handleGetAccount`, `handleUpdateAccount`, `handleDeleteAccount`
- `handleSaveAccountCredentials`, `handleTestAccountCredentials`
- `handleListAccountServiceOverrides`, `handleSaveAccountServiceOverride`, `handleDeleteAccountServiceOverride`
- `handleDiscoverOrgAccounts`
- `handleGetPlanAccounts`, `handleSetPlanAccounts`

Follow existing handler pattern: method on `*Handler`, takes `*events.LambdaFunctionURLRequest`, returns `(any, error)`. Use `NewClientError(code, msg)` for all 4xx errors including 404 (e.g. `NewClientError(404, "account not found")`). The `errNotFound` sentinel and `notFoundError{}` type are router-internal (for unmatched routes) — do not use them in handlers. Never return Go errors as HTTP 500 — always wrap.

**Verification:** Unit tests with mock store: `go test ./internal/api/... -run TestAccounts`

---

### Task 2.2: Route Registration

**Files to modify:**

- `internal/api/router.go` — add new routes to `registerRoutes()` slice

**What to do:** Register all new `/api/accounts/*` and `/api/plans/:id/accounts` routes using the existing `Route{ExactPath/PathPrefix/PathSuffix, Method, Handler}` struct pattern. Add `parseAccountIDs` helper to `internal/api/handler.go`. Wire `account_ids` param into existing handlers: look up the handler functions in `handler_recommendations.go`, `handler_dashboard.go`, `handler_history.go`, and `handler_analytics.go` and add account_ids extraction using `parseAccountIDs(req)`. (`handler_config.go` handles service config only — no account_ids needed there.)

**Note on route ordering:** Exact-path routes must be registered before prefix/suffix routes in the slice to avoid shadowing — match the ordering pattern already established in `router.go`. Specifically, `POST /api/accounts/discover-org` must be registered as an `ExactPath` route before any `PathPrefix: "/api/accounts/"` POST routes.

**Note on multi-segment variable paths:** The current router's `extractParams` produces a single `params["id"]` — the path segment between `PathPrefix` and `PathSuffix`. For the service override routes (`PUT/DELETE /api/accounts/:id/service-overrides/:provider/:service`), the path can't be expressed with a fixed suffix because `:provider/:service` varies. Register these as `{PathPrefix: "/api/accounts/", Method: "PUT"/"DELETE"}` with no suffix, which causes `params["id"]` to be the full tail (e.g. `"<uuid>/service-overrides/aws/ec2"`). The handler must detect the `/service-overrides/` segment and split accordingly:

```go
parts := strings.SplitN(params["id"], "/service-overrides/", 2)
accountID := parts[0]                    // uuid
providerService := strings.SplitN(parts[1], "/", 2)  // ["aws", "ec2"]
```

Handlers for simple `PUT /api/accounts/:id` (no service-overrides in path) must be registered with a priority check so the dispatcher skips accounts whose params["id"] contains `/service-overrides/`.

**Verification:** `go test ./internal/api/...`; existing handler tests still pass.

---

### Task 2.3: Org Discovery Implementation

**Files to create:**

- `internal/accounts/org_discovery.go`
- `internal/accounts/org_discovery_test.go`

**What to do:** Implement `DiscoverOrgAccounts` as specified in `backend.md`. Use paginated `organizations.ListAccounts`. Map results to `CloudAccount` structs with `enabled=false` and `aws_auth_mode='bastion'` defaulting to the management account as bastion.

**Tests:** Mock Organizations client; test pagination, account mapping, idempotency (existing accounts skipped).

**Verification:** `go test ./internal/accounts/...`

---

## Phase 3 — Execution Engine

Can begin once Phase 1 is complete (parallel with Phase 2).

### Task 3.1: Fan-Out Engine

**Files to create:**

- `internal/execution/executor.go`
- `internal/execution/fanout.go`
- `internal/execution/collector.go`
- `internal/execution/fanout_test.go`

**What to do:** Implement `PrepareAccountContext`, `FanOut[T]` (generic, with `sync.WaitGroup` + buffered semaphore for parallelism — NOT `errgroup.WithContext`; see `backend.md` for rationale), and aggregation helpers as specified in `backend.md`.

**Tests:** Table-driven tests: all succeed, one fails, all fail, parallelism limit respected (mock executors with artificial delay).

**Verification:** `go test ./internal/execution/... -race` (race detector must pass).

---

### Task 3.2: Wire Fan-Out into Plan Execution

**Files to modify:**

- `internal/purchase/execution.go` — per-plan execution logic (confirmed location)
- `internal/purchase/manager.go` — `ProcessScheduledPurchases` which calls execution

**What to do:** Change plan execution to:

1. Derive plan's provider from `plan.Services` map keys (format `"provider:service"`). `PurchasePlan` has no direct `provider` field.
2. Load `plan_accounts` for the plan (or all enabled `cloud_accounts` matching the derived provider if `plan_accounts` is empty).
3. Call `FanOut(ctx, accounts, maxParallel, fn, credStore)` — `FanOut` calls `PrepareAccountContext` internally per goroutine (do NOT call PrepareAccountContext externally before FanOut). Use `sync.WaitGroup` + semaphore (not `errgroup.WithContext`).
4. For each `FanOutResult`: create one `purchase_execution` row with `cloud_account_id` set; on `FanOutResult.Err != nil`, mark execution failed and log.

**Verification:** Existing plan execution tests pass; new tests for multi-account fan-out.

---

## Phase 4 — Frontend: Accounts Management

Can begin once Phase 2 Task 2.1 is complete (API endpoints available).

### Task 4.1: Accounts API Module

**Files to create:**

- `frontend/src/api/accounts.ts`

**Files to modify:**

- `frontend/src/api/index.ts` (re-export)
- `frontend/src/api/types.ts` (add `CloudAccount` type here — it's an API response type; also add `accountIDs` to filter types)

**What to do:** Implement all functions from `frontend.md` (New API Module section). Follow existing `apiRequest` pattern.

**Verification:** `npm test` — add `frontend/src/__tests__/api-accounts.test.ts` with mocked fetch.

---

### Task 4.2: State and Type Changes

**Files to modify:**

- `frontend/src/types.ts` (add `AppState.currentAccountIDs` only — `CloudAccount` goes in `api/types.ts`)
- `frontend/src/state.ts` (add `currentAccountIDs` getter/setter, initialise to `[]`)

**Verification:** `npm test` — existing tests pass; `currentAccountIDs` defaults to `[]`.

---

### Task 4.3: Settings HTML — Account Sections and Modals

**Files to modify:**

- `frontend/src/index.html`

**What to do:** Add the following to `index.html`:

- Inside each provider fieldset (`#aws-settings`, `#azure-settings`, `#gcp-settings`), insert an Accounts sub-section **above** the existing `<h4 class="subsection-title">Service Defaults</h4>` heading. Each sub-section contains an account list `<div id="aws-account-list">` (etc.) and an "+ Add Account" button.
- Add `#aws-account-modal`, `#azure-account-modal`, `#gcp-account-modal` modal overlays as siblings of the existing modals (`#azure-creds-modal`, `#gcp-creds-modal`). Use `class="modal hidden"` + inner `class="modal-content modal-wide"`. Cancel button pattern: `class="btn-secondary"` with id `close-aws-account-modal-btn` etc. (see `frontend.md` Modals section).
- Add `#account-creds-modal` for updating credentials on existing accounts (same modal structure).

Auth mode sections in the AWS modal use the existing `hidden` class toggle pattern.

---

### Task 4.4: Settings TypeScript — Account CRUD

**Files to modify:**

- `frontend/src/settings.ts`

**What to do:** Implement:

- `loadAccountsList(provider)` — fetch + render account rows using DOM methods
- `setupAccountHandlers()` — event delegation on account list for Test / Credentials / Overrides / Edit / Delete buttons
- `openAddAccountModal(provider)` / `openEditAccountModal(account)`
- `handleAccountSave(e)` — validate + call createAccount or updateAccount
- `handleDeleteAccount(accountId)` — confirm + call deleteAccount + refresh list
- `handleTestCredentials(accountId)` — call testAccountCredentials + show inline result
- `openCredsModal(account)` + `handleCredsSave` — save credentials via saveAccountCredentials
- `toggleOverridesPanel(accountId)` — expand/collapse override sub-panel
- Override CRUD: load overrides, render table, handle edit/reset

**Verification:** `npm test` — add `frontend/src/__tests__/settings-accounts.test.ts`.

---

## Phase 5 — Frontend: Filters

Can begin once Task 4.1 and 4.2 are complete.

### Task 5.1: Filter HTML Additions

**Files to modify:**

- `frontend/src/index.html`

**What to do:** Add account filter `<select>` elements to the controls bars of each tab:

- `#dashboard-account-filter` — inside `#dashboard-controls .controls-bar` (existing controls bar at line ~36)
- `#recommendations-account-filter` — inside `#recommendations-controls .controls-bar` (existing)
- `#history-account-filter` — inside `#history-controls` (existing)
- `#plans-account-filter` — the plans tab has **no existing controls bar**; create `<section id="plans-controls">` with a `.controls-bar` before `#plans-header` (see `frontend.md` for the HTML)

Add the `populateAccountFilter` utility to `frontend/src/utils.ts`.

---

### Task 5.2: Wire Account Filters in Tab Modules

**Files to modify:**

- `frontend/src/dashboard.ts`
- `frontend/src/recommendations.ts`
- `frontend/src/history.ts`

**What to do:** In each module's setup function, add account filter population and change handler (see `frontend.md` JS changes section). Pass `state.getCurrentAccountIDs()` to API calls via the `accountIDs` filter field.

**Verification:** `npm test` — existing tests pass; add filter-passing assertion to each module's test.

---

### Task 5.3: Update API Client Functions to Pass `account_ids`

**Files to modify:**

- `frontend/src/api/recommendations.ts`
- `frontend/src/api/history.ts`
- `frontend/src/api/dashboard.ts`

**What to do:** Serialize `accountIDs` as `account_ids=uuid1,uuid2` query param when present.

**Verification:** `npm test`

---

## Phase 6 — Frontend: Plans

Can begin once Task 4.1, 4.2 are complete.

### Task 6.1: Plan Account Association UI

**Files to modify:**

- `frontend/src/plans.ts`
- `frontend/src/index.html` (plan modal additions + plans-controls section)

**What to do:**

- Add `<section id="plans-controls">` with `.controls-bar` before the plans list in `index.html` (see `frontend.md` Filter Bar section — Plans tab has no existing controls bar; create the entire section including `#plans-provider-filter` and `#plans-account-filter`)
- Add Target Accounts section to plan create/edit modal (see `frontend.md`)
- On plan modal open: call `getPlanAccounts(planId)` for existing plans
- Search + Add + Remove logic (DOM-based, no raw HTML injection)
- On plan save: call `setPlanAccounts(planId, accountIds)` after plan save succeeds
- Plan list: add Accounts column showing count or "All accounts"; use `element.title` for tooltip

**Verification:** `npm test`

---

## Phase 7 — Integration and Cleanup

### Task 7.1: End-to-End Smoke Test Script

**What to do:** Write a manual test runbook (or automated integration test) covering:

1. Create AWS account (role_arn)
2. Save credentials
3. Test credentials
4. Set service override
5. Create plan targeting account
6. Trigger recommendations refresh
7. Verify recommendations show account name
8. Check history filter by account

---

### Task 7.2: Known Issues Update

**Files to modify:**

- `known-issues.md` (if it exists at project root)

**What to do:** Add any discovered issues during implementation. Remove any pre-existing issues that this feature resolves.

---

### Task 7.3: Documentation

**Files to modify:**

- `docs/DEVELOPMENT.md` — add local-dev instructions: set `CREDENTIAL_ENCRYPTION_KEY` (64-char hex from `openssl rand -hex 32`); note that production uses `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN` instead
- `docs/DEPLOYMENT.md` — add migration step 000011; document all new env vars (`CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN`, `CUDLY_MAX_ACCOUNT_PARALLELISM`); reference `iac.md` for Terraform changes

---

### Task 7.4: IaC Changes

**Reference:** `specs/multi-account-execution/iac.md`

**Files to modify/create:**

- `terraform/modules/secrets/aws/variables.tf` — add `create_credential_encryption_key` (bool, default false) and `credential_encryption_key` (sensitive string) variables
- `terraform/modules/secrets/aws/main.tf` — add `aws_secretsmanager_secret.credential_encryption_key` + version resource (count-gated on `var.create_credential_encryption_key`)
- `terraform/modules/secrets/aws/outputs.tf` — add `credential_encryption_key_secret_arn` output
- `terraform/environments/aws/secrets.tf` — update existing `module "secrets"` invocation to add `create_credential_encryption_key = true` and `credential_encryption_key = var.credential_encryption_key`
- `terraform/environments/aws/variables.tf` — add `credential_encryption_key` (sensitive, no default) and `max_account_parallelism` (default: 10)
- `terraform/environments/aws/compute.tf` — add `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN` (set to `module.secrets.credential_encryption_key_secret_arn`) and `CUDLY_MAX_ACCOUNT_PARALLELISM` to `additional_env_vars`; pass `enable_cross_account_sts = true`, `enable_org_discovery = true`, `credential_encryption_key_secret_arn = module.secrets.credential_encryption_key_secret_arn` to the Lambda module
- `terraform/modules/compute/aws/lambda/main.tf` — add `credential_encryption_key_secret_arn` variable; update `secrets_access` policy to include it; add `cross_account_sts` IAM policy (conditioned on `enable_cross_account_sts`); add `org_discovery` IAM policy (conditioned on `enable_org_discovery`)
- `terraform/environments/aws/dev.tfvars.example` — document the two new variables (`credential_encryption_key`, `max_account_parallelism`)
- `terraform/environments/azure/secrets.tf` — add `azurerm_key_vault_secret.credential_encryption_key`
- `terraform/environments/gcp/` — add `credential-enc-key` to secrets module invocation

**Verification:** `terraform validate` + `terraform plan` in a dev environment shows only the new resources and IAM policy changes; `terraform apply` succeeds; Lambda can call `secretsmanager:GetSecretValue` on the new secret ARN.

---

## Dependency Graph

```
Task 1.1 (migration)
  └─► Task 1.2 (types)
        └─► Task 1.3 (interface)
              ├─► Task 1.4 (credentials pkg) ──► Task 3.1 (fanout) ──► Task 3.2 (wire)
              ├─► Task 1.5 (resolver)
              └─► Task 1.6 (store impl)
                    └─► Task 2.1 (handlers) ──► Task 2.2 (routes) ──► Task 2.3 (org discovery)
                          └─► Task 4.1 (FE api module)
                                ├─► Task 4.2 (FE state)
                                │     ├─► Task 4.3 (FE HTML)
                                │     │     └─► Task 4.4 (FE settings.ts)
                                │     ├─► Task 5.1 (filter HTML)
                                │     │     └─► Task 5.2 (filter JS)
                                │     │           └─► Task 5.3 (API params)
                                │     └─► Task 6.1 (plans UI)
                                └─► Task 7.x (integration/docs)

Task 7.4 (IaC) — independent of all code tasks; can be done in parallel with any phase
```
