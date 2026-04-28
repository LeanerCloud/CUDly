/**
 * Settings — account management tests
 * DOM is built with createElement to avoid innerHTML in source.
 */
import {
  loadAccountsForProvider,
  loadOverridesPanel,
  openOverrideModal,
  setupSettingsHandlers
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
  deleteAccountServiceOverride: jest.fn()
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

  // ── Override modal (issue #104) ────────────────────────────
  const overrideModal = div('override-modal', 'modal hidden');
  const overrideForm = el('form', {}, 'override-form');
  overrideForm.appendChild(input('override-account-id', 'hidden'));
  overrideForm.appendChild(input('override-provider', 'hidden'));
  overrideForm.appendChild(select('override-service', []));
  overrideForm.appendChild(select('override-term', ['', '1', '3']));
  overrideForm.appendChild(select('override-payment', ['', 'no-upfront', 'partial-upfront', 'all-upfront']));
  overrideForm.appendChild(input('override-coverage', 'number'));
  const overrideErr = el('p', {}, 'override-form-error');
  overrideForm.appendChild(overrideErr);
  const overrideSubmit = el('button', { type: 'submit' }) as HTMLButtonElement;
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

  test('non-AWS rows render the existing read-only payment cell, no <select>', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'o1', account_id: 'acc-1', provider: 'azure', service: 'vm', payment: 'all-upfront' },
    ]);

    const panel = await openOverridesPanel('acc-1');
    expect(panel.querySelector('select.override-payment-select')).toBeNull();
    expect(panel.textContent).toContain('all-upfront');
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

  test('non-AWS account: empty state shows passive text, no modal auto-open, no Add button', async () => {
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

    // The inner create modal must NOT have auto-opened for a non-AWS account.
    expect(modal.classList.contains('hidden')).toBe(true);

    // No Add override button on non-AWS for now (issue #104 follow-up).
    const addBtn = Array.from(panel.querySelectorAll('button')).find(b => b.textContent === 'Add override');
    expect(addBtn).toBeUndefined();

    // Passive empty-state copy is what they see.
    expect(panel.textContent).toContain('No service overrides set');
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
