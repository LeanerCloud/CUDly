<!-- markdownlint-disable MD040 MD060 -->
# Frontend Design

## Overview

The frontend changes fall into four areas:

1. **Settings Tab** — account CRUD UI per provider (AWS / Azure / GCP)
2. **Service Override UI** — per-account overrides for service defaults
3. **Filter bar additions** — account multi-select in Dashboard, Recommendations, History, Plans
4. **Plan account association** — account selector in plan create/edit modal

All changes are backward-compatible: if no accounts are configured, the UI behaves identically to today.

---

## State Model Changes

### `frontend/src/api/types.ts`

Add `CloudAccount` here — it is an API response type, consistent with all other API response shapes (`AzureCredentials`, `GCPCredentials`, `Plan`, `PurchaseHistory`, etc.) that live in `frontend/src/api/types.ts`.

```typescript
// New type — add to frontend/src/api/types.ts
export interface CloudAccount {
  id: string;
  name: string;
  description?: string;
  contact_email?: string;
  enabled: boolean;
  provider: Provider;
  external_id: string;

  // AWS-specific (may be absent for Azure/GCP)
  aws_auth_mode?: 'access_keys' | 'role_arn' | 'bastion';
  aws_role_arn?: string;
  aws_external_id?: string;
  aws_bastion_id?: string;
  aws_is_org_root?: boolean;
  bastion_account_name?: string;  // resolved display name

  // Azure-specific
  azure_subscription_id?: string;
  azure_tenant_id?: string;
  azure_client_id?: string;

  // GCP-specific
  gcp_project_id?: string;
  gcp_client_email?: string;

  credentials_configured: boolean;
  created_at: string;
  updated_at: string;
}
```

### `frontend/src/types.ts`

Only the `AppState` interface needs modification here (not `CloudAccount`):

```typescript
// Add currentAccountIDs to AppState
export interface AppState {
  currentUser: api.User | null;
  currentProvider: api.Provider | 'all';
  currentAccountIDs: string[];           // ← NEW: selected account UUIDs; [] = all accounts
  currentRecommendations: api.Recommendation[];
  selectedRecommendations: Set<number>;
  savingsChart: Chart | null;
}
```

### `frontend/src/state.ts`

```typescript
// Add getter/setter pair
export function getCurrentAccountIDs(): string[] {
  return state.currentAccountIDs;
}
export function setCurrentAccountIDs(ids: string[]): void {
  state.currentAccountIDs = ids;
}
```

---

## New Types: `frontend/src/api/types.ts` additions

Add these alongside `CloudAccount`:

```typescript
// Request/response types for accounts API — add to frontend/src/api/types.ts

export interface CreateAccountRequest {
  name: string;
  description?: string;
  contact_email?: string;
  enabled: boolean;
  provider: Provider;
  external_id: string;
  // AWS
  aws_auth_mode?: 'access_keys' | 'role_arn' | 'bastion';
  aws_role_arn?: string;
  aws_external_id?: string;
  aws_bastion_id?: string;
  aws_is_org_root?: boolean;
  // Azure
  azure_subscription_id?: string;
  azure_tenant_id?: string;
  azure_client_id?: string;
  // GCP
  gcp_project_id?: string;
  // Inline credentials (optional — inferred from auth_mode; no "type" field needed here)
  credentials?: {
    access_key_id?: string;        // aws_auth_mode=access_keys
    secret_access_key?: string;    // aws_auth_mode=access_keys
    client_secret?: string;        // azure
    service_account_json?: string; // gcp
  };
}

// provider and external_id are immutable after creation — server rejects changes to them.
// Omit them from UpdateAccountRequest to make the type honest and prevent accidental sends.
export type UpdateAccountRequest = Omit<CreateAccountRequest, 'credentials' | 'provider' | 'external_id'>;

export interface AccountCredentials {
  type: 'aws_access_keys' | 'azure_client_secret' | 'gcp_service_account';
  access_key_id?: string;
  secret_access_key?: string;
  client_secret?: string;
  service_account_json?: string;
}

export interface AccountServiceOverride {
  id: string;
  account_id: string;
  provider: string;
  service: string;
  enabled?: boolean;
  term?: number;
  payment?: string;
  coverage?: number;
  ramp_schedule?: string;
  include_regions?: string[];
  exclude_regions?: string[];
  include_engines?: string[];
  exclude_engines?: string[];
  include_types?: string[];
  exclude_types?: string[];
}

export type ServiceOverrideFields = Omit<AccountServiceOverride, 'id' | 'account_id' | 'provider' | 'service'>;

export interface OrgDiscoveryResult {
  discovered: number;
  created: number;
  skipped: number;
  accounts: Array<{ name: string; external_id: string; created: boolean }>;
}

export interface PlanAccountsResponse {
  account_ids: string[];
  accounts: Array<Pick<CloudAccount, 'id' | 'name' | 'provider' | 'external_id'>>;
}
```

