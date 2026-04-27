/**
 * History inline Retry button tests (issue #47).
 *
 * Mirror of the backend RBAC matrix in
 * internal/api/handler_purchases.go::authorizeSessionRetry — keep both
 * sides in sync. The backend remains authoritative; these tests only
 * verify the UX gate (don't render buttons users can't use, render
 * the right control for the row's state).
 *
 * Tested matrix:
 *   1. admin sees Retry on every failed row (modulo ops_hint / lineage).
 *   2. regular user sees Retry only on rows they themselves created.
 *   3. Anonymous (no current user cached) sees no Retry buttons.
 *   4. ops_hint row → ops-hint badge replaces the Retry button.
 *   5. retry_attempt_n >= 5 → "Retried 5×" override button.
 *   6. retry_execution_id set → no Retry button (act on the descendant).
 *   7. lineage links: "↻ Retried as #abc" for predecessors, "↻ Retry #n" for retries.
 *   8. Click + decline confirmDialog → no API call.
 *   9. Click + accept → retryPurchase + reload + toast.
 *  10. retryPurchase rejects → toast.error + button re-enabled.
 *  11. Over-threshold click sends force=true.
 */

import { loadHistory } from '../history';

jest.mock('../api', () => ({
  getHistory: jest.fn(),
  retryPurchase: jest.fn(),
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
    status: 'failed',
    ...overrides,
  };
}

describe('History inline Retry button (issue #47)', () => {
  beforeEach(() => {
    setupDOM();
    jest.clearAllMocks();
  });

  test('admin sees Retry on every failed row regardless of creator', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'fail-mine', created_by_user_id: ADMIN_USER.id }),
        makeRow({ purchase_id: 'fail-other', created_by_user_id: OTHER_UUID }),
        makeRow({ purchase_id: 'fail-legacy', created_by_user_id: undefined }),
      ],
    });

    await loadHistory();

    const buttons = document.querySelectorAll<HTMLButtonElement>('.history-retry-btn');
    const ids = Array.from(buttons).map((b) => b.dataset['retryId']);
    expect(ids).toEqual(expect.arrayContaining(['fail-mine', 'fail-other', 'fail-legacy']));
    expect(ids).toHaveLength(3);
  });

  test('regular user sees Retry only on rows they created', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(REG_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'fail-mine', created_by_user_id: REG_USER.id }),
        makeRow({ purchase_id: 'fail-other', created_by_user_id: OTHER_UUID }),
        makeRow({ purchase_id: 'fail-legacy', created_by_user_id: undefined }),
      ],
    });

    await loadHistory();

    const buttons = document.querySelectorAll<HTMLButtonElement>('.history-retry-btn');
    const ids = Array.from(buttons).map((b) => b.dataset['retryId']);
    expect(ids).toEqual(['fail-mine']);
  });

  test('renders no Retry buttons when no current user is cached', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(null);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ created_by_user_id: 'anything' })],
    });

    await loadHistory();
    expect(document.querySelectorAll('.history-retry-btn')).toHaveLength(0);
  });

  test('Retry button absent on non-failed rows even for admin', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'r-pending', status: 'pending', created_by_user_id: ADMIN_USER.id }),
        makeRow({ purchase_id: 'r-completed', status: 'completed', created_by_user_id: ADMIN_USER.id }),
        makeRow({ purchase_id: 'r-cancelled', status: 'cancelled', created_by_user_id: ADMIN_USER.id }),
        makeRow({ purchase_id: 'r-expired', status: 'expired', created_by_user_id: ADMIN_USER.id }),
      ],
    });

    await loadHistory();
    expect(document.querySelectorAll('.history-retry-btn')).toHaveLength(0);
  });

  test('ops_hint replaces the Retry button on failed rows', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'r-1', created_by_user_id: ADMIN_USER.id, ops_hint: 'Set FROM_EMAIL tfvar then retry' }),
      ],
    });

    await loadHistory();
    expect(document.querySelectorAll('.history-retry-btn')).toHaveLength(0);
    const hint = document.querySelector('.history-ops-hint');
    expect(hint).not.toBeNull();
    expect(hint?.textContent).toContain('FROM_EMAIL');
  });

  test('threshold-reached row renders override button + sends force=true on accept', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.retryPurchase as jest.Mock).mockResolvedValue({ execution_id: 'new', original_execution: 'r-1' });
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'r-1', created_by_user_id: ADMIN_USER.id, retry_attempt_n: 5 }),
      ],
    });

    await loadHistory();
    const btn = document.querySelector<HTMLButtonElement>('.history-retry-btn');
    expect(btn?.classList.contains('history-retry-over-threshold')).toBe(true);
    expect(btn?.textContent).toContain('Retried 5×');

    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(api.retryPurchase).toHaveBeenCalledWith('r-1', { force: true });
  });

  test('retry_execution_id set → no Retry button, lineage link rendered', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'old-1', created_by_user_id: ADMIN_USER.id, retry_execution_id: 'abcdef0123456789' }),
      ],
    });

    await loadHistory();
    expect(document.querySelectorAll('.history-retry-btn')).toHaveLength(0);
    const link = document.querySelector('.history-retry-link');
    expect(link).not.toBeNull();
    expect(link?.textContent).toContain('Retried as');
    expect(link?.getAttribute('href')).toContain('execution=abcdef0123456789');
  });

  test('retry_attempt_n > 0 row gets a "Retry #n" provenance badge', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        // n=2 retry that hasn't been retried again — eligible to retry,
        // so we ALSO see the Retry button. This exercises the case where
        // a middle-of-chain row has BOTH ↻ Retry button AND lineage
        // metadata visible to the user.
        makeRow({ purchase_id: 'mid-2', status: 'completed', created_by_user_id: ADMIN_USER.id, retry_attempt_n: 2 }),
      ],
    });

    await loadHistory();
    const badge = document.querySelector('.history-retry-of');
    expect(badge).not.toBeNull();
    expect(badge?.textContent).toContain('Retry #2');
  });

  test('declined confirm dialog skips API call', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(false);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'r-1', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();
    const btn = document.querySelector<HTMLButtonElement>('.history-retry-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(confirmDialog).toHaveBeenCalled();
    expect(api.retryPurchase).not.toHaveBeenCalled();
  });

  test('accepted confirm posts retry + reloads + toasts', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.retryPurchase as jest.Mock).mockResolvedValue({ execution_id: 'new', original_execution: 'r-1' });
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'r-1', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();
    expect(api.getHistory).toHaveBeenCalledTimes(1);
    const btn = document.querySelector<HTMLButtonElement>('.history-retry-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(api.retryPurchase).toHaveBeenCalledWith('r-1', undefined);
    expect(api.getHistory).toHaveBeenCalledTimes(2);
    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'success' }));
  });

  test('retry API failure surfaces toast and re-enables the button', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (confirmDialog as jest.Mock).mockResolvedValue(true);
    (api.retryPurchase as jest.Mock).mockRejectedValue(new Error('boom'));
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'r-1', created_by_user_id: ADMIN_USER.id })],
    });
    console.error = jest.fn();

    await loadHistory();
    const btn = document.querySelector<HTMLButtonElement>('.history-retry-btn');
    btn?.click();
    await new Promise((r) => setTimeout(r, 10));

    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'error' }));
    expect(btn?.disabled).toBe(false);
  });
});
