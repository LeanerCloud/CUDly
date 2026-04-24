/**
 * Settings module for CUDly
 */

import * as api from './api';
import { fetchAndPopulateCommitmentOptions } from './commitmentOptions';
import { initFederationPanel } from './federation';
import { confirmDialog } from './confirmDialog';
import { reflectDirtyState } from './settings-subnav';
import { showToast } from './toast';

type AccountProvider = 'aws' | 'azure' | 'gcp';

let cachedSourceCloud: string | undefined;

/** Options for the account modal (used by the registrations approval flow). */
export interface AccountModalOptions {
  onSave?: (provider: AccountProvider, request: api.CloudAccountRequest) => Promise<void>;
}

// Track which provider's account is being edited (for the modal)
let accountModalProvider: AccountProvider = 'aws';
// Optional callback for the registrations approval flow.
let accountModalOnSave: AccountModalOptions['onSave'] | undefined;

// Per-service field definitions — used for loading and saving service configs.
const SERVICE_FIELDS = [
  { provider: 'aws', service: 'ec2',          termId: 'aws-ec2-term',          paymentId: 'aws-ec2-payment' },
  { provider: 'aws', service: 'rds',          termId: 'aws-rds-term',          paymentId: 'aws-rds-payment' },
  { provider: 'aws', service: 'elasticache',  termId: 'aws-elasticache-term',  paymentId: 'aws-elasticache-payment' },
  { provider: 'aws', service: 'opensearch',   termId: 'aws-opensearch-term',   paymentId: 'aws-opensearch-payment' },
  { provider: 'aws', service: 'redshift',     termId: 'aws-redshift-term',     paymentId: 'aws-redshift-payment' },
  { provider: 'aws', service: 'savingsplans', termId: 'aws-savingsplans-term', paymentId: 'aws-savingsplans-payment' },
  { provider: 'azure', service: 'vm',         termId: 'azure-vm-term',         paymentId: 'azure-vm-payment' },
  { provider: 'azure', service: 'sql',        termId: 'azure-sql-term',        paymentId: 'azure-sql-payment' },
  { provider: 'azure', service: 'cosmosdb',   termId: 'azure-cosmosdb-term',   paymentId: 'azure-cosmosdb-payment' },
  { provider: 'azure', service: 'redis',      termId: 'azure-redis-term',      paymentId: 'azure-redis-payment' },
  { provider: 'azure', service: 'search',     termId: 'azure-search-term',     paymentId: 'azure-search-payment' },
  { provider: 'gcp',   service: 'compute',    termId: 'gcp-compute-term',      paymentId: null },
  { provider: 'gcp',   service: 'sql',        termId: 'gcp-sql-term',          paymentId: null },
  { provider: 'gcp',   service: 'memorystore',termId: 'gcp-memorystore-term',  paymentId: null },
  { provider: 'gcp',   service: 'storage',    termId: 'gcp-storage-term',      paymentId: null },
] as const;

// Fields that are persisted to the backend via saveGlobalSettings.
// Used for dirty tracking and unsaved-changes detection.
const TRACKED_FIELDS = [
  'provider-aws', 'provider-azure', 'provider-gcp',
  'setting-notification-email', 'setting-auto-collect',
  'setting-collection-schedule', 'setting-notification-days',
  'setting-default-term', 'setting-default-payment', 'setting-default-coverage',
  // Per-provider grace-period inputs
  'setting-grace-aws', 'setting-grace-azure', 'setting-grace-gcp',
  // Per-service fields
  ...SERVICE_FIELDS.map(f => f.termId),
  ...SERVICE_FIELDS.filter(f => f.paymentId !== null).map(f => f.paymentId as string),
];

// Snapshot of field values at last save (or initial load).
let savedSnapshot: Record<string, string> = {};

// Cached service configs from last load — used as base when saving to preserve
// non-UI fields (ramp_schedule, include_engines, etc.) that SaveServiceConfig replaces entirely.
let loadedServiceConfigs: api.ServiceConfig[] = [];

// saveInFlight guards saveGlobalSettings against concurrent invocations
// (rapid Save clicks, Enter-in-form, etc.). Toggled in a try/finally and
// mirrored on the Save button's disabled attribute so the UI reflects the
// in-progress state.
let saveInFlight = false;

/**
 * byId is the null-safe replacement for `document.getElementById(...) as T`.
 * Returning `T | null` forces callers to guard with optional chaining (`?.value`,
 * `?.checked`) rather than dereferencing blindly, so a missing element in the
 * DOM produces a silent no-op instead of a TypeError.
 *
 * Pattern to migrate from:
 *   (document.getElementById('foo') as HTMLInputElement).value = v;
 * to:
 *   const el = byId<HTMLInputElement>('foo'); if (el) el.value = v;
 * or:
 *   byId<HTMLInputElement>('foo')?.value ?? '';  // read site
 */
// Grace-period input: 0 is a valid user choice (disables the feature
// for that provider), so we use empty-string to signal "fall back to
// default (7)". An explicit 0 must round-trip as 0, not get swallowed.
function populateGraceInput(id: string, value: number | undefined): void {
  const el = document.getElementById(id) as HTMLInputElement | null;
  if (!el) return;
  el.value = String(value ?? 7);
}

// readGraceInput parses the grace-period input, returning the numeric
// value or an error message (out of range, not a number). Empty input
// defaults to 7.
function readGraceInput(id: string): { value: number; err?: undefined } | { value: 0; err: string } {
  const el = document.getElementById(id) as HTMLInputElement | null;
  if (!el) return { value: 7 };
  const raw = el.value.trim();
  if (raw === '') return { value: 7 };
  const n = Number(raw);
  if (!Number.isFinite(n) || !Number.isInteger(n)) {
    return { value: 0, err: 'enter a whole number of days' };
  }
  if (n < 0 || n > 30) {
    return { value: 0, err: 'must be between 0 and 30 days' };
  }
  return { value: n };
}

function byId<T extends HTMLElement>(id: string): T | null {
  return document.getElementById(id) as T | null;
}

/**
 * setInputValue writes a value to an <input>/<textarea> looked up by id, no-op
 * if the element is missing. Consolidates the repetitive
 * `byId<HTMLInputElement>('x'); if (el) el.value = v;` pattern used across
 * modal-populate helpers.
 */
function setInputValue(id: string, value: string): void {
  const el = byId<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(id);
  if (el) el.value = value;
}

/**
 * setInputChecked writes `checked` to a checkbox input, no-op if the element
 * is missing. Same motivation as setInputValue.
 */
function setInputChecked(id: string, checked: boolean): void {
  const el = byId<HTMLInputElement>(id);
  if (el) el.checked = checked;
}

function getFieldValue(id: string): string {
  const el = document.getElementById(id);
  if (!el) return '';
  if (el instanceof HTMLInputElement) {
    return el.type === 'checkbox' ? String(el.checked) : el.value;
  }
  if (el instanceof HTMLSelectElement || el instanceof HTMLTextAreaElement) return el.value;
  return '';
}

function snapshotAllFields(): void {
  TRACKED_FIELDS.forEach(id => { savedSnapshot[id] = getFieldValue(id); });
}

function updateDirtyMarkers(): void {
  let anyDirty = false;
  TRACKED_FIELDS.forEach(id => {
    const el = document.getElementById(id);
    if (!el) return;
    const dirty = getFieldValue(id) !== savedSnapshot[id];
    if (dirty) anyDirty = true;
    if (el instanceof HTMLInputElement && el.type === 'checkbox') {
      // Highlight the containing setting-input div for checkboxes
      el.closest<HTMLElement>('.setting-input')?.classList.toggle('dirty', dirty);
    } else {
      el.classList.toggle('dirty', dirty);
    }
  });
  // Surface aggregate dirty state to the Settings top-tab (badge dot) and
  // the sticky save bar (toggle "Unsaved changes" affordance).
  reflectDirtyState(anyDirty);
}