---

## New API Module: `frontend/src/api/accounts.ts`

`listAccounts` unwraps the `{ "accounts": [...] }` API wrapper and returns the array directly
(same pattern as existing API functions).

```typescript
export async function listAccounts(provider?: Provider): Promise<CloudAccount[]>
  // calls GET /api/accounts?provider=... → unwraps response.accounts
export async function createAccount(data: CreateAccountRequest): Promise<CloudAccount>
export async function updateAccount(id: string, data: UpdateAccountRequest): Promise<CloudAccount>
export async function deleteAccount(id: string): Promise<void>
export async function saveAccountCredentials(id: string, creds: AccountCredentials): Promise<void>
export async function testAccountCredentials(id: string): Promise<{ ok: boolean; error?: string; caller_identity?: string }>
export async function listAccountServiceOverrides(id: string): Promise<AccountServiceOverride[]>
  // unwraps response.overrides
export async function saveAccountServiceOverride(id: string, provider: string, service: string, override: Partial<ServiceOverrideFields>): Promise<AccountServiceOverride>
export async function deleteAccountServiceOverride(id: string, provider: string, service: string): Promise<void>
export async function discoverOrgAccounts(orgRootAccountId: string): Promise<OrgDiscoveryResult>
export async function getPlanAccounts(planId: string): Promise<PlanAccountsResponse>
export async function setPlanAccounts(planId: string, accountIds: string[]): Promise<PlanAccountsResponse>
```

---

## Settings Tab: Account Management Section

### Placement in `index.html`

Inside each provider fieldset (`#aws-settings`, `#azure-settings`, `#gcp-settings`), add an **Accounts** sub-section **before** the existing "Service Defaults" heading:

```html
<!-- Inside #aws-settings fieldset, before <h4 class="subsection-title">Service Defaults</h4> -->
<h4 class="subsection-title">
  Accounts
  <button type="button" class="btn btn-small btn-primary" id="aws-add-account-btn">+ Add Account</button>
</h4>
<div class="account-list" id="aws-account-list">
  <!-- Rendered dynamically by settings.ts using safe DOM methods (textContent / appendChild) -->
  <p class="empty-state" id="aws-no-accounts">No AWS accounts configured.</p>
</div>
```

Repeat for `#azure-account-list` and `#gcp-account-list`.

### Account Row (rendered by JS via DOM methods — no raw HTML injection)

Each row is built with `document.createElement` / `textContent` to prevent XSS:

```
[Account Name]  [external_id]  [auth-mode badge]  [✓ Credentials set | ⚠ No credentials]
  [Test]  [Credentials]  [Overrides]  [Edit]  [Delete]
```

Button `data-account-id` attributes drive click handlers (no inline event attributes).

---

## Modals

### Notes on existing modal patterns

Existing modals (`#azure-creds-modal`, `#gcp-creds-modal`) use class `modal hidden` (NOT `modal-overlay hidden`).
Their close buttons use id pattern `close-X-modal-btn` with class `btn-secondary`.
New account modals must follow the same pattern to be consistent with `app.ts` event binding.

### AWS Account Modal (`#aws-account-modal`)

Dynamic form — shows different credential fields based on selected auth mode via CSS class toggling.

