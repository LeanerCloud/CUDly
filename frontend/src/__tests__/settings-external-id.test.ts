/**
 * Issue #126/#127/#130 regression tests for the AWS External ID
 * lifecycle and the IAM trust-policy snippet renderer.
 *
 * Covers:
 *   - #126 — External ID is stable across modal close/reopen for a
 *     never-saved draft, and is cleared after a successful save.
 *   - #127 — generator never falls back to Math.random; uses
 *     crypto.randomUUID when present and crypto.getRandomValues
 *     otherwise.
 *   - #130a — trust-policy renderer short-circuits if the auth-mode
 *     select changes while waiting on getConfig.
 *   - #130c — trust-policy ARN respects the partition reported by the
 *     backend (`aws`, `aws-cn`, `aws-us-gov`).
 *   - #130d — getConfig is fetched at most once per page load even
 *     across multiple modal opens.
 */
import {
  resetConfigCache,
  setupSettingsHandlers,
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
}));

jest.mock('../toast', () => ({
  showToast: () => ({ dismiss: () => undefined }),
}));

jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn(),
}));

import * as api from '../api';

// ---------------------------------------------------------------------------
// DOM helpers (subset of settings-accounts.test.ts — kept private so the
// two suites stay independent).
// ---------------------------------------------------------------------------

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Record<string, string> = {},
  id?: string,
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

function buildAccountsDOM(): void {
  document.body.replaceChildren();

  document.body.appendChild(div('aws-accounts-list'));
  document.body.appendChild(div('azure-accounts-list'));
  document.body.appendChild(div('gcp-accounts-list'));
  document.body.appendChild(btn('add-aws-account-btn'));
  document.body.appendChild(btn('add-azure-account-btn'));
  document.body.appendChild(btn('add-gcp-account-btn'));

  const modal = div('account-modal', 'hidden');
  modal.appendChild(el('h3', {}, 'account-modal-title'));

  const form = el('form', {}, 'account-form');
  form.appendChild(input('account-id', 'hidden'));
  form.appendChild(input('account-provider', 'hidden'));
  form.appendChild(input('account-name'));
  form.appendChild(el('textarea', {}, 'account-description') as HTMLTextAreaElement);
  form.appendChild(input('account-contact-email', 'email'));
  form.appendChild(input('account-external-id'));
  const enabledCb = input('account-enabled', 'checkbox');
  enabledCb.checked = true;
  form.appendChild(enabledCb);

  const awsFields = div('account-aws-fields');
  awsFields.appendChild(select('account-aws-auth-mode', ['access_keys', 'role_arn', 'bastion', 'workload_identity_federation']));

  const keysDiv = div('account-aws-keys-fields');
  keysDiv.appendChild(input('account-aws-access-key-id'));
  keysDiv.appendChild(input('account-aws-secret-access-key', 'password'));
  awsFields.appendChild(keysDiv);

  const roleDiv = div('account-aws-role-fields', 'hidden');
  roleDiv.appendChild(input('account-aws-role-arn'));
  roleDiv.appendChild(input('account-aws-external-id'));
  roleDiv.appendChild(el('pre', {}, 'account-aws-trust-policy'));
  roleDiv.appendChild(el('small', {}, 'account-aws-trust-policy-hint'));
  awsFields.appendChild(roleDiv);

  const bastionDiv = div('account-aws-bastion-fields', 'hidden');
  bastionDiv.appendChild(select('account-aws-bastion-id', ['']));
  bastionDiv.appendChild(input('account-aws-bastion-role-arn'));
  bastionDiv.appendChild(input('account-aws-external-id-bastion'));
  bastionDiv.appendChild(el('pre', {}, 'account-aws-trust-policy-bastion'));
  bastionDiv.appendChild(el('small', {}, 'account-aws-trust-policy-bastion-hint'));
  awsFields.appendChild(bastionDiv);

  const wifDiv = div('account-aws-wif-fields', 'hidden');
  wifDiv.appendChild(input('account-aws-wif-role-arn'));
  wifDiv.appendChild(input('account-aws-wif-token-file'));
  awsFields.appendChild(wifDiv);

  awsFields.appendChild(input('account-aws-is-org-root', 'checkbox'));
  form.appendChild(awsFields);

  const azureFields = div('account-azure-fields', 'hidden');
  form.appendChild(azureFields);
  const gcpFields = div('account-gcp-fields', 'hidden');
  form.appendChild(gcpFields);

  modal.appendChild(form);
  modal.appendChild(btn('close-account-modal-btn'));
  document.body.appendChild(modal);
}

