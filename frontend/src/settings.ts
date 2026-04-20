/**
 * Settings module for CUDly
 */

import * as api from './api';
import { initFederationPanel } from './federation';

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
  { provider: 'azure', service: 'vm',         termId: 'azure-vm-term',         paymentId: null },
  { provider: 'azure', service: 'sql',        termId: 'azure-sql-term',        paymentId: null },
  { provider: 'azure', service: 'cosmosdb',   termId: 'azure-cosmosdb-term',   paymentId: null },
  { provider: 'azure', service: 'redis',      termId: 'azure-redis-term',      paymentId: null },
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
  // Per-service fields
  ...SERVICE_FIELDS.map(f => f.termId),
  ...SERVICE_FIELDS.filter(f => f.paymentId !== null).map(f => f.paymentId as string),
];

// Snapshot of field values at last save (or initial load).
let savedSnapshot: Record<string, string> = {};

// Cached service configs from last load — used as base when saving to preserve
// non-UI fields (ramp_schedule, include_engines, etc.) that SaveServiceConfig replaces entirely.
let loadedServiceConfigs: api.ServiceConfig[] = [];

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
  TRACKED_FIELDS.forEach(id => {
    const el = document.getElementById(id);
    if (!el) return;
    const dirty = getFieldValue(id) !== savedSnapshot[id];
    if (el instanceof HTMLInputElement && el.type === 'checkbox') {
      // Highlight the containing setting-input div for checkboxes
      el.closest<HTMLElement>('.setting-input')?.classList.toggle('dirty', dirty);
    } else {
      el.classList.toggle('dirty', dirty);
    }
  });
}

/** Returns true if any tracked field has been changed since the last save/load. */
export function isUnsavedChanges(): boolean {
  return TRACKED_FIELDS.some(id => getFieldValue(id) !== savedSnapshot[id]);
}

