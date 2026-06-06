/**
 * Settings — account management tests
 * DOM is built with createElement to avoid innerHTML in source.
 */
import {
  loadAccountsForProvider,
  loadOverridesPanel,
  openOverrideModal,
  setupSettingsHandlers,
  setGlobalDefaultsForTest,
} from '../settings';

jest.mock('../api', () => ({
  getConfig: jest.fn(),
  updateConfig: jest.fn(),
  listAccounts: jest.fn(),
  createAccount: jest.fn(),
  updateAccount: jest.fn(),
  deleteAccount: jest.fn(),
  testAccountCredentials: jest.fn(),
  saveAccountCredentials: jest.fn(),
  listAccountServiceOverrides: jest.fn(),
  saveAccountServiceOverride: jest.fn(),
  deleteAccountServiceOverride: jest.fn(),
  // Issue #606 — Cancel-All-Then-Delete UX calls cancelPurchase per
  // pending execution before retrying the account delete.
  cancelPurchase: jest.fn()
}));

const mockShowToast = jest.fn<{ dismiss: () => void }, [unknown]>(() => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts)
}));

const mockConfirmDialog = jest.fn<Promise<boolean>, [unknown]>();
jest.mock('../confirmDialog', () => ({
  confirmDialog: (opts: unknown) => mockConfirmDialog(opts)
}));

// Mock the recommendations module to keep the settings tests focused on
// the settings flows. Issue #196 wires settings.ts to call loadRecommendations
// after override mutations; without this mock the test bundle would pull in
// the full recommendations page module (~2k LOC) and its DOM expectations.
const mockLoadRecommendations = jest.fn<Promise<void>, []>().mockResolvedValue(undefined);
jest.mock('../recommendations', () => ({
  loadRecommendations: () => mockLoadRecommendations()
}));

import * as api from '../api';

// ---------------------------------------------------------------------------
// DOM helpers
// ---------------------------------------------------------------------------

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Record<string, string> = {},
  id?: string
): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (id) e.id = id;
  Object.entries(attrs).forEach(([k, v]) => e.setAttribute(k, v));
  return e;
}

function input(id: string, type = 'text'): HTMLInputElement {
  return el('input', { type }, id) as HTMLInputElement;
}

function select(id: string, values: string[]): HTMLSelectElement {
  const s = el('select', {}, id) as HTMLSelectElement;
  values.forEach(v => {
    const opt = document.createElement('option');
    opt.value = v;
    opt.textContent = v;
    s.appendChild(opt);
  });
  return s;
}

function div(id: string, cls = ''): HTMLDivElement {
  const d = el('div', {}, id) as HTMLDivElement;
  if (cls) d.className = cls;
  return d;
}

function btn(id: string): HTMLButtonElement {
  return el('button', {}, id) as HTMLButtonElement;
}

/** Build the account-management DOM needed for these tests. */
function buildAccountsDOM(): void {
  document.body.replaceChildren();

  // Account list containers
  document.body.appendChild(div('aws-accounts-list'));
  document.body.appendChild(div('azure-accounts-list'));
  document.body.appendChild(div('gcp-accounts-list'));

  // "Add account" buttons
  document.body.appendChild(btn('add-aws-account-btn'));
  document.body.appendChild(btn('add-azure-account-btn'));
  document.body.appendChild(btn('add-gcp-account-btn'));

  // ── Account modal ──────────────────────────────────────────
  const modal = div('account-modal', 'hidden');

  const title = el('h3', {}, 'account-modal-title');
  modal.appendChild(title);

  const form = el('form', {}, 'account-form');

  form.appendChild(input('account-id', 'hidden'));
  form.appendChild(input('account-provider', 'hidden'));
  form.appendChild(input('account-name'));
  const desc = el('textarea', {}, 'account-description') as HTMLTextAreaElement;
  form.appendChild(desc);
  form.appendChild(input('account-contact-email', 'email'));
  form.appendChild(input('account-external-id'));
  const enabledCb = input('account-enabled', 'checkbox');
  enabledCb.checked = true;
  form.appendChild(enabledCb);

  // AWS fields
  const awsFields = div('account-aws-fields');
  const authModeSelect = select('account-aws-auth-mode', ['access_keys', 'role_arn', 'bastion']);
  awsFields.appendChild(authModeSelect);

  const keysDiv = div('account-aws-keys-fields');
  keysDiv.appendChild(input('account-aws-access-key-id'));
  keysDiv.appendChild(input('account-aws-secret-access-key', 'password'));
  awsFields.appendChild(keysDiv);

  const roleDiv = div('account-aws-role-fields', 'hidden');
  roleDiv.appendChild(input('account-aws-role-arn'));
  roleDiv.appendChild(input('account-aws-external-id'));
  awsFields.appendChild(roleDiv);

  const bastionDiv = div('account-aws-bastion-fields', 'hidden');
  bastionDiv.appendChild(select('account-aws-bastion-id', ['']));
  bastionDiv.appendChild(input('account-aws-bastion-role-arn'));
  bastionDiv.appendChild(input('account-aws-external-id-bastion'));
  bastionDiv.appendChild(el('pre', {}, 'account-aws-trust-policy-bastion'));
  bastionDiv.appendChild(el('small', {}, 'account-aws-trust-policy-bastion-hint'));
  awsFields.appendChild(bastionDiv);

  const orgRootCb = input('account-aws-is-org-root', 'checkbox');
  awsFields.appendChild(orgRootCb);
  form.appendChild(awsFields);

  // Azure fields
  const azureFields = div('account-azure-fields', 'hidden');
  azureFields.appendChild(input('account-azure-tenant-id'));
  azureFields.appendChild(input('account-azure-client-id'));
  azureFields.appendChild(input('account-azure-client-secret', 'password'));
  form.appendChild(azureFields);

  // GCP fields
  const gcpFields = div('account-gcp-fields', 'hidden');
  const gcpJson = el('textarea', {}, 'account-gcp-service-account-json') as HTMLTextAreaElement;
  gcpFields.appendChild(gcpJson);
  form.appendChild(gcpFields);

  modal.appendChild(form);
  modal.appendChild(btn('close-account-modal-btn'));
  document.body.appendChild(modal);

  // ── Override modal (issue #104, bulk multi-service mode issue #119) ───
  const overrideModal = div('override-modal', 'modal hidden');
  const overrideForm = el('form', {}, 'override-form');
  overrideForm.appendChild(input('override-account-id', 'hidden'));
  overrideForm.appendChild(input('override-provider', 'hidden'));
  // Single-service row (default mode).
  const singleRow = div('override-single-service-row');
  singleRow.appendChild(select('override-service', []));
  overrideForm.appendChild(singleRow);
  // Bulk-mode toggle (issue #119).
  const bulkToggle = input('override-bulk-toggle', 'checkbox');
  overrideForm.appendChild(bulkToggle);
  // Bulk services container (hidden by default).
  const bulkServicesDiv = div('override-bulk-services', 'hidden');
  const bulkServicesList = div('override-bulk-services-list');
  bulkServicesDiv.appendChild(bulkServicesList);
  overrideForm.appendChild(bulkServicesDiv);
  overrideForm.appendChild(select('override-term', ['', '1', '3']));
  overrideForm.appendChild(select('override-payment', ['', 'no-upfront', 'partial-upfront', 'all-upfront']));
  overrideForm.appendChild(input('override-coverage', 'number'));
  const overrideErr = el('p', {}, 'override-form-error');
  overrideForm.appendChild(overrideErr);
  const overrideSubmit = el('button', { type: 'submit' }, 'override-submit-btn') as HTMLButtonElement;
  overrideSubmit.textContent = 'Save override';
  overrideForm.appendChild(overrideSubmit);
  overrideModal.appendChild(overrideForm);
  overrideModal.appendChild(btn('close-override-modal-btn'));
  document.body.appendChild(overrideModal);

  // ── Per-account overrides modal (issue #122) ───────────────
  const accountOverridesModal = div('account-overrides-modal', 'modal hidden');
  const accountOverridesTitle = el('h2', {}, 'account-overrides-modal-title');
  accountOverridesTitle.textContent = 'Service overrides';
  accountOverridesModal.appendChild(accountOverridesTitle);
  accountOverridesModal.appendChild(div('account-overrides-modal-body'));
  accountOverridesModal.appendChild(btn('close-account-overrides-modal-btn'));
  document.body.appendChild(accountOverridesModal);
}

// ---------------------------------------------------------------------------
// loadAccountsForProvider
// ---------------------------------------------------------------------------

