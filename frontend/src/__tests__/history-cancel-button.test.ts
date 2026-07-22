/**
 * History inline Cancel button tests (issue #46).
 *
 * Mirror of the backend RBAC matrix in
 * internal/api/handler_purchases.go::authorizeSessionCancel — keep both
 * sides in sync. The backend remains authoritative; these tests only
 * verify the UX gate (don't render buttons users can't use).
 *
 * Tested matrix:
 *   1. admin sees Cancel on every pending row, regardless of creator.
 *   2. regular user sees Cancel only on rows they themselves created.
 *   3. Anonymous (no current user cached) sees no Cancel buttons.
 *   4. Cancel button absent for non-pending rows (completed, cancelled, ...).
 *   5. Click + decline confirmDialog → no API call.
 *   6. Click + accept → cancelPurchase + reload + toast.
 *   7. cancelPurchase rejects → toast.error + button re-enabled.
 */

import { loadHistory } from '../history';

jest.mock('../api', () => ({
  getHistory: jest.fn(),
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
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
  // Issue #344 T2: history.ts reads provider/account from the global
  // topbar filter via state. Default provider 'all' + no account scope
  // preserves the pre-topbar behaviour these tests assume.
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

import * as api from '../api';
import { confirmDialog } from '../confirmDialog';
import { showToast } from '../toast';
import { getCurrentUser } from '../state';
import { ADMINISTRATORS_GROUP_ID } from '../permissions';

const ADMIN_USER = { id: 'admin-uuid', email: 'admin@example.com', groups: [ADMINISTRATORS_GROUP_ID] };
// REG_USER carries the default-user effective permission set (cancel-own
// + approve-own + retry-own on purchases) so canAccess('cancel-own',
// 'purchases') returns true without needing the bootstrap fetch. The
// previous `groups: []` shape relied on the pre-#917 role-string gate
// that no longer exists; the post-rebase canAccess only honors the
// loaded effectivePermissions, so the test fixture must populate it.
const REG_USER = {
  id: 'user-uuid',
  email: 'user@example.com',
  groups: [],
  effectivePermissions: [
    { action: 'cancel-own', resource: 'purchases' },
    { action: 'approve-own', resource: 'purchases' },
    { action: 'retry-own', resource: 'purchases' },
    { action: 'view', resource: 'history' },
  ],
};
const OTHER_UUID = 'other-uuid';

function setupDOM(): void {
  // Build the test fixture programmatically rather than via innerHTML so
  // the lint hook stays happy. Element list mirrors the production
  // history page's controls used by loadHistory().
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
  // Issue #340 sub-task: loadHistory now also paints the approval-queue
  // card. The container must exist so renderApprovalQueue's
  // getElementById lookup succeeds.
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

describe('History inline Cancel button (issue #46)', () => {
  beforeEach(() => {
    setupDOM();
    jest.clearAllMocks();
  });

  test('admin sees Cancel on every pending row, regardless of creator', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'exec-mine', created_by_user_id: ADMIN_USER.id }),
        makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_UUID }),
        makeRow({ purchase_id: 'exec-legacy', created_by_user_id: undefined }),
      ],
    });

    await loadHistory();

    // Scope to history-list. The queue card (issue #340 sub-task)
    // also renders Cancel buttons for pending rows.
    const list = document.getElementById('history-list')!;
    const buttons = list.querySelectorAll<HTMLButtonElement>('.history-cancel-btn');
    const ids = Array.from(buttons).map((b) => b.dataset['cancelId']);
    expect(ids).toEqual(expect.arrayContaining(['exec-mine', 'exec-other', 'exec-legacy']));
    expect(ids).toHaveLength(3);
  });

  test('regular user sees Cancel only on rows they created', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(REG_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'exec-mine', created_by_user_id: REG_USER.id }),
        makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_UUID }),
        makeRow({ purchase_id: 'exec-legacy', created_by_user_id: undefined }),
      ],
    });

    await loadHistory();

    // Scope to history-list. The queue card (issue #340 sub-task)
    // also renders Cancel buttons for pending rows.
    const list = document.getElementById('history-list')!;
    const buttons = list.querySelectorAll<HTMLButtonElement>('.history-cancel-btn');
    const ids = Array.from(buttons).map((b) => b.dataset['cancelId']);
    expect(ids).toEqual(['exec-mine']);
  });

  test('renders no Cancel buttons when no current user is cached', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(null);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ created_by_user_id: 'anything' })],
    });

    await loadHistory();

    expect(document.querySelectorAll('.history-cancel-btn')).toHaveLength(0);
  });

  test('Cancel button absent on non-pending rows even for admin', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'exec-completed', status: 'completed', created_by_user_id: ADMIN_USER.id }),
        makeRow({ purchase_id: 'exec-cancelled', status: 'cancelled', created_by_user_id: ADMIN_USER.id }),
        makeRow({ purchase_id: 'exec-failed', status: 'failed', created_by_user_id: ADMIN_USER.id }),
        // Sanity: notified is treated like pending and SHOULD render the button.
        makeRow({ purchase_id: 'exec-notified', status: 'notified', created_by_user_id: ADMIN_USER.id }),
      ],
    });

    await loadHistory();

    // Scope to history-list. The queue card also paints the same
    // notified row.
    const list = document.getElementById('history-list')!;
    const ids = Array.from(list.querySelectorAll<HTMLButtonElement>('.history-cancel-btn'))
      .map((b) => b.dataset['cancelId']);
    expect(ids).toEqual(['exec-notified']);
  });

  test('declined confirm dialog skips API call', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(false);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();
    const btn = document.querySelector<HTMLButtonElement>('.history-cancel-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(confirmDialog).toHaveBeenCalled();
    expect(api.cancelPurchase).not.toHaveBeenCalled();
  });

  test('accepted confirm posts cancel + reloads + toasts', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.cancelPurchase as jest.Mock).mockResolvedValue(undefined);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();
    expect(api.getHistory).toHaveBeenCalledTimes(1);

    const btn = document.querySelector<HTMLButtonElement>('.history-cancel-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(api.cancelPurchase).toHaveBeenCalledWith('exec-1');
    // Reload was triggered → second getHistory call.
    expect(api.getHistory).toHaveBeenCalledTimes(2);
    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'success' }));
  });

  test('successful cancel + failed reload still surfaces success toast', async () => {
    // CR feedback (PR #145): the cancel POST and refresh must not share
    // a try/catch — a failed reload can't be allowed to override the
    // success of the cancel itself. The user already gave intent and
    // the row IS cancelled on the backend; surfacing "Failed to cancel"
    // would be wrong.
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.cancelPurchase as jest.Mock).mockResolvedValue(undefined);
    let getCallCount = 0;
    (api.getHistory as jest.Mock).mockImplementation(() => {
      getCallCount++;
      if (getCallCount === 1) {
        return Promise.resolve({
          summary: {},
          purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: ADMIN_USER.id })],
        });
      }
      return Promise.reject(new Error('refresh failed'));
    });
    console.error = jest.fn();

    await loadHistory();
    const btn = document.querySelector<HTMLButtonElement>('.history-cancel-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    // Success toast lands BEFORE the reload error.
    const successCalls = (showToast as jest.Mock).mock.calls.filter((c) => c[0].kind === 'success');
    const errorCalls = (showToast as jest.Mock).mock.calls.filter((c) => c[0].kind === 'error');
    expect(successCalls.length).toBeGreaterThan(0);
    expect(errorCalls).toHaveLength(0); // the reload-failure path must NOT toast error
  });

  test('cancel API failure surfaces toast and re-enables the button', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.cancelPurchase as jest.Mock).mockRejectedValue(new Error('boom'));
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: ADMIN_USER.id })],
    });
    console.error = jest.fn();

    await loadHistory();
    const btn = document.querySelector<HTMLButtonElement>('.history-cancel-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'error' }));
    expect(btn?.disabled).toBe(false);
  });
});
