<!-- markdownlint-disable MD040 MD060 -->
# Backend Architecture

## Package Structure

```
internal/
  credentials/         ← NEW: credential encryption + resolution
    cipher.go          ← AES-256-GCM encrypt/decrypt primitives
    store.go           ← save/load encrypted blobs from account_credentials
    resolver.go        ← decrypt + return provider-specific credential structs
    cipher_test.go
    resolver_test.go

  accounts/            ← NEW: account-level business logic
    org_discovery.go   ← AWS Organizations member-account discovery
    org_discovery_test.go

  execution/           ← NEW: parallel multi-account fan-out engine
    executor.go        ← Executor interface + per-account executor
    fanout.go          ← FanOut: concurrently runs a function across N accounts
    collector.go       ← aggregates tagged per-account results
    fanout_test.go

  config/
    types.go           ← ADD: CloudAccount, AccountServiceOverride, CloudAccountFilter
    interfaces.go      ← ADD: new store method signatures
    store_postgres.go  ← ADD: implementations for new StoreInterface methods
                          NOTE: does NOT implement credentials.CredentialStore —
                          that would create a circular import (credentials→config→credentials)
    resolver.go        ← ADD: ResolveServiceConfig (cascade logic)

  api/
    handler_accounts.go    ← NEW: HTTP handlers for /api/accounts/* routes
    handler_accounts_test.go
    handler.go             ← MODIFY: add credStore credentials.CredentialStore to Handler struct
    types.go               ← MODIFY: add CredentialStore to HandlerConfig
    router.go              ← MODIFY: register new routes (existing table-driven router)
    handler_recommendations.go ← MODIFY: add account_ids filter to recommendations handler
    handler_dashboard.go   ← MODIFY: add account_ids filter
    handler_history.go     ← MODIFY: add account_ids filter to history handler
    handler_analytics.go   ← MODIFY: add account_ids filter to analytics handlers
    -- NOTE: internal/server/handler.go is for SCHEDULED TASK dispatch only,
    --       not HTTP routing. HTTP routing lives entirely in internal/api/.

  server/
    app.go                 ← MODIFY: add CredentialStore field to Application, initialise from
                             credentials.NewCredentialStore(pool, encKey) in DB connect path
```

---

## Credential Management (`internal/credentials/`)

### `cipher.go`

```go
package credentials

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns "<base64(nonce)>.<base64(ciphertext)>".
func Encrypt(key []byte, plaintext []byte) (string, error)

// Decrypt reverses Encrypt.
func Decrypt(key []byte, blob string) ([]byte, error)

// KeyFromEnv loads the 32-byte AES encryption key using the following priority order:
//   1. CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN set → call secretsmanager.GetSecretValue(arn),
//      treat returned string as 64-char hex key; cache result for Lambda lifetime.
//   2. CREDENTIAL_ENCRYPTION_KEY set → decode as 64-char hex key directly (local dev / non-AWS).
//   3. Neither set → use hardcoded dev-only key; logs WARN "using insecure dev credential key".
func KeyFromEnv() ([]byte, error)
```

Key format: `CREDENTIAL_ENCRYPTION_KEY` env var must be a 64-character hex string (32 bytes). Generate with: `openssl rand -hex 32`.

### `store.go`

**Import note**: `internal/credentials` imports `internal/config` (for `CloudAccount`). It must NOT be imported by `internal/config` — that would create a circular dependency. The concrete `CredentialStore` implementation lives here in `internal/credentials/store.go` and takes a `*pgxpool.Pool` directly (same mechanism `store_postgres.go` uses), keeping the packages independent.

