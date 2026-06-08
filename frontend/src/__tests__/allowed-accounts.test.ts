/**
 * allowed_accounts UI enforcement tests (issue #313).
 *
 * The backend enforces `allowed_accounts` on every API endpoint; these
 * tests pin the UI surfaces that rely on that filtering:
 *
 *   1. Account chip in the topbar populates exclusively from the
 *      `listAccounts` response. When the backend returns a filtered
 *      subset (reflecting the user's `allowed_accounts`), only those
 *      accounts appear — disallowed accounts are never shown.
 *
 *   2. History list renders exactly the rows the API returns. The
 *      frontend does no client-side account filtering of its own;
 *      dropping that responsibility on the backend prevents the flicker
 *      scenario where disallowed rows briefly render before being hidden.
 *
 *   3. A 403 response on a mutate-by-id path (Cancel purchase) surfaces
 *      a user-friendly error toast instead of an unhandled exception.
 *
 *   4. When `listAccounts` returns an empty list (zero allowed accounts),
 *      the account chip collapses to the "All Accounts" sentinel only —
 *      no stale option from a previous session bleeds through.
 *
 * Related: backend enforcement tests in #307.
 */

// ---------------------------------------------------------------------------
// Shared mocks (must be declared before any imports)
// ---------------------------------------------------------------------------

jest.mock('../api', () => ({
  // #949/#951: the topbar dropdown now reads the minimal-disclosure endpoint
  // (view:recommendations) instead of the view:accounts list. The backend
  // applies the same allowed_accounts filter, so the mock still stands in for
  // a backend-filtered subset.
  listAccountsMinimal: jest.fn(),
  getHistory: jest.fn(),
  cancelPurchase: jest.fn(),
}));

jest.mock('../toast', () => ({
  showToast: jest.fn(),
}));

jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn(),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
}));

jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val: number) => `$${val || 0}`),
  formatDate: jest.fn((val: string) => (val ? new Date(val).toLocaleDateString() : '')),
  formatTerm: jest.fn((years: number) => (years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`)),
  escapeHtml: jest.fn((str: string) => str || ''),
  escapeHtmlAttr: jest.fn((str: string | null | undefined) => {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }),
  populateAccountFilter: jest.fn(() => Promise.resolve()),
}));

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
  getCurrentProvider: jest.fn().mockReturnValue(''),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
}));

// ---------------------------------------------------------------------------
// Imports (after mocks)
// ---------------------------------------------------------------------------

import { waitFor } from '@testing-library/dom';
import { initTopbarFilters } from '../topbar-filters';
import { loadHistory } from '../history';
import * as api from '../api';
import * as state from '../state';
import { showToast } from '../toast';
import { confirmDialog } from '../confirmDialog';
import { getCurrentUser } from '../state';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const ADMIN = { id: 'admin-uuid', email: 'admin@example.com', groups: ['00000000-0000-5000-8000-000000000001'] };

function setupTopbarSlot(): void {
  while (document.body.firstChild) document.body.removeChild(document.body.firstChild);
  const slot = document.createElement('div');
  slot.id = 'topbar-filters';
  document.body.appendChild(slot);
}

function setupHistoryDOM(): void {
  while (document.body.firstChild) document.body.removeChild(document.body.firstChild);
  const mkInput = (id: string): HTMLInputElement => {
    const el = document.createElement('input');
    el.type = 'date';
    el.id = id;
    return el;
  };
  const mkDiv = (id: string): HTMLDivElement => {
    const el = document.createElement('div');
    el.id = id;
    return el;
  };
  document.body.appendChild(mkInput('history-start'));
  document.body.appendChild(mkInput('history-end'));
  document.body.appendChild(mkDiv('history-summary'));
  document.body.appendChild(mkDiv('history-list'));
  document.body.appendChild(mkDiv('purchases-approval-queue'));
}

function makeHistoryRow(overrides: Record<string, unknown> = {}) {
  return {
    purchase_id: 'exec-1',
    timestamp: '2024-03-01T00:00:00Z',
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    account_id: 'allowed-1',
    cloud_account_id: 'allowed-1',
    count: 1,
    term: 1,
    upfront_cost: 200,
    estimated_savings: 80,
    plan_name: '',
    status: 'pending',
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// 1. Account chip - allowed_accounts enforcement
// ---------------------------------------------------------------------------

describe('Account chip - allowed_accounts enforcement (issue #313)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupTopbarSlot();
    state.setCurrentProvider('');
    state.setCurrentAccountIDs([]);
  });

  afterEach(() => {
    state.setCurrentProvider('');
    state.setCurrentAccountIDs([]);
  });

  test('chip lists only the accounts returned by listAccounts (backend-filtered subset)', async () => {
    // Simulate backend returning only the two accounts the user is allowed
    // to access (the rest are filtered server-side by allowed_accounts).
    (api.listAccountsMinimal as jest.Mock).mockResolvedValue([
      { id: 'allowed-1', name: 'Prod AWS', external_id: '111111111111' },
      { id: 'allowed-2', name: 'Staging AWS', external_id: '222222222222' },
    ]);

    initTopbarFilters();
    // Drain the async populateAccountOptions call.
    await new Promise((r) => setTimeout(r, 0));

    // Open the account chip (second .chip-select trigger).
    const triggers = document.querySelectorAll<HTMLButtonElement>('.chip-select');
    const accountTrigger = triggers[1] as HTMLButtonElement;
    accountTrigger.click();

    const options = Array.from(
      document.querySelectorAll<HTMLLIElement>('.chip-select-option'),
    ).map((el) => el.dataset['value']);

    // "All Accounts" sentinel + exactly the two allowed accounts.
    expect(options).toEqual(['', 'allowed-1', 'allowed-2']);
  });

  test('chip does not contain accounts absent from the listAccounts response', async () => {
    (api.listAccountsMinimal as jest.Mock).mockResolvedValue([
      { id: 'allowed-only', name: 'Allowed Prod', external_id: '999999999999' },
    ]);

    initTopbarFilters();
    await new Promise((r) => setTimeout(r, 0));

    const triggers = document.querySelectorAll<HTMLButtonElement>('.chip-select');
    const accountTrigger = triggers[1] as HTMLButtonElement;
    accountTrigger.click();

    const optionValues = Array.from(
      document.querySelectorAll<HTMLLIElement>('.chip-select-option'),
    ).map((el) => el.dataset['value']);

    expect(optionValues).not.toContain('disallowed-acct');
    expect(optionValues).toContain('allowed-only');
  });

  test('chip shows only All Accounts when listAccounts returns empty list (zero allowed accounts)', async () => {
    (api.listAccountsMinimal as jest.Mock).mockResolvedValue([]);

    initTopbarFilters();
    await new Promise((r) => setTimeout(r, 0));

    const triggers = document.querySelectorAll<HTMLButtonElement>('.chip-select');
    const accountTrigger = triggers[1] as HTMLButtonElement;
    accountTrigger.click();

    const options = Array.from(
      document.querySelectorAll<HTMLLIElement>('.chip-select-option'),
    ).map((el) => el.dataset['value']);

    // Only the sentinel option — no stale accounts from a prior session.
    expect(options).toEqual(['']);
  });
});

// ---------------------------------------------------------------------------
// 2 + 3. History list and 403 on Cancel
// ---------------------------------------------------------------------------

describe('History list - allowed_accounts enforcement (issue #313)', () => {
  beforeEach(() => {
    setupHistoryDOM();
    jest.clearAllMocks();
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.listAccountsMinimal as jest.Mock).mockResolvedValue([]);
  });

  test('renders exactly the rows returned by getHistory (no extra client-side rows)', async () => {
    // Simulate backend returning only two allowed-account rows.
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeHistoryRow({ purchase_id: 'exec-allowed-1', account_id: 'allowed-1' }),
        makeHistoryRow({ purchase_id: 'exec-allowed-2', account_id: 'allowed-1' }),
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    // history.ts renders rows as <tr data-execution-id="..."> (see history.ts renderHistoryList).
    const rows = list.querySelectorAll('tr[data-execution-id]');
    // Two rows — exactly what the API returned.
    expect(rows.length).toBe(2);
  });

  test('renders empty list when getHistory returns zero rows (all accounts disallowed)', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    // No purchase rows rendered.
    const rows = list.querySelectorAll('tr[data-execution-id]');
    expect(rows.length).toBe(0);
  });

  test('403 on Cancel surfaces a user-friendly error toast, not an unhandled exception', async () => {
    // Silence the expected console.error from the cancel button handler's catch block.
    const spy = jest.spyOn(console, 'error').mockImplementation(() => {});

    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeHistoryRow({ purchase_id: 'exec-1', created_by_user_id: ADMIN.id })],
    });

    // Simulate the backend returning 403 (disallowed account).
    const forbidden = new Error('Forbidden') as Error & { status?: number };
    forbidden.status = 403;
    (api.cancelPurchase as jest.Mock).mockRejectedValue(forbidden);

    await loadHistory();

    const btn = document.querySelector<HTMLButtonElement>('.history-cancel-btn');
    btn?.click();

    // A toast with kind:'error' must surface — not a blank crash.
    await waitFor(() => {
      expect(showToast).toHaveBeenCalledWith(
        expect.objectContaining({ kind: 'error' }),
      );
    });

    spy.mockRestore();
  });
});