```
┌─ Add AWS Account ──────────────────────────────────────────┐
│ Name *                 [_________________________]          │
│ AWS Account ID *       [_________________________]          │
│ Description            [_________________________]          │
│ Contact Email          [_________________________]          │
│ Auth Mode *            [▼ Role ARN              ]          │
│ ─── Role ARN ─────────────────────────────────────────     │
│ Role ARN *             [arn:aws:iam::...         ]          │
│ External ID            [_________________________]          │
│ ─── (if Access Keys selected) ────────────────────         │
│ Access Key ID *        [_________________________]          │
│ Secret Access Key *    [_________________________]          │
│ ─── (if Bastion selected) ─────────────────────────        │
│ Bastion Account *      [▼ Select bastion account]          │
│ Role ARN in Target *   [arn:aws:iam::...         ]          │
│ External ID            [_________________________]          │
│                                                             │
│ [ ] This is an AWS Organizations root account              │
│     (enables member account discovery)                      │
│                                                             │
│              [Cancel]  [Save Account]                       │
└─────────────────────────────────────────────────────────────┘
```

Modal HTML structure (matching existing pattern — class `modal hidden`, close button id `close-X-modal-btn`):

```html
<div id="aws-account-modal" class="modal hidden">
  <div class="modal-content modal-wide">
    <h2 id="aws-account-modal-title">Add AWS Account</h2>
    <div id="aws-account-error" class="error hidden"></div>
    <form id="aws-account-form"><!-- fields --></form>
    <button type="button" class="btn-secondary" id="close-aws-account-modal-btn">Cancel</button>
  </div>
</div>
```

Auth mode sections use `hidden` CSS class toggled by JS (same pattern as provider settings sections in `settings.ts`).

### Azure Subscription Modal (`#azure-account-modal`)

```
┌─ Add Azure Subscription ───────────────────────────────────┐
│ Name *                 [_________________________]          │
│ Subscription ID *      [xxxxxxxx-xxxx-xxxx-xxxx-]          │
│ Tenant ID *            [yyyyyyyy-xxxx-xxxx-xxxx-]          │
│ Client ID *            [zzzzzzzz-xxxx-xxxx-xxxx-]          │
│ Client Secret *        [_________________________]          │
│ Description            [_________________________]          │
│ Contact Email          [_________________________]          │
│              [Cancel]  [Save Subscription]                  │
└─────────────────────────────────────────────────────────────┘
```

HTML: `<div id="azure-account-modal" class="modal hidden">` / close btn `id="close-azure-account-modal-btn"`

### GCP Project Modal (`#gcp-account-modal`)

```
┌─ Add GCP Project ──────────────────────────────────────────┐
│ Name *                 [_________________________]          │
│ GCP Project ID *       [my-project-123           ]          │
│ Description            [_________________________]          │
│ Contact Email          [_________________________]          │
│ Service Account JSON * [                         ]          │
│                        [   paste JSON here...   ]          │
│                        [                         ]          │
│              [Cancel]  [Save Project]                       │
└─────────────────────────────────────────────────────────────┘
```

HTML: `<div id="gcp-account-modal" class="modal hidden">` / close btn `id="close-gcp-account-modal-btn"`

### Credentials-Only Modal (`#account-creds-modal`)

Used when "Credentials" button is clicked on an existing account:

```
┌─ Update Credentials: {account name} ───────────────────────┐
│  [provider-specific credential fields only]                 │
│              [Cancel]  [Save Credentials]                   │
└─────────────────────────────────────────────────────────────┘
```

HTML: `<div id="account-creds-modal" class="modal hidden">` / close btn `id="close-account-creds-modal-btn"`

---

## Service Overrides UI

Clicking "Overrides" on an account row expands an inline override panel below that row:

```
  ▼ Service Overrides for Prod-US
  ┌────────────────────┬──────────────────┬──────────────────────┐
  │ Service            │ Term             │ Coverage             │
  ├────────────────────┼──────────────────┼──────────────────────┤
  │ EC2                │ 1yr (override)   │ 60% (override) [Edit]│
  │ RDS                │ 3yr (global)     │ 80% (global)   [Edit]│
  │ ElastiCache        │ 3yr (global)     │ 80% (global)   [Edit]│
  └────────────────────┴──────────────────┴──────────────────────┘
  [+ Add Override]
```

