/**
 * Settings — account management tests
 * DOM is built with createElement to avoid innerHTML in source.
 */
import {
  loadAccountsForProvider,
  setupSettingsHandlers
} from '../settings';

jest.mock('../api', () => ({
  getConfig: jest.fn(),
  updateConfig: jest.fn(),
  saveAzureCredentials: jest.fn(),
  saveGCPCredentials: jest.fn(),
  listAccounts: jest.fn(),
  createAccount: jest.fn(),
  updateAccount: jest.fn(),
  deleteAccount: jest.fn(),
  testAccountCredentials: jest.fn(),
  saveAccountCredentials: jest.fn()
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
    expect(container.querySelectorAll('.account-row')).toHaveLength(2);
    expect(container.textContent).toContain('Prod');
    expect(container.textContent).toContain('[disabled]');
  });

  test('renders "No accounts configured." for empty list', async () => {
    (api.listAccounts as jest.Mock).mockResolvedValue([]);

    await loadAccountsForProvider('aws');

    expect(document.getElementById('aws-accounts-list')!.textContent).toBe('No accounts configured.');
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
    window.alert = jest.fn();

    document.getElementById('add-aws-account-btn')!.click();
    (document.getElementById('account-name') as HTMLInputElement).value = 'Acct';
    (document.getElementById('account-external-id') as HTMLInputElement).value = '123456789012';

    document.getElementById('account-form')!.dispatchEvent(new Event('submit'));
    await new Promise(r => setTimeout(r, 0));

    expect(window.alert).toHaveBeenCalledWith(expect.stringContaining('Failed to save account'));
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