describe('loadAccountsForProvider', () => {
  beforeEach(() => {
    buildAccountsDOM();
    jest.clearAllMocks();
  });

  test('renders account rows when API returns accounts', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'id1', name: 'Prod',    external_id: '111111111111', enabled: true },
      { id: 'id2', name: 'Dev',     external_id: '222222222222', enabled: false }
    ]);

    await loadAccountsForProvider('aws');

    const container = document.getElementById('aws-accounts-list')!;
    // P3 replaced inline .account-row divs with an accounts-table.
    const table = container.querySelector('table.accounts-table');
    expect(table).not.toBeNull();
    expect(table?.querySelectorAll('tbody tr')).toHaveLength(2);
    expect(container.textContent).toContain('Prod');
    expect(container.textContent).toContain('Disabled');
  });

  test('renders "No accounts configured." for empty list', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([]);

    await loadAccountsForProvider('aws');

    // P3 wraps the empty copy in a dedicated <p.accounts-empty>.
    const container = document.getElementById('aws-accounts-list')!;
    expect(container.querySelector('.accounts-empty')?.textContent).toBe('No accounts configured.');
  });

  test('renders error message on API failure', async () => {
    (api.listAccounts as jest.Mock).mockRejectedValue(new Error('Network error'));

    await loadAccountsForProvider('azure');

    expect(document.getElementById('azure-accounts-list')!.textContent).toBe('Failed to load accounts.');
  });

  test('does nothing when container element is missing', async () => {
    document.body.replaceChildren();
    (api.listAccounts as jest.Mock).mockResolvedValue([]);

    await expect(loadAccountsForProvider('aws')).resolves.toBeUndefined();
    expect(api.listAccounts).not.toHaveBeenCalled();
  });

  test('passes provider filter to listAccounts', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([]);

    await loadAccountsForProvider('gcp');

    expect(api.listAccounts).toHaveBeenCalledWith({ provider: 'gcp' });
  });

  test('renders status-chip row with counts and All active by default', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: '1', name: 'Prod', provider: 'aws', external_id: '111', enabled: true },
      { id: '2', name: 'Stage', provider: 'aws', external_id: '222', enabled: false },
      { id: '3', name: 'Dev', provider: 'aws', external_id: '333', enabled: true },
    ]);

    await loadAccountsForProvider('aws');

    const container = document.getElementById('aws-accounts-list')!;
    const chips = Array.from(container.querySelectorAll('.status-chip'));
    expect(chips).toHaveLength(3);
    expect(chips[0]!.textContent).toBe('All (3)');
    expect(chips[1]!.textContent).toBe('Active (2)');
    expect(chips[2]!.textContent).toBe('Disabled (1)');
    expect(chips[0]!.classList.contains('active')).toBe(true);
    expect(chips[1]!.classList.contains('active')).toBe(false);
    expect(container.querySelectorAll('tbody tr')).toHaveLength(3);
  });

  test('clicking Active chip filters out disabled rows', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: '1', name: 'Prod', provider: 'aws', external_id: '111', enabled: true },
      { id: '2', name: 'Stage', provider: 'aws', external_id: '222', enabled: false },
    ]);

    await loadAccountsForProvider('aws');
    const container = document.getElementById('aws-accounts-list')!;
    const chips = Array.from(container.querySelectorAll('.status-chip')) as HTMLButtonElement[];
    chips[1]!.click();

    const rows = Array.from(container.querySelectorAll('tbody tr'));
    expect(rows).toHaveLength(1);
    expect(rows[0]!.textContent).toContain('Prod');
    expect(rows[0]!.textContent).not.toContain('Stage');
    const afterChips = Array.from(container.querySelectorAll('.status-chip')) as HTMLButtonElement[];
    expect(afterChips[1]!.classList.contains('active')).toBe(true);
  });

  test('clicking All restores full row set after a filter', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: '1', name: 'Prod', provider: 'aws', external_id: '111', enabled: true },
      { id: '2', name: 'Stage', provider: 'aws', external_id: '222', enabled: false },
    ]);

    await loadAccountsForProvider('aws');
    const container = document.getElementById('aws-accounts-list')!;
    const chips = () => Array.from(container.querySelectorAll('.status-chip')) as HTMLButtonElement[];
    chips()[2]!.click();
    expect(container.querySelectorAll('tbody tr')).toHaveLength(1);
    chips()[0]!.click();
    expect(container.querySelectorAll('tbody tr')).toHaveLength(2);
  });
});

// ---------------------------------------------------------------------------
// Account modal (opened via setupSettingsHandlers)
// ---------------------------------------------------------------------------

describe('Account modal via setupSettingsHandlers', () => {
  beforeEach(() => {
    buildAccountsDOM();
    jest.clearAllMocks();
    (api.listAccounts as jest.Mock).mockResolvedValue([]);
    setupSettingsHandlers();
  });

  test('add-aws-account-btn opens modal as "Add Account"', () => {
    document.getElementById('add-aws-account-btn')!.click();

    expect(document.getElementById('account-modal')!.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('account-modal-title')!.textContent).toBe('Add Account');
  });

  test('add-azure-account-btn shows azure fields, hides aws fields', () => {
    document.getElementById('add-azure-account-btn')!.click();

    expect(document.getElementById('account-modal')!.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('account-azure-fields')!.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('account-aws-fields')!.classList.contains('hidden')).toBe(true);
  });

  test('add-gcp-account-btn shows gcp fields, hides aws fields', () => {
    document.getElementById('add-gcp-account-btn')!.click();

    expect(document.getElementById('account-gcp-fields')!.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('account-aws-fields')!.classList.contains('hidden')).toBe(true);
  });

  test('close button hides account modal', () => {
    document.getElementById('add-aws-account-btn')!.click();
    document.getElementById('close-account-modal-btn')!.click();
    expect(document.getElementById('account-modal')!.classList.contains('hidden')).toBe(true);
  });

  test('clicking modal backdrop closes it', () => {
    document.getElementById('add-aws-account-btn')!.click();
    const modal = document.getElementById('account-modal')!;

    const ev = new MouseEvent('click', { bubbles: true });
    Object.defineProperty(ev, 'target', { value: modal });
    modal.dispatchEvent(ev);

    expect(modal.classList.contains('hidden')).toBe(true);
  });

  test('switching auth mode to role_arn shows role fields, hides key fields', () => {
    document.getElementById('add-aws-account-btn')!.click();

    const authMode = document.getElementById('account-aws-auth-mode') as HTMLSelectElement;
    authMode.value = 'role_arn';
    authMode.dispatchEvent(new Event('change'));

    expect(document.getElementById('account-aws-keys-fields')!.classList.contains('hidden')).toBe(true);
    expect(document.getElementById('account-aws-role-fields')!.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('account-aws-bastion-fields')!.classList.contains('hidden')).toBe(true);
  });

  test('switching auth mode back to access_keys shows key fields', () => {
    document.getElementById('add-aws-account-btn')!.click();

    const authMode = document.getElementById('account-aws-auth-mode') as HTMLSelectElement;
    authMode.value = 'role_arn';
    authMode.dispatchEvent(new Event('change'));

    authMode.value = 'access_keys';
    authMode.dispatchEvent(new Event('change'));

    expect(document.getElementById('account-aws-keys-fields')!.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('account-aws-role-fields')!.classList.contains('hidden')).toBe(true);
  });

  test('add-aws-account-btn auto-generates the External ID (issue #18)', () => {
    document.getElementById('add-aws-account-btn')!.click();

    const extID = document.getElementById('account-aws-external-id') as HTMLInputElement;
    // UUIDs are 36 chars with dashes (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx);
    // the test fixture doesn't set a readonly attribute (the production
    // index.html does), so we only assert on the generated value here.
    expect(extID.value).not.toBe('');
    expect(extID.value.length).toBeGreaterThanOrEqual(16);
  });
});

// ---------------------------------------------------------------------------
// Account form submit
// ---------------------------------------------------------------------------