function setupDirtyTracking(): void {
  TRACKED_FIELDS.forEach(id => {
    const el = document.getElementById(id);
    if (!el) return;
    el.addEventListener('change', () => updateDirtyMarkers());
    // Also listen to input for text fields so highlighting is immediate
    if (el instanceof HTMLInputElement && el.type !== 'checkbox') {
      el.addEventListener('input', () => updateDirtyMarkers());
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
    banner.style.borderLeft = '3px solid var(--accent, #4a9eff)';
    banner.style.padding = '8px 12px';
    banner.style.marginBottom = '8px';

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
 * Render accounts list into a container element
 */
function renderAccountsList(container: HTMLElement, accounts: api.CloudAccount[], provider: AccountProvider): void {
  // Remove old account rows (banner is managed by renderSelfAccountBanner)
  container.querySelectorAll('.account-row:not(.self-account-banner), .account-overrides-panel').forEach(el => el.remove());

  if (!accounts || accounts.length === 0) {
    if (!container.querySelector('.self-account-banner')) {
      container.textContent = 'No accounts configured.';
    }
    return;
  }

  accounts.forEach(account => {
    const row = document.createElement('div');
    row.className = 'account-row';

    const info = document.createElement('span');
    info.className = 'account-info';
    const selfBadge = account.is_self ? ' [Self]' : '';
    info.textContent = `${account.name} (${account.external_id})${selfBadge}${account.enabled ? '' : ' [disabled]'}`;
    row.appendChild(info);

    const editBtn = document.createElement('button');
    editBtn.type = 'button';
    editBtn.className = 'btn btn-small';
    editBtn.textContent = 'Edit';
    editBtn.addEventListener('click', () => openAccountModal(provider, account));
    row.appendChild(editBtn);

    const testBtn = document.createElement('button');
    testBtn.type = 'button';
    testBtn.className = 'btn btn-small';
    testBtn.textContent = 'Test';
    testBtn.addEventListener('click', () => void testAccount(account.id, testBtn));
    row.appendChild(testBtn);

    const credsBtn = document.createElement('button');
    credsBtn.type = 'button';
    credsBtn.className = 'btn btn-small';
    credsBtn.textContent = 'Credentials';
    credsBtn.addEventListener('click', () => openAccountModal(provider, account));
    row.appendChild(credsBtn);

    const overridesBtn = document.createElement('button');
    overridesBtn.type = 'button';
    overridesBtn.className = 'btn btn-small';
    overridesBtn.textContent = 'Overrides';
    const overridesPanel = document.createElement('div');
    overridesPanel.className = 'account-overrides-panel hidden';
    overridesBtn.addEventListener('click', () => {
      const hidden = overridesPanel.classList.toggle('hidden');
      if (!hidden) void loadOverridesPanel(account.id, overridesPanel);
    });
    row.appendChild(overridesBtn);

    const deleteBtn = document.createElement('button');
    deleteBtn.type = 'button';
    deleteBtn.className = 'btn btn-small btn-danger';
    deleteBtn.textContent = 'Delete';
    deleteBtn.addEventListener('click', () => void deleteAccount(account.id, provider, container));
    row.appendChild(deleteBtn);

    container.appendChild(row);
    container.appendChild(overridesPanel);
  });
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
        if (!confirm(`Reset ${o.provider}/${o.service} override to global default?`)) return;
        try {
          await api.deleteAccountServiceOverride(accountId, o.provider, o.service);
          await loadOverridesPanel(accountId, panel);
        } catch (err) {
          alert(`Failed to reset override: ${(err as Error).message}`);
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
  if (!confirm('Delete this account? This also removes its credentials and service overrides.')) return;
  try {
    await api.deleteAccount(accountId);
    await loadAccountsForProvider(provider);
  } catch (err) {
    console.error('Failed to delete account:', err);
    alert(`Failed to delete account: ${(err as Error).message}`);
    void loadAccountsForProvider(provider);
  }
}

/**
 * Test account credentials
 */
async function testAccount(accountId: string, btn: HTMLButtonElement): Promise<void> {
  const original = btn.textContent;
  btn.disabled = true;
  btn.textContent = 'Testing...';
  try {
    const result = await api.testAccountCredentials(accountId);
    btn.textContent = result.ok ? 'OK' : 'Failed';
    setTimeout(() => {
      btn.textContent = original;
      btn.disabled = false;
    }, 3000);
  } catch {
    btn.textContent = 'Error';
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

  (document.getElementById('account-id') as HTMLInputElement).value = account?.id ?? '';
  (document.getElementById('account-provider') as HTMLInputElement).value = provider;
  (document.getElementById('account-name') as HTMLInputElement).value = account?.name ?? '';
  (document.getElementById('account-description') as HTMLTextAreaElement).value = account?.description ?? '';
  (document.getElementById('account-contact-email') as HTMLInputElement).value = account?.contact_email ?? '';
  (document.getElementById('account-external-id') as HTMLInputElement).value = account?.external_id ?? '';
  (document.getElementById('account-enabled') as HTMLInputElement).checked = account?.enabled ?? true;

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
    (document.getElementById('account-azure-tenant-id') as HTMLInputElement).value = account?.azure_tenant_id ?? '';
    (document.getElementById('account-azure-client-id') as HTMLInputElement).value = account?.azure_client_id ?? '';
    (document.getElementById('account-azure-client-secret') as HTMLInputElement).value = '';
    (document.getElementById('account-azure-wif-private-key') as HTMLTextAreaElement | null ?? { value: '' }).value = '';
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
function populateAwsAccountFields(account?: api.CloudAccount): void {
  const authMode = (account?.aws_auth_mode ?? 'workload_identity_federation') as string;
  const authModeSelect = document.getElementById('account-aws-auth-mode') as HTMLSelectElement | null;
  if (authModeSelect) authModeSelect.value = authMode;

  (document.getElementById('account-aws-access-key-id') as HTMLInputElement).value = '';
  (document.getElementById('account-aws-secret-access-key') as HTMLInputElement).value = '';
  // aws_role_arn is the backend column used by all three auth modes that need
  // a role: role_arn (direct), bastion (target role assumed via bastion creds),
  // and workload_identity_federation (role assumed via OIDC). Only one of
  // these fields is visible at a time (see updateAwsAuthModeFields), and the
  // save path writes whichever one is visible back to req.aws_role_arn. We
  // pre-fill all three with the same stored value so switching modes mid-edit
  // doesn't blank the input that just became visible.
  (document.getElementById('account-aws-role-arn') as HTMLInputElement).value = account?.aws_role_arn ?? '';
  (document.getElementById('account-aws-external-id') as HTMLInputElement).value = account?.aws_external_id ?? '';
  (document.getElementById('account-aws-bastion-role-arn') as HTMLInputElement).value = account?.aws_role_arn ?? '';
  const wifRoleEl = document.getElementById('account-aws-wif-role-arn') as HTMLInputElement | null;
  if (wifRoleEl) wifRoleEl.value = account?.aws_role_arn ?? '';
  const wifTokenEl = document.getElementById('account-aws-wif-token-file') as HTMLInputElement | null;
  if (wifTokenEl) wifTokenEl.value = account?.aws_web_identity_token_file ?? '';
  (document.getElementById('account-aws-is-org-root') as HTMLInputElement).checked = account?.aws_is_org_root ?? false;

  updateAwsAuthModeFields(authMode, account?.aws_bastion_id);
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
 * Show/hide Azure auth mode sub-fields
 */
function updateAzureAuthModeFields(mode: string): void {
  const secretFields = document.getElementById('account-azure-secret-fields');
  const wifFields = document.getElementById('account-azure-wif-fields');
  secretFields?.classList.toggle('hidden', mode !== 'client_secret');
  wifFields?.classList.toggle('hidden', mode !== 'workload_identity_federation');
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
    name: (document.getElementById('account-name') as HTMLInputElement).value.trim(),
    description: (document.getElementById('account-description') as HTMLTextAreaElement).value.trim() || undefined,
    contact_email: (document.getElementById('account-contact-email') as HTMLInputElement).value.trim() || undefined,
    provider,
    external_id: (document.getElementById('account-external-id') as HTMLInputElement).value.trim(),
    enabled: (document.getElementById('account-enabled') as HTMLInputElement).checked
  };

  if (provider === 'aws') {
    const authMode = (document.getElementById('account-aws-auth-mode') as HTMLSelectElement).value;
    req.aws_auth_mode = authMode;
    req.aws_is_org_root = (document.getElementById('account-aws-is-org-root') as HTMLInputElement).checked;
    if (authMode === 'role_arn') {
      req.aws_role_arn = (document.getElementById('account-aws-role-arn') as HTMLInputElement).value.trim();
      req.aws_external_id = (document.getElementById('account-aws-external-id') as HTMLInputElement).value.trim() || undefined;
    } else if (authMode === 'bastion') {
      req.aws_bastion_id = (document.getElementById('account-aws-bastion-id') as HTMLSelectElement).value;
      req.aws_role_arn = (document.getElementById('account-aws-bastion-role-arn') as HTMLInputElement).value.trim();
    } else if (authMode === 'workload_identity_federation') {
      req.aws_role_arn = (document.getElementById('account-aws-wif-role-arn') as HTMLInputElement | null)?.value.trim();
      req.aws_web_identity_token_file = (document.getElementById('account-aws-wif-token-file') as HTMLInputElement | null)?.value.trim() || undefined;
    }
  } else if (provider === 'azure') {
    req.azure_subscription_id = req.external_id; // external_id IS the subscription ID for Azure
    req.azure_auth_mode = (document.getElementById('account-azure-auth-mode') as HTMLSelectElement | null)?.value || 'client_secret';
    req.azure_tenant_id = (document.getElementById('account-azure-tenant-id') as HTMLInputElement).value.trim();
    req.azure_client_id = (document.getElementById('account-azure-client-id') as HTMLInputElement).value.trim();
  } else if (provider === 'gcp') {
    req.gcp_project_id = req.external_id; // external_id IS the project ID for GCP
    req.gcp_auth_mode = (document.getElementById('account-gcp-auth-mode') as HTMLSelectElement | null)?.value || 'service_account_key';
    req.gcp_client_email = (document.getElementById('account-gcp-client-email') as HTMLInputElement | null)?.value.trim() ?? '';
  }

  return req;
}

/**
 * Build and save credentials from the account modal form (if filled)
 */
async function saveAccountCredentialsIfFilled(accountId: string, provider: AccountProvider): Promise<void> {
  if (provider === 'aws') {
    const authMode = (document.getElementById('account-aws-auth-mode') as HTMLSelectElement).value;
    if (authMode === 'access_keys') {
      const keyId = (document.getElementById('account-aws-access-key-id') as HTMLInputElement).value.trim();
      const secretKey = (document.getElementById('account-aws-secret-access-key') as HTMLInputElement).value;
      if (keyId && secretKey) {
        await api.saveAccountCredentials(accountId, {
          credential_type: 'aws_access_keys',
          payload: { access_key_id: keyId, secret_access_key: secretKey }
        });
      }
    }
  } else if (provider === 'azure') {
    const azureMode = (document.getElementById('account-azure-auth-mode') as HTMLSelectElement | null)?.value ?? 'client_secret';
    if (azureMode === 'managed_identity') {
      // No credential to store
    } else if (azureMode === 'workload_identity_federation') {
      const pem = (document.getElementById('account-azure-wif-private-key') as HTMLTextAreaElement | null)?.value.trim();
      if (pem) {
        await api.saveAccountCredentials(accountId, {
          credential_type: 'azure_wif_private_key',
          payload: { private_key_pem: pem }
        });
      }
    } else {
      const secret = (document.getElementById('account-azure-client-secret') as HTMLInputElement).value;
      if (secret) {
        await api.saveAccountCredentials(accountId, {
          credential_type: 'azure_client_secret',
          payload: { client_secret: secret }
        });
      }
    }
  } else if (provider === 'gcp') {
    const gcpMode = (document.getElementById('account-gcp-auth-mode') as HTMLSelectElement | null)?.value ?? 'service_account_key';
    if (gcpMode === 'application_default') {
      // No credential to store
    } else if (gcpMode === 'workload_identity_federation') {
      const config = (document.getElementById('account-gcp-wif-config') as HTMLTextAreaElement | null)?.value.trim();
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
      const jsonText = (document.getElementById('account-gcp-service-account-json') as HTMLTextAreaElement | null)?.value.trim() ?? '';
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
      alert(`Failed to save: ${(err as Error).message}`);
    }
    return;
  }

  const accountId = (document.getElementById('account-id') as HTMLInputElement).value;

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
      alert(`Account saved, but credentials could not be stored: ${(credErr as Error).message}\nPlease edit the account to re-enter credentials.`);
      return;
    }

    closeAccountModal();
    await loadAccountsForProvider(provider);
  } catch (err) {
    console.error('Failed to save account:', err);
    alert(`Failed to save account: ${(err as Error).message}`);
  }
}

/**
 * Trigger org account discovery from the management (org root) account
 */
async function handleDiscoverOrgAccounts(): Promise<void> {
  try {
    const result = await api.discoverOrgAccounts();
    alert(`Org discovery started: ${result.message || result.status}`);
    await loadAccountsForProvider('aws');
  } catch (err) {
    console.error('Org discovery failed:', err);
    alert(`Org discovery failed: ${(err as Error).message}`);
  }
}

/**
 * Set up settings event handlers
 */
export function setupSettingsHandlers(): void {
  // Provider checkbox handlers - toggle visibility of provider settings sections
  const awsCheck = document.getElementById('provider-aws') as HTMLInputElement | null;
  const azureCheck = document.getElementById('provider-azure') as HTMLInputElement | null;
  const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement | null;

  awsCheck?.addEventListener('change', () => updateProviderSettingsVisibility());
  azureCheck?.addEventListener('change', () => updateProviderSettingsVisibility());
  gcpCheck?.addEventListener('change', () => updateProviderSettingsVisibility());

  // Auto-collect checkbox - toggle schedule visibility
  const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement | null;
  autoCollect?.addEventListener('change', () => updateCollectionScheduleVisibility());

  // Global defaults - propagate to all services when changed
  const defaultTerm = document.getElementById('setting-default-term') as HTMLSelectElement | null;
  const defaultPayment = document.getElementById('setting-default-payment') as HTMLSelectElement | null;

  defaultTerm?.addEventListener('change', () => propagateTermToServices(defaultTerm.value));
  defaultPayment?.addEventListener('change', () => propagatePaymentToServices(defaultPayment.value));

  // Set up dirty-field tracking
  setupDirtyTracking();

  // Account management buttons
  document.getElementById('add-aws-account-btn')?.addEventListener('click', () => openAccountModal('aws'));
  document.getElementById('add-azure-account-btn')?.addEventListener('click', () => openAccountModal('azure'));
  document.getElementById('add-gcp-account-btn')?.addEventListener('click', () => openAccountModal('gcp'));

  // Org discovery button
  document.getElementById('discover-org-accounts-btn')?.addEventListener('click', () => void handleDiscoverOrgAccounts());

  // Account form submit handler
  document.getElementById('account-form')?.addEventListener('submit', (e) => void handleAccountFormSubmit(e));

  // Account modal close
  document.getElementById('close-account-modal-btn')?.addEventListener('click', closeAccountModal);

  // AWS auth mode change handler
  const awsAuthMode = document.getElementById('account-aws-auth-mode') as HTMLSelectElement | null;
  awsAuthMode?.addEventListener('change', () => updateAwsAuthModeFields(awsAuthMode.value));

  // Azure auth mode change handler
  const azureAuthMode = document.getElementById('account-azure-auth-mode') as HTMLSelectElement | null;
  azureAuthMode?.addEventListener('change', () => updateAzureAuthModeFields(azureAuthMode.value));

  // GCP auth mode change handler
  const gcpAuthMode = document.getElementById('account-gcp-auth-mode') as HTMLSelectElement | null;
  gcpAuthMode?.addEventListener('change', () => updateGCPAuthModeFields(gcpAuthMode.value));

  // Close account modal when clicking outside
  const accountModal = document.getElementById('account-modal');
  accountModal?.addEventListener('click', (e) => {
    if (e.target === accountModal) closeAccountModal();
  });
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
    scheduleRow.style.display = autoCollect?.checked ? 'flex' : 'none';
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
}

/**
 * Save global settings
 */
export async function saveGlobalSettings(e: Event): Promise<void> {
  e.preventDefault();

  const enabledProviders: api.Provider[] = [];
  if ((document.getElementById('provider-aws') as HTMLInputElement | null)?.checked) enabledProviders.push('aws');
  if ((document.getElementById('provider-azure') as HTMLInputElement | null)?.checked) enabledProviders.push('azure');
  if ((document.getElementById('provider-gcp') as HTMLInputElement | null)?.checked) enabledProviders.push('gcp');

  const settings: api.Config = {
    enabled_providers: enabledProviders,
    notification_email: (document.getElementById('setting-notification-email') as HTMLInputElement | null)?.value || '',
    auto_collect: (document.getElementById('setting-auto-collect') as HTMLInputElement | null)?.checked ?? true,
    collection_schedule: (document.getElementById('setting-collection-schedule') as HTMLSelectElement | null)?.value || 'daily',
    default_term: parseInt((document.getElementById('setting-default-term') as HTMLSelectElement | null)?.value || '3', 10),
    default_payment: ((document.getElementById('setting-default-payment') as HTMLSelectElement | null)?.value || 'all-upfront') as api.PaymentOption,
    default_coverage: parseInt((document.getElementById('setting-default-coverage') as HTMLInputElement | null)?.value || '80', 10),
    notification_days_before: parseInt((document.getElementById('setting-notification-days') as HTMLInputElement | null)?.value || '3', 10)
  };

  try {
    await api.updateConfig(settings);

    const serviceSaves = SERVICE_FIELDS.map(({ provider, service, termId, paymentId }) => {
      const term = parseInt((document.getElementById(termId) as HTMLSelectElement)?.value || '3', 10);
      const payment = paymentId
        ? ((document.getElementById(paymentId) as HTMLSelectElement)?.value || 'all-upfront')
        : settings.default_payment;
      const base = loadedServiceConfigs.find(s => s.provider === provider && s.service === service);
      const cfg: api.ServiceConfig = {
        provider,
        service,
        enabled: base?.enabled ?? true,
        term,
        payment,
        coverage: settings.default_coverage,
      };
      return api.updateServiceConfig(provider, service, cfg);
    });
    await Promise.all(serviceSaves);

    snapshotAllFields();
    updateDirtyMarkers();
    alert('Settings saved successfully');
  } catch (error) {
    console.error('Failed to save settings:', error);
    const err = error as Error;
    alert(`Failed to save settings: ${err.message}`);
  }
}

/**
 * Reset settings to defaults
 */
export function resetSettings(): void {
  if (!confirm('Are you sure you want to reset all settings to defaults?')) return;

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
}

/**
 * Copy text from an element to clipboard
 */
export function copyToClipboard(elementId: string): void {
  const element = document.getElementById(elementId);
  if (!element) return;

  const text = element.textContent || '';
  navigator.clipboard.writeText(text).then(() => {
    // Show feedback
    const btn = element.nextElementSibling as HTMLButtonElement;
    if (btn) {
      const originalContent = btn.innerHTML;
      btn.innerHTML = '<span class="copy-icon">&#10003;</span>';
      btn.classList.add('copied');
      setTimeout(() => {
        btn.innerHTML = originalContent;
        btn.classList.remove('copied');
      }, 2000);
    }
  }).catch(err => {
    console.error('Failed to copy:', err);
  });
}