/** Returns true if any tracked field has been changed since the last save/load. */
export function isUnsavedChanges(): boolean {
  return TRACKED_FIELDS.some(id => getFieldValue(id) !== savedSnapshot[id]);
}

function setupDirtyTracking(signal?: AbortSignal): void {
  TRACKED_FIELDS.forEach(id => {
    const el = document.getElementById(id);
    if (!el) return;
    el.addEventListener('change', () => updateDirtyMarkers(), { signal });
    // Also listen to input for text fields so highlighting is immediate
    if (el instanceof HTMLInputElement && el.type !== 'checkbox') {
      el.addEventListener('input', () => updateDirtyMarkers(), { signal });
    }
  });
}

/**
 * Load and render accounts list for a provider
 */
export async function loadAccountsForProvider(provider: AccountProvider): Promise<void> {
  const container = document.getElementById(`${provider}-accounts-list`);
  if (!container) return;
  try {
    const accounts = await api.listAccounts({ provider });
    renderSelfAccountBanner(container, accounts, provider);
    renderAccountsList(container, accounts, provider);
  } catch {
    container.textContent = 'Failed to load accounts.';
  }
}

/**
 * Show a banner to add CUDly's own host account if not already present.
 * Only shown for the provider matching CUDLY_SOURCE_CLOUD.
 */
async function renderSelfAccountBanner(container: HTMLElement, accounts: api.CloudAccount[], provider: AccountProvider): Promise<void> {
  // Remove any existing banner first (prevents duplicates on re-render)
  container.querySelector('.self-account-banner')?.remove();

  const hasSelf = accounts.some(a => a.is_self);
  if (hasSelf) return;

  // Check if this provider matches the source cloud
  try {
    const cfg = await api.getConfig();
    if (!cfg.source_identity || cfg.source_identity.provider !== provider) return;
    const extId = cfg.source_identity.account_id || cfg.source_identity.subscription_id || cfg.source_identity.project_id;
    if (!extId) return;

    const banner = document.createElement('div');
    banner.className = 'account-row self-account-banner';

    const info = document.createElement('span');
    info.className = 'account-info';
    info.textContent = `CUDly host account (${extId})`;
    banner.appendChild(info);

    const addBtn = document.createElement('button');
    addBtn.type = 'button';
    addBtn.className = 'btn btn-small';
    addBtn.textContent = 'Add';
    addBtn.addEventListener('click', async () => {
      addBtn.disabled = true;
      addBtn.textContent = 'Adding...';
      try {
        await api.createSelfAccount();
        await loadAccountsForProvider(provider);
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        if (msg.includes('409')) {
          await loadAccountsForProvider(provider);
        } else {
          addBtn.textContent = 'Failed';
        }
      }
    });
    banner.appendChild(addBtn);

    container.prepend(banner);
  } catch {
    // Config fetch failed — skip silently
  }
}

/**
 * Render accounts list into a container element as a scannable table.
 *
 * Replaces the prior inline-row layout ("Account Name (12345) Edit Test
 * Credentials Overrides Delete" as a single text-flow). The table surfaces
 * each field in its own column, uses a status pill for Active/Disabled,
 * and isolates the destructive Delete action in the right-most column.
 */
type AccountStatusFilter = 'all' | 'active' | 'disabled';

function renderAccountsList(
  container: HTMLElement,
  accounts: api.CloudAccount[],
  provider: AccountProvider,
  filter: AccountStatusFilter = 'all'
): void {
  // Remove prior rendered rows (Overrides panels are sibling elements,
  // banner lives in a separate className managed by renderSelfAccountBanner).
  container.querySelectorAll(
    '.accounts-table, .account-overrides-panel, .accounts-empty, .status-chip-row'
  ).forEach(el => el.remove());

  if (!accounts || accounts.length === 0) {
    if (!container.querySelector('.self-account-banner')) {
      const empty = document.createElement('p');
      empty.className = 'accounts-empty';
      empty.textContent = 'No accounts configured.';
      container.appendChild(empty);
    }
    return;
  }

  // Status chip filter — mirrors the P3 audit recommendation scoped to
  // states the per-provider table actually exposes (Active/Disabled).
  const activeCount = accounts.filter(a => a.enabled).length;
  const disabledCount = accounts.length - activeCount;
  const chipRow = document.createElement('div');
  chipRow.className = 'status-chip-row';
  chipRow.setAttribute('role', 'tablist');
  chipRow.setAttribute('aria-label', 'Filter accounts by status');
  const chips: Array<{ key: AccountStatusFilter; label: string; count: number }> = [
    { key: 'all', label: 'All', count: accounts.length },
    { key: 'active', label: 'Active', count: activeCount },
    { key: 'disabled', label: 'Disabled', count: disabledCount },
  ];
  chips.forEach(({ key, label, count }) => {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'status-chip' + (filter === key ? ' active' : '');
    chip.textContent = `${label} (${count})`;
    chip.setAttribute('role', 'tab');
    chip.setAttribute('aria-selected', String(filter === key));
    chip.addEventListener('click', () => {
      if (filter === key) return;
      renderAccountsList(container, accounts, provider, key);
    });
    chipRow.appendChild(chip);
  });
  container.appendChild(chipRow);

  const visible = accounts.filter(a => {
    if (filter === 'active') return a.enabled;
    if (filter === 'disabled') return !a.enabled;
    return true;
  });

  const table = document.createElement('table');
  table.className = 'accounts-table';
  const thead = document.createElement('thead');
  const headerRow = document.createElement('tr');
  ['Name', 'Account ID', 'Status', 'Actions'].forEach((label) => {
    const th = document.createElement('th');
    th.textContent = label;
    headerRow.appendChild(th);
  });
  thead.appendChild(headerRow);
  table.appendChild(thead);

  const tbody = document.createElement('tbody');
  const panels: HTMLDivElement[] = [];

  visible.forEach((account) => {
    const accountLabel = `${account.name} (${account.external_id})`;
    const tr = document.createElement('tr');

    const nameTd = document.createElement('td');
    nameTd.textContent = account.name;
    if (account.is_self) {
      const self = document.createElement('span');
      self.className = 'badge badge-info';
      self.textContent = ' Self';
      self.setAttribute('title', 'The cloud account running CUDly itself');
      nameTd.appendChild(self);
    }
    tr.appendChild(nameTd);

    const idTd = document.createElement('td');
    idTd.className = 'monospace';
    idTd.textContent = account.external_id;
    tr.appendChild(idTd);

    const statusTd = document.createElement('td');
    const status = document.createElement('span');
    status.className = account.enabled
      ? 'badge badge-success'
      : 'badge badge-warning';
    status.textContent = account.enabled ? 'Active' : 'Disabled';
    statusTd.appendChild(status);
    tr.appendChild(statusTd);

    const actionsTd = document.createElement('td');
    actionsTd.className = 'account-actions';

    const editBtn = document.createElement('button');
    editBtn.type = 'button';
    editBtn.className = 'btn btn-small';
    editBtn.textContent = 'Edit';
    editBtn.setAttribute('aria-label', `Edit ${accountLabel}`);
    editBtn.addEventListener('click', () => openAccountModal(provider, account));
    actionsTd.appendChild(editBtn);

    const testBtn = document.createElement('button');
    testBtn.type = 'button';
    testBtn.className = 'btn btn-small';
    testBtn.textContent = 'Test';
    testBtn.setAttribute('aria-label', `Test credentials for ${accountLabel}`);
    testBtn.addEventListener('click', () => void testAccount(account.id, accountLabel, testBtn));
    actionsTd.appendChild(testBtn);

    const overridesBtn = document.createElement('button');
    overridesBtn.type = 'button';
    overridesBtn.className = 'btn btn-small';
    overridesBtn.textContent = 'Overrides';
    overridesBtn.setAttribute('aria-label', `Service overrides for ${accountLabel}`);
    const overridesPanel = document.createElement('div');
    overridesPanel.className = 'account-overrides-panel hidden';
    overridesBtn.addEventListener('click', () => {
      const hidden = overridesPanel.classList.toggle('hidden');
      if (!hidden) void loadOverridesPanel(account.id, overridesPanel);
    });
    actionsTd.appendChild(overridesBtn);

    // Destructive Delete lives in its own subgroup to isolate it from the
    // routine actions. A small spacer + btn-destructive class (from P2)
    // signal "be careful".
    const spacer = document.createElement('span');
    spacer.className = 'account-actions-spacer';
    spacer.setAttribute('aria-hidden', 'true');
    actionsTd.appendChild(spacer);

    const deleteBtn = document.createElement('button');
    deleteBtn.type = 'button';
    deleteBtn.className = 'btn btn-small btn-destructive';
    deleteBtn.textContent = 'Delete';
    deleteBtn.setAttribute('aria-label', `Delete ${accountLabel}`);
    deleteBtn.addEventListener('click', () => void deleteAccount(account.id, provider, container));
    actionsTd.appendChild(deleteBtn);

    tr.appendChild(actionsTd);
    tbody.appendChild(tr);
    panels.push(overridesPanel);
  });
  table.appendChild(tbody);
  container.appendChild(table);
  // Append overrides panels after the table so "show overrides" expands
  // below the row rather than inside it.
  panels.forEach((p) => container.appendChild(p));
}

