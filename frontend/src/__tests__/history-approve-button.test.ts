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
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
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
import { ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID } from '../permissions';

// Admin user includes Purchaser membership (mirrors the auto-migration for
// existing admins on first deploy of issue #923). approve-any:purchases is
// carved out of admin:* and requires Purchaser group membership.
const ADMIN_USER = { id: 'admin-uuid', email: 'admin@example.com', groups: [ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID] };
// Admin without Purchaser membership and without effectivePermissions
// for any carved-out spending verb. Issue #923 explicitly carves
// approve-any:purchases / retry-any:purchases / execute:purchases OUT
// of admin:*, so this user MUST NOT see Approve / Retry buttons on
// rows they did not create. Regression guard for CR #924 F5 — if a
// future refactor reintroduces isAdmin() as the gate, this test
// catches it.
const ADMIN_NO_PURCH = { id: 'admin-no-purch-uuid', email: 'admin-no-purch@example.com', groups: [ADMINISTRATORS_GROUP_ID] };
// REG_USER carries the default-user effective permission set (approve-own
// + cancel-own + retry-own on purchases) so canAccess returns true for
// own-row actions without needing the bootstrap fetch. The previous
// `groups: []` shape relied on the pre-#917 role-string gate that no
// longer exists; the post-rebase canAccess only honors the loaded
// effectivePermissions, so the test fixture must populate it.
const REG_USER = {
  id: 'user-uuid',
  email: 'user@example.com',
  groups: [],
  effectivePermissions: [
    { action: 'approve-own', resource: 'purchases' },
    { action: 'cancel-own', resource: 'purchases' },
    { action: 'retry-own', resource: 'purchases' },
    { action: 'view', resource: 'history' },
  ],
};
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

    // Scope to the Purchase History table only. The Approval queue
    // card (issue #340 sub-task) ALSO renders Approve buttons for
    // pending rows, so a document-wide query would double-count.
    const list = document.getElementById('history-list')!;
    const buttons = list.querySelectorAll<HTMLButtonElement>('.history-approve-btn');
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

    // Scope to the Purchase History table only. The Approval queue
    // card (issue #340 sub-task) ALSO renders Approve buttons for
    // pending rows, so a document-wide query would double-count.
    const list = document.getElementById('history-list')!;
    const buttons = list.querySelectorAll<HTMLButtonElement>('.history-approve-btn');
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

    // Scope to history-list. The queue card also paints the same
    // notified row, but this test is about the table-side filter.
    const list = document.getElementById('history-list')!;
    const ids = Array.from(list.querySelectorAll<HTMLButtonElement>('.history-approve-btn'))
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

    // Scope to history-list. The queue card also renders the pair.
    const list = document.getElementById('history-list')!;
    const approveBtns = list.querySelectorAll<HTMLButtonElement>('.history-approve-btn[data-approve-id]');
    const cancelBtns = list.querySelectorAll<HTMLButtonElement>('.history-cancel-btn[data-cancel-id]');
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
    // Scope to history-list for the disable assertion. The click
    // handler scopes its disable to the clicked button's containing
    // <td> (sameRowActions), so we need both buttons from the SAME row
    // of the SAME view to verify the pair-disable behaviour.
    const list = document.getElementById('history-list')!;
    const approveBtn = list.querySelector<HTMLButtonElement>('.history-approve-btn');
    const cancelBtn = list.querySelector<HTMLButtonElement>('.history-cancel-btn');
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

  test('user without any approve permission sees NO Approve button even on their own rows (issue #1407 four-eyes)', async () => {
    // Regression guard for issue #1407. Before the fix, canApprovePendingRow
    // returned true based on ownership alone (created_by_user_id === user.id)
    // without verifying that the session holds approve-own or approve-any.
    // This caused Viewer-role users (no approve-* in effectivePermissions) to
    // see the Approve button on purchases they submitted.
    //
    // After the fix, approve-own must be checked explicitly before ownership is
    // evaluated; a session without it sees no Approve buttons at all.
    const VIEWER_USER = {
      id: 'viewer-uuid',
      email: 'viewer@example.com',
      groups: [],
      effectivePermissions: [
        { action: 'view', resource: 'recommendations' },
        { action: 'view', resource: 'plans' },
        { action: 'view', resource: 'history' },
        // cancel-own / retry-own / revoke-own present; approve-own intentionally absent.
        { action: 'cancel-own', resource: 'purchases' },
      ],
    };

    (getCurrentUser as jest.Mock).mockReturnValue(VIEWER_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        // Own row: before fix this showed Approve; after fix it must NOT.
        makeRow({ purchase_id: 'exec-mine', created_by_user_id: VIEWER_USER.id }),
        makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_UUID }),
      ],
    });

    await loadHistory();

    // Scope to the history list (not the approval queue card).
    const list = document.getElementById('history-list')!;
    const buttons = list.querySelectorAll<HTMLButtonElement>('.history-approve-btn');
    // No Approve buttons must appear for any row when the session has no
    // approve-own or approve-any permission.
    expect(buttons).toHaveLength(0);
  });

  test('admin WITHOUT Purchaser membership does not see Approve on rows they did not create (CR #924 F5)', async () => {
    // Issue #923 + CR #924 F5: approve-any:purchases is carved out of
    // admin:*. canApprovePendingRow must gate on
    // canAccess('approve-any', 'purchases'), NOT on isAdmin() or
    // isPurchaser() group membership alone. A bare admin (no Purchaser
    // group, no effectivePermissions yet) is exactly the case where
    // the carve-out matters: the legacy implementation would have
    // shown Approve on every pending row.
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_NO_PURCH);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'exec-mine', created_by_user_id: ADMIN_NO_PURCH.id }),
        makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_UUID }),
        makeRow({ purchase_id: 'exec-legacy', created_by_user_id: undefined }),
      ],
    });

    await loadHistory();

    // Scope to the history list (not the approval queue card).
    const list = document.getElementById('history-list')!;
    const buttons = list.querySelectorAll<HTMLButtonElement>('.history-approve-btn');
    const ids = Array.from(buttons).map((b) => b.dataset['approveId']);
    // Approve renders only via the approve-own fallback (matching
    // created_by_user_id), NOT approve-any.
    expect(ids).toEqual(['exec-mine']);
  });
});