// ---------------------------------------------------------------------------
// Issue #126 — External ID stable across modal close/reopen
// ---------------------------------------------------------------------------

// installRealSessionStorage swaps the global jest mock (setup.ts) for a
// Map-backed shim that actually persists writes across calls within a
// single test. The setup mock is intentionally inert (every getItem
// returns null) so it doesn't satisfy the issue #126 stable-draft
// invariant — these tests need a real-ish backing store.
function installRealSessionStorage(): void {
  const store = new Map<string, string>();
  Object.defineProperty(global, 'sessionStorage', {
    value: {
      getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
      setItem: (k: string, v: string) => { store.set(k, String(v)); },
      removeItem: (k: string) => { store.delete(k); },
      clear: () => { store.clear(); },
      get length() { return store.size; },
      key: (i: number) => Array.from(store.keys())[i] ?? null,
    },
    writable: true,
    configurable: true,
  });
}

describe('AWS External ID lifecycle (issue #126)', () => {
  beforeEach(() => {
    buildAccountsDOM();
    installRealSessionStorage();
    sessionStorage.clear();
    jest.clearAllMocks();
    (api.listAccounts as jest.Mock).mockResolvedValue([]);
    (api.getConfig as jest.Mock).mockResolvedValue({
      source_identity: { provider: 'aws', account_id: 'cudly-host-acct', partition: 'aws' },
    });
    resetConfigCache();
    setupSettingsHandlers();
  });

  test('reopening Add AWS Account returns the same External ID until saved', () => {
    document.getElementById('add-aws-account-btn')!.click();
    const first = (document.getElementById('account-aws-external-id') as HTMLInputElement).value;
    expect(first).not.toBe('');

    // Close without saving, then reopen.
    document.getElementById('close-account-modal-btn')!.click();
    document.getElementById('add-aws-account-btn')!.click();

    const second = (document.getElementById('account-aws-external-id') as HTMLInputElement).value;
    expect(second).toBe(first);
  });

  test('successful save clears the draft so the next Add starts fresh', async () => {
    (api.createAccount as jest.Mock).mockResolvedValue({
      id: 'new-id', name: 'Acct', provider: 'aws', external_id: '123456789012', enabled: true,
    });

    document.getElementById('add-aws-account-btn')!.click();
    const draft = (document.getElementById('account-aws-external-id') as HTMLInputElement).value;
    expect(draft).not.toBe('');

    (document.getElementById('account-name') as HTMLInputElement).value = 'Acct';
    (document.getElementById('account-external-id') as HTMLInputElement).value = '123456789012';

    document.getElementById('account-form')!.dispatchEvent(new Event('submit'));
    await new Promise(r => setTimeout(r, 0));

    document.getElementById('add-aws-account-btn')!.click();
    const fresh = (document.getElementById('account-aws-external-id') as HTMLInputElement).value;
    expect(fresh).not.toBe('');
    expect(fresh).not.toBe(draft);
  });

  test('first open writes the draft to sessionStorage', () => {
    document.getElementById('add-aws-account-btn')!.click();
    const draftValue = (document.getElementById('account-aws-external-id') as HTMLInputElement).value;
    expect(draftValue).not.toBe('');
    expect(sessionStorage.getItem('cudly:aws-account-modal:draft-external-id'))
      .toBe(draftValue);
  });
});

// ---------------------------------------------------------------------------
// Issue #127 — generator uses CSPRNG, never Math.random
// ---------------------------------------------------------------------------