describe('Account form submit', () => {
  beforeEach(() => {
    buildAccountsDOM();
    jest.clearAllMocks();
    (api.listAccounts as jest.Mock).mockResolvedValue([]);
    setupSettingsHandlers();
  });

  test('creates new account when account-id is empty', async () => {
    (api.createAccount as jest.Mock).mockResolvedValue({
      id: 'new-id', name: 'Acct', provider: 'aws', external_id: '123456789012', enabled: true
    });

    document.getElementById('add-aws-account-btn')!.click();
    (document.getElementById('account-name') as HTMLInputElement).value = 'Acct';
    (document.getElementById('account-external-id') as HTMLInputElement).value = '123456789012';

    document.getElementById('account-form')!.dispatchEvent(new Event('submit'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.createAccount).toHaveBeenCalled();
    expect(api.updateAccount).not.toHaveBeenCalled();
    expect(api.listAccounts).toHaveBeenCalled(); // list refreshed
  });

  test('updates existing account when account-id is set', async () => {
    (api.updateAccount as jest.Mock).mockResolvedValue({
      id: 'existing-id', name: 'Updated', provider: 'aws', external_id: '123456789012', enabled: true
    });

    document.getElementById('add-aws-account-btn')!.click();
    (document.getElementById('account-id') as HTMLInputElement).value = 'existing-id';
    (document.getElementById('account-name') as HTMLInputElement).value = 'Updated';
    (document.getElementById('account-external-id') as HTMLInputElement).value = '123456789012';

    document.getElementById('account-form')!.dispatchEvent(new Event('submit'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.updateAccount).toHaveBeenCalledWith('existing-id', expect.any(Object));
    expect(api.createAccount).not.toHaveBeenCalled();
  });

  test('saves AWS access_key credentials when key fields are filled', async () => {
    (api.createAccount as jest.Mock).mockResolvedValue({
      id: 'new-id', name: 'Acct', provider: 'aws', external_id: '123456789012', enabled: true
    });
    (api.saveAccountCredentials as jest.Mock).mockResolvedValue(undefined);

    document.getElementById('add-aws-account-btn')!.click();
    (document.getElementById('account-name') as HTMLInputElement).value = 'Acct';
    (document.getElementById('account-external-id') as HTMLInputElement).value = '123456789012';
    (document.getElementById('account-aws-auth-mode') as HTMLSelectElement).value = 'access_keys';
    (document.getElementById('account-aws-access-key-id') as HTMLInputElement).value = 'AKID';
    (document.getElementById('account-aws-secret-access-key') as HTMLInputElement).value = 'SECRET';

    document.getElementById('account-form')!.dispatchEvent(new Event('submit'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountCredentials).toHaveBeenCalledWith('new-id', {
      credential_type: 'aws_access_keys',
      payload: { access_key_id: 'AKID', secret_access_key: 'SECRET' }
    });
  });

  test('skips credential save when key fields are empty', async () => {
    (api.createAccount as jest.Mock).mockResolvedValue({
      id: 'new-id', name: 'Acct', provider: 'aws', external_id: '123456789012', enabled: true
    });

    document.getElementById('add-aws-account-btn')!.click();
    (document.getElementById('account-name') as HTMLInputElement).value = 'Acct';
    (document.getElementById('account-external-id') as HTMLInputElement).value = '123456789012';
    (document.getElementById('account-aws-auth-mode') as HTMLSelectElement).value = 'access_keys';
    // leave key fields empty

    document.getElementById('account-form')!.dispatchEvent(new Event('submit'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountCredentials).not.toHaveBeenCalled();
  });

  test('shows alert on create failure', async () => {
    (api.createAccount as jest.Mock).mockRejectedValue(new Error('Server error'));
    mockShowToast.mockClear();

    document.getElementById('add-aws-account-btn')!.click();
    (document.getElementById('account-name') as HTMLInputElement).value = 'Acct';
    (document.getElementById('account-external-id') as HTMLInputElement).value = '123456789012';

    document.getElementById('account-form')!.dispatchEvent(new Event('submit'));
    await new Promise(r => setTimeout(r, 0));

    expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({
      message: expect.stringContaining('Failed to save account'),
      kind: 'error'
    }));
  });

  test('saves azure credentials when secret field is filled', async () => {
    (api.createAccount as jest.Mock).mockResolvedValue({
      id: 'az-id', name: 'AzureAcct', provider: 'azure', external_id: 'sub-id', enabled: true
    });
    (api.saveAccountCredentials as jest.Mock).mockResolvedValue(undefined);

    document.getElementById('add-azure-account-btn')!.click();
    (document.getElementById('account-name') as HTMLInputElement).value = 'AzureAcct';
    (document.getElementById('account-external-id') as HTMLInputElement).value = 'sub-id';
    (document.getElementById('account-azure-client-secret') as HTMLInputElement).value = 'mysecret';

    document.getElementById('account-form')!.dispatchEvent(new Event('submit'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountCredentials).toHaveBeenCalledWith('az-id', {
      credential_type: 'azure_client_secret',
      payload: { client_secret: 'mysecret' }
    });
  });
});

// ---------------------------------------------------------------------------
// Account overrides panel — payment option selector (issue #23)
// ---------------------------------------------------------------------------

describe('Overrides panel — AWS payment selector', () => {
  /**
   * Render the AWS accounts list and expand the first account's overrides
   * panel so the panel DOM is populated with whatever
   * listAccountServiceOverrides has been mocked to return.
   */
  async function openOverridesPanel(accountId = 'acc-1'): Promise<HTMLElement> {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: accountId, name: 'Prod', provider: 'aws', external_id: '111', enabled: true },
    ]);
    await loadAccountsForProvider('aws');
    const overridesBtn = document.querySelector(
      `button[aria-label="Service overrides for Prod (111)"]`,
    ) as HTMLButtonElement | null;
    expect(overridesBtn).not.toBeNull();
    overridesBtn!.click();
    // loadOverridesPanel is async; let microtasks flush.
    await new Promise(r => setTimeout(r, 0));
    // Issue #122: the inline expandable panel was replaced by a per-account
    // modal. The body element is what loadOverridesPanel renders into.
    const panel = document.getElementById('account-overrides-modal-body') as HTMLElement | null;
    expect(panel).not.toBeNull();
    return panel!;
  }

  beforeEach(() => {
    buildAccountsDOM();
    jest.clearAllMocks();
  });

  test('renders payment <select> with Inherit + 3 AWS options for AWS overrides', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2' },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const select = panel.querySelector('select.override-payment-select') as HTMLSelectElement | null;
    expect(select).not.toBeNull();
    const values = Array.from(select!.options).map(o => o.value);
    expect(values).toEqual(['', 'no-upfront', 'partial-upfront', 'all-upfront']);
    expect(select!.value).toBe(''); // Inherit by default when no payment set
    expect(select!.options[0]!.disabled).toBe(false);
  });

  test('changing payment from Inherit calls saveAccountServiceOverride with only {payment}', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'rds', term: 1 },
    ]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({
      id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'rds', term: 1, payment: 'all-upfront',
    });

    const panel = await openOverridesPanel('acc-1');
    const select = panel.querySelector('select.override-payment-select') as HTMLSelectElement;
    select.value = 'all-upfront';
    select.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).toHaveBeenCalledTimes(1);
    expect(api.saveAccountServiceOverride).toHaveBeenCalledWith(
      'acc-1', 'aws', 'rds', { payment: 'all-upfront' },
    );
  });

  // Issue #196 — once the read path consults per-account overrides, the
  // recs list must refresh after a mutation or the user keeps seeing
  // stale data until the next page navigation.
  test('inline payment change triggers a recommendations refresh (issue #196)', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'rds', term: 1 },
    ]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({
      id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'rds', term: 1, payment: 'all-upfront',
    });

    const panel = await openOverridesPanel('acc-1');
    const select = panel.querySelector('select.override-payment-select') as HTMLSelectElement;
    select.value = 'all-upfront';
    select.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));

    expect(mockLoadRecommendations).toHaveBeenCalledTimes(1);
  });

  // The refresh is best-effort: a failure to refresh must not surface to
  // the user as an error toast, and must not block the override mutation
  // from completing successfully.
  test('refresh failure after override save is swallowed (issue #196)', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'rds', term: 1 },
    ]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({
      id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'rds', term: 1, payment: 'all-upfront',
    });
    mockLoadRecommendations.mockRejectedValueOnce(new Error('network blip'));
    const consoleWarnSpy = jest.spyOn(console, 'warn').mockImplementation(() => {});

    try {
      const panel = await openOverridesPanel('acc-1');
      const select = panel.querySelector('select.override-payment-select') as HTMLSelectElement;
      select.value = 'all-upfront';
      select.dispatchEvent(new Event('change'));
      await new Promise(r => setTimeout(r, 0));

      expect(api.saveAccountServiceOverride).toHaveBeenCalledTimes(1);
      expect(consoleWarnSpy).toHaveBeenCalled();
      // No error toast should have been shown for the refresh failure: the
      // success toast from the save path is what the user sees.
      const toastCalls = mockShowToast.mock.calls.map(c => c[0]);
      const errorToasts = toastCalls.filter(t => (t as { kind?: string }).kind === 'error');
      expect(errorToasts).toHaveLength(0);
    } finally {
      consoleWarnSpy.mockRestore();
    }
  });

  test('Inherit is disabled when override already has a payment set (no clear-field channel)', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', payment: 'partial-upfront' },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const select = panel.querySelector('select.override-payment-select') as HTMLSelectElement;
    expect(select.value).toBe('partial-upfront');
    expect(select.options[0]!.disabled).toBe(true);
    // The selector still does NOT call save on initial render (no change yet).
    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
  });

  test('Azure rows render an editable payment select with provider-appropriate options (issue #109)', async () => {
    // Issue #109: Azure override rows now get inline payment editing.
    // Azure uses upfront/monthly (not the AWS no-upfront/partial/all-upfront set).
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'azure', service: 'vm', term: 1, payment: 'upfront' },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const sel = panel.querySelector('select.override-payment-select') as HTMLSelectElement | null;
    expect(sel).not.toBeNull();
    const options = Array.from(sel!.options).map(o => o.value);
    // Azure has upfront + monthly (not the AWS no-upfront/partial/all-upfront set).
    expect(options).toContain('upfront');
    expect(options).toContain('monthly');
    expect(options).not.toContain('no-upfront');
    expect(options).not.toContain('all-upfront');
  });
});

// ---------------------------------------------------------------------------
// Inline edit on existing override rows: Term, Coverage, Enabled (issue #110)
// ---------------------------------------------------------------------------

describe('Overrides panel: inline Term/Coverage/Enabled (issue #110)', () => {
  /**
   * Render the AWS accounts list and open the per-account overrides modal,
   * mirroring openOverridesPanel from the Payment-selector describe block.
   * The body element is what loadOverridesPanel renders into.
   */
  async function openOverridesPanel(accountId = 'acc-1'): Promise<HTMLElement> {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: accountId, name: 'Prod', provider: 'aws', external_id: '111', enabled: true },
    ]);
    await loadAccountsForProvider('aws');
    const overridesBtn = document.querySelector(
      `button[aria-label="Service overrides for Prod (111)"]`,
    ) as HTMLButtonElement | null;
    expect(overridesBtn).not.toBeNull();
    overridesBtn!.click();
    await new Promise(r => setTimeout(r, 0));
    const panel = document.getElementById('account-overrides-modal-body') as HTMLElement | null;
    expect(panel).not.toBeNull();
    return panel!;
  }

  beforeEach(() => {
    buildAccountsDOM();
    jest.clearAllMocks();
  });

  // ----- Term cell -----

  test('Term: renders <select> with Inherit + 1yr/3yr options for an AWS EC2 row', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2' },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const sel = panel.querySelector('select.override-term-select') as HTMLSelectElement | null;
    expect(sel).not.toBeNull();
    const values = Array.from(sel!.options).map(o => o.value);
    expect(values).toEqual(['', '1', '3']);
    expect(sel!.value).toBe(''); // Inherit by default when no term set
    expect(sel!.options[0]!.disabled).toBe(false);
  });

  test('Term: change from Inherit calls saveAccountServiceOverride with only {term}', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2' },
    ]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({
      id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', term: 3,
    });

    const panel = await openOverridesPanel('acc-1');
    const sel = panel.querySelector('select.override-term-select') as HTMLSelectElement;
    sel.value = '3';
    sel.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).toHaveBeenCalledTimes(1);
    expect(api.saveAccountServiceOverride).toHaveBeenCalledWith(
      'acc-1', 'aws', 'ec2', { term: 3 },
    );
  });

  test('Term: RDS row with payment=no-upfront hides term=3 (commitmentOptions parity)', async () => {
    // RDS has the only AWS hard rule: no 3yr/no-upfront combination. The
    // Term dropdown for an RDS row currently set to no-upfront must omit
    // the 3yr option, same way the inline Payment selector omits no-upfront
    // for RDS term=3 rows.
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'rds', payment: 'no-upfront' },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const sel = panel.querySelector('select.override-term-select') as HTMLSelectElement;
    const values = Array.from(sel.options).map(o => o.value);
    expect(values).toEqual(['', '1']);
    expect(values).not.toContain('3');
  });

  test('Term: Inherit is disabled when term already set (no clear-field channel)', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', term: 1 },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const sel = panel.querySelector('select.override-term-select') as HTMLSelectElement;
    expect(sel.value).toBe('1');
    expect(sel.options[0]!.disabled).toBe(true);
    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
  });

  test('Term: save failure reverts the cell and shows an error toast', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2' },
    ]);
    (api.saveAccountServiceOverride as jest.Mock).mockRejectedValueOnce(new Error('boom'));

    const panel = await openOverridesPanel('acc-1');
    const sel = panel.querySelector('select.override-term-select') as HTMLSelectElement;
    sel.value = '3';
    sel.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));

    expect(sel.value).toBe(''); // reverted to previous
    const toastCalls = mockShowToast.mock.calls.map(c => c[0]) as Array<{ kind?: string; message?: string }>;
    expect(toastCalls.some(t => t.kind === 'error' && t.message?.includes('term'))).toBe(true);
  });

  // ----- Coverage cell -----

  test('Coverage: renders numeric <input> with value=50 for preset, placeholder when absent', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', coverage: 50 },
      { id: 'o2', account_id: 'acc-1', provider: 'aws', service: 'rds' },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const inputs = panel.querySelectorAll<HTMLInputElement>('input.override-coverage-input');
    expect(inputs.length).toBe(2);
    expect(inputs[0]!.type).toBe('number');
    expect(inputs[0]!.value).toBe('50');
    expect(inputs[1]!.value).toBe('');
    expect(inputs[1]!.placeholder).toBe('Inherit');
  });

  test('Coverage: change to 75 calls saveAccountServiceOverride with only {coverage: 75}', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', coverage: 50 },
    ]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({
      id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', coverage: 75,
    });

    const panel = await openOverridesPanel('acc-1');
    const inp = panel.querySelector('input.override-coverage-input') as HTMLInputElement;
    inp.value = '75';
    inp.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).toHaveBeenCalledTimes(1);
    expect(api.saveAccountServiceOverride).toHaveBeenCalledWith(
      'acc-1', 'aws', 'ec2', { coverage: 75 },
    );
  });

  test('Coverage: out-of-range value (150) does NOT call save, reverts, and posts error toast', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', coverage: 50 },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const inp = panel.querySelector('input.override-coverage-input') as HTMLInputElement;
    inp.value = '150';
    inp.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
    expect(inp.value).toBe('50');
    const toastCalls = mockShowToast.mock.calls.map(c => c[0]) as Array<{ kind?: string; message?: string }>;
    expect(toastCalls.some(t => t.kind === 'error' && t.message?.includes('between 0 and 100'))).toBe(true);
  });

  test('Coverage: negative value does NOT call save, reverts, and posts error toast', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', coverage: 50 },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const inp = panel.querySelector('input.override-coverage-input') as HTMLInputElement;
    inp.value = '-5';
    inp.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
    expect(inp.value).toBe('50');
  });

  test('Coverage: clearing a preset value reverts (no clear-field channel) and posts info toast', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', coverage: 50 },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const inp = panel.querySelector('input.override-coverage-input') as HTMLInputElement;
    inp.value = '';
    inp.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
    expect(inp.value).toBe('50');
    const toastCalls = mockShowToast.mock.calls.map(c => c[0]) as Array<{ kind?: string; message?: string }>;
    expect(toastCalls.some(t => t.kind === 'info' && t.message?.includes('Delete'))).toBe(true);
  });

  // ----- Enabled toggle -----

  test('Enabled: checkbox is checked by default for legacy rows (enabled === undefined)', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2' },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const cb = panel.querySelector('input.override-enabled-toggle') as HTMLInputElement;
    expect(cb).not.toBeNull();
    expect(cb.type).toBe('checkbox');
    expect(cb.checked).toBe(true);
    const tr = cb.closest('tr')!;
    expect(tr.classList.contains('override-disabled')).toBe(false);
  });

  test('Enabled: rows with enabled=false render unchecked with .override-disabled dim', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', enabled: false },
    ]);

    const panel = await openOverridesPanel('acc-1');
    const cb = panel.querySelector('input.override-enabled-toggle') as HTMLInputElement;
    expect(cb.checked).toBe(false);
    const tr = cb.closest('tr')!;
    expect(tr.classList.contains('override-disabled')).toBe(true);
  });

  test('Enabled: toggle off calls save with {enabled: false} and dims the row', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2' },
    ]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({
      id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', enabled: false,
    });

    const panel = await openOverridesPanel('acc-1');
    const cb = panel.querySelector('input.override-enabled-toggle') as HTMLInputElement;
    const tr = cb.closest('tr')!;
    cb.checked = false;
    cb.dispatchEvent(new Event('change'));
    // After the optimistic visual update, the row should already be dimmed
    // even before the network roundtrip completes.
    expect(tr.classList.contains('override-disabled')).toBe(true);
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).toHaveBeenCalledTimes(1);
    expect(api.saveAccountServiceOverride).toHaveBeenCalledWith(
      'acc-1', 'aws', 'ec2', { enabled: false },
    );
  });

  test('Enabled: toggle on calls save with {enabled: true} and removes dim', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', enabled: false },
    ]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({
      id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', enabled: true,
    });

    const panel = await openOverridesPanel('acc-1');
    const cb = panel.querySelector('input.override-enabled-toggle') as HTMLInputElement;
    const tr = cb.closest('tr')!;
    expect(tr.classList.contains('override-disabled')).toBe(true);
    cb.checked = true;
    cb.dispatchEvent(new Event('change'));
    expect(tr.classList.contains('override-disabled')).toBe(false);
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).toHaveBeenCalledTimes(1);
    expect(api.saveAccountServiceOverride).toHaveBeenCalledWith(
      'acc-1', 'aws', 'ec2', { enabled: true },
    );
  });

  // ----- Non-AWS rows -----

  test('Azure rows: term/coverage/enabled are all editable (issue #109)', async () => {
    // Issue #109: Azure override rows now have inline editing on all fields,
    // same as AWS rows after issue #110.
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'azure', service: 'vm', term: 1, coverage: 80 },
    ]);

    const panel = await openOverridesPanel('acc-1');
    // Term select is present and editable.
    const termSel = panel.querySelector('select.override-term-select') as HTMLSelectElement | null;
    expect(termSel).not.toBeNull();
    expect(termSel!.disabled).toBe(false);
    // Coverage input is present.
    const covInput = panel.querySelector('input.override-coverage-input') as HTMLInputElement | null;
    expect(covInput).not.toBeNull();
    // Enabled toggle is present and not disabled.
    const cb = panel.querySelector('input.override-enabled-toggle') as HTMLInputElement;
    expect(cb).not.toBeNull();
    expect(cb.disabled).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Override creation modal — issue #104
