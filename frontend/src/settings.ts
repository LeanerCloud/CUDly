/**
 * Settings module for CUDly
 */

import * as api from './api';

type AccountProvider = 'aws' | 'azure' | 'gcp';

// Track which provider's account is being edited (for the modal)
let accountModalProvider: AccountProvider = 'aws';

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
    renderAccountsList(container, accounts, provider);
  } catch {
    container.textContent = 'Failed to load accounts.';
  }
}

/**
 * Render accounts list into a container element
 */
function renderAccountsList(container: HTMLElement, accounts: api.CloudAccount[], provider: AccountProvider): void {
  if (!accounts || accounts.length === 0) {
    container.textContent = 'No accounts configured.';
    return;
  }
  container.textContent = '';
  accounts.forEach(account => {
    const row = document.createElement('div');
    row.className = 'account-row';

    const info = document.createElement('span');
    info.className = 'account-info';
    info.textContent = `${account.name} (${account.external_id})${account.enabled ? '' : ' [disabled]'}`;
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
function openAccountModal(provider: AccountProvider, account?: api.CloudAccount): void {
  accountModalProvider = provider;
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
    (document.getElementById('account-azure-tenant-id') as HTMLInputElement).value = account?.azure_tenant_id ?? '';
    (document.getElementById('account-azure-client-id') as HTMLInputElement).value = account?.azure_client_id ?? '';
    (document.getElementById('account-azure-client-secret') as HTMLInputElement).value = '';
  }

  modal.classList.remove('hidden');
}

/**
 * Populate AWS-specific fields in the account modal
 */
function populateAwsAccountFields(account?: api.CloudAccount): void {
  const authMode = (account?.aws_auth_mode ?? 'access_keys') as string;
  const authModeSelect = document.getElementById('account-aws-auth-mode') as HTMLSelectElement | null;
  if (authModeSelect) authModeSelect.value = authMode;

  (document.getElementById('account-aws-access-key-id') as HTMLInputElement).value = '';
  (document.getElementById('account-aws-secret-access-key') as HTMLInputElement).value = '';
  (document.getElementById('account-aws-role-arn') as HTMLInputElement).value = account?.aws_role_arn ?? '';
  (document.getElementById('account-aws-external-id') as HTMLInputElement).value = account?.aws_external_id ?? '';
  (document.getElementById('account-aws-bastion-role-arn') as HTMLInputElement).value = account?.aws_role_arn ?? '';
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

  keysFields?.classList.toggle('hidden', mode !== 'access_keys');
  roleFields?.classList.toggle('hidden', mode !== 'role_arn');
  bastionFields?.classList.toggle('hidden', mode !== 'bastion');

  if (mode === 'bastion') {
    void populateBastionAccountDropdown(bastionId);
  }
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
    }
  } else if (provider === 'azure') {
    req.azure_subscription_id = req.external_id; // external_id IS the subscription ID for Azure
    req.azure_tenant_id = (document.getElementById('account-azure-tenant-id') as HTMLInputElement).value.trim();
    req.azure_client_id = (document.getElementById('account-azure-client-id') as HTMLInputElement).value.trim();
  } else if (provider === 'gcp') {
    req.gcp_project_id = req.external_id; // external_id IS the project ID for GCP
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
    const secret = (document.getElementById('account-azure-client-secret') as HTMLInputElement).value;
    if (secret) {
      await api.saveAccountCredentials(accountId, {
        credential_type: 'azure_client_secret',
        payload: { client_secret: secret }
      });
    }
  } else if (provider === 'gcp') {
    const jsonText = (document.getElementById('account-gcp-service-account-json') as HTMLTextAreaElement).value.trim();
    if (jsonText) {
      await api.saveAccountCredentials(accountId, {
        credential_type: 'gcp_service_account',
        payload: JSON.parse(jsonText) as Record<string, unknown>
      });
    }
  }
}

/**
 * Handle account form submission
 */
