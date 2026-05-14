/**
 * History approval queue card tests (issue #340 sub-task).
 *
 * The queue card sits above the Purchase History table inside
 * #purchases-tab and surfaces pending|notified purchases in a dedicated
 * action-focused view. Approve / Cancel buttons reuse the existing
 * history.ts helpers (renderPendingActionButtons + wireRowActionHandlers)
 * so the click flow is the SAME as approving from the history table.
 *
 * Tested matrix:
 *   1. Queue renders only pending|notified rows; skips completed,
 *      cancelled, failed, expired.
 *   2. Queue empty-state copy when no pending rows exist.
 *   3. Approve click on a queue row goes through confirmDialog →
 *      api.approvePurchase → showToast → reload.
 *   4. Cancel click on a queue row goes through confirmDialog →
 *      api.cancelPurchase → showToast → reload.
 *   5. The SAME fixture row renders in BOTH the queue card and the
 *      history table, proving the helper extraction kept the two
 *      views in sync rather than dropping one.
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

function setupDOM(): void {
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

describe('Approval queue card (issue #340 sub-task)', () => {
  beforeEach(() => {
    setupDOM();
    jest.clearAllMocks();
  });

  test('renders only pending|notified rows; skips completed / cancelled / failed / expired', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'p-pending', status: 'pending', created_by_user_id: ADMIN_USER.id }),
        makeRow({ purchase_id: 'p-notified', status: 'notified', created_by_user_id: ADMIN_USER.id }),
        makeRow({ purchase_id: 'p-completed', status: 'completed' }),
        makeRow({ purchase_id: 'p-cancelled', status: 'cancelled' }),
        makeRow({ purchase_id: 'p-failed', status: 'failed' }),
        makeRow({ purchase_id: 'p-expired', status: 'expired' }),
      ],
    });

    await loadHistory();

    const queue = document.getElementById('purchases-approval-queue');
    expect(queue).toBeTruthy();
    const queueRows = queue!.querySelectorAll('tr[data-execution-id]');
    const ids = Array.from(queueRows).map((r) => r.getAttribute('data-execution-id'));
    expect(ids).toEqual(expect.arrayContaining(['p-pending', 'p-notified']));
    expect(ids).toHaveLength(2);
    // Negative assertions on the queue.
    for (const skipped of ['p-completed', 'p-cancelled', 'p-failed', 'p-expired']) {
      expect(ids).not.toContain(skipped);
    }
  });

  test('shows empty-state copy when no pending rows', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'p-completed', status: 'completed' }),
        makeRow({ purchase_id: 'p-cancelled', status: 'cancelled' }),
      ],
    });

    await loadHistory();

    const queue = document.getElementById('purchases-approval-queue');
    expect(queue?.textContent || '').toMatch(/no pending approvals/i);
    expect(queue?.querySelectorAll('tr[data-execution-id]').length).toBe(0);
  });

  test('shows empty-state copy when the full history is empty', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({ summary: {}, purchases: [] });

    await loadHistory();

    const queue = document.getElementById('purchases-approval-queue');
    expect(queue?.textContent || '').toMatch(/no pending approvals/i);
  });

  test('Approve click on a queue row runs confirm + api.approvePurchase + toast + reload', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.approvePurchase as jest.Mock).mockResolvedValue(undefined);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'p-pending', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();
    expect(api.getHistory).toHaveBeenCalledTimes(1);

    // Pick the Approve button INSIDE the queue card specifically.
    const queue = document.getElementById('purchases-approval-queue')!;
    const btn = queue.querySelector<HTMLButtonElement>('.history-approve-btn[data-approve-id="p-pending"]');
    expect(btn).toBeTruthy();
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(confirmDialog).toHaveBeenCalled();
    expect(api.approvePurchase).toHaveBeenCalledWith('p-pending');
    // Reload triggers a second getHistory call.
    expect(api.getHistory).toHaveBeenCalledTimes(2);
    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'success' }));
  });

  test('Cancel click on a queue row runs confirm + api.cancelPurchase + toast + reload', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.cancelPurchase as jest.Mock).mockResolvedValue(undefined);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'p-pending', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();
    expect(api.getHistory).toHaveBeenCalledTimes(1);

    const queue = document.getElementById('purchases-approval-queue')!;
    const btn = queue.querySelector<HTMLButtonElement>('.history-cancel-btn[data-cancel-id="p-pending"]');
    expect(btn).toBeTruthy();
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(confirmDialog).toHaveBeenCalled();
    expect(api.cancelPurchase).toHaveBeenCalledWith('p-pending');
    expect(api.getHistory).toHaveBeenCalledTimes(2);
    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'success' }));
  });

  test('approve API failure on a queue row surfaces error toast (no reload)', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.approvePurchase as jest.Mock).mockRejectedValue(new Error('boom'));
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'p-pending', created_by_user_id: ADMIN_USER.id })],
    });
    console.error = jest.fn();

    await loadHistory();
    const queue = document.getElementById('purchases-approval-queue')!;
    const btn = queue.querySelector<HTMLButtonElement>('.history-approve-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'error' }));
    // No reload after failure.
    expect(api.getHistory).toHaveBeenCalledTimes(1);
  });

  test('pending row appears in BOTH the queue card and the history table (extraction did not drop a view)', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'p-pending', status: 'pending', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();

    const queue = document.getElementById('purchases-approval-queue')!;
    const list = document.getElementById('history-list')!;
    const queueRow = queue.querySelector('tr[data-execution-id="p-pending"]');
    const listRow = list.querySelector('tr[data-execution-id="p-pending"]');
    expect(queueRow).toBeTruthy();
    expect(listRow).toBeTruthy();
    // Approve button in BOTH views.
    expect(queue.querySelectorAll('.history-approve-btn[data-approve-id="p-pending"]').length).toBe(1);
    expect(list.querySelectorAll('.history-approve-btn[data-approve-id="p-pending"]').length).toBe(1);
  });
});