// ---------------------------------------------------------------------------

describe('Create-override modal', () => {
  /**
   * Click the "Service overrides" expander button on an AWS account row to
   * trigger loadOverridesPanel, then return the panel + override-modal DOM
   * for further interaction.
   */
  async function expandOverridesPanel(accountId = 'acc-1'): Promise<{ panel: HTMLElement; modal: HTMLElement }> {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: accountId, name: 'Prod', provider: 'aws', external_id: '111', enabled: true },
    ]);
    await loadAccountsForProvider('aws');
    const overridesBtn = document.querySelector(
      `button[aria-label="Service overrides for Prod (111)"]`,
    ) as HTMLButtonElement | null;
    expect(overridesBtn).not.toBeNull();
    overridesBtn!.click();
    await new Promise(r => setTimeout(r, 0));
    // Issue #122: panel is now inside the per-account overrides modal.
    const panel = document.getElementById('account-overrides-modal-body') as HTMLElement | null;
    expect(panel).not.toBeNull();
    const modal = document.getElementById('override-modal') as HTMLElement | null;
    expect(modal).not.toBeNull();
    return { panel: panel!, modal: modal! };
  }

  beforeEach(() => {
    buildAccountsDOM();
    setupSettingsHandlers();
    jest.clearAllMocks();
  });

  test('empty-state auto-opens the override modal', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);

    const { modal } = await expandOverridesPanel('acc-1');

    expect(modal.classList.contains('hidden')).toBe(false);
    // Service dropdown should be populated with all 9 AWS service options
    // (5 RIs + 4 per-plan-type SP slugs after the issue #22 follow-up).
    const svcSelect = document.getElementById('override-service') as HTMLSelectElement;
    const values = Array.from(svcSelect.options).map(o => o.value);
    expect(values).toEqual([
      'ec2', 'rds', 'elasticache', 'opensearch', 'redshift',
      'savings-plans-compute', 'savings-plans-ec2instance',
      'savings-plans-sagemaker', 'savings-plans-database',
    ]);
  });

  test('populated panel shows an Add override button that opens the modal', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', term: 1 },
    ]);

    const { panel, modal } = await expandOverridesPanel('acc-1');
    // The auto-open path must NOT fire when overrides already exist.
    expect(modal.classList.contains('hidden')).toBe(true);

    const addBtn = Array.from(panel.querySelectorAll('button')).find(b => b.textContent === 'Add override');
    expect(addBtn).toBeDefined();
    addBtn!.click();
    expect(modal.classList.contains('hidden')).toBe(false);

    // ec2 already has an override; the dropdown excludes it.
    const svcSelect = document.getElementById('override-service') as HTMLSelectElement;
    const values = Array.from(svcSelect.options).map(o => o.value);
    expect(values).not.toContain('ec2');
    expect(values).toContain('rds');
  });

  test('submit sends only the fields the user filled in (sparse PUT)', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({
      id: 'o-new', account_id: 'acc-1', provider: 'aws', service: 'rds', payment: 'no-upfront',
    });

    await expandOverridesPanel('acc-1');

    (document.getElementById('override-service') as HTMLSelectElement).value = 'rds';
    (document.getElementById('override-payment') as HTMLSelectElement).value = 'no-upfront';
    // term + coverage left blank → omitted from the request

    const form = document.getElementById('override-form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit', { cancelable: true }));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).toHaveBeenCalledTimes(1);
    expect(api.saveAccountServiceOverride).toHaveBeenCalledWith(
      'acc-1', 'aws', 'rds', { payment: 'no-upfront' },
    );
  });

  test('coercion: term parses to int, coverage parses to number', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({});

    await expandOverridesPanel('acc-1');
    (document.getElementById('override-service') as HTMLSelectElement).value = 'ec2';
    (document.getElementById('override-term') as HTMLSelectElement).value = '3';
    (document.getElementById('override-payment') as HTMLSelectElement).value = 'all-upfront';
    (document.getElementById('override-coverage') as HTMLInputElement).value = '85';

    (document.getElementById('override-form') as HTMLFormElement).dispatchEvent(
      new Event('submit', { cancelable: true }),
    );
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).toHaveBeenCalledWith(
      'acc-1', 'aws', 'ec2',
      { term: 3, payment: 'all-upfront', coverage: 85 },
    );
  });

  test('blocks submit when no field is set (would be a no-op override)', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);

    await expandOverridesPanel('acc-1');
    (document.getElementById('override-service') as HTMLSelectElement).value = 'ec2';
    // All three override fields left blank.

    (document.getElementById('override-form') as HTMLFormElement).dispatchEvent(
      new Event('submit', { cancelable: true }),
    );
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
    const errEl = document.getElementById('override-form-error');
    expect(errEl?.textContent).toContain('Set at least one');
  });

  test('blocks submit when coverage is out of range', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);

    await expandOverridesPanel('acc-1');
    (document.getElementById('override-service') as HTMLSelectElement).value = 'ec2';
    (document.getElementById('override-coverage') as HTMLInputElement).value = '150';

    (document.getElementById('override-form') as HTMLFormElement).dispatchEvent(
      new Event('submit', { cancelable: true }),
    );
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
    const errEl = document.getElementById('override-form-error');
    expect(errEl?.textContent).toContain('Coverage must be between 0 and 100');
  });

  test('save success closes the modal and reloads the panel', async () => {
    (api.listAccountServiceOverrides as jest.Mock)
      .mockResolvedValueOnce([])  // initial empty
      .mockResolvedValueOnce([    // after save reload
        { id: 'o-new', account_id: 'acc-1', provider: 'aws', service: 'rds', payment: 'all-upfront' },
      ]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({});

    const { modal } = await expandOverridesPanel('acc-1');
    (document.getElementById('override-service') as HTMLSelectElement).value = 'rds';
    (document.getElementById('override-payment') as HTMLSelectElement).value = 'all-upfront';

    (document.getElementById('override-form') as HTMLFormElement).dispatchEvent(
      new Event('submit', { cancelable: true }),
    );
    await new Promise(r => setTimeout(r, 0));

    expect(modal.classList.contains('hidden')).toBe(true);
    expect(api.listAccountServiceOverrides).toHaveBeenCalledTimes(2);
  });

  test('cancel button closes the modal without calling the API', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);

    const { modal } = await expandOverridesPanel('acc-1');
    expect(modal.classList.contains('hidden')).toBe(false);

    (document.getElementById('close-override-modal-btn') as HTMLButtonElement).click();
    expect(modal.classList.contains('hidden')).toBe(true);
    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
  });

  test('Azure account: empty state auto-opens the create modal with Azure service list (issue #109)', async () => {
    // Issue #109: Azure accounts now get the same "Add override" flow as AWS.
    // The empty-state auto-opens the create modal and the service dropdown
    // is populated with Azure services (vm, sql, cosmosdb, redis, search).
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'az-1', name: 'AzureProd', provider: 'azure', external_id: 'sub-x', enabled: true },
    ]);
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);

    await loadAccountsForProvider('azure');
    const overridesBtn = document.querySelector(
      `button[aria-label="Service overrides for AzureProd (sub-x)"]`,
    ) as HTMLButtonElement;
    overridesBtn.click();
    await new Promise(r => setTimeout(r, 0));

    const panel = document.getElementById('account-overrides-modal-body') as HTMLElement;
    const modal = document.getElementById('override-modal') as HTMLElement;

    // The inner create modal auto-opens (same as AWS empty state).
    expect(modal.classList.contains('hidden')).toBe(false);

    // Add override button is present.
    const addBtn = Array.from(panel.querySelectorAll('button')).find(b => b.textContent === 'Add override');
    expect(addBtn).toBeDefined();

    // Service dropdown lists Azure services, not AWS services.
    const svcSel = document.getElementById('override-service') as HTMLSelectElement;
    const svcValues = Array.from(svcSel.options).map(o => o.value);
    expect(svcValues).toContain('vm');
    expect(svcValues).toContain('sql');
    expect(svcValues).toContain('cosmosdb');
    expect(svcValues).not.toContain('ec2');
    expect(svcValues).not.toContain('rds');
  });

  test('all services already overridden disables submit', async () => {
    // Mirrors AWS_OVERRIDE_SERVICES post-issue-#22-follow-up: 5 RI services
    // plus the four per-plan-type SP slugs.
    const all = [
      'ec2', 'rds', 'elasticache', 'opensearch', 'redshift',
      'savings-plans-compute', 'savings-plans-ec2instance',
      'savings-plans-sagemaker', 'savings-plans-database',
    ];
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue(
      all.map((service, i) => ({
        id: `o-${i}`, account_id: 'acc-1', provider: 'aws', service, term: 1,
      })),
    );

    const { panel, modal } = await expandOverridesPanel('acc-1');
    const addBtn = Array.from(panel.querySelectorAll('button')).find(b => b.textContent === 'Add override');
    addBtn!.click();
    expect(modal.classList.contains('hidden')).toBe(false);

    const submitBtn = modal.querySelector('button[type="submit"]') as HTMLButtonElement;
    expect(submitBtn.disabled).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// Per-account overrides modal — issue #122
// ---------------------------------------------------------------------------

describe('Account overrides modal', () => {
  beforeEach(() => {
    buildAccountsDOM();
    setupSettingsHandlers();
    jest.clearAllMocks();
  });

  test('Overrides button opens an account-scoped modal whose title binds to the account', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-1', name: 'AWS Prod', provider: 'aws', external_id: '540659244915', enabled: true },
    ]);
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', payment: 'all-upfront' },
    ]);

    await loadAccountsForProvider('aws');
    const btn = document.querySelector(
      `button[aria-label="Service overrides for AWS Prod (540659244915)"]`,
    ) as HTMLButtonElement;
    btn.click();
    await new Promise(r => setTimeout(r, 0));

    const modal = document.getElementById('account-overrides-modal') as HTMLElement;
    expect(modal.classList.contains('hidden')).toBe(false);
    const title = document.getElementById('account-overrides-modal-title') as HTMLElement;
    expect(title.textContent).toBe('Service overrides for AWS Prod (540659244915)');
  });

  test('switching accounts swaps the modal title; only one account context is active at a time', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-a', name: 'AWS Prod',  provider: 'aws', external_id: '540659244915', enabled: true },
      { id: 'acc-b', name: 'CUDly host', provider: 'aws', external_id: '909626172446', enabled: true },
    ]);
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);

    await loadAccountsForProvider('aws');
    const titleEl = document.getElementById('account-overrides-modal-title') as HTMLElement;

    // Click Overrides on Account A.
    (document.querySelector(
      `button[aria-label="Service overrides for AWS Prod (540659244915)"]`,
    ) as HTMLButtonElement).click();
    await new Promise(r => setTimeout(r, 0));
    expect(titleEl.textContent).toContain('AWS Prod');

    // Click Overrides on Account B without closing the modal first.
    (document.querySelector(
      `button[aria-label="Service overrides for CUDly host (909626172446)"]`,
    ) as HTMLButtonElement).click();
    await new Promise(r => setTimeout(r, 0));

    // Title now reflects Account B; only ONE modal exists in the DOM.
    expect(titleEl.textContent).toContain('CUDly host');
    expect(titleEl.textContent).not.toContain('AWS Prod');
    expect(document.querySelectorAll('#account-overrides-modal').length).toBe(1);
  });

  test('Close button hides the modal and clears the body to avoid stale flash on next open', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-1', name: 'Prod', provider: 'aws', external_id: '111', enabled: true },
    ]);
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', payment: 'all-upfront' },
    ]);

    await loadAccountsForProvider('aws');
    (document.querySelector(
      `button[aria-label="Service overrides for Prod (111)"]`,
    ) as HTMLButtonElement).click();
    await new Promise(r => setTimeout(r, 0));

    const modal = document.getElementById('account-overrides-modal') as HTMLElement;
    const body = document.getElementById('account-overrides-modal-body') as HTMLElement;
    expect(modal.classList.contains('hidden')).toBe(false);
    expect(body.querySelector('table.overrides-table')).not.toBeNull();

    (document.getElementById('close-account-overrides-modal-btn') as HTMLButtonElement).click();
    expect(modal.classList.contains('hidden')).toBe(true);
    // Body cleared so the next open doesn't flash stale rows for ~1ms.
    expect(body.children.length).toBe(0);
  });

  test('no inline panel renders below the accounts table (issue #122 regression guard)', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-1', name: 'Prod', provider: 'aws', external_id: '111', enabled: true },
    ]);

    await loadAccountsForProvider('aws');

    // The inline expandable panel was the cause of the panel-stacking bug.
    // Verify it is gone — the only override container is the modal body.
    expect(document.querySelector('.account-overrides-panel')).toBeNull();
  });

  test('Delete-override button + dialog wording match the actual data semantics (issue #114)', async () => {
    // The action DELETEs the override row; pre-#114 the dialog said
    // "Reset … will be replaced" which implied a stuck-around row with
    // new values. Pin the post-fix wording so a future regression doesn't
    // silently re-introduce the mismatch.
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      {
        account_id: 'acc-1',
        provider: 'aws',
        service: 'ec2',
        term: 1,
        payment: 'no-upfront',
        coverage: 80,
      },
    ]);
    mockConfirmDialog.mockResolvedValue(false); // user cancels — we don't need the API call to fire

    const panel = document.createElement('div');
    document.body.appendChild(panel);
    await loadOverridesPanel('acc-1', panel, 'aws');

    // The action button now reads "Delete" (not "Reset").
    const buttons = Array.from(panel.querySelectorAll('button'));
    const deleteBtn = buttons.find(b => b.textContent === 'Delete');
    expect(deleteBtn).toBeDefined();
    expect(buttons.find(b => b.textContent === 'Reset')).toBeUndefined();

    // Click it and inspect the confirmDialog opts.
    deleteBtn!.click();
    await Promise.resolve(); // let the click handler's await chain start
    expect(mockConfirmDialog).toHaveBeenCalledTimes(1);
    const opts = mockConfirmDialog.mock.calls[0]![0] as {
      title: string;
      body: string;
      confirmLabel: string;
    };
    expect(opts.title).toBe('Delete override?');
    expect(opts.confirmLabel).toBe('Delete override');
    expect(opts.body).toContain('Delete the aws/ec2 override');
    expect(opts.body).toContain('revert to the global default');
    expect(opts.body).toContain('removed');
    // Pre-#114 wording must not appear.
    expect(opts.body).not.toContain('replaced');
    expect(opts.body).not.toContain('Reset');
  });

  describe('toast-undo for Delete override (issue #113)', () => {
    const overrideFixture = {
      account_id: 'acc-1',
      provider: 'aws',
      service: 'rds',
      term: 1,
      payment: 'all-upfront',
      coverage: 75,
      enabled: true,
    };

    test('Delete shows info toast with Undo action and clears row (click-Undo path)', async () => {
      (api.listAccountServiceOverrides as jest.Mock)
        .mockResolvedValueOnce([overrideFixture])  // initial render
        .mockResolvedValue([]);                     // reload after delete + reload after undo
      (api.deleteAccountServiceOverride as jest.Mock).mockResolvedValue(undefined);
      (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue(overrideFixture);
      mockConfirmDialog.mockResolvedValue(true);

      const panel = document.createElement('div');
      document.body.appendChild(panel);
      await loadOverridesPanel('acc-1', panel, 'aws');

      const deleteBtn = Array.from(panel.querySelectorAll('button'))
        .find(b => b.textContent === 'Delete') as HTMLButtonElement;
      expect(deleteBtn).toBeDefined();

      deleteBtn.click();
      // Flush the confirm + delete + reload chain.
      for (let i = 0; i < 5; i++) { await new Promise(r => setTimeout(r, 0)); }

      expect(api.deleteAccountServiceOverride).toHaveBeenCalledWith('acc-1', 'aws', 'rds');

      // An info toast with an Undo action must have been shown.
      const toastCalls = mockShowToast.mock.calls.map(c => c[0]) as Array<{
        kind?: string;
        message?: string;
        actions?: Array<{ label: string; onClick: () => void }>;
        timeout?: number | null;
      }>;
      const undoToast = toastCalls.find(t => t.kind === 'info' && t.message?.includes('aws/rds'));
      expect(undoToast).toBeDefined();
      expect(undoToast!.actions).toBeDefined();
      expect(undoToast!.actions!.length).toBe(1);
      expect(undoToast!.actions![0]!.label).toBe('Undo');
      // 5-second TTL per issue spec.
      expect(undoToast!.timeout).toBe(5_000);

      // Simulate clicking Undo.
      undoToast!.actions![0]!.onClick();
      for (let i = 0; i < 5; i++) { await new Promise(r => setTimeout(r, 0)); }

      // saveAccountServiceOverride must have been called with the original snapshot.
      expect(api.saveAccountServiceOverride).toHaveBeenCalledWith(
        'acc-1', 'aws', 'rds',
        expect.objectContaining({
          term: 1,
          payment: 'all-upfront',
          coverage: 75,
          enabled: true,
        }),
      );
    });

    test('let-it-expire path: override is permanently gone after toast timeout', async () => {
      (api.listAccountServiceOverrides as jest.Mock)
        .mockResolvedValueOnce([overrideFixture])
        .mockResolvedValue([]);
      (api.deleteAccountServiceOverride as jest.Mock).mockResolvedValue(undefined);
      mockConfirmDialog.mockResolvedValue(true);

      const panel = document.createElement('div');
      document.body.appendChild(panel);
      await loadOverridesPanel('acc-1', panel, 'aws');

      const deleteBtn = Array.from(panel.querySelectorAll('button'))
        .find(b => b.textContent === 'Delete') as HTMLButtonElement;
      deleteBtn.click();
      for (let i = 0; i < 5; i++) { await new Promise(r => setTimeout(r, 0)); }

      // Undo was NOT clicked; saveAccountServiceOverride must not have been called.
      expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
      // The delete did fire.
      expect(api.deleteAccountServiceOverride).toHaveBeenCalledTimes(1);
      // The panel reloaded to show the empty state (no more override rows).
      const table = panel.querySelector('table.overrides-table');
      expect(table).toBeNull();
    });
  });

  describe('override commitmentOptions parity (issue #107)', () => {
    test('inline payment selector hides invalid options for RDS term=3 row', async () => {
      // RDS rejects 3yr no-upfront per commitmentOptions invalidCombinations.
      // The inline payment selector on an existing RDS override with term=3
      // must not list no-upfront — the global Settings card already hides
      // it; this PR brings the override surfaces in line.
      (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
        {
          account_id: 'acc-1',
          provider: 'aws',
          service: 'rds',
          term: 3,
          payment: 'all-upfront',
          coverage: 80,
        },
      ]);

      const panel = document.createElement('div');
      document.body.appendChild(panel);
      await loadOverridesPanel('acc-1', panel, 'aws');

      const select = panel.querySelector<HTMLSelectElement>('select.override-payment-select');
      expect(select).not.toBeNull();
      const options = Array.from(select!.options).map(o => o.value);
      expect(options).toContain('all-upfront');
      expect(options).toContain('partial-upfront');
      expect(options).not.toContain('no-upfront');
    });

    test('inline payment selector shows full list when term is unset', async () => {
      // Without a term we can't pre-validate; fall back to the full list
      // and let the user pick. The submit-side guard would catch any
      // invalid combo.
      (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
        {
          account_id: 'acc-1',
          provider: 'aws',
          service: 'rds',
          term: 0,
          payment: '',
          coverage: 80,
        },
      ]);

      const panel = document.createElement('div');
      document.body.appendChild(panel);
      await loadOverridesPanel('acc-1', panel, 'aws');

      const select = panel.querySelector<HTMLSelectElement>('select.override-payment-select');
      const options = Array.from(select!.options).map(o => o.value);
      expect(options).toContain('all-upfront');
      expect(options).toContain('partial-upfront');
      expect(options).toContain('no-upfront');
    });

    test('override-create modal hides invalid payment options on service+term change', async () => {
      // Build the override modal DOM the production app provides via createElement
      // (avoid innerHTML; same pattern as the rest of this test file).
      document.body.replaceChildren();
      const modal = document.createElement('div');
      modal.id = 'override-modal';
      modal.className = 'modal hidden';

      const form = document.createElement('form');
      form.id = 'override-form';

      const acctIdInput = document.createElement('input');
      acctIdInput.type = 'hidden';
      acctIdInput.id = 'override-account-id';
      form.appendChild(acctIdInput);

      const provInput = document.createElement('input');
      provInput.type = 'hidden';
      provInput.id = 'override-provider';
      form.appendChild(provInput);

      const svcSel = document.createElement('select');
      svcSel.id = 'override-service';
      form.appendChild(svcSel);

      const termSel = document.createElement('select');
      termSel.id = 'override-term';
      for (const v of ['', '1', '3']) {
        const o = document.createElement('option');
        o.value = v;
        termSel.appendChild(o);
      }
      form.appendChild(termSel);

      const paySel = document.createElement('select');
      paySel.id = 'override-payment';
      form.appendChild(paySel);

      const covInput = document.createElement('input');
      covInput.type = 'number';
      covInput.id = 'override-coverage';
      form.appendChild(covInput);

      const errEl = document.createElement('p');
      errEl.id = 'override-form-error';
      form.appendChild(errEl);

      const submitBtn = document.createElement('button');
      submitBtn.type = 'submit';
      form.appendChild(submitBtn);

      modal.appendChild(form);
      document.body.appendChild(modal);

      const panel = document.createElement('div');
      openOverrideModal('acc-1', 'aws', [], panel);

      const svc = document.getElementById('override-service') as HTMLSelectElement;
      const term = document.getElementById('override-term') as HTMLSelectElement;
      const pay = document.getElementById('override-payment') as HTMLSelectElement;

      // Pick rds + 3yr — payment dropdown must drop no-upfront.
      svc.value = 'rds';
      svc.dispatchEvent(new Event('change'));
      term.value = '3';
      term.dispatchEvent(new Event('change'));

      const options = Array.from(pay.options).map(o => o.value);
      expect(options).toContain(''); // Inherit
      expect(options).toContain('all-upfront');
      expect(options).toContain('partial-upfront');
      expect(options).not.toContain('no-upfront');

      // Switch term to 1 — no-upfront becomes valid again.
      term.value = '1';
      term.dispatchEvent(new Event('change'));
      const opts2 = Array.from(pay.options).map(o => o.value);
      expect(opts2).toContain('no-upfront');
    });
  });
});