"Edit" opens a compact inline form for that service row. "Reset to global" (shown for overridden rows) calls `DELETE /api/accounts/:id/service-overrides/:provider/:service`.

---

## Filter Bar Changes

### HTML additions (per tab)

**Existing controls bars** (confirmed in HTML):

- `#dashboard-controls` → has `#dashboard-provider-filter`
- `#recommendations-controls` → has `#recommendations-provider-filter`, `#service-filter`
- `#history-controls` → has `#history-provider-filter`, `#history-start`, `#history-end`
- Plans tab → **no existing controls bar**; a new `<section id="plans-controls">` must be added

Add after the existing provider filter `<select>` in Dashboard, Recommendations, and History:

```html
<label>Account:
  <select id="dashboard-account-filter" multiple size="1">
    <option value="">All Accounts</option>
    <!-- dynamically populated via DOM methods -->
  </select>
</label>
```

For Plans, add a new controls section before the plans list:

```html
<section id="plans-controls">
  <div class="controls-bar">
    <div class="filter-group">
      <label>Provider:
        <select id="plans-provider-filter">
          <option value="">All Providers</option>
          <option value="aws">AWS</option>
          <option value="azure">Azure</option>
          <option value="gcp">GCP</option>
        </select>
      </label>
      <label>Account:
        <select id="plans-account-filter" multiple size="1">
          <option value="">All Accounts</option>
        </select>
      </label>
    </div>
  </div>
</section>
```

The account select is populated dynamically when:

- The tab loads (fetch all accounts)
- The provider filter changes (re-fetch filtered accounts for that provider)

An empty selection means "all accounts" — no `account_ids` param is sent to the API.

Element IDs:

- `#dashboard-account-filter`
- `#recommendations-account-filter`
- `#history-account-filter`
- `#plans-account-filter`

### JS changes (shared utility in `utils.ts`)

```typescript
// populateAccountFilter: fetches accounts and rebuilds <select> options using DOM methods.
// Preserves existing selection state.
export async function populateAccountFilter(
  select: HTMLSelectElement,
  provider: string
): Promise<void> {
  const accounts = await api.listAccounts(provider !== 'all' ? provider as api.Provider : undefined);
  const existing = state.getCurrentAccountIDs();

  // Clear and rebuild using DOM methods (not innerHTML) to avoid XSS
  while (select.options.length > 1) select.remove(1); // keep "All Accounts"

  // Only show enabled accounts in filter bars — disabled accounts don't contribute data
  for (const acct of accounts.filter(a => a.enabled)) {
    const opt = document.createElement('option');
    opt.value = acct.id;
    opt.textContent = `${acct.name} (${acct.external_id})`;
    if (existing.includes(acct.id)) opt.selected = true;
    select.appendChild(opt);
  }
}
```

In each tab module, after provider filter setup:

```typescript
const accountFilter = document.getElementById('dashboard-account-filter') as HTMLSelectElement | null;
if (accountFilter) {
  void populateAccountFilter(accountFilter, state.getCurrentProvider());
  accountFilter.addEventListener('change', () => {
    const selected = Array.from(accountFilter.selectedOptions)
      .map(o => o.value)
      .filter(v => v !== '');
    state.setCurrentAccountIDs(selected);
    void loadDashboard();
  });
}
```

### API filter type additions (`frontend/src/api/types.ts`)

```typescript
export interface RecommendationFilters {
  provider?: Provider | 'all';
  service?: string;
  region?: string;
  minSavings?: number;
  accountIDs?: string[];    // ← NEW
}

export interface HistoryFilters {
  start?: string;
  end?: string;
  provider?: Provider;
  planId?: string;
  accountIDs?: string[];    // ← NEW
}

export interface SavingsAnalyticsFilters {
  start?: string;
  end?: string;
  interval?: 'hourly' | 'daily' | 'weekly' | 'monthly';  // preserve existing literal union
  provider?: Provider;                                    // preserve existing Provider type
  service?: string;
  accountIDs?: string[];    // ← NEW
}
```

Serialization in API client functions:

```typescript
if (filters.accountIDs?.length) params.set('account_ids', filters.accountIDs.join(','));
```

---

## Plan Account Association

### Plan Create/Edit Modal additions