describe('External ID generator entropy (issue #127)', () => {
  beforeEach(() => {
    buildAccountsDOM();
    installRealSessionStorage();
    sessionStorage.clear();
    jest.clearAllMocks();
    (api.listAccounts as jest.Mock).mockResolvedValue([]);
    (api.getConfig as jest.Mock).mockResolvedValue({
      source_identity: { provider: 'aws', account_id: 'cudly-host-acct', partition: 'aws' },
    });
    resetConfigCache();
    setupSettingsHandlers();
  });

  test('production path uses crypto.randomUUID', () => {
    const spy = jest.spyOn(crypto, 'randomUUID');
    document.getElementById('add-aws-account-btn')!.click();
    expect(spy).toHaveBeenCalled();
    spy.mockRestore();
  });

  test('fallback path uses crypto.getRandomValues, not Math.random', () => {
    // Simulate a runtime where randomUUID is missing (e.g., older
    // Safari) but getRandomValues is available — the documented
    // browser-support superset for the fallback.
    const original = (crypto as { randomUUID?: () => string }).randomUUID;
    (crypto as { randomUUID?: () => string }).randomUUID = undefined;

    const grvSpy = jest.spyOn(crypto, 'getRandomValues');
    const mathSpy = jest.spyOn(Math, 'random');

    sessionStorage.clear();
    document.getElementById('add-aws-account-btn')!.click();

    const value = (document.getElementById('account-aws-external-id') as HTMLInputElement).value;
    expect(grvSpy).toHaveBeenCalled();
    // Must NOT touch Math.random — that's the issue #127 regression.
    expect(mathSpy).not.toHaveBeenCalled();
    // 16 bytes hex-encoded → 32 chars.
    expect(value).toMatch(/^[0-9a-f]{32}$/);

    grvSpy.mockRestore();
    mathSpy.mockRestore();
    (crypto as { randomUUID?: () => string }).randomUUID = original;
  });

  test('generator yields unique values across repeated draft resets (no collisions)', () => {
    // 200 fresh sessions is enough to flush out a buggy non-CSPRNG that
    // collides quickly (Math.random fed from a 30-bit pool would still
    // not collide here, but a constant or seeded fallback would). The
    // underlying CSPRNG correctness is exercised separately above.
    const seen = new Set<string>();
    for (let i = 0; i < 200; i++) {
      sessionStorage.clear();
      document.getElementById('add-aws-account-btn')!.click();
      seen.add((document.getElementById('account-aws-external-id') as HTMLInputElement).value);
      document.getElementById('close-account-modal-btn')!.click();
    }
    expect(seen.size).toBe(200);
  });
});

// ---------------------------------------------------------------------------
// Issue #130a/c/d — trust-policy snippet renderer
// ---------------------------------------------------------------------------