// ---------------------------------------------------------------------------
// Issue #606 — Cancel-All-Then-Delete UX
// ---------------------------------------------------------------------------

describe('deleteAccount — pending-executions 409 handling (issue #606)', () => {
  beforeEach(() => {
    buildAccountsDOM();
    jest.clearAllMocks();
  });

  // Helper to construct the 409 ApiError shape that client.ts attaches
  // for backend ClientError responses. The status + details are what
  // settings.ts branches on.
  function pendingExecutionsApiError(count: number, ids: string[]): Error & {
    status: number;
    details: Record<string, unknown>;
  } {
    const err = new Error(
      `cannot delete account: ${count} pending purchase(s) must be cancelled first`,
    ) as Error & { status: number; details: Record<string, unknown> };
    err.status = 409;
    err.details = {
      pending_count: count,
      pending_execution_ids: ids,
      reason: 'pending_executions',
    };
    return err;
  }

  // Variant: backend returned a count but no IDs (the server-side list
  // call failed — see TestDeleteAccount_PendingListErr_StillReturns409
  // on the Go side). The UI cannot auto-cancel without the ID list.
  function pendingExecutionsApiErrorNoIDs(count: number): Error & {
    status: number;
    details: Record<string, unknown>;
  } {
    const err = new Error(
      `cannot delete account: ${count} pending purchase(s) must be cancelled first`,
    ) as Error & { status: number; details: Record<string, unknown> };
    err.status = 409;
    err.details = {
      pending_count: count,
      reason: 'pending_executions',
      // No pending_execution_ids on this path.
    };
    return err;
  }

  // Click the Delete button on the only row of the rendered accounts table.
  async function clickDeleteOnFirstAccount(): Promise<void> {
    const container = document.getElementById('aws-accounts-list')!;
    const buttons = Array.from(container.querySelectorAll('button'));
    const deleteBtn = buttons.find(b => b.textContent === 'Delete');
    expect(deleteBtn).toBeDefined();
    deleteBtn!.click();
    // The click handler is async; await microtask flushes so the chain
    // (deleteAccount -> confirmDialog -> api.deleteAccount) starts.
    await Promise.resolve();
  }

  test('409 → second confirm dialog renders with correct count + label', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-1', name: 'Prod', external_id: '111111111111', enabled: true, provider: 'aws' },
    ]);
    // First confirm: "Delete account?" → user confirms.
    // Second confirm: "Pending purchases must be cancelled" → user declines so
    // we can inspect the opts without exercising the cancel + retry path.
    mockConfirmDialog.mockResolvedValueOnce(true).mockResolvedValueOnce(false);
    (api.deleteAccount as jest.Mock).mockRejectedValueOnce(
      pendingExecutionsApiError(2, ['exec-1', 'exec-2']),
    );

    await loadAccountsForProvider('aws');
    await clickDeleteOnFirstAccount();
    // Wait for the confirmDialog promise chain to advance through both calls.
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));

    expect(mockConfirmDialog).toHaveBeenCalledTimes(2);
    const secondOpts = mockConfirmDialog.mock.calls[1]![0] as {
      title: string;
      body: string;
      confirmLabel: string;
      destructive: boolean;
    };
    expect(secondOpts.title).toBe('Pending purchases must be cancelled');
    expect(secondOpts.body).toContain('2 pending purchases');
    expect(secondOpts.confirmLabel).toBe('Cancel All 2 Pending + Delete');
    expect(secondOpts.destructive).toBe(true);

    // User declined the second confirm — no cancelPurchase calls fire.
    expect(api.cancelPurchase).not.toHaveBeenCalled();
    // And no retry of deleteAccount.
    expect(api.deleteAccount).toHaveBeenCalledTimes(1);
  });

  test('confirm Cancel-All-Then-Delete posts cancel for each pending id then retries delete', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-1', name: 'Prod', external_id: '111111111111', enabled: true, provider: 'aws' },
    ]);
    mockConfirmDialog.mockResolvedValueOnce(true).mockResolvedValueOnce(true);
    (api.deleteAccount as jest.Mock)
      .mockRejectedValueOnce(pendingExecutionsApiError(3, ['exec-a', 'exec-b', 'exec-c']))
      .mockResolvedValueOnce(undefined);
    (api.cancelPurchase as jest.Mock).mockResolvedValue(undefined);

    await loadAccountsForProvider('aws');
    await clickDeleteOnFirstAccount();
    // Flush several microtasks so all sequential awaits in the handler resolve.
    for (let i = 0; i < 10; i++) {
      await new Promise(r => setTimeout(r, 0));
    }

    // cancelPurchase called once per execution id, in order.
    expect(api.cancelPurchase).toHaveBeenCalledTimes(3);
    expect((api.cancelPurchase as jest.Mock).mock.calls[0][0]).toBe('exec-a');
    expect((api.cancelPurchase as jest.Mock).mock.calls[1][0]).toBe('exec-b');
    expect((api.cancelPurchase as jest.Mock).mock.calls[2][0]).toBe('exec-c');

    // deleteAccount called twice (initial 409 + retry after cancels).
    expect(api.deleteAccount).toHaveBeenCalledTimes(2);

    // Success toast surfaced.
    expect(mockShowToast).toHaveBeenCalled();
    const successCalls = mockShowToast.mock.calls.filter(c => {
      const opts = c[0] as { kind?: string };
      return opts.kind === 'success';
    });
    expect(successCalls.length).toBe(1);
  });

  test('partial cancel failure leaves account in place and surfaces error toast', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-1', name: 'Prod', external_id: '111111111111', enabled: true, provider: 'aws' },
    ]);
    mockConfirmDialog.mockResolvedValueOnce(true).mockResolvedValueOnce(true);
    (api.deleteAccount as jest.Mock).mockRejectedValueOnce(
      pendingExecutionsApiError(2, ['exec-1', 'exec-2']),
    );
    (api.cancelPurchase as jest.Mock)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('cancel failed'));

    await loadAccountsForProvider('aws');
    await clickDeleteOnFirstAccount();
    for (let i = 0; i < 10; i++) {
      await new Promise(r => setTimeout(r, 0));
    }

    // Second cancelPurchase rejected — deleteAccount must NOT retry.
    expect(api.deleteAccount).toHaveBeenCalledTimes(1);
    // Error toast surfaced (kind: 'error').
    const errorCalls = mockShowToast.mock.calls.filter(c => {
      const opts = c[0] as { kind?: string; message?: string };
      return opts.kind === 'error';
    });
    expect(errorCalls.length).toBeGreaterThan(0);
    const lastErrorMsg = (errorCalls[errorCalls.length - 1]![0] as { message: string }).message;
    expect(lastErrorMsg).toContain('1 failed');
    expect(lastErrorMsg).toContain('Account NOT deleted');
  });

  test('409 with empty pending_execution_ids surfaces History-page fallback', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-1', name: 'Prod', external_id: '111111111111', enabled: true, provider: 'aws' },
    ]);
    // Only the first "Delete account?" confirm fires. There is no second
    // confirm because the UI can't enumerate cancellations without IDs.
    mockConfirmDialog.mockResolvedValueOnce(true);
    (api.deleteAccount as jest.Mock).mockRejectedValueOnce(
      pendingExecutionsApiErrorNoIDs(3),
    );

    await loadAccountsForProvider('aws');
    await clickDeleteOnFirstAccount();
    for (let i = 0; i < 5; i++) {
      await new Promise(r => setTimeout(r, 0));
    }

    // Exactly one confirm dialog — no second one for cancellation.
    expect(mockConfirmDialog).toHaveBeenCalledTimes(1);
    // No cancelPurchase calls (we have no IDs to cancel).
    expect(api.cancelPurchase).not.toHaveBeenCalled();
    // deleteAccount called only once (initial attempt; no retry).
    expect(api.deleteAccount).toHaveBeenCalledTimes(1);
    // Error toast references the History page so the operator knows where
    // to go next.
    const errorCalls = mockShowToast.mock.calls.filter(c => {
      const opts = c[0] as { kind?: string };
      return opts.kind === 'error';
    });
    expect(errorCalls.length).toBeGreaterThan(0);
    const lastErrorMsg = (errorCalls[errorCalls.length - 1]![0] as { message: string }).message;
    expect(lastErrorMsg).toEqual(expect.stringContaining('History'));
  });

  test('non-409 error path keeps original behaviour (no second dialog)', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-1', name: 'Prod', external_id: '111111111111', enabled: true, provider: 'aws' },
    ]);
    mockConfirmDialog.mockResolvedValueOnce(true);
    (api.deleteAccount as jest.Mock).mockRejectedValueOnce(new Error('boom'));

    await loadAccountsForProvider('aws');
    await clickDeleteOnFirstAccount();
    for (let i = 0; i < 5; i++) {
      await new Promise(r => setTimeout(r, 0));
    }

    expect(mockConfirmDialog).toHaveBeenCalledTimes(1);
    expect(api.cancelPurchase).not.toHaveBeenCalled();
    const errorCalls = mockShowToast.mock.calls.filter(c => {
      const opts = c[0] as { kind?: string };
      return opts.kind === 'error';
    });
    expect(errorCalls.length).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// Issue #112 — "Inherit (currently: X)" labels in the override modal
