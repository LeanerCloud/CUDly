/**
 * History inline Approve button tests (issue #286).
 *
 * Mirror of the backend RBAC matrix in
 * internal/api/handler_purchases.go::authorizeSessionApprove — keep
 * both sides in sync. The backend remains authoritative; these tests
 * only verify the UX gate (don't render buttons users can't use).
 *
 * Tested matrix:
 *   1. admin sees Approve on every pending row, regardless of creator.
 *   2. regular user sees Approve only on rows they themselves created.
 *   3. Anonymous (no current user cached) sees no Approve buttons.
 *   4. Approve button absent for non-pending rows (completed, cancelled, ...).
 *   5. Click + decline confirmDialog → no API call.
 *   6. Click + accept → approvePurchase + reload + toast.
 *   7. approvePurchase rejects → toast.error + button re-enabled.
 *   8. Approve and Cancel render together for pending rows where the
 *      session qualifies for both verbs.
 */

import { loadHistory } from '../history';

jest.mock('../api', () => ({
  getHistory: jest.fn(),
  approvePurchase: jest.fn(),
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
}));

import * as api from '../api';
import { confirmDialog } from '../confirmDialog';
import { showToast } from '../toast';
import { getCurrentUser } from '../state';

const ADMIN_USER = { id: 'admin-uuid', email: 'admin@example.com', role: 'admin' };
const REG_USER = { id: 'user-uuid', email: 'user@example.com', role: 'user' };
const OTHER_UUID = 'other-uuid';

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

describe('History inline Approve button (issue #286)', () => {
  beforeEach(() => {
    setupDOM();
    jest.clearAllMocks();
  });

  test('admin sees Approve on every pending row, regardless of creator', async () => {
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

    const buttons = document.querySelectorAll<HTMLButtonElement>('.history-approve-btn');
    const ids = Array.from(buttons).map((b) => b.dataset['approveId']);
    expect(ids).toEqual(expect.arrayContaining(['exec-mine', 'exec-other', 'exec-legacy']));
    expect(ids).toHaveLength(3);
  });

  test('regular user sees Approve only on rows they created', async () => {
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

    const buttons = document.querySelectorAll<HTMLButtonElement>('.history-approve-btn');
    const ids = Array.from(buttons).map((b) => b.dataset['approveId']);
    expect(ids).toEqual(['exec-mine']);
  });

  test('renders no Approve buttons when no current user is cached', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(null);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ created_by_user_id: 'anything' })],
    });

    await loadHistory();

    expect(document.querySelectorAll('.history-approve-btn')).toHaveLength(0);
  });

  test('Approve button absent on non-pending rows even for admin', async () => {
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

    const ids = Array.from(document.querySelectorAll<HTMLButtonElement>('.history-approve-btn'))
      .map((b) => b.dataset['approveId']);
    expect(ids).toEqual(['exec-notified']);
  });

  test('Approve and Cancel render side-by-side for pending rows the session qualifies for', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(REG_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: REG_USER.id })],
    });

    await loadHistory();

    const approveBtns = document.querySelectorAll<HTMLButtonElement>('.history-approve-btn[data-approve-id]');
    const cancelBtns = document.querySelectorAll<HTMLButtonElement>('.history-cancel-btn[data-cancel-id]');
    expect(approveBtns).toHaveLength(1);
    expect(cancelBtns).toHaveLength(1);
    expect(approveBtns[0]?.dataset['approveId']).toBe('exec-1');
    expect(cancelBtns[0]?.dataset['cancelId']).toBe('exec-1');
  });

  test('declined confirm dialog skips API call', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(false);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();
    const btn = document.querySelector<HTMLButtonElement>('.history-approve-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(confirmDialog).toHaveBeenCalled();
    expect(api.approvePurchase).not.toHaveBeenCalled();
  });

  test('accepted confirm posts approve + reloads + toasts', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.approvePurchase as jest.Mock).mockResolvedValue(undefined);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();
    expect(api.getHistory).toHaveBeenCalledTimes(1);

    const btn = document.querySelector<HTMLButtonElement>('.history-approve-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(api.approvePurchase).toHaveBeenCalledWith('exec-1');
    // Reload was triggered → second getHistory call.
    expect(api.getHistory).toHaveBeenCalledTimes(2);
    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'success' }));
  });

  test('approve API failure surfaces toast and re-enables the button', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.approvePurchase as jest.Mock).mockRejectedValue(new Error('boom'));
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: ADMIN_USER.id })],
    });
    console.error = jest.fn();

    await loadHistory();
    const btn = document.querySelector<HTMLButtonElement>('.history-approve-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'error' }));
    expect(btn?.disabled).toBe(false);
  });

  test('approve click disables BOTH Approve and Cancel for the row while in flight (CR pass)', async () => {
    // Issue #286 + CR pass on PR #299: Approve and Cancel can render
    // together. Clicking Approve should disable BOTH buttons until the
    // request completes — otherwise a quick double-click could fire
    // a conflicting Cancel on the same row before the reload runs.
    (getCurrentUser as jest.Mock).mockReturnValue(REG_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    let resolveApprove: (() => void) | undefined;
    (api.approvePurchase as jest.Mock).mockReturnValue(
      new Promise<void>((r) => { resolveApprove = r; }),
    );
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: REG_USER.id })],
    });

    await loadHistory();
    const approveBtn = document.querySelector<HTMLButtonElement>('.history-approve-btn');
    const cancelBtn = document.querySelector<HTMLButtonElement>('.history-cancel-btn');
    expect(approveBtn).not.toBeNull();
    expect(cancelBtn).not.toBeNull();
    expect(approveBtn?.disabled).toBe(false);
    expect(cancelBtn?.disabled).toBe(false);

    approveBtn?.click();
    // Let the click handler reach the disable step (microtasks for the
    // confirmDialog await chain).
    await new Promise((r) => setTimeout(r, 0));
    await new Promise((r) => setTimeout(r, 0));

    // While the API call is unresolved, BOTH buttons are disabled.
    expect(approveBtn?.disabled).toBe(true);
    expect(cancelBtn?.disabled).toBe(true);

    // Resolve the in-flight promise + let the success path complete.
    resolveApprove?.();
    await new Promise((r) => setTimeout(r, 10));
  });

  test('approve API failure re-enables BOTH Approve and Cancel for the row (CR pass)', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(REG_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.approvePurchase as jest.Mock).mockRejectedValue(new Error('boom'));
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-1', created_by_user_id: REG_USER.id })],
    });
    console.error = jest.fn();

    await loadHistory();
    const approveBtn = document.querySelector<HTMLButtonElement>('.history-approve-btn');
    const cancelBtn = document.querySelector<HTMLButtonElement>('.history-cancel-btn');
    approveBtn?.click();
    await new Promise((r) => setTimeout(r, 10));

    // After the API rejects, BOTH buttons must be re-enabled.
    expect(approveBtn?.disabled).toBe(false);
    expect(cancelBtn?.disabled).toBe(false);
    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'error' }));
  });
});