/**
 * Load and render the service overrides panel for an account
 */
async function loadOverridesPanel(accountId: string, panel: HTMLElement): Promise<void> {
  panel.textContent = 'Loading\u2026';
  try {
    const overrides = await api.listAccountServiceOverrides(accountId);
    panel.textContent = '';
    if (!overrides || overrides.length === 0) {
      const msg = document.createElement('p');
      msg.className = 'help-text';
      msg.textContent = 'No service overrides set. All services use global defaults.';
      panel.appendChild(msg);
      return;
    }
    const table = document.createElement('table');
    table.className = 'overrides-table';
    const thead = table.createTHead();
    const hrow = thead.insertRow();
    ['Service', 'Term', 'Payment', 'Coverage', ''].forEach(h => {
      const th = document.createElement('th');
      th.textContent = h;
      hrow.appendChild(th);
    });
    const tbody = table.createTBody();
    overrides.forEach(o => {
      const tr = tbody.insertRow();
      [
        `${o.provider}/${o.service}`,
        o.term !== undefined ? `${o.term}yr` : '\u2014',
        o.payment ?? '\u2014',
        o.coverage !== undefined ? `${o.coverage}%` : '\u2014',
      ].forEach(text => {
        const td = tr.insertCell();
        td.textContent = text;
      });
      const actionTd = tr.insertCell();
      const resetBtn = document.createElement('button');
      resetBtn.type = 'button';
      resetBtn.className = 'btn btn-small btn-danger';
      resetBtn.textContent = 'Reset';
      resetBtn.addEventListener('click', async () => {
        const ok = await confirmDialog({
          title: 'Reset override?',
          body: `Reset ${o.provider}/${o.service} override to the global default? Any per-service values you set will be replaced.`,
          confirmLabel: 'Reset override',
          destructive: true,
        });
        if (!ok) return;
        try {
          await api.deleteAccountServiceOverride(accountId, o.provider, o.service);
          await loadOverridesPanel(accountId, panel);
        } catch (err) {
          showToast({ message: `Failed to reset override: ${(err as Error).message}`, kind: 'error' });
        }
      });
      actionTd.appendChild(resetBtn);
    });
    panel.appendChild(table);
  } catch (err) {
    panel.textContent = `Failed to load overrides: ${(err as Error).message}`;
  }
}

/**
 * Delete an account after confirmation
 */
async function deleteAccount(accountId: string, provider: AccountProvider, _container: HTMLElement): Promise<void> {
  const ok = await confirmDialog({
    title: 'Delete account?',
    body: 'Delete this account? This also removes its credentials and service overrides. This action cannot be undone.',
    confirmLabel: 'Delete account',
    destructive: true,
  });
  if (!ok) return;
  try {
    await api.deleteAccount(accountId);
    await loadAccountsForProvider(provider);
  } catch (err) {
    console.error('Failed to delete account:', err);
    showToast({ message: `Failed to delete account: ${(err as Error).message}`, kind: 'error' });
    void loadAccountsForProvider(provider);
  }
}

/**
 * Test account credentials. Surfaces progress + outcome via toast so the
 * signal isn't confined to the button label (which auto-clears in 3s and
 * provides no detail beyond OK/Failed). The button keeps its inline status
 * as a secondary affordance so the user's gaze point — the row they just
 * clicked — still gets feedback.
 */
async function testAccount(accountId: string, accountLabel: string, btn: HTMLButtonElement): Promise<void> {
  const original = btn.textContent;
  btn.disabled = true;
  btn.textContent = 'Testing...';
  showToast({ message: `Testing credentials for ${accountLabel}…`, kind: 'info', timeout: 3_000 });
  try {
    const result = await api.testAccountCredentials(accountId);
    btn.textContent = result.ok ? 'OK' : 'Failed';
    if (result.ok) {
      const detail = result.message ? `: ${result.message}` : '';
      showToast({ message: `Credentials OK for ${accountLabel}${detail}`, kind: 'success', timeout: 5_000 });
    } else {
      showToast({
        message: `Credentials failed for ${accountLabel}${result.message ? `: ${result.message}` : ''}`,
        kind: 'error',
        timeout: null,
      });
    }
    setTimeout(() => {
      btn.textContent = original;
      btn.disabled = false;
    }, 3000);
  } catch (err: unknown) {
    btn.textContent = 'Error';
    const msg = err instanceof Error ? err.message : String(err);
    showToast({ message: `Failed to test credentials for ${accountLabel}: ${msg}`, kind: 'error', timeout: null });
    setTimeout(() => {
      btn.textContent = original;
      btn.disabled = false;
    }, 3000);
  }
}

/**
 * Open account modal for add or edit
 */
export function openAccountModal(provider: AccountProvider, account?: api.CloudAccount, options?: AccountModalOptions): void {
  accountModalProvider = provider;
  accountModalOnSave = options?.onSave;
  const modal = document.getElementById('account-modal');
  if (!modal) return;

  const title = document.getElementById('account-modal-title');
  if (title) title.textContent = account ? 'Edit Account' : 'Add Account';

  setInputValue('account-id', account?.id ?? '');
  setInputValue('account-provider', provider);
  setInputValue('account-name', account?.name ?? '');
  setInputValue('account-description', account?.description ?? '');
  setInputValue('account-contact-email', account?.contact_email ?? '');
  setInputValue('account-external-id', account?.external_id ?? '');
  setInputChecked('account-enabled', account?.enabled ?? true);

  // Show/hide provider-specific fields
  const awsFields = document.getElementById('account-aws-fields');
  const azureFields = document.getElementById('account-azure-fields');
  const gcpFields = document.getElementById('account-gcp-fields');
  if (awsFields) awsFields.classList.toggle('hidden', provider !== 'aws');
  if (azureFields) azureFields.classList.toggle('hidden', provider !== 'azure');
  if (gcpFields) gcpFields.classList.toggle('hidden', provider !== 'gcp');

  if (provider === 'aws') {
    populateAwsAccountFields(account);
  } else if (provider === 'azure') {
    const azureAuthMode = account?.azure_auth_mode ?? 'workload_identity_federation';
    const azureAuthSelect = document.getElementById('account-azure-auth-mode') as HTMLSelectElement | null;
    if (azureAuthSelect) azureAuthSelect.value = azureAuthMode;
    setInputValue('account-azure-tenant-id', account?.azure_tenant_id ?? '');
    setInputValue('account-azure-client-id', account?.azure_client_id ?? '');
    setInputValue('account-azure-client-secret', '');
    updateAzureAuthModeFields(azureAuthMode);
  } else if (provider === 'gcp') {
    const gcpMode = account?.gcp_auth_mode ?? 'workload_identity_federation';
    const gcpAuthSelect = document.getElementById('account-gcp-auth-mode') as HTMLSelectElement | null;
    if (gcpAuthSelect) gcpAuthSelect.value = gcpMode;
    const gcpEmailEl = document.getElementById('account-gcp-client-email') as HTMLInputElement | null;
    if (gcpEmailEl) gcpEmailEl.value = account?.gcp_client_email ?? '';
    const gcpJsonEl = document.getElementById('account-gcp-service-account-json') as HTMLTextAreaElement | null;
    if (gcpJsonEl) gcpJsonEl.value = '';
    const wifConfig = document.getElementById('account-gcp-wif-config') as HTMLTextAreaElement | null;
    if (wifConfig) wifConfig.value = '';
    updateGCPAuthModeFields(gcpMode);
  }

  modal.classList.remove('hidden');
}