// ---------------------------------------------------------------------------

describe('Override modal Inherit labels (issue #112)', () => {
  /** Minimal DOM for openOverrideModal: just the fields the function touches. */
  function buildOverrideModalDOM(): void {
    document.body.replaceChildren();

    const modal = document.createElement('div');
    modal.id = 'override-modal';
    modal.className = 'modal hidden';

    const form = document.createElement('form');
    form.id = 'override-form';

    const acctIdInput = document.createElement('input');
    acctIdInput.type = 'hidden';
    acctIdInput.id = 'override-account-id';
    form.appendChild(acctIdInput);

    const provInput = document.createElement('input');
    provInput.type = 'hidden';
    provInput.id = 'override-provider';
    form.appendChild(provInput);

    const svcSel = document.createElement('select');
    svcSel.id = 'override-service';
    form.appendChild(svcSel);

    const termSel = document.createElement('select');
    termSel.id = 'override-term';
    const termInherit = document.createElement('option');
    termInherit.value = '';
    termInherit.textContent = 'Inherit (use global default)';
    termSel.appendChild(termInherit);
    for (const v of ['1', '3']) {
      const o = document.createElement('option');
      o.value = v;
      termSel.appendChild(o);
    }
    form.appendChild(termSel);

    const paySel = document.createElement('select');
    paySel.id = 'override-payment';
    form.appendChild(paySel);

    const covInput = document.createElement('input');
    covInput.type = 'number';
    covInput.id = 'override-coverage';
    covInput.placeholder = 'Inherit (use global default)';
    form.appendChild(covInput);

    const errEl = document.createElement('p');
    errEl.id = 'override-form-error';
    form.appendChild(errEl);

    const submitBtn = document.createElement('button');
    submitBtn.type = 'submit';
    form.appendChild(submitBtn);

    modal.appendChild(form);
    document.body.appendChild(modal);
  }

  afterEach(() => {
    // Reset to default so other tests are not affected.
    setGlobalDefaultsForTest({ term: 3, payment: 'all-upfront', coverage: 80 });
  });

  test('term and payment inherit options reflect the current global defaults (3yr / all-upfront)', () => {
    buildOverrideModalDOM();
    setGlobalDefaultsForTest({ term: 3, payment: 'all-upfront', coverage: 80 });

    const panel = document.createElement('div');
    openOverrideModal('acc-1', 'aws', [], panel);

    const termInherit = document.querySelector<HTMLOptionElement>('#override-term option[value=""]');
    expect(termInherit?.textContent).toBe('Inherit (currently: 3 Years)');

    const payInherit = document.querySelector<HTMLOptionElement>('#override-payment option[value=""]');
    expect(payInherit?.textContent).toBe('Inherit (currently: All Upfront)');

    const covInput = document.getElementById('override-coverage') as HTMLInputElement;
    expect(covInput.placeholder).toBe('Inherit (currently: 80%)');
  });

  test('inherit labels update when globals differ from defaults (1yr / no-upfront / 70%)', () => {
    buildOverrideModalDOM();
    setGlobalDefaultsForTest({ term: 1, payment: 'no-upfront', coverage: 70 });

    const panel = document.createElement('div');
    openOverrideModal('acc-1', 'aws', [], panel);

    const termInherit = document.querySelector<HTMLOptionElement>('#override-term option[value=""]');
    expect(termInherit?.textContent).toBe('Inherit (currently: 1 Year)');

    const payInherit = document.querySelector<HTMLOptionElement>('#override-payment option[value=""]');
    expect(payInherit?.textContent).toBe('Inherit (currently: No Upfront)');

    const covInput = document.getElementById('override-coverage') as HTMLInputElement;
    expect(covInput.placeholder).toBe('Inherit (currently: 70%)');
  });
});