describe('AWS trust-policy snippet (issue #130)', () => {
  beforeEach(() => {
    buildAccountsDOM();
    installRealSessionStorage();
    sessionStorage.clear();
    jest.clearAllMocks();
    (api.listAccounts as jest.Mock).mockResolvedValue([]);
    resetConfigCache();
  });

  test('partition aws-cn produces an arn:aws-cn:iam:: principal (#130c)', async () => {
    (api.getConfig as jest.Mock).mockResolvedValue({
      source_identity: { provider: 'aws', account_id: 'cudly-cn-host', partition: 'aws-cn' },
    });
    setupSettingsHandlers();
    document.getElementById('add-aws-account-btn')!.click();
    // The trust-policy <pre> is rendered unconditionally inside the
    // populateAwsAccountFields path — it lives in the role_arn fields
    // block but is written regardless of the visible auth mode (the
    // user just doesn't see it until they switch). Wait for the
    // void-async renderer to settle, then assert on the JSON.
    await new Promise(r => setTimeout(r, 0));
    const blockText = document.getElementById('account-aws-trust-policy')!.textContent ?? '';
    const policy = JSON.parse(blockText);
    expect(policy.Statement[0].Principal.AWS).toBe('arn:aws-cn:iam::cudly-cn-host:root');
  });

  test('partition aws-us-gov produces an arn:aws-us-gov:iam:: principal (#130c)', async () => {
    (api.getConfig as jest.Mock).mockResolvedValue({
      source_identity: { provider: 'aws', account_id: 'cudly-gov-host', partition: 'aws-us-gov' },
    });
    setupSettingsHandlers();
    document.getElementById('add-aws-account-btn')!.click();
    await new Promise(r => setTimeout(r, 0));
    const blockText = document.getElementById('account-aws-trust-policy')!.textContent ?? '';
    const policy = JSON.parse(blockText);
    expect(policy.Statement[0].Principal.AWS).toBe('arn:aws-us-gov:iam::cudly-gov-host:root');
  });

  test('missing/unknown partition defaults to "aws" (#130c)', async () => {
    (api.getConfig as jest.Mock).mockResolvedValue({
      source_identity: { provider: 'aws', account_id: 'cudly-classic-host' /* no partition */ },
    });
    setupSettingsHandlers();
    document.getElementById('add-aws-account-btn')!.click();
    await new Promise(r => setTimeout(r, 0));
    const blockText = document.getElementById('account-aws-trust-policy')!.textContent ?? '';
    const policy = JSON.parse(blockText);
    expect(policy.Statement[0].Principal.AWS).toBe('arn:aws:iam::cudly-classic-host:root');
  });

  test('renderer short-circuits when auth mode changes during getConfig (#130a)', async () => {
    let resolveCfg!: (value: unknown) => void;
    (api.getConfig as jest.Mock).mockImplementation(
      () => new Promise(r => { resolveCfg = r; }),
    );
    setupSettingsHandlers();

    document.getElementById('add-aws-account-btn')!.click();
    // Set auth mode to role_arn so the snapshot is "role_arn".
    const authMode = document.getElementById('account-aws-auth-mode') as HTMLSelectElement;
    authMode.value = 'role_arn';
    authMode.dispatchEvent(new Event('change'));

    // Now switch away while getConfig is still pending.
    authMode.value = 'bastion';
    authMode.dispatchEvent(new Event('change'));

    // Resolve with a valid AWS partition. Snapshot != live → renderer
    // must NOT write into the now-hidden role-fields block.
    resolveCfg({
      source_identity: { provider: 'aws', account_id: 'cudly-host-acct', partition: 'aws' },
    });
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));

    expect(document.getElementById('account-aws-trust-policy')!.textContent).toBe('');
  });

  test('getConfig is called at most once across multiple modal opens (#130d)', async () => {
    (api.getConfig as jest.Mock).mockResolvedValue({
      source_identity: { provider: 'aws', account_id: 'cudly-host-acct', partition: 'aws' },
    });
    setupSettingsHandlers();

    document.getElementById('add-aws-account-btn')!.click();
    await new Promise(r => setTimeout(r, 0));
    document.getElementById('close-account-modal-btn')!.click();

    document.getElementById('add-aws-account-btn')!.click();
    await new Promise(r => setTimeout(r, 0));
    document.getElementById('close-account-modal-btn')!.click();

    document.getElementById('add-aws-account-btn')!.click();
    await new Promise(r => setTimeout(r, 0));

    expect((api.getConfig as jest.Mock).mock.calls.length).toBe(1);
  });
});

// ---------------------------------------------------------------------------
// Issue #129 — bastion auth mode collects + renders the External ID
// ---------------------------------------------------------------------------