```go
type CredentialStore interface {
    // SaveCredential encrypts and stores credential material.
    SaveCredential(ctx context.Context, accountID string, credType string, payload []byte) error

    // DeleteCredential removes stored credential material.
    DeleteCredential(ctx context.Context, accountID string, credType string) error

    // HasCredential returns true if credential material exists for the account+type.
    HasCredential(ctx context.Context, accountID string, credType string) (bool, error)

    // LoadRaw decrypts and returns raw credential bytes.
    // Only called by resolver.go — not for application use.
    // Must be exported (uppercase) since the concrete struct is in the same package
    // (pgCredentialStore) but callers across packages need the interface.
    LoadRaw(ctx context.Context, accountID string, credType string) ([]byte, error)
}

// NewCredentialStore creates a CredentialStore backed by the account_credentials table.
// Takes a *pgxpool.Pool directly — does not go through StoreInterface.
func NewCredentialStore(pool *pgxpool.Pool, encKey []byte) CredentialStore
```

### `resolver.go`

```go
// AWSCredentials holds resolved AWS access credentials.
// Note: for role-based auth modes (role_arn, bastion) this struct is not used;
// callers use ResolveAWSCredentialProvider which returns an aws.CredentialsProvider.
type AWSCredentials struct {
    AccessKeyID     string
    SecretAccessKey string
}

func (c *AWSCredentials) String() string { return "[REDACTED]" }

// AzureCredentials holds resolved Azure service principal credentials.
type AzureCredentials struct {
    ClientSecret string
}

func (c *AzureCredentials) String() string { return "[REDACTED]" }

// STSClient is the minimal STS interface needed for role assumption.
// Satisfied by *sts.Client from github.com/aws/aws-sdk-go-v2/service/sts.
type STSClient interface {
    AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// ResolveAWSCredentialProvider returns an aws.CredentialsProvider for the given account.
//
// Logic per auth mode:
//   access_keys: decrypt blob → static credentials provider
//   role_arn:    use ambient credentials (instance role/env) → STS AssumeRole
//   bastion:     resolve bastion account creds first → STS AssumeRole into target account
//
// The returned provider is safe to use for the duration of one API call.
// Callers should NOT cache the returned provider across requests.
func ResolveAWSCredentialProvider(
    ctx context.Context,
    account *config.CloudAccount,
    store CredentialStore,
    stsClient STSClient,
) (aws.CredentialsProvider, error)

// ResolveAzureCredentials decrypts and returns the Azure client secret.
func ResolveAzureCredentials(ctx context.Context, account *config.CloudAccount, store CredentialStore) (*AzureCredentials, error)

// ResolveGCPCredentials decrypts and returns the GCP service account JSON.
func ResolveGCPCredentials(ctx context.Context, account *config.CloudAccount, store CredentialStore) ([]byte, error)
```

**Bastion chain resolution (AWS)**:

1. Load the hub account (`cloud_accounts.aws_bastion_id`).
2. Resolve hub account's credentials (either access_keys or role_arn — bastion accounts cannot themselves be bastions).
3. Use hub credentials to call `sts:AssumeRole` with `aws_role_arn` of the target account and optional `aws_external_id`.
4. Return a `aws.CredentialsProvider` wrapping the assumed-role session token.

---

## Org Discovery (`internal/accounts/org_discovery.go`)

```go
// DiscoverOrgAccounts calls AWS Organizations ListAccounts using the management
// account's credentials and returns a slice of CloudAccount records (not yet persisted).
// Caller is responsible for deduplication and saving.
func DiscoverOrgAccounts(
    ctx context.Context,
    mgmtAccount *config.CloudAccount,
    credStore credentials.CredentialStore,
) ([]config.CloudAccount, error)
```

Implementation:

