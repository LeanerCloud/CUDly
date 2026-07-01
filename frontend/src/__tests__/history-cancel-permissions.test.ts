/**
 * History inline Cancel button permission-gating tests (issue #158).
 *
 * Verifies that canCancelPendingRow surfaces the Cancel button for every
 * session the backend authorizeSessionCancel would allow through, not just
 * admins and cancel-own holders.
 *
 * Tested matrix (mirrors history-cancel-button.test.ts for the admin /
 * cancel-own cases; adds the cancel-any operator-role case from issue #158):
 *   1. admin sees Cancel on any pending row regardless of creator.
 *   2. cancel-any:purchases non-admin sees Cancel on any pending row (NEW).
 *   3. cancel-own:purchases non-admin sees Cancel only on their own row.
 *   4. cancel-own:purchases non-admin does NOT see Cancel on another user's row.
 *   5. no-cancel-permission user sees no Cancel buttons.
 *   6. anonymous (no current user) sees no Cancel buttons.
 *
 * The permissions module is mocked so we can inject arbitrary permission sets
 * without adding a built-in role that carries cancel-any.
 */

import { loadHistory } from '../history';

jest.mock('../api', () => ({
  getHistory: jest.fn(),
  cancelPurchase: jest.fn(),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
}));

jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  formatDate: jest.fn((val) => (val ? new Date(val).toLocaleDateString() : '')),
  formatTerm: jest.fn((years) => (years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`)),
  escapeHtml: jest.fn((str) => str || ''),
  escapeHtmlAttr: jest.fn((str: string | null | undefined) => {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }),
  populateAccountFilter: jest.fn(() => Promise.resolve()),
}));

jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn(),
}));

jest.mock('../toast', () => ({
  showToast: jest.fn(),
}));

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
  getCurrentProvider: jest.fn().mockReturnValue(''),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
  getAmortizeUpfront: jest.fn().mockReturnValue(false),
  setAmortizeUpfront: jest.fn(),
  subscribeAmortizeUpfront: jest.fn().mockReturnValue(() => {}),
  getPurchaseHistoryColumnFilters: jest.fn().mockReturnValue({}),
  setPurchaseHistoryColumnFilter: jest.fn(),
  clearAllPurchaseHistoryColumnFilters: jest.fn(),
  getApprovalQueueColumnFilters: jest.fn().mockReturnValue({}),
  setApprovalQueueColumnFilter: jest.fn(),
  clearAllApprovalQueueColumnFilters: jest.fn(),
}));

// Mock permissions so we can inject arbitrary permission sets, including
// cancel-any which belongs to no built-in role in the current defaults.
// isAdmin is included because history.ts still relies on it for the
// approve / retry helpers (out of scope of this PR); leaving it out would
// surface as a TypeError inside renderApprovalQueue and short-circuit the
// render path the cancel-permission assertions depend on.
jest.mock('../permissions', () => ({
  canAccess: jest.fn(),
  isAdmin: jest.fn().mockReturnValue(false),
}));

import * as api from '../api';
import { getCurrentUser } from '../state';
import { canAccess, isAdmin } from '../permissions';

const ADMIN_ID = 'admin-uuid';
const USER_ID = 'user-uuid';
const OTHER_ID = 'other-uuid';

// Helper: configure canAccess to behave like a specific permission set.
// Also configures isAdmin to follow the same admin:* signal so the
// out-of-scope approve / retry helpers (which still call isAdmin) stay
// consistent with the canAccess gate this test exercises.
function setPermissions(perms: string[]): void {
  const isAdminSet = perms.includes('admin:*');
  (canAccess as jest.Mock).mockImplementation((action: string, resource: string) => {
    const key = `${action}:${resource}`;
    if (isAdminSet) return true;
    return perms.includes(key);
  });
  (isAdmin as jest.Mock).mockReturnValue(isAdminSet);
}

function setupDOM(): void {
  while (document.body.firstChild) document.body.removeChild(document.body.firstChild);

  const mkInput = (id: string): HTMLInputElement => {
    const el = document.createElement('input');
    el.type = 'date';
    el.id = id;
    return el;
  };
  const mkSelect = (id: string): HTMLSelectElement => {
    const el = document.createElement('select');
    el.id = id;
    const opt = document.createElement('option');
    opt.value = '';
    opt.textContent = 'All';
    el.appendChild(opt);
    return el;
  };
  const mkDiv = (id: string): HTMLDivElement => {
    const el = document.createElement('div');
    el.id = id;
    return el;
  };

  document.body.appendChild(mkInput('history-start'));
  document.body.appendChild(mkInput('history-end'));
  document.body.appendChild(mkSelect('history-provider-filter'));
  document.body.appendChild(mkSelect('history-account-filter'));
  document.body.appendChild(mkDiv('history-summary'));
  document.body.appendChild(mkDiv('history-list'));
  document.body.appendChild(mkDiv('purchases-approval-queue'));
}

function makeRow(overrides: Record<string, unknown>) {
  return {
    purchase_id: 'exec-1',
    timestamp: '2024-01-15T00:00:00Z',
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    upfront_cost: 100,
    estimated_savings: 50,
    plan_name: '',
    status: 'pending',
    ...overrides,
  };
}

describe('History Cancel button permission gating (issue #158)', () => {
  beforeEach(() => {
    setupDOM();
    jest.clearAllMocks();
  });

  test('admin sees Cancel on every pending row regardless of creator', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue({ id: ADMIN_ID, email: 'admin@example.com', role: 'admin' });
    setPermissions(['admin:*']);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'exec-mine', created_by_user_id: ADMIN_ID }),
        makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_ID }),
        makeRow({ purchase_id: 'exec-legacy', created_by_user_id: undefined }),
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    const ids = Array.from(list.querySelectorAll<HTMLButtonElement>('.history-cancel-btn'))
      .map((b) => b.dataset['cancelId']);
    expect(ids).toEqual(expect.arrayContaining(['exec-mine', 'exec-other', 'exec-legacy']));
    expect(ids).toHaveLength(3);
  });

  test('cancel-any:purchases non-admin sees Cancel on any pending row (issue #158)', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue({ id: USER_ID, email: 'operator@example.com', role: 'operator' });
    setPermissions(['cancel-any:purchases']);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'exec-mine', created_by_user_id: USER_ID }),
        makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_ID }),
        makeRow({ purchase_id: 'exec-legacy', created_by_user_id: undefined }),
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    const ids = Array.from(list.querySelectorAll<HTMLButtonElement>('.history-cancel-btn'))
      .map((b) => b.dataset['cancelId']);
    expect(ids).toEqual(expect.arrayContaining(['exec-mine', 'exec-other', 'exec-legacy']));
    expect(ids).toHaveLength(3);
  });

  test('cancel-own:purchases user sees Cancel only on their own pending row', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue({ id: USER_ID, email: 'user@example.com', role: 'user' });
    setPermissions(['cancel-own:purchases']);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'exec-mine', created_by_user_id: USER_ID }),
        makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_ID }),
        makeRow({ purchase_id: 'exec-legacy', created_by_user_id: undefined }),
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    const ids = Array.from(list.querySelectorAll<HTMLButtonElement>('.history-cancel-btn'))
      .map((b) => b.dataset['cancelId']);
    expect(ids).toEqual(['exec-mine']);
  });

  test('cancel-own:purchases user does not see Cancel on another user row', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue({ id: USER_ID, email: 'user@example.com', role: 'user' });
    setPermissions(['cancel-own:purchases']);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_ID }),
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    const buttons = list.querySelectorAll<HTMLButtonElement>('.history-cancel-btn');
    expect(buttons).toHaveLength(0);
  });

  test('no-cancel-permission user sees no Cancel buttons', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue({ id: USER_ID, email: 'readonly@example.com', role: 'readonly' });
    setPermissions(['view:history', 'view:purchases']);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'exec-mine', created_by_user_id: USER_ID }),
        makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_ID }),
      ],
    });

    await loadHistory();

    expect(document.querySelectorAll('.history-cancel-btn')).toHaveLength(0);
  });

  test('anonymous session (no current user) sees no Cancel buttons', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(null);
    (canAccess as jest.Mock).mockReturnValue(false);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ created_by_user_id: USER_ID })],
    });

    await loadHistory();

    expect(document.querySelectorAll('.history-cancel-btn')).toHaveLength(0);
  });
});