describe('AWS bastion-mode External ID + trust policy (issue #129)', () => {
  beforeEach(() => {
    buildAccountsDOM();
    installRealSessionStorage();
    sessionStorage.clear();
    jest.clearAllMocks();
    (api.listAccounts as jest.Mock).mockResolvedValue([
      { id: 'bastion-uuid-1', name: 'Bastion', provider: 'aws',
        external_id: '999888777666', enabled: true },
    ]);
    (api.getConfig as jest.Mock).mockResolvedValue({
      source_identity: { provider: 'aws', account_id: 'cudly-host-acct', partition: 'aws' },
    });
    resetConfigCache();
    setupSettingsHandlers();
  });

  test('bastion mode shows the same draft External ID as role_arn mode', () => {
    document.getElementById('add-aws-account-btn')!.click();
    const roleVal = (document.getElementById('account-aws-external-id') as HTMLInputElement).value;
    const bastionVal = (document.getElementById('account-aws-external-id-bastion') as HTMLInputElement).value;
    expect(bastionVal).not.toBe('');
    expect(bastionVal).toBe(roleVal);
  });

  test('selecting a bastion renders a trust-policy snippet whose Principal is the bastion account', async () => {
    document.getElementById('add-aws-account-btn')!.click();
    const authMode = document.getElementById('account-aws-auth-mode') as HTMLSelectElement;
    authMode.value = 'bastion';
    authMode.dispatchEvent(new Event('change'));
    // Give populateBastionAccountDropdown a tick to settle.
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));

    const select = document.getElementById('account-aws-bastion-id') as HTMLSelectElement;
    select.value = 'bastion-uuid-1';
    select.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));

    const snippet = document.getElementById('account-aws-trust-policy-bastion')!.textContent ?? '';
    expect(snippet).not.toBe('');
    const policy = JSON.parse(snippet);
    expect(policy.Statement[0].Principal.AWS).toBe('arn:aws:iam::999888777666:root');
    const externalID = (document.getElementById('account-aws-external-id-bastion') as HTMLInputElement).value;
    expect(policy.Statement[0].Condition.StringEquals['sts:ExternalId']).toBe(externalID);
  });

  test('buildAccountRequest collects aws_external_id for bastion mode', async () => {
    (api.createAccount as jest.Mock).mockResolvedValue({
      id: 'new-id', name: 'Acct', provider: 'aws', external_id: '111122223333', enabled: true,
    });

    document.getElementById('add-aws-account-btn')!.click();
    const authMode = document.getElementById('account-aws-auth-mode') as HTMLSelectElement;
    authMode.value = 'bastion';
    authMode.dispatchEvent(new Event('change'));
    await new Promise(r => setTimeout(r, 0));

    (document.getElementById('account-name') as HTMLInputElement).value = 'Target';
    (document.getElementById('account-external-id') as HTMLInputElement).value = '111122223333';
    (document.getElementById('account-aws-bastion-id') as HTMLSelectElement).value = 'bastion-uuid-1';
    (document.getElementById('account-aws-bastion-role-arn') as HTMLInputElement).value =
      'arn:aws:iam::111122223333:role/CUDly';

    const draft = (document.getElementById('account-aws-external-id-bastion') as HTMLInputElement).value;
    expect(draft).not.toBe('');

    document.getElementById('account-form')!.dispatchEvent(new Event('submit'));
    await new Promise(r => setTimeout(r, 0));

    expect((api.createAccount as jest.Mock).mock.calls.length).toBe(1);
    const submitted = (api.createAccount as jest.Mock).mock.calls[0][0];
    expect(submitted.aws_auth_mode).toBe('bastion');
    expect(submitted.aws_external_id).toBe(draft);
    expect(submitted.aws_bastion_id).toBe('bastion-uuid-1');
  });

  test('toggling between role_arn and bastion preserves the draft value', () => {
    document.getElementById('add-aws-account-btn')!.click();
    const roleInput = document.getElementById('account-aws-external-id') as HTMLInputElement;
    const bastionInput = document.getElementById('account-aws-external-id-bastion') as HTMLInputElement;
    const initial = roleInput.value;
    expect(initial).not.toBe('');
    expect(bastionInput.value).toBe(initial);

    // Close + reopen — both inputs should still carry the same draft.
    document.getElementById('close-account-modal-btn')!.click();
    document.getElementById('add-aws-account-btn')!.click();
    expect(roleInput.value).toBe(initial);
    expect(bastionInput.value).toBe(initial);
  });
});

// ---------------------------------------------------------------------------
// Regression guard: source code must not contain a Math.random fallback
// in the External ID generator (issue #127).
// ---------------------------------------------------------------------------

describe('source-level regression guard for #127', () => {
  test('settings.ts generateExternalID has no Math.random call', () => {
    const fs = jest.requireActual('fs') as typeof import('fs');
    const path = jest.requireActual('path') as typeof import('path');
    const src = fs.readFileSync(
      path.resolve(__dirname, '..', 'settings.ts'),
      'utf8',
    );
    // Strip line comments and block comments so the assertion only fires
    // on real call sites (we keep a couple of inline comments referencing
    // Math.random() to explain *why* we don't use it). Block-strip first
    // so trailing `// ...` inside a block comment line doesn't survive.
    const stripped = src
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/^\s*\/\/.*$/gm, '')
      .replace(/\/\/.*$/gm, '');
    expect(stripped).not.toMatch(/Math\.random\s*\(/);
  });
});