1. Resolve credentials for `mgmtAccount` using the credentials package.
2. Create an `organizations.Client` with those credentials.
3. Call `organizations.ListAccounts` (paginated).
4. Map each member account to a `CloudAccount` struct with:
   - `Provider = "aws"`
   - `ExternalID = member.Id`
   - `Name = *member.Name`
   - `Enabled = false` (operator must review and enable)
   - `AWSAuthMode = "bastion"` (default: assume they'll use the org root as bastion)
   - `AWSBastionID = mgmtAccount.ID`
5. Return slice — caller saves to DB with `CreateCloudAccount`, skipping duplicates by `(provider, external_id)`.

---

## Parallel Execution Engine (`internal/execution/`)

### `executor.go`

```go
// AccountContext bundles an account with its resolved credentials for one execution.
type AccountContext struct {
    Account     config.CloudAccount
    AWSProvider aws.CredentialsProvider   // non-nil for AWS accounts
    AzureCreds  *credentials.AzureCredentials // non-nil for Azure
    GCPKeyJSON  []byte                    // non-nil for GCP
}

// PrepareAccountContext resolves credentials and returns an AccountContext.
// This is the only function that calls the credentials package.
func PrepareAccountContext(ctx context.Context, account config.CloudAccount, credStore credentials.CredentialStore) (*AccountContext, error)
```

### `fanout.go`

```go
// FanOutResult wraps the result of one account's execution with its account context.
type FanOutResult[T any] struct {
    Account config.CloudAccount
    Result  T
    Err     error
}

// FanOut runs fn concurrently for each account with a configurable parallelism limit
// (default: 10). Results are collected in a slice ordered by account ID (deterministic
// for testing — sort by account ID after all goroutines complete).
//
// Implementation note: do NOT use errgroup.WithContext — that cancels the shared context
// on the first error, which would abort in-flight goroutines for other accounts.
// Instead use sync.WaitGroup + a buffered semaphore channel for the parallelism cap,
// and capture each account's error in its own FanOutResult.Err.
//
// A failure in one account's fn does NOT affect others.
// Each error is captured in FanOutResult.Err.
func FanOut[T any](
    ctx context.Context,
    accounts []config.CloudAccount,
    maxParallel int,
    fn func(context.Context, AccountContext) (T, error),
    credStore credentials.CredentialStore,
) []FanOutResult[T]
```

**Parallelism limit**: Controlled by `CUDLY_MAX_ACCOUNT_PARALLELISM` env var, default `10`. This prevents overwhelming AWS API rate limits when running across many accounts simultaneously.

### `collector.go`

```go
// AggregatedRecommendations merges per-account recommendation slices into one slice,
// adding Account metadata to each item.
func AggregatedRecommendations(results []FanOutResult[[]config.RecommendationRecord]) []config.RecommendationRecord

// AggregatedPurchaseHistory merges per-account history slices.
func AggregatedPurchaseHistory(results []FanOutResult[[]config.PurchaseHistoryRecord]) []config.PurchaseHistoryRecord
```

---

## Service Config Cascade (`internal/config/resolver.go`)

```go
// ResolveServiceConfig merges an account-level sparse override on top of the
// global service config. Any nil pointer in override inherits from global.
// Returns a fully-populated ServiceConfig (no nil fields).
func ResolveServiceConfig(global *ServiceConfig, override *AccountServiceOverride) *ServiceConfig {
    if override == nil {
        return global
    }
    result := *global // copy
    if override.Enabled      != nil { result.Enabled      = *override.Enabled }
    if override.Term         != nil { result.Term         = *override.Term }
    if override.Payment      != nil { result.Payment      = *override.Payment }
    if override.Coverage     != nil { result.Coverage     = *override.Coverage }
    if override.RampSchedule != nil { result.RampSchedule = *override.RampSchedule }
    if override.IncludeEngines != nil { result.IncludeEngines = override.IncludeEngines }
    if override.ExcludeEngines != nil { result.ExcludeEngines = override.ExcludeEngines }
    if override.IncludeRegions != nil { result.IncludeRegions = override.IncludeRegions }
    if override.ExcludeRegions != nil { result.ExcludeRegions = override.ExcludeRegions }
    if override.IncludeTypes   != nil { result.IncludeTypes   = override.IncludeTypes }
    if override.ExcludeTypes   != nil { result.ExcludeTypes   = override.ExcludeTypes }
    return &result
}
```

---

## Store Interface Additions (`internal/config/interfaces.go`)

```go
// Cloud Account management
CreateCloudAccount(ctx context.Context, account *CloudAccount) error
GetCloudAccount(ctx context.Context, id string) (*CloudAccount, error)
UpdateCloudAccount(ctx context.Context, account *CloudAccount) error
DeleteCloudAccount(ctx context.Context, id string) error
ListCloudAccounts(ctx context.Context, filter CloudAccountFilter) ([]CloudAccount, error)

// Account service overrides
GetAccountServiceOverride(ctx context.Context, accountID, provider, service string) (*AccountServiceOverride, error)
SaveAccountServiceOverride(ctx context.Context, override *AccountServiceOverride) error
DeleteAccountServiceOverride(ctx context.Context, accountID, provider, service string) error
ListAccountServiceOverrides(ctx context.Context, accountID string) ([]AccountServiceOverride, error)

// Plan ↔ Account association
SetPlanAccounts(ctx context.Context, planID string, accountIDs []string) error
GetPlanAccounts(ctx context.Context, planID string) ([]CloudAccount, error)
```

---

## Purchase Plan Execution Changes

Current flow is triggered via `app.Purchase.ProcessScheduledPurchases(ctx)` (called from
`internal/server/handler.go:handleProcessScheduledPurchases`). The implementation lives in:

- `internal/purchase/manager.go` — `ProcessScheduledPurchases` orchestrates the flow
- `internal/purchase/execution.go` — per-plan execution logic

New flow — changes required in `internal/purchase/execution.go`:

```
ProcessScheduledPurchases(ctx):
  for each plan:
    1. Derive plan's provider: iterate plan.Services map keys (format "provider:service",
       e.g. "aws:ec2") and extract the provider. In practice the frontend always sends a
       single provider, so take the first distinct provider found.
    2. Load plan accounts from plan_accounts (if empty: load all enabled accounts
       matching that derived provider)
    3. For each account: PrepareAccountContext(ctx, account, credStore)
    4. FanOut(ctx, accountContexts, fn=executeForAccount)
    5. For each FanOutResult:
         a. Create purchase_execution row tagged with cloud_account_id
         b. If Err != nil: mark execution failed, log, continue
         c. If ok: execute purchases against account's cloud API
    6. Aggregate and return summary
```

**Note on `PurchasePlan.Provider`**: The `PurchasePlan` struct (`internal/config/types.go`) has no direct `provider` field. Provider is embedded in the `Services` map keys (e.g. `"aws:ec2"`). Do not add a `provider` column to `purchase_plans` as part of this feature; derive it from the services map at runtime.

The `purchase_executions` table now has a `cloud_account_id` column so each execution row is linked to the specific account it ran against.

---

## Modified Handler: `account_ids` Filtering

The existing handlers in `internal/api/` use a Lambda Function URL request model, not
`*http.Request`. Query parameter extraction uses the Lambda event:

```go
// parseAccountIDs extracts and validates the account_ids query param from a Lambda request.
// Returns nil slice (= all accounts) if param is absent or empty.
// Add this helper to internal/api/handler.go or a shared helpers file.
func parseAccountIDs(req *events.LambdaFunctionURLRequest) ([]string, error) {
    raw := req.QueryStringParameters["account_ids"]
    if raw == "" {
        return nil, nil
    }
    parts := strings.Split(raw, ",")
    ids := make([]string, 0, len(parts))
    for _, part := range parts {
        id := strings.TrimSpace(part)
        if _, err := uuid.Parse(id); err != nil {
            return nil, fmt.Errorf("invalid account_id %q: %w", id, err)
        }
        ids = append(ids, id)
    }
    return ids, nil
}
```

History queries that currently filter by `account_id VARCHAR(20)` (raw AWS account ID) will also support filtering by `cloud_account_id UUID` for multi-account scenarios. The existing single-account behavior is preserved when `account_ids` is absent.