/**
 * Populate AWS-specific fields in the account modal
 */
function generateExternalID(): string {
  // crypto.randomUUID is available in all supported browsers (Chrome
  // 92+, Firefox 95+, Safari 15.4+). Fall back to a timestamp-plus-
  // random string only if it's somehow missing (test environments,
  // odd webviews) so we never hand the user an empty field.
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `cudly-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

function populateAwsAccountFields(account?: api.CloudAccount): void {
  const authMode = (account?.aws_auth_mode ?? 'workload_identity_federation') as string;
  const authModeSelect = document.getElementById('account-aws-auth-mode') as HTMLSelectElement | null;
  if (authModeSelect) authModeSelect.value = authMode;

  setInputValue('account-aws-access-key-id', '');
  setInputValue('account-aws-secret-access-key', '');
  // aws_role_arn is the backend column used by all three auth modes that need
  // a role: role_arn (direct), bastion (target role assumed via bastion creds),
  // and workload_identity_federation (role assumed via OIDC). Only one of
  // these fields is visible at a time (see updateAwsAuthModeFields), and the
  // save path writes whichever one is visible back to req.aws_role_arn. We
  // pre-fill all three with the same stored value so switching modes mid-edit
  // doesn't blank the input that just became visible.
  setInputValue('account-aws-role-arn', account?.aws_role_arn ?? '');
  // External ID is a CUDly-managed shared secret for cross-account role
  // assumption (issue #18). On edit, keep whatever was stored before; on
  // create, auto-generate a UUID so every new account has a distinct
  // secret the operator can paste into the IAM trust policy. The field
  // itself is marked readonly in index.html so users can't hand-craft
  // values that would defeat the purpose.
  setInputValue('account-aws-external-id', account?.aws_external_id ?? generateExternalID());
  setInputValue('account-aws-bastion-role-arn', account?.aws_role_arn ?? '');
  setInputValue('account-aws-wif-role-arn', account?.aws_role_arn ?? '');
  setInputValue('account-aws-wif-token-file', account?.aws_web_identity_token_file ?? '');
  setInputChecked('account-aws-is-org-root', account?.aws_is_org_root ?? false);

  updateAwsAuthModeFields(authMode, account?.aws_bastion_id);

  // Render the trust-policy snippet asynchronously — we need the CUDly
  // host account ID from getConfig.source_identity (issue #19). Kept
  // void so the modal doesn't block waiting for the network.
  void renderAwsTrustPolicy();
}

/**
 * Render the IAM trust-policy JSON snippet into the AWS role-mode
 * section. The snippet interpolates the CUDly host AWS account ID and
 * the per-account External ID so operators can copy it straight into
 * the role's trust relationship (issue #19).
 */
async function renderAwsTrustPolicy(): Promise<void> {
  const block = document.getElementById('account-aws-trust-policy');
  const hint = document.getElementById('account-aws-trust-policy-hint');
  if (!block) return;
  const externalID = (document.getElementById('account-aws-external-id') as HTMLInputElement | null)?.value ?? '';

  let sourceAccountID = '';
  try {
    const cfg = await api.getConfig();
    if (cfg.source_identity?.provider === 'aws') {
      sourceAccountID = cfg.source_identity.account_id ?? '';
    }
  } catch {
    // Non-critical — fall through to the placeholder text below.
  }

  if (!sourceAccountID) {
    block.textContent = '';
    if (hint) {
      hint.textContent =
        "CUDly can't determine its host AWS account ID from this deployment " +
        "(either CUDly isn't running on AWS, or the Lambda/Container role " +
        "lacks sts:GetCallerIdentity). Ask a CUDly admin for the account ID, " +
        "then build the trust policy manually with Principal=arn:aws:iam::" +
        "<CUDly account ID>:root, Action=sts:AssumeRole, and a StringEquals " +
        "sts:ExternalId condition on the value above.";
    }
    return;
  }

  const policy = {
    Version: '2012-10-17',
    Statement: [
      {
        Effect: 'Allow',
        Principal: { AWS: `arn:aws:iam::${sourceAccountID}:root` },
        Action: 'sts:AssumeRole',
        Condition: {
          StringEquals: { 'sts:ExternalId': externalID },
        },
      },
    ],
  };
  block.textContent = JSON.stringify(policy, null, 2);
  if (hint) {
    hint.textContent =
      "Attach this policy to the IAM role's trust relationship so CUDly can " +
      "assume it. The sts:ExternalId condition locks the role to this " +
      "specific CUDly registration.";
  }
}

/**
 * Show/hide AWS auth mode sub-fields
 */
function updateAwsAuthModeFields(mode: string, bastionId?: string): void {
  const keysFields = document.getElementById('account-aws-keys-fields');
  const roleFields = document.getElementById('account-aws-role-fields');
  const bastionFields = document.getElementById('account-aws-bastion-fields');
  const wifFields = document.getElementById('account-aws-wif-fields');

  keysFields?.classList.toggle('hidden', mode !== 'access_keys');
  roleFields?.classList.toggle('hidden', mode !== 'role_arn');
  bastionFields?.classList.toggle('hidden', mode !== 'bastion');
  wifFields?.classList.toggle('hidden', mode !== 'workload_identity_federation');

  if (mode === 'bastion') {
    void populateBastionAccountDropdown(bastionId);
  }
}

/**
 * Show/hide Azure auth mode sub-fields. Only client_secret mode has an
 * input block; workload_identity_federation is credential-free (CUDly's
 * OIDC issuer + the App Registration's federated-identity-credential
 * handle authentication) and managed_identity is ambient, so neither
 * needs a visible field block here.
 */
function updateAzureAuthModeFields(mode: string): void {
  const secretFields = document.getElementById('account-azure-secret-fields');
  secretFields?.classList.toggle('hidden', mode !== 'client_secret');
}

/**
 * Show/hide GCP auth mode sub-fields
 */
function updateGCPAuthModeFields(mode: string): void {
  const keyFields = document.getElementById('account-gcp-key-fields');
  const wifFields = document.getElementById('account-gcp-wif-fields');
  keyFields?.classList.toggle('hidden', mode !== 'service_account_key');
  wifFields?.classList.toggle('hidden', mode !== 'workload_identity_federation');
}

/**
 * Populate bastion account dropdown with AWS accounts, optionally pre-selecting one
 */
async function populateBastionAccountDropdown(selectedId?: string): Promise<void> {
  const select = document.getElementById('account-aws-bastion-id') as HTMLSelectElement | null;
  if (!select) return;
  try {
    const accounts = await api.listAccounts({ provider: 'aws' });
    while (select.options.length > 1) select.remove(1);
    accounts.forEach(a => {
      const opt = document.createElement('option');
      opt.value = a.id;
      opt.textContent = `${a.name} (${a.external_id})`;
      select.appendChild(opt);
    });
    if (selectedId) select.value = selectedId;
  } catch {
    // Non-critical
  }
}

/**
 * Close the account modal
 */
function closeAccountModal(): void {
  const modal = document.getElementById('account-modal');
  modal?.classList.add('hidden');
  accountModalOnSave = undefined;
}

/**
 * Build the account request from the modal form
 */
function buildAccountRequest(provider: AccountProvider): api.CloudAccountRequest {
  const req: api.CloudAccountRequest = {
    name: (byId<HTMLInputElement>('account-name')?.value ?? '').trim(),
    description: (byId<HTMLTextAreaElement>('account-description')?.value ?? '').trim() || undefined,
    contact_email: (byId<HTMLInputElement>('account-contact-email')?.value ?? '').trim() || undefined,
    provider,
    external_id: (byId<HTMLInputElement>('account-external-id')?.value ?? '').trim(),
    enabled: byId<HTMLInputElement>('account-enabled')?.checked ?? true,
  };

  if (provider === 'aws') {
    const authMode = byId<HTMLSelectElement>('account-aws-auth-mode')?.value ?? '';
    req.aws_auth_mode = authMode;
    req.aws_is_org_root = byId<HTMLInputElement>('account-aws-is-org-root')?.checked ?? false;
    if (authMode === 'role_arn') {
      req.aws_role_arn = (byId<HTMLInputElement>('account-aws-role-arn')?.value ?? '').trim();
      req.aws_external_id = (byId<HTMLInputElement>('account-aws-external-id')?.value ?? '').trim() || undefined;
    } else if (authMode === 'bastion') {
      req.aws_bastion_id = byId<HTMLSelectElement>('account-aws-bastion-id')?.value ?? '';
      req.aws_role_arn = (byId<HTMLInputElement>('account-aws-bastion-role-arn')?.value ?? '').trim();
    } else if (authMode === 'workload_identity_federation') {
      req.aws_role_arn = byId<HTMLInputElement>('account-aws-wif-role-arn')?.value.trim();
      req.aws_web_identity_token_file = byId<HTMLInputElement>('account-aws-wif-token-file')?.value.trim() || undefined;
    }
  } else if (provider === 'azure') {
    req.azure_subscription_id = req.external_id; // external_id IS the subscription ID for Azure
    req.azure_auth_mode = byId<HTMLSelectElement>('account-azure-auth-mode')?.value || 'client_secret';
    req.azure_tenant_id = (byId<HTMLInputElement>('account-azure-tenant-id')?.value ?? '').trim();
    req.azure_client_id = (byId<HTMLInputElement>('account-azure-client-id')?.value ?? '').trim();
  } else if (provider === 'gcp') {
    req.gcp_project_id = req.external_id; // external_id IS the project ID for GCP
    req.gcp_auth_mode = byId<HTMLSelectElement>('account-gcp-auth-mode')?.value || 'service_account_key';
    req.gcp_client_email = byId<HTMLInputElement>('account-gcp-client-email')?.value.trim() ?? '';
  }

  return req;
}

/**
 * Build and save credentials from the account modal form (if filled)
 */
async function saveAccountCredentialsIfFilled(accountId: string, provider: AccountProvider): Promise<void> {
  if (provider === 'aws') {
    const authMode = byId<HTMLSelectElement>('account-aws-auth-mode')?.value ?? '';
    if (authMode === 'access_keys') {
      const keyId = (byId<HTMLInputElement>('account-aws-access-key-id')?.value ?? '').trim();
      const secretKey = byId<HTMLInputElement>('account-aws-secret-access-key')?.value ?? '';
      if (keyId && secretKey) {
        await api.saveAccountCredentials(accountId, {
          credential_type: 'aws_access_keys',
          payload: { access_key_id: keyId, secret_access_key: secretKey }
        });
      }
    }
  } else if (provider === 'azure') {
    const azureMode = byId<HTMLSelectElement>('account-azure-auth-mode')?.value ?? 'client_secret';
    // managed_identity is ambient; workload_identity_federation is
    // secret-free (CUDly's OIDC issuer + the Azure App Registration's
    // federated-identity-credential handle authentication, no material
    // stored in CUDly). Only client_secret accepts a stored credential.
    if (azureMode === 'client_secret') {
      const secret = byId<HTMLInputElement>('account-azure-client-secret')?.value ?? '';
      if (secret) {
        await api.saveAccountCredentials(accountId, {
          credential_type: 'azure_client_secret',
          payload: { client_secret: secret }
        });
      }
    }
  } else if (provider === 'gcp') {
    const gcpMode = byId<HTMLSelectElement>('account-gcp-auth-mode')?.value ?? 'service_account_key';
    if (gcpMode === 'application_default') {
      // No credential to store
    } else if (gcpMode === 'workload_identity_federation') {
      const config = byId<HTMLTextAreaElement>('account-gcp-wif-config')?.value.trim();
      if (config) {
        let parsed: Record<string, unknown>;
        try {
          parsed = JSON.parse(config) as Record<string, unknown>;
        } catch {
          throw new Error('GCP WIF config is not valid JSON');
        }
        await api.saveAccountCredentials(accountId, { credential_type: 'gcp_workload_identity_config', payload: parsed });
      }
    } else {
      const jsonText = byId<HTMLTextAreaElement>('account-gcp-service-account-json')?.value.trim() ?? '';
      if (jsonText) {
        let parsed: Record<string, unknown>;
        try {
          parsed = JSON.parse(jsonText) as Record<string, unknown>;
        } catch {
          throw new Error('Service account JSON is not valid JSON');
        }
        await api.saveAccountCredentials(accountId, { credential_type: 'gcp_service_account', payload: parsed });
      }
    }
  }
}

/**
 * Handle account form submission
 */
async function handleAccountFormSubmit(e: Event): Promise<void> {
  e.preventDefault();
  const provider = accountModalProvider;
  const req = buildAccountRequest(provider);

  // If a custom onSave callback was provided (e.g., registration approval),
  // delegate to it instead of the normal create/update flow.
  if (accountModalOnSave) {
    try {
      await accountModalOnSave(provider, req);
      closeAccountModal();
    } catch (err) {
      console.error('Custom save failed:', err);
      showToast({ message: `Failed to save: ${(err as Error).message}`, kind: 'error' });
    }
    return;
  }

  const accountId = byId<HTMLInputElement>('account-id')?.value ?? '';

  try {
    let savedId = accountId;
    if (accountId) {
      await api.updateAccount(accountId, req);
    } else {
      const created = await api.createAccount(req);
      savedId = created.id;
    }

    // Credential save is best-effort: if it fails we still close the modal
    // (the account was already persisted) and show a targeted warning so the
    // user knows to retry just the credential step.
    try {
      await saveAccountCredentialsIfFilled(savedId, provider);
    } catch (credErr) {
      console.error('Account saved but credentials could not be stored:', credErr);
      closeAccountModal();
      await loadAccountsForProvider(provider);
      showToast({
        message: `Account saved, but credentials could not be stored: ${(credErr as Error).message}. Edit the account to re-enter credentials.`,
        kind: 'warning',
        timeout: null,
      });
      return;
    }

    closeAccountModal();
    await loadAccountsForProvider(provider);
  } catch (err) {
    console.error('Failed to save account:', err);
    showToast({ message: `Failed to save account: ${(err as Error).message}`, kind: 'error' });
  }
}

/**
 * Trigger org account discovery from the management (org root) account
 */
async function handleDiscoverOrgAccounts(): Promise<void> {
  try {
    const result = await api.discoverOrgAccounts();
    showToast({ message: `Org discovery started: ${result.message || result.status}`, kind: 'success' });
    await loadAccountsForProvider('aws');
  } catch (err) {
    console.error('Org discovery failed:', err);
    showToast({ message: `Org discovery failed: ${(err as Error).message}`, kind: 'error' });
  }
}

/**
 * Set up settings event handlers
 */
export function setupSettingsHandlers(signal?: AbortSignal): void {
  // Provider checkbox handlers - toggle visibility of provider settings sections
  const awsCheck = document.getElementById('provider-aws') as HTMLInputElement | null;
  const azureCheck = document.getElementById('provider-azure') as HTMLInputElement | null;
  const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement | null;

  awsCheck?.addEventListener('change', () => updateProviderSettingsVisibility(), { signal });
  azureCheck?.addEventListener('change', () => updateProviderSettingsVisibility(), { signal });
  gcpCheck?.addEventListener('change', () => updateProviderSettingsVisibility(), { signal });

  // Auto-collect checkbox - toggle schedule visibility
  const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement | null;
  autoCollect?.addEventListener('change', () => updateCollectionScheduleVisibility(), { signal });

  // Global defaults — propagate to all services when changed. The actual
  // propagation is gated behind a diff-preview confirmation (see
  // confirmAndPropagateTerm / confirmAndPropagatePayment) so the user
  // understands which per-service rows will be rewritten.
  const defaultTerm = document.getElementById('setting-default-term') as HTMLSelectElement | null;
  const defaultPayment = document.getElementById('setting-default-payment') as HTMLSelectElement | null;
  if (defaultTerm) {
    defaultTerm.dataset['previous'] = defaultTerm.value;
    defaultTerm.addEventListener('focus', () => { defaultTerm.dataset['previous'] = defaultTerm.value; }, { signal });
    defaultTerm.addEventListener('change', () => {
      void confirmAndPropagateTerm(defaultTerm);
    }, { signal });
  }
  if (defaultPayment) {
    defaultPayment.dataset['previous'] = defaultPayment.value;
    defaultPayment.addEventListener('focus', () => { defaultPayment.dataset['previous'] = defaultPayment.value; }, { signal });
    defaultPayment.addEventListener('change', () => {
      void confirmAndPropagatePayment(defaultPayment);
    }, { signal });
  }

  // Set up dirty-field tracking
  setupDirtyTracking(signal);

  // Account management buttons
  document.getElementById('add-aws-account-btn')?.addEventListener('click', () => openAccountModal('aws'), { signal });
  document.getElementById('add-azure-account-btn')?.addEventListener('click', () => openAccountModal('azure'), { signal });
  document.getElementById('add-gcp-account-btn')?.addEventListener('click', () => openAccountModal('gcp'), { signal });

  // Org discovery button
  document.getElementById('discover-org-accounts-btn')?.addEventListener('click', () => void handleDiscoverOrgAccounts(), { signal });

  // Account form submit handler
  document.getElementById('account-form')?.addEventListener('submit', (e) => void handleAccountFormSubmit(e), { signal });

  // Account modal close
  document.getElementById('close-account-modal-btn')?.addEventListener('click', closeAccountModal, { signal });

  // AWS auth mode change handler
  const awsAuthMode = document.getElementById('account-aws-auth-mode') as HTMLSelectElement | null;
  awsAuthMode?.addEventListener('change', () => updateAwsAuthModeFields(awsAuthMode.value), { signal });

  // Copy the auto-generated AWS External ID to the clipboard (issue #18).
  document.getElementById('account-aws-external-id-copy')?.addEventListener(
    'click',
    () => copyToClipboard('account-aws-external-id'),
    { signal },
  );

  // Copy the rendered AWS IAM trust policy snippet (issue #19).
  document.getElementById('account-aws-trust-policy-copy')?.addEventListener(
    'click',
    () => copyToClipboard('account-aws-trust-policy'),
    { signal },
  );

  // Azure auth mode change handler
  const azureAuthMode = document.getElementById('account-azure-auth-mode') as HTMLSelectElement | null;
  azureAuthMode?.addEventListener('change', () => updateAzureAuthModeFields(azureAuthMode.value), { signal });

  // GCP auth mode change handler
  const gcpAuthMode = document.getElementById('account-gcp-auth-mode') as HTMLSelectElement | null;
  gcpAuthMode?.addEventListener('change', () => updateGCPAuthModeFields(gcpAuthMode.value), { signal });

  // Close account modal when clicking outside
  const accountModal = document.getElementById('account-modal');
  accountModal?.addEventListener('click', (e) => {
    if (e.target === accountModal) closeAccountModal();
  }, { signal });
}

/**
 * Update visibility of provider settings sections based on checkboxes
 */
function updateProviderSettingsVisibility(): void {
  const providers: Array<{ key: AccountProvider; checkId: string; settingsId: string; blockId: string }> = [
    { key: 'aws',   checkId: 'provider-aws',   settingsId: 'aws-settings',   blockId: 'accounts-aws-block'   },
    { key: 'azure', checkId: 'provider-azure', settingsId: 'azure-settings', blockId: 'accounts-azure-block' },
    { key: 'gcp',   checkId: 'provider-gcp',   settingsId: 'gcp-settings',   blockId: 'accounts-gcp-block'   },
  ];
  for (const p of providers) {
    const checked = (document.getElementById(p.checkId) as HTMLInputElement | null)?.checked ?? false;
    // Per-provider settings section (credentials + service defaults): hide when disabled
    document.getElementById(p.settingsId)?.classList.toggle('hidden', !checked);
    // Per-provider accounts block in Accounts section: dim (not hide) when disabled
    const block = document.getElementById(p.blockId);
    if (block) {
      const wasDisabled = block.classList.contains('provider-disabled');
      block.classList.toggle('provider-disabled', !checked);
      // Re-enabling: refresh the account list from the backend
      if (wasDisabled && checked) {
        void loadAccountsForProvider(p.key);
      }
    }
  }
}

/**
 * Update visibility of collection schedule row based on auto-collect checkbox
 */
function updateCollectionScheduleVisibility(): void {
  const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement | null;
  const scheduleRow = document.getElementById('collection-schedule-row');

  if (scheduleRow) {
    scheduleRow.classList.toggle('hidden', !autoCollect?.checked);
  }
}

/**
 * Propagate default term to all service-specific term selects
 */
function propagateTermToServices(term: string): void {
  SERVICE_FIELDS.forEach(({ termId }) => {
    const select = document.getElementById(termId) as HTMLSelectElement | null;
    if (select) {
      select.value = term;
    }
  });
}

/**
 * Propagate default payment option to all service-specific payment selects
 * Note: Only AWS services have payment options; Azure/GCP use full upfront only
 */
function propagatePaymentToServices(payment: string): void {
  SERVICE_FIELDS
    .filter(f => f.paymentId !== null)
    .forEach(({ paymentId }) => {
      const select = document.getElementById(paymentId!) as HTMLSelectElement | null;
      if (select) {
        select.value = payment;
      }
    });
}

function termLabel(value: string): string {
  return value === '1' ? '1 Year' : value === '3' ? '3 Years' : `${value} Years`;
}

function paymentLabel(value: string): string {
  switch (value) {
    case 'no-upfront': return 'No Upfront';
    case 'partial-upfront': return 'Partial Upfront';
    case 'all-upfront': return 'All Upfront';
    default: return value;
  }
}

function buildAffectedList(affected: { provider: string; service: string }[]): HTMLDivElement {
  // Build the diff list via DOM APIs (not innerHTML) so we don't need an
  // escapeHtml import; provider + service values come from the SERVICE_FIELDS
  // constant so they're inherently safe, but constructor-free DOM is the
  // simpler contract.
  const wrap = document.createElement('div');
  const count = affected.length;
  const intro = document.createElement('p');
  intro.textContent = `${count} service${count === 1 ? '' : 's'} will change:`;
  wrap.appendChild(intro);
  const ul = document.createElement('ul');
  affected.forEach((f) => {
    const li = document.createElement('li');
    li.textContent = `${f.provider.toUpperCase()} ${f.service}`;
    ul.appendChild(li);
  });
  wrap.appendChild(ul);
  return wrap;
}

async function confirmAndPropagateTerm(select: HTMLSelectElement): Promise<void> {
  const previousValue = select.dataset['previous'] ?? select.value;
  const newValue = select.value;
  if (newValue === previousValue) return;
  const affected = SERVICE_FIELDS.filter(({ termId }) => {
    const svc = document.getElementById(termId) as HTMLSelectElement | null;
    return svc && svc.value !== newValue;
  });
  if (affected.length === 0) {
    // Everything already matches — nothing to propagate, no need to prompt.
    select.dataset['previous'] = newValue;
    return;
  }
  const ok = await confirmDialog({
    title: `Apply "${termLabel(newValue)}" to ${affected.length} service${affected.length === 1 ? '' : 's'}?`,
    body: buildAffectedList(affected),
    confirmLabel: 'Apply to all',
  });
  if (ok) {
    propagateTermToServices(newValue);
    select.dataset['previous'] = newValue;
    updateDirtyMarkers();
  } else {
    // User cancelled — restore the default select to its prior value so
    // the visible state matches what's persisted/saved.
    select.value = previousValue;
  }
}

async function confirmAndPropagatePayment(select: HTMLSelectElement): Promise<void> {
  const previousValue = select.dataset['previous'] ?? select.value;
  const newValue = select.value;
  if (newValue === previousValue) return;
  const affected = SERVICE_FIELDS
    .filter(f => f.paymentId !== null)
    .filter(({ paymentId }) => {
      const svc = document.getElementById(paymentId!) as HTMLSelectElement | null;
      return svc && svc.value !== newValue;
    });
  if (affected.length === 0) {
    select.dataset['previous'] = newValue;
    return;
  }
  const ok = await confirmDialog({
    title: `Apply "${paymentLabel(newValue)}" to ${affected.length} AWS service${affected.length === 1 ? '' : 's'}?`,
    body: buildAffectedList(affected),
    confirmLabel: 'Apply to all',
  });
  if (ok) {
    propagatePaymentToServices(newValue);
    select.dataset['previous'] = newValue;
    updateDirtyMarkers();
  } else {
    select.value = previousValue;
  }
}

/**
 * Load global settings
 */
export async function loadGlobalSettings(): Promise<void> {
  const loadingEl = document.getElementById('settings-loading');
  const formEl = document.getElementById('global-settings-form');
  const errorEl = document.getElementById('settings-error');

  if (loadingEl) loadingEl.classList.remove('hidden');
  if (formEl) formEl.classList.add('hidden');
  if (errorEl) errorEl.classList.add('hidden');

  // Overlay dynamically-probed AWS commitment rules before we render the
  // form, so the first paint already respects server data. Failures fall
  // back to hardcoded rules silently — we never block Settings on this.
  await fetchAndPopulateCommitmentOptions();

  try {
    const data = await api.getConfig();

    if (data.global) {
      const providers = data.global.enabled_providers || [];
      const awsCheck = document.getElementById('provider-aws') as HTMLInputElement | null;
      const azureCheck = document.getElementById('provider-azure') as HTMLInputElement | null;
      const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement | null;

      if (awsCheck) awsCheck.checked = providers.includes('aws');
      if (azureCheck) azureCheck.checked = providers.includes('azure');
      if (gcpCheck) gcpCheck.checked = providers.includes('gcp');

      const emailInput = document.getElementById('setting-notification-email') as HTMLInputElement | null;
      if (emailInput) emailInput.value = data.global.notification_email || '';

      const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement | null;
      if (autoCollect) autoCollect.checked = data.global.auto_collect !== false;

      const collectionSchedule = document.getElementById('setting-collection-schedule') as HTMLSelectElement | null;
      if (collectionSchedule) collectionSchedule.value = data.global.collection_schedule || 'daily';

      const termSelect = document.getElementById('setting-default-term') as HTMLSelectElement | null;
      if (termSelect) termSelect.value = String(data.global.default_term || 3);

      const paymentSelect = document.getElementById('setting-default-payment') as HTMLSelectElement | null;
      if (paymentSelect) paymentSelect.value = data.global.default_payment || 'all-upfront';

      const coverageInput = document.getElementById('setting-default-coverage') as HTMLInputElement | null;
      if (coverageInput) coverageInput.value = String(data.global.default_coverage || 80);

      const notifyDaysInput = document.getElementById('setting-notification-days') as HTMLInputElement | null;
      if (notifyDaysInput) notifyDaysInput.value = String(data.global.notification_days_before || 3);

      // Grace-period inputs (per provider). An absent key renders the
      // default (7) so the UI always shows a concrete value; an
      // explicit 0 is preserved so users can see which providers
      // have the feature disabled.
      const gpMap = data.global.grace_period_days || {};
      populateGraceInput('setting-grace-aws', gpMap['aws']);
      populateGraceInput('setting-grace-azure', gpMap['azure']);
      populateGraceInput('setting-grace-gcp', gpMap['gcp']);

      // Update visibility based on loaded settings
      updateProviderSettingsVisibility();
      updateCollectionScheduleVisibility();
    }

    if (data.services) {
      loadedServiceConfigs = data.services;
      for (const svc of data.services) {
        const key = `${svc.provider}-${svc.service}`;
        const termEl = document.getElementById(`${key}-term`) as HTMLSelectElement | null;
        if (termEl) termEl.value = String(svc.term);
        const paymentEl = document.getElementById(`${key}-payment`) as HTMLSelectElement | null;
        if (paymentEl) paymentEl.value = svc.payment;
      }
    }

    if (loadingEl) loadingEl.classList.add('hidden');
    if (formEl) formEl.classList.remove('hidden');

    // Establish the clean baseline for dirty tracking
    snapshotAllFields();
    updateDirtyMarkers();

    cachedSourceCloud = data.source_cloud ?? 'aws';
  } catch (error) {
    console.error('Failed to load settings:', error);
    if (loadingEl) loadingEl.classList.add('hidden');
    if (errorEl) {
      const err = error as Error;
      errorEl.textContent = `Failed to load settings: ${err.message}`;
      errorEl.classList.remove('hidden');
    }
  }
}

/**
 * Load the Accounts sub-tab: account lists, federation IaC panel, and
 * pending registrations. Self-contained — fetches sourceCloud from
 * cache or the config API if not yet available.
 */
export async function loadAccountsTab(): Promise<void> {
  let source = cachedSourceCloud;
  if (!source) {
    try {
      const cfg = await api.getConfig();
      source = cfg.source_cloud ?? 'aws';
      cachedSourceCloud = source;
    } catch {
      source = 'aws';
    }
  }
  void loadAccountsForProvider('aws');
  void loadAccountsForProvider('azure');
  void loadAccountsForProvider('gcp');
  void initFederationPanel(source);
  void import('./modules/registrations').then(m => m.initRegistrations());
  void renderAccountsOverview();
}

/**
 * Render the Accounts overview chip row (known_issues #29 partial).
 * Pulls counts from /api/accounts and /api/registrations across all
 * providers, presents `[Pending N | Active N | Disabled N | Rejected N]`
 * chips. Each chip click scrolls to the relevant section so users get
 * a single at-a-glance view without rebuilding into a unified table.
 *
 * Failures are rendered as an empty state — the per-section tables are
 * the authoritative views and will surface their own errors.
 */
async function renderAccountsOverview(): Promise<void> {
  const container = document.getElementById('accounts-overview');
  if (!container) return;
  container.replaceChildren();

  type Bucket = { label: string; count: number; targetId: string };
  const buckets: Bucket[] = [
    { label: 'Pending', count: 0, targetId: 'accounts-registrations' },
    { label: 'Active', count: 0, targetId: 'accounts-aws-block' },
    { label: 'Disabled', count: 0, targetId: 'accounts-aws-block' },
    { label: 'Rejected', count: 0, targetId: 'accounts-registrations' },
  ];

  try {
    const [accounts, registrations] = await Promise.all([
      api.listAccounts(),
      // Registrations endpoint may 404 / 403 in some deployments; tolerate.
      api.listRegistrations().catch(() => [] as api.AccountRegistration[]),
    ]);
    accounts.forEach(a => {
      if (a.enabled) buckets[1]!.count += 1;
      else buckets[2]!.count += 1;
    });
    registrations.forEach(r => {
      if (r.status === 'pending') buckets[0]!.count += 1;
      else if (r.status === 'rejected') buckets[3]!.count += 1;
    });
  } catch {
    // Leave overview empty — per-section tables surface their own errors.
    return;
  }

  buckets.forEach(({ label, count, targetId }) => {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'accounts-overview-chip';
    chip.dataset['count'] = String(count);
    chip.setAttribute('aria-label', `${count} ${label.toLowerCase()} — jump to section`);
    const labelEl = document.createElement('span');
    labelEl.className = 'accounts-overview-chip-label';
    labelEl.textContent = label;
    const countEl = document.createElement('span');
    countEl.className = 'accounts-overview-chip-count';
    countEl.textContent = String(count);
    chip.appendChild(countEl);
    chip.appendChild(labelEl);
    chip.addEventListener('click', () => {
      const target = document.getElementById(targetId);
      if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
    });
    container.appendChild(chip);
  });
}

/**
 * Save global settings
 */
export async function saveGlobalSettings(e: Event): Promise<void> {
  e.preventDefault();

  // In-flight guard: silently drop concurrent submissions so rapid Save
  // clicks or an Enter-triggered resubmit don't produce duplicate PUTs
  // with non-deterministic last-write-wins ordering.
  if (saveInFlight) return;

  const saveBtn = byId<HTMLButtonElement>('save-settings-btn');
  saveInFlight = true;
  if (saveBtn) saveBtn.disabled = true;

  const enabledProviders: api.Provider[] = [];
  if (byId<HTMLInputElement>('provider-aws')?.checked) enabledProviders.push('aws');
  if (byId<HTMLInputElement>('provider-azure')?.checked) enabledProviders.push('azure');
  if (byId<HTMLInputElement>('provider-gcp')?.checked) enabledProviders.push('gcp');

  // Collect per-provider grace-period values before building the
  // settings object so we can reject out-of-range input early with a
  // targeted error instead of letting the API surface "invalid range"
  // without saying which input.
  const gracePeriodDays: Record<string, number> = {};
  for (const [provider, id] of [['aws', 'setting-grace-aws'], ['azure', 'setting-grace-azure'], ['gcp', 'setting-grace-gcp']] as const) {
    const v = readGraceInput(id);
    if (v.err) {
      showToast({ message: `${provider.toUpperCase()} grace period: ${v.err}`, kind: 'error' });
      if (saveBtn) saveBtn.disabled = false;
      saveInFlight = false;
      return;
    }
    gracePeriodDays[provider] = v.value;
  }

  const settings: api.Config = {
    enabled_providers: enabledProviders,
    notification_email: byId<HTMLInputElement>('setting-notification-email')?.value || '',
    auto_collect: byId<HTMLInputElement>('setting-auto-collect')?.checked ?? true,
    collection_schedule: byId<HTMLSelectElement>('setting-collection-schedule')?.value || 'daily',
    default_term: parseInt(byId<HTMLSelectElement>('setting-default-term')?.value || '3', 10),
    default_payment: (byId<HTMLSelectElement>('setting-default-payment')?.value || 'all-upfront') as api.PaymentOption,
    default_coverage: parseInt(byId<HTMLInputElement>('setting-default-coverage')?.value || '80', 10),
    notification_days_before: parseInt(byId<HTMLInputElement>('setting-notification-days')?.value || '3', 10),
    grace_period_days: gracePeriodDays,
  };

  try {
    await api.updateConfig(settings);

    const serviceSaves = SERVICE_FIELDS.map(({ provider, service, termId, paymentId }) => {
      const term = parseInt(byId<HTMLSelectElement>(termId)?.value || '3', 10);
      const payment = paymentId
        ? (byId<HTMLSelectElement>(paymentId)?.value || 'all-upfront')
        : settings.default_payment;
      const base = loadedServiceConfigs.find(s => s.provider === provider && s.service === service);
      // Carry forward every field the UI doesn't own. coverage, ramp_schedule,
      // include_engines, etc. can be set out-of-band (API, future UI, migration)
      // and a full UPSERT that only honoured the four term/payment/enabled/coverage
      // fields would silently wipe them every time the user clicked Save.
      const cfg: api.ServiceConfig = {
        ...(base ?? {}),
        provider,
        service,
        enabled: base?.enabled ?? true,
        term,
        payment,
        coverage: base?.coverage ?? settings.default_coverage,
      };
      return api.updateServiceConfig(provider, service, cfg);
    });
    await Promise.all(serviceSaves);

    snapshotAllFields();
    updateDirtyMarkers();
    showToast({ message: 'Settings saved successfully', kind: 'success', timeout: 5_000 });
  } catch (error) {
    console.error('Failed to save settings:', error);
    const err = error as Error;
    showToast({ message: `Failed to save settings: ${err.message}`, kind: 'error' });
  } finally {
    saveInFlight = false;
    if (saveBtn) saveBtn.disabled = false;
  }
}

/**
 * Reset settings to defaults
 */
export async function resetSettings(): Promise<void> {
  const ok = await confirmDialog({
    title: 'Reset all settings?',
    body: 'This clears your provider selection, collection schedule, and notification email back to factory defaults. Service-level defaults stay as-is.',
    confirmLabel: 'Reset settings',
    destructive: true,
  });
  if (!ok) return;

  const awsCheck = document.getElementById('provider-aws') as HTMLInputElement | null;
  const azureCheck = document.getElementById('provider-azure') as HTMLInputElement | null;
  const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement | null;
  if (awsCheck) awsCheck.checked = true;
  if (azureCheck) azureCheck.checked = false;
  if (gcpCheck) gcpCheck.checked = false;

  const emailInput = document.getElementById('setting-notification-email') as HTMLInputElement | null;
  if (emailInput) emailInput.value = '';

  const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement | null;
  if (autoCollect) autoCollect.checked = true;

  const termSelect = document.getElementById('setting-default-term') as HTMLSelectElement | null;
  if (termSelect) termSelect.value = '3';

  const paymentSelect = document.getElementById('setting-default-payment') as HTMLSelectElement | null;
  if (paymentSelect) paymentSelect.value = 'all-upfront';

  const coverageInput = document.getElementById('setting-default-coverage') as HTMLInputElement | null;
  if (coverageInput) coverageInput.value = '80';

  const notifyDaysInput = document.getElementById('setting-notification-days') as HTMLInputElement | null;
  if (notifyDaysInput) notifyDaysInput.value = '3';

  populateGraceInput('setting-grace-aws', 7);
  populateGraceInput('setting-grace-azure', 7);
  populateGraceInput('setting-grace-gcp', 7);
}

/**
 * Copy text from an element to clipboard
 */
export function copyToClipboard(elementId: string): void {
  const element = document.getElementById(elementId);
  if (!element) return;

  // Prefer .value for form controls (the AWS External ID uses an
  // <input readonly>) and fall back to textContent for code/span
  // elements.
  const text = element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement
    ? element.value
    : element.textContent ?? '';
  navigator.clipboard.writeText(text).then(() => {
    // Show feedback on the first .copy-btn following the source in DOM
    // order — handles both the adjacent-sibling layout (code + btn) and
    // the .input-with-copy layout (input + btn nested under a wrapper).
    const btn = element.nextElementSibling instanceof HTMLButtonElement
      ? element.nextElementSibling
      : element.parentElement?.querySelector<HTMLButtonElement>('.copy-btn') ?? null;
    if (!btn) return;
    const previousChildren = Array.from(btn.childNodes);
    // Swap for a single "copied" checkmark without touching innerHTML —
    // the content is static but XSS-scanner-friendly code avoids the
    // pattern entirely so we don't need to re-prove safety at each edit.
    btn.replaceChildren();
    const tick = document.createElement('span');
    tick.className = 'copy-icon';
    tick.textContent = '✓'; // ✓
    btn.appendChild(tick);
    btn.classList.add('copied');
    setTimeout(() => {
      btn.replaceChildren(...previousChildren);
      btn.classList.remove('copied');
    }, 2000);
  }).catch(err => {
    console.error('Failed to copy:', err);
  });
}