After the existing provider/service fields, add a new **Target Accounts** section:

```
Target Accounts
  Search: [____________________] [Add]
  ┌─────────────────────────────────────────────┐
  │ Prod-US (123456789012) [AWS]       [Remove] │
  │ Staging (234567890123) [AWS]       [Remove] │
  └─────────────────────────────────────────────┘
  Tip: Leave empty to target all accounts for the selected provider.
```

Implementation in `plans.ts`:

1. On plan modal open: call `getPlanAccounts(planId)` and populate the list (empty for create).
2. Search input calls `listAccounts(provider)` and filters client-side by name/ID substring.
3. "Add" appends to in-memory list (deduplicated by ID).
4. "Remove" deletes from in-memory list.
5. On plan save: call `setPlanAccounts(planId, accountIds)` after the plan PUT/POST succeeds.

### Plan List Display

Add an "Accounts" column to the plans table:

```
| Name      | Provider | Service | Accounts             | Status  |
|-----------|----------|---------|----------------------|---------|
| Prod Plan | AWS      | EC2     | 2 accounts           | Enabled |
| All-AWS   | AWS      | RDS     | All AWS accounts     | Enabled |
```

"N accounts" text has a `title` attribute (tooltip) listing the account names — set via `element.title = names.join(', ')` (no HTML injection).

---

## Files to Modify

| File | Change |
|------|--------|
| `frontend/src/types.ts` | Add `AppState.currentAccountIDs` only |
| `frontend/src/api/types.ts` | Add `CloudAccount` type; add `accountIDs` to filter types |
| `frontend/src/api/index.ts` | Re-export from new `accounts.ts` module |
| `frontend/src/state.ts` | Add `currentAccountIDs` state + accessors |
| `frontend/src/index.html` | Account list sections in settings, account filter selects in tab controls, all account modals |
| `frontend/src/settings.ts` | Account CRUD handlers, service override handlers, org discovery |
| `frontend/src/dashboard.ts` | Account filter setup + pass accountIDs to API |
| `frontend/src/recommendations.ts` | Account filter setup + pass accountIDs to API |
| `frontend/src/history.ts` | Account filter setup + pass accountIDs to API |
| `frontend/src/plans.ts` | Account association UI in plan modal |
| `frontend/src/utils.ts` | Add `populateAccountFilter` shared utility |

## New Files

| File | Purpose |
|------|---------|
| `frontend/src/api/accounts.ts` | All cloud account API calls |
| `frontend/src/__tests__/api-accounts.test.ts` | Unit tests for accounts API functions (mocked fetch) |
| `frontend/src/__tests__/settings-accounts.test.ts` | Unit tests for settings account CRUD handlers |

## CSS Notes

**Classes that need to be added** (confirmed absent in existing stylesheets):

- `.account-list` — container for the account rows in settings; style similarly to `.service-defaults-grid`
- `.account-row` — individual account row; use flex layout consistent with `.setting-row`
- `.account-row-info` / `.account-row-actions` — left/right sections within a row
- `.account-auth-badge` — small badge for auth mode; style similarly to `.provider-badge` but neutral colour

**Classes that already exist and should be reused** (confirmed in CSS):

- `.modal hidden` — existing modal pattern (note: class is `modal`, toggle is `hidden`)
- `.modal-content.modal-wide` — wide modal variant, used for Azure/GCP creds modals
- `.provider-badge` (with `.aws`, `.azure`, `.gcp` variants)
- `.credential-status` (with `.configured` variant)
- `.btn`, `.btn-small`, `.btn-primary`, `.btn-secondary`, `.btn-danger`
- `.controls-bar`, `.filter-group` — existing filter bar layout
- `.hidden` — utility class for show/hide toggles
- `.error` — error message display

## Rendering Pattern Note

Existing frontend code (e.g., `history.ts`, `plans.ts`) renders table rows via template literals in `container.innerHTML` with `escapeHtml()` for all user-supplied values. New account-rendering code in `settings.ts` should use DOM methods (`createElement`, `textContent`, `appendChild`) for account rows that mix static structure with user-controlled data (account names, external IDs). This is a stricter approach than the existing pattern but appropriate for settings where account metadata is displayed.