// ---------------------------------------------------------------------------
// Bulk multi-service override modal — issue #119
// ---------------------------------------------------------------------------

describe('Bulk override modal (issue #119)', () => {
  /**
   * Open the override modal in bulk mode with no pre-existing overrides,
   * toggle the bulk switch, and return the DOM elements needed by tests.
   */
  async function openBulkModal(accountId = 'acc-1'): Promise<{
    modal: HTMLElement;
    bulkToggle: HTMLInputElement;
    bulkList: HTMLElement;
    submitBtn: HTMLButtonElement;
    form: HTMLFormElement;
  }> {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: accountId, name: 'Prod', provider: 'aws', external_id: '111', enabled: true },
    ]);
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    await loadAccountsForProvider('aws');

    const overridesBtn = document.querySelector(
      `button[aria-label="Service overrides for Prod (111)"]`,
    ) as HTMLButtonElement;
    expect(overridesBtn).not.toBeNull();
    overridesBtn.click();
    await new Promise(r => setTimeout(r, 0));

    const modal = document.getElementById('override-modal') as HTMLElement;
    const bulkToggle = document.getElementById('override-bulk-toggle') as HTMLInputElement;
    const bulkList = document.getElementById('override-bulk-services-list') as HTMLElement;
    const submitBtn = document.getElementById('override-submit-btn') as HTMLButtonElement;
    const form = document.getElementById('override-form') as HTMLFormElement;

    // Flip to bulk mode.
    bulkToggle.checked = true;
    bulkToggle.dispatchEvent(new Event('change'));

    return { modal, bulkToggle, bulkList, submitBtn, form };
  }

  beforeEach(() => {
    buildAccountsDOM();
    setupSettingsHandlers();
    jest.clearAllMocks();
  });

  test('bulk toggle hides single-service row and shows checkbox list', async () => {
    const { bulkList } = await openBulkModal();

    const singleRow = document.getElementById('override-single-service-row') as HTMLElement;
    const bulkDiv = document.getElementById('override-bulk-services') as HTMLElement;

    expect(singleRow.classList.contains('hidden')).toBe(true);
    expect(bulkDiv.classList.contains('hidden')).toBe(false);

    // All 9 AWS services should be rendered as checkboxes.
    const checkboxes = bulkList.querySelectorAll('input[type="checkbox"]');
    expect(checkboxes).toHaveLength(9);
    const values = Array.from(checkboxes).map(cb => (cb as HTMLInputElement).value);
    expect(values).toContain('ec2');
    expect(values).toContain('rds');
    expect(values).toContain('elasticache');
  });

  test('submit is disabled until at least one checkbox is checked', async () => {
    const { submitBtn, bulkList } = await openBulkModal();

    // No boxes checked yet.
    expect(submitBtn.disabled).toBe(true);

    // Check one box.
    const firstCb = bulkList.querySelector('input[type="checkbox"]') as HTMLInputElement;
    firstCb.checked = true;
    firstCb.dispatchEvent(new Event('change'));
    expect(submitBtn.disabled).toBe(false);
  });

  test('all 3 saves succeed: aggregated success toast, modal closes, panel reloads', async () => {
    (api.listAccountServiceOverrides as jest.Mock)
      .mockResolvedValueOnce([])  // initial: empty -> auto-opens modal
      .mockResolvedValueOnce([   // reload after save: returns new rows so modal stays closed
        { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'rds',         term: 1 },
        { id: 'o2', account_id: 'acc-1', provider: 'aws', service: 'elasticache', term: 1 },
        { id: 'o3', account_id: 'acc-1', provider: 'aws', service: 'opensearch',  term: 1 },
      ]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({});

    const { form, bulkList } = await openBulkModal();

    // Select rds, elasticache, opensearch.
    const targets = ['rds', 'elasticache', 'opensearch'];
    for (const svc of targets) {
      const cb = bulkList.querySelector(`input[value="${svc}"]`) as HTMLInputElement;
      cb.checked = true;
      cb.dispatchEvent(new Event('change'));
    }
    (document.getElementById('override-payment') as HTMLSelectElement).value = 'all-upfront';

    form.dispatchEvent(new Event('submit', { cancelable: true }));
    // Flush all async chains: allSettled + loadOverridesPanel + recommendations.
    for (let i = 0; i < 10; i++) await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).toHaveBeenCalledTimes(3);
    const toastArg = mockShowToast.mock.calls[0]?.[0] as { message: string; kind: string };
    expect(toastArg.kind).toBe('success');
    expect(toastArg.message).toMatch(/Created 3 overrides/);

    // Modal should be hidden after success.
    const modal = document.getElementById('override-modal') as HTMLElement;
    expect(modal.classList.contains('hidden')).toBe(true);
  });

  test('2 successes / 1 failure: warning toast names the failed service', async () => {
    (api.listAccountServiceOverrides as jest.Mock)
      .mockResolvedValueOnce([])  // initial: empty -> auto-opens modal
      .mockResolvedValueOnce([   // reload after partial save: non-empty keeps modal closed
        { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'rds',        term: 1 },
        { id: 'o2', account_id: 'acc-1', provider: 'aws', service: 'opensearch', term: 1 },
      ]);
    (api.saveAccountServiceOverride as jest.Mock)
      .mockResolvedValueOnce({})  // rds OK
      .mockRejectedValueOnce(new Error('server error'))  // elasticache fails
      .mockResolvedValueOnce({});  // opensearch OK

    const { form, bulkList } = await openBulkModal();

    const targets = ['rds', 'elasticache', 'opensearch'];
    for (const svc of targets) {
      const cb = bulkList.querySelector(`input[value="${svc}"]`) as HTMLInputElement;
      cb.checked = true;
      cb.dispatchEvent(new Event('change'));
    }
    (document.getElementById('override-payment') as HTMLSelectElement).value = 'no-upfront';

    form.dispatchEvent(new Event('submit', { cancelable: true }));
    // Flush all async chains: allSettled + loadOverridesPanel + recommendations.
    for (let i = 0; i < 10; i++) await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).toHaveBeenCalledTimes(3);
    const toastArg = mockShowToast.mock.calls[0]?.[0] as { message: string; kind: string };
    expect(toastArg.kind).toBe('warning');
    expect(toastArg.message).toMatch(/Created 2 overrides/);
    expect(toastArg.message).toContain('elasticache');

    // Modal closes even on partial success.
    const modal = document.getElementById('override-modal') as HTMLElement;
    expect(modal.classList.contains('hidden')).toBe(true);
  });

  test('all saves fail: error shown in form, modal stays open', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    (api.saveAccountServiceOverride as jest.Mock).mockRejectedValue(new Error('timeout'));

    const { form, bulkList } = await openBulkModal();

    const targets = ['rds', 'elasticache'];
    for (const svc of targets) {
      const cb = bulkList.querySelector(`input[value="${svc}"]`) as HTMLInputElement;
      cb.checked = true;
      cb.dispatchEvent(new Event('change'));
    }
    (document.getElementById('override-payment') as HTMLSelectElement).value = 'no-upfront';

    form.dispatchEvent(new Event('submit', { cancelable: true }));
    await new Promise(r => setTimeout(r, 0));

    // No toast on total failure.
    expect(mockShowToast).not.toHaveBeenCalled();

    const errEl = document.getElementById('override-form-error') as HTMLElement;
    expect(errEl.textContent).toMatch(/failed/i);

    // Modal stays open so user can retry.
    const modal = document.getElementById('override-modal') as HTMLElement;
    expect(modal.classList.contains('hidden')).toBe(false);
  });

  test('submit without any box checked shows inline error, never calls API', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({});

    const { form } = await openBulkModal();

    (document.getElementById('override-payment') as HTMLSelectElement).value = 'no-upfront';
    // No checkboxes checked.
    form.dispatchEvent(new Event('submit', { cancelable: true }));
    await new Promise(r => setTimeout(r, 0));

    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
    const errEl = document.getElementById('override-form-error') as HTMLElement;
    expect(errEl.textContent).toMatch(/Select at least one/i);
  });

  test('already-overridden services are excluded from the checkbox list', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'acc-1', name: 'Prod', external_id: '111', enabled: true, provider: 'aws' },
    ]);
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'aws', service: 'ec2', term: 1 },
      { id: 'o2', account_id: 'acc-1', provider: 'aws', service: 'rds',  term: 1 },
    ]);
    await loadAccountsForProvider('aws');

    const overridesBtn = document.querySelector(
      `button[aria-label="Service overrides for Prod (111)"]`,
    ) as HTMLButtonElement;
    // Panel has existing overrides so the modal is not auto-opened; open manually.
    const panel = document.getElementById('account-overrides-modal-body') as HTMLElement;
    overridesBtn.click();
    await new Promise(r => setTimeout(r, 0));

    const addBtn = Array.from(panel.querySelectorAll('button')).find(b => b.textContent === 'Add override');
    expect(addBtn).toBeDefined();
    addBtn!.click();

    const bulkToggle = document.getElementById('override-bulk-toggle') as HTMLInputElement;
    bulkToggle.checked = true;
    bulkToggle.dispatchEvent(new Event('change'));

    const bulkList = document.getElementById('override-bulk-services-list') as HTMLElement;
    const values = Array.from(bulkList.querySelectorAll('input[type="checkbox"]')).map(
      cb => (cb as HTMLInputElement).value,
    );
    expect(values).not.toContain('ec2');
    expect(values).not.toContain('rds');
    expect(values).toContain('elasticache');
  });

  test('closing the modal resets bulk toggle to off', async () => {
    const { bulkToggle } = await openBulkModal();
    expect(bulkToggle.checked).toBe(true);

    (document.getElementById('close-override-modal-btn') as HTMLButtonElement).click();

    expect(bulkToggle.checked).toBe(false);
    const singleRow = document.getElementById('override-single-service-row') as HTMLElement;
    expect(singleRow.classList.contains('hidden')).toBe(false);
  });

  // Preventive conflict gating (issue #119): selecting an incompatible
  // term/payment combo must disable the conflicting service checkbox BEFORE
  // the user hits Save, so the all-or-nothing submit block is unreachable in
  // normal use.  The only AWS hard restriction is rds + 3yr + no-upfront.
  test('selecting 3yr/no-upfront disables rds checkbox and API is never called', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    (api.saveAccountServiceOverride as jest.Mock).mockResolvedValue({});

    const { bulkList, form } = await openBulkModal();

    // Check rds first so it is "selected" before the conflict appears.
    const rdsCb = bulkList.querySelector<HTMLInputElement>('input[value="rds"]');
    expect(rdsCb).not.toBeNull();
    rdsCb!.checked = true;
    rdsCb!.dispatchEvent(new Event('change'));
    expect(rdsCb!.disabled).toBe(false);

    // Set term = 3, then payment = no-upfront (the invalid combo for rds).
    const termSel = document.getElementById('override-term') as HTMLSelectElement;
    termSel.value = '3';
    termSel.dispatchEvent(new Event('change'));

    const paymentSel = document.getElementById('override-payment') as HTMLSelectElement;
    paymentSel.value = 'no-upfront';
    paymentSel.dispatchEvent(new Event('change'));

    // rds must now be disabled and unchecked.
    expect(rdsCb!.disabled).toBe(true);
    expect(rdsCb!.checked).toBe(false);

    // A service with no restriction (e.g. ec2) stays enabled.
    const ec2Cb = bulkList.querySelector<HTMLInputElement>('input[value="ec2"]');
    expect(ec2Cb!.disabled).toBe(false);

    // Attempting to submit with rds disabled (nothing checked) must not call
    // the API and must show an inline error.
    form.dispatchEvent(new Event('submit', { cancelable: true }));
    await new Promise(r => setTimeout(r, 0));
    expect(api.saveAccountServiceOverride).not.toHaveBeenCalled();
  });

  test('switching back to a compatible combo re-enables the rds checkbox', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);

    const { bulkList } = await openBulkModal();

    // Trigger the invalid combo.
    const termSel = document.getElementById('override-term') as HTMLSelectElement;
    termSel.value = '3';
    termSel.dispatchEvent(new Event('change'));
    const paymentSel = document.getElementById('override-payment') as HTMLSelectElement;
    paymentSel.value = 'no-upfront';
    paymentSel.dispatchEvent(new Event('change'));

    const rdsCb = bulkList.querySelector<HTMLInputElement>('input[value="rds"]');
    expect(rdsCb!.disabled).toBe(true);

    // Switch payment to a valid combo (partial-upfront).
    paymentSel.value = 'partial-upfront';
    paymentSel.dispatchEvent(new Event('change'));

    // rds should be re-enabled.
    expect(rdsCb!.disabled).toBe(false);
  });
});