async function handleAccountFormSubmit(e: Event): Promise<void> {
  e.preventDefault();
  const provider = accountModalProvider;
  const accountId = (document.getElementById('account-id') as HTMLInputElement).value;
  const req = buildAccountRequest(provider);

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

  // Credential configure button handlers
  const azureConfigBtn = document.getElementById('azure-configure-btn');
  const gcpConfigBtn = document.getElementById('gcp-configure-btn');

  azureConfigBtn?.addEventListener('click', showAzureCredsModal);
  gcpConfigBtn?.addEventListener('click', showGCPCredsModal);

  // Azure credentials form handler
  const azureCredsForm = document.getElementById('azure-creds-form');
  azureCredsForm?.addEventListener('submit', handleAzureCredsSave);

  // GCP credentials form handler
  const gcpCredsForm = document.getElementById('gcp-creds-form');
  gcpCredsForm?.addEventListener('submit', handleGCPCredsSave);

  // Close modals when clicking outside
  const azureModal = document.getElementById('azure-creds-modal');
  const gcpModal = document.getElementById('gcp-creds-modal');

  azureModal?.addEventListener('click', (e) => {
    if (e.target === azureModal) closeAzureCredsModal();
  });
  gcpModal?.addEventListener('click', (e) => {
    if (e.target === gcpModal) closeGCPCredsModal();
  });

  // Per-section save buttons — each triggers a full form submit
  ['save-general-btn', 'save-defaults-btn', 'save-aws-btn', 'save-azure-btn', 'save-gcp-btn'].forEach(id => {
    document.getElementById(id)?.addEventListener('click', () => {
      const form = document.getElementById('global-settings-form') as HTMLFormElement | null;
      form?.requestSubmit();
    });
  });

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
  const awsCheck = document.getElementById('provider-aws') as HTMLInputElement | null;
  const azureCheck = document.getElementById('provider-azure') as HTMLInputElement | null;
  const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement | null;

  const awsSettings = document.getElementById('aws-settings');
  const azureSettings = document.getElementById('azure-settings');
  const gcpSettings = document.getElementById('gcp-settings');

  if (awsSettings) awsSettings.classList.toggle('hidden', !awsCheck?.checked);
  if (azureSettings) azureSettings.classList.toggle('hidden', !azureCheck?.checked);
  if (gcpSettings) gcpSettings.classList.toggle('hidden', !gcpCheck?.checked);
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
  // AWS services
  const awsTermSelects = [
    'aws-ec2-term',
    'aws-rds-term',
    'aws-elasticache-term',
    'aws-opensearch-term',
    'aws-redshift-term',
    'aws-savingsplans-term'
  ];

  // Azure services
  const azureTermSelects = [
    'azure-vm-term',
    'azure-sql-term',
    'azure-cosmos-term'
  ];

  // GCP services
  const gcpTermSelects = [
    'gcp-compute-term',
    'gcp-sql-term'
  ];

  [...awsTermSelects, ...azureTermSelects, ...gcpTermSelects].forEach(id => {
    const select = document.getElementById(id) as HTMLSelectElement | null;
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
  // Only AWS services have payment options
  const awsPaymentSelects = [
    'aws-ec2-payment',
    'aws-rds-payment',
    'aws-elasticache-payment',
    'aws-opensearch-payment',
    'aws-redshift-payment',
    'aws-savingsplans-payment'
  ];

  awsPaymentSelects.forEach(id => {
    const select = document.getElementById(id) as HTMLSelectElement | null;
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

    if (data.credentials) {
      const azureStatus = document.getElementById('azure-creds-status');
      const gcpStatus = document.getElementById('gcp-creds-status');

      if (azureStatus) {
        azureStatus.textContent = data.credentials.azure_configured ? 'Configured' : 'Not Configured';
        azureStatus.classList.toggle('configured', data.credentials.azure_configured === true);
      }
      if (gcpStatus) {
        gcpStatus.textContent = data.credentials.gcp_configured ? 'Configured' : 'Not Configured';
        gcpStatus.classList.toggle('configured', data.credentials.gcp_configured === true);
      }
    }

    if (loadingEl) loadingEl.classList.add('hidden');
    if (formEl) formEl.classList.remove('hidden');

    // Establish the clean baseline for dirty tracking
    snapshotAllFields();
    updateDirtyMarkers();

    // Load accounts for all providers (non-blocking)
    void loadAccountsForProvider('aws');
    void loadAccountsForProvider('azure');
    void loadAccountsForProvider('gcp');
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
      const cfg: api.ServiceConfig = { ...base, provider, service, enabled: base?.enabled ?? true, term, payment, coverage: base?.coverage ?? settings.default_coverage };
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
 * Show Azure credentials modal
 */
export function showAzureCredsModal(): void {
  const modal = document.getElementById('azure-creds-modal');
  const errorEl = document.getElementById('azure-creds-error');
  if (modal) modal.classList.remove('hidden');
  if (errorEl) errorEl.classList.add('hidden');
}

/**
 * Close Azure credentials modal
 */
export function closeAzureCredsModal(): void {
  const modal = document.getElementById('azure-creds-modal');
  if (modal) modal.classList.add('hidden');
  // Clear form
  const form = document.getElementById('azure-creds-form') as HTMLFormElement | null;
  form?.reset();
}

/**
 * Show GCP credentials modal
 */
export function showGCPCredsModal(): void {
  const modal = document.getElementById('gcp-creds-modal');
  const errorEl = document.getElementById('gcp-creds-error');
  if (modal) modal.classList.remove('hidden');
  if (errorEl) errorEl.classList.add('hidden');
}

/**
 * Close GCP credentials modal
 */
export function closeGCPCredsModal(): void {
  const modal = document.getElementById('gcp-creds-modal');
  if (modal) modal.classList.add('hidden');
  // Clear form
  const form = document.getElementById('gcp-creds-form') as HTMLFormElement | null;
  form?.reset();
}

/**
 * Handle Azure credentials save
 */
async function handleAzureCredsSave(e: Event): Promise<void> {
  e.preventDefault();
  const errorEl = document.getElementById('azure-creds-error');

  const tenantId = (document.getElementById('azure-tenant-id') as HTMLInputElement)?.value.trim();
  const clientId = (document.getElementById('azure-client-id') as HTMLInputElement)?.value.trim();
  const clientSecret = (document.getElementById('azure-client-secret') as HTMLInputElement)?.value;
  const subscriptionId = (document.getElementById('azure-subscription-id') as HTMLInputElement)?.value.trim();

  if (!tenantId || !clientId || !clientSecret || !subscriptionId) {
    if (errorEl) {
      errorEl.textContent = 'All fields are required';
      errorEl.classList.remove('hidden');
    }
    return;
  }

  try {
    await api.saveAzureCredentials({
      tenant_id: tenantId,
      client_id: clientId,
      client_secret: clientSecret,
      subscription_id: subscriptionId
    });

    // Update status
    const statusEl = document.getElementById('azure-creds-status');
    if (statusEl) {
      statusEl.textContent = 'Configured';
      statusEl.classList.add('configured');
    }

    closeAzureCredsModal();
    alert('Azure credentials saved successfully');
  } catch (error) {
    console.error('Failed to save Azure credentials:', error);
    if (errorEl) {
      const err = error as Error;
      errorEl.textContent = `Failed to save: ${err.message}`;
      errorEl.classList.remove('hidden');
    }
  }
}

/**
 * Handle GCP credentials save
 */
async function handleGCPCredsSave(e: Event): Promise<void> {
  e.preventDefault();
  const errorEl = document.getElementById('gcp-creds-error');

  const jsonText = (document.getElementById('gcp-service-account-json') as HTMLTextAreaElement)?.value.trim();

  if (!jsonText) {
    if (errorEl) {
      errorEl.textContent = 'Service account JSON is required';
      errorEl.classList.remove('hidden');
    }
    return;
  }

  // Parse and validate JSON
  let credentials: api.GCPCredentials;
  try {
    credentials = JSON.parse(jsonText) as api.GCPCredentials;
  } catch {
    if (errorEl) {
      errorEl.textContent = 'Invalid JSON format';
      errorEl.classList.remove('hidden');
    }
    return;
  }

  // Validate required fields
  if (!credentials.type || !credentials.project_id || !credentials.private_key || !credentials.client_email) {
    if (errorEl) {
      errorEl.textContent = 'Missing required fields: type, project_id, private_key, client_email';
      errorEl.classList.remove('hidden');
    }
    return;
  }

  try {
    await api.saveGCPCredentials(credentials);

    // Update status
    const statusEl = document.getElementById('gcp-creds-status');
    if (statusEl) {
      statusEl.textContent = 'Configured';
      statusEl.classList.add('configured');
    }

    closeGCPCredsModal();
    alert('GCP credentials saved successfully');
  } catch (error) {
    console.error('Failed to save GCP credentials:', error);
    if (errorEl) {
      const err = error as Error;
      errorEl.textContent = `Failed to save: ${err.message}`;
      errorEl.classList.remove('hidden');
    }
  }
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
